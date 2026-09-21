package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/genkio/tui/core"
)

// drainApps names the services whose backlog can only be reached by telling
// them what we have already fetched is read. Inoreader is the whole list: its
// web RPC renders one page and its "load more" offset returns a stale copy of
// that same page (see the Unreads comment in the plugin's client), so marking a
// page read and asking again is the only way past the first fifty articles.
//
// Everything else pages honestly, or has no server-side read state at all — for
// those, unread means "in this cache and not marked here", and nothing is said
// upstream until you actually read it.
var drainApps = map[string]bool{"inoreader": true}

const (
	// sweepMax is how deep a sweep asks each service to go, well past the
	// per-app TUI caps (40-50) that made the old page-load fetch shallow.
	sweepMax = 300
	// drainRounds bounds one service's mark-and-refetch loop, so a first sweep
	// against a huge Inoreader backlog finishes in minutes rather than hours.
	// Whatever is left is reported as a capped count and picked up next sweep.
	drainRounds = 8
	// sweepAppTimeout is one --json call's budget. Generous: a cold x timeline
	// or a deep folo cursor walk is slow, and nothing is waiting on it.
	sweepAppTimeout = 4 * time.Minute
	// sweepAppBudget bounds everything one service can cost, drain rounds
	// included. A sweep holds the only sweep slot, so one pathological service
	// must not be able to block every later sweep behind it.
	sweepAppBudget = 15 * time.Minute
	// sweepJitter spreads the interval by up to this fraction either way, so
	// the sweeps don't arrive on a machine-perfect cadence.
	sweepJitter = 0.15
	// appStagger spreads the services within a sweep, for the same reason.
	appStagger = 25 * time.Second
	// markChunk is how many ids go into one --mark-read call. Inoreader spends
	// an HTTP round trip per id, so a few hundred in one subprocess would run
	// past any sane timeout with no record of how far it got.
	markChunk = 25
	// markTimeout is one --mark-read call's budget. Inoreader's own request
	// ceiling is two minutes, so leave room for its error to come back instead
	// of killing the subprocess and learning nothing.
	markTimeout = 3 * time.Minute
	// flushEvery is how often read marks that failed to reach their app are
	// retried, on top of being flushed the moment they are made.
	flushEvery = 45 * time.Second
)

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "tui serve: "+format+"\n", a...)
}

// fetchFunc reads one app's unread items; markFunc tells one app that ids have
// been read. Both are fields rather than direct calls because both are
// subprocesses, and the order they happen in is the load-bearing part of a
// drain — worth being able to test without shelling out.
type fetchFunc func(ctx context.Context, app string, max int, now time.Time) ([]core.Item, bool, error)

type markFunc func(ctx context.Context, app string, ids []string) error

func subprocessFetch(root string) fetchFunc {
	return func(ctx context.Context, app string, max int, now time.Time) ([]core.Item, bool, error) {
		return fetchApp(ctx, root, app, max, now)
	}
}

func subprocessMark(root string) markFunc {
	return func(_ context.Context, app string, ids []string) error {
		return runMarkRead(root, app, ids, markTimeout)
	}
}

// sweeper keeps the cache fed. It runs one sweep at a time on a jittered
// interval, plus whenever the page asks for one, and it is the only writer of
// the cache file while the server is up.
type sweeper struct {
	root  string
	cache *feedCache
	flush *markFlusher
	block *blocker
	drain bool
	every time.Duration
	after func() error
	fetch fetchFunc
	mark  markFunc
	// Shared with /ytlen, so the lengths a page asks for and the ones a sweep
	// resolves warm one another. Nil in tests, which have no YouTube to ask.
	ylen  *ytLens
	saved *savedStore // measured alongside the backlog; see measureBacklog
	// Asked for at the end of every sweep: what a fetch brings in is judged
	// without anyone asking, so a feed is sifted by the time it is read. It
	// returns at once — the run itself is the sifter's own goroutine, and it is
	// minutes long. Nil in tests, and when there is no sifter to ask.
	sift func()
	// ...and the other half of it: the next couple of hundred unread items are
	// summarized as they arrive, so the backlog is described by the time it is
	// opened. Returns at once for the same reason the sift does. Nil in tests.
	digest func()

	busy atomic.Bool
	wake chan struct{}
}

func newSweeper(root string, cache *feedCache, flush *markFlusher, block *blocker, drain bool, every time.Duration) *sweeper {
	return &sweeper{
		root: root, cache: cache, flush: flush, block: block, drain: drain, every: every,
		fetch: subprocessFetch(root), mark: subprocessMark(root),
		wake: make(chan struct{}, 1),
	}
}

// kick asks for a sweep now, without waiting for one to finish. Extra kicks
// while a sweep runs are dropped: the one in flight is already the answer.
func (s *sweeper) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sweeper) sweeping() bool { return s.busy.Load() }

// run sweeps once at startup so the first page load has something to serve,
// then on the jittered interval or on demand. An interval of zero means only
// on demand.
func (s *sweeper) run(ctx context.Context) {
	s.sweep(ctx, true)
	for {
		var tick <-chan time.Time
		var timer *time.Timer
		if s.every > 0 {
			timer = time.NewTimer(jitter(s.every))
			tick = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.wake:
		case <-tick:
		}
		if timer != nil {
			timer.Stop() // a kick beat the timer; the next pass re-arms it
		}
		s.sweep(ctx, false)
	}
}

// jitter spreads d by up to sweepJitter either way.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	span := float64(d) * sweepJitter
	return time.Duration(float64(d) - span + rand.Float64()*2*span)
}

// sweep refreshes every logged-in feed app concurrently. first skips the
// per-app stagger, so a cold start has a page as soon as it can.
//
// Concurrently by plugin, not by source: x's two timelines are one login and one
// rate limit, so they are swept one after the other rather than racing each
// other for it.
func (s *sweeper) sweep(ctx context.Context, first bool) {
	if !s.busy.CompareAndSwap(false, true) {
		return // one already in flight; it is the answer
	}
	defer s.busy.Store(false)

	var wg sync.WaitGroup
	for _, group := range byPlugin(authedFeedApps(s.root)) {
		wg.Add(1)
		go func(group []string) {
			defer wg.Done()
			if !first {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(rand.Int64N(int64(appStagger)))):
				}
			}
			for _, app := range group {
				s.sweepApp(ctx, app)
			}
		}(group)
	}
	wg.Wait()
	s.measureBacklog(ctx)
	s.cache.setSwept(time.Now())
	if err := s.cache.save(); err != nil {
		logf("save cache: %v", err)
	}
	// A sweep is also the moment to retry read marks that never reached their
	// app: whatever was down for the flush may well be up again by now.
	if s.flush != nil {
		s.flush.kick()
	}
	// ...and the moment to judge what it just brought in. After the save rather
	// than before it, so the run reads a backlog that is on disk, and after the
	// whole sweep rather than after each app, so one run covers the fetch.
	if s.sift != nil {
		s.sift()
	}
	// ...and to describe it. Behind the sift, which is the cheaper and faster of
	// the two and decides what is worth reading at all.
	if s.digest != nil {
		s.digest()
	}
	if s.after != nil {
		if err := s.after(); err != nil {
			logf("backup database: %v", err)
		}
	}
}

// sweepApp refreshes one service. For a draining service it then walks the
// backlog: mark what we have, ask again, repeat until a round brings nothing
// new or the round cap is hit.
//
// The order is load-bearing. Items are on disk before a word goes upstream: an
// article Inoreader has been told we read but that never reached the cache is
// gone for good, so a failed save aborts the drain rather than risking it.
func (s *sweeper) sweepApp(ctx context.Context, app string) {
	ctx, cancel := context.WithTimeout(ctx, sweepAppBudget)
	defer cancel()

	now := time.Now()
	items, stale, err := s.fetch(ctx, app, sweepMax, now)
	if err != nil {
		s.cache.setStatus(app, appStatus{At: s.cache.statusOf(app).At, Err: err.Error(), Stale: stale})
		logf("%s: %v", app, err)
		return
	}
	kept, _ := s.screen(items, now)
	s.cache.upsert(s.measure(ctx, s.dropTwins(app, kept)), now)
	if err := s.cache.save(); err != nil {
		logf("%s: save cache: %v", app, err)
		s.cache.setStatus(app, appStatus{At: s.cache.statusOf(app).At, Err: "cache write failed"})
		return
	}

	stamp := now.UTC().Format(time.RFC3339)
	if !s.drain || !drainApps[app] {
		// A page that came back full is a page with more behind it, the same
		// reasoning the picker's "N+" badge uses.
		s.cache.setStatus(app, appStatus{At: stamp, Capped: len(items) >= sweepMax})
		return
	}

	capped := true
	for round := 0; round < drainRounds; round++ {
		if ctx.Err() != nil {
			break
		}
		ids := itemIDs(items)
		if len(ids) == 0 {
			capped = false
			break
		}
		// Upstream is told; the entries stay unread for you, but they arrive
		// already synced, so reading one later costs no second round trip.
		done, err := markInChunks(ctx, s.mark, app, ids)
		s.cache.markSynced(app, done)
		if serr := s.cache.save(); serr != nil {
			logf("%s: save cache: %v", app, serr)
			break
		}
		if err != nil {
			logf("%s: drain stopped after %d of %d: %v", app, len(done), len(ids), err)
			break
		}
		items, _, err = s.fetch(ctx, app, sweepMax, now)
		if err != nil {
			logf("%s: drain refetch: %v", app, err)
			break
		}
		kept, blocked := s.screen(items, now)
		fresh := s.cache.upsert(s.measure(ctx, s.dropTwins(app, kept)), now) + blocked
		if err := s.cache.save(); err != nil {
			logf("%s: save cache: %v", app, err)
			break
		}
		if fresh == 0 {
			capped = false
			break
		}
	}
	s.cache.setStatus(app, appStatus{At: stamp, Capped: capped})
}

// screen files whatever the block list caught and hands back the rest, so a
// blocked post never becomes backlog. The count is how many of them were new,
// which the drain adds to the cache's own: a round that brought nothing but
// blocked posts still brought something, and calling it exhausted there would
// leave the rest of the backlog unreachable.
//
// The ids the drain marks upstream are the whole fetch, blocked ones included.
// They were fetched; a service that only pages by being told what it handed
// over has been read has to hear about them too.
func (s *sweeper) screen(items []core.Item, now time.Time) ([]core.Item, int) {
	keep, caught := s.block.split(items, now)
	if len(caught) == 0 {
		return keep, 0
	}
	fresh, err := s.block.file(caught)
	if err != nil {
		logf("blocked list: %v", err)
	}
	return keep, fresh
}

// dropTwins keeps a post that both of x's timelines carry out of the backlog
// twice. A tweet is one tweet whichever window surfaced it, so the tab that
// cached it first keeps it and the other tab's sighting is dropped — including
// when the first copy has already been read, which is the whole point: being
// shown a post again under the other name is being asked to read it twice.
//
// Everything else has no twin and passes straight through.
func (s *sweeper) dropTwins(app string, items []core.Item) []core.Item {
	twin := twinApp(app)
	if twin == "" || len(items) == 0 {
		return items
	}
	out := make([]core.Item, 0, len(items))
	for _, it := range items {
		if s.cache.has(twin, it.ID) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// measure states the length of a linked YouTube clip before the cache takes the
// item, so the feed can tell a 40-second clip from an hour-long talk. An app
// that attached its own video already said, and nothing here asks twice.
func (s *sweeper) measure(ctx context.Context, items []core.Item) []core.Item {
	if s.ylen != nil {
		s.ylen.fill(ctx, items)
	}
	return items
}

// measureBacklog asks the same question of what is already held: an item
// arrives once and is read days later, so one whose length nothing ever stated
// would wear the wrong chip for as long as it sat there. The saved list is
// asked too — what is kept to watch later is kept longest of all.
func (s *sweeper) measureBacklog(ctx context.Context) {
	if s.ylen == nil {
		return
	}
	now := time.Now()
	items := s.cache.unmeasured(now)
	if s.saved != nil {
		items = append(items, s.saved.unmeasured(now)...)
	}
	s.ylen.fill(ctx, items)
	for _, it := range items {
		if it.VidSecs == 0 {
			continue
		}
		// Both stores hear it, whichever list the item came off. They share one
		// row on disk, and a length known to one copy and not the other is
		// written back over by whichever saves last.
		s.cache.setVidSecs(it.App, it.ID, it.VidSecs)
		if s.saved == nil {
			continue
		}
		if _, err := s.saved.setVidSecs(it.App, it.ID, it.VidSecs); err != nil {
			logf("saved: record clip length: %v", err)
		}
	}
}

func itemIDs(items []core.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.ID != "" {
			out = append(out, it.ID)
		}
	}
	return out
}

// fetchApp runs one app's --json and parses it, the same subprocess contract
// the terminal views use. max lifts the app's own fetch cap for a sweep, which
// wants the backlog rather than the screenful a TUI shows.
//
// Which plugin and which timeline that is comes from the source's name: x's two
// are one plugin asked for two tabs, and what comes back is filed under the
// source that asked, since the plugin only ever says "x".
func fetchApp(ctx context.Context, root, app string, max int, now time.Time) ([]core.Item, bool, error) {
	appCtx, cancel := context.WithTimeout(ctx, sweepAppTimeout)
	defer cancel()
	plugin, tab := pluginOf(app)
	args := []string{plugin, "--json"}
	if plugin == "x" {
		args = append(args, "--tab", tab) // For You / Following
	}
	if max > 0 {
		args = append(args, "--max", fmt.Sprint(max))
	}
	cmd := exec.CommandContext(appCtx, self(), args...)
	cmd.Env = appEnv(filepath.Join(root, "plugins", plugin))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// every plugin emits this marker for an expired session
		stale := bytes.Contains(stderr.Bytes(), []byte("session is stale"))
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, stale, fmt.Errorf("%s", firstLine(msg))
	}
	items, perr := core.ParseItems(out, now)
	if perr != nil {
		return nil, false, fmt.Errorf("unreadable --json: %w", perr)
	}
	stampApp(items, app)
	return items, false, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// markInChunks feeds ids to the app's --mark-read a chunk at a time and returns
// the ones that landed. Partial progress is the point: a service that spends a
// round trip per id will time out on a big backlog, and repeating the ids that
// already went through is wasted work at best.
func markInChunks(ctx context.Context, mark markFunc, app string, ids []string) ([]string, error) {
	var done []string
	size := markChunkOf(app)
	for i := 0; i < len(ids); i += size {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		chunk := ids[i:min(i+size, len(ids))]
		if err := mark(ctx, app, chunk); err != nil {
			return done, err
		}
		done = append(done, chunk...)
	}
	return done, nil
}

// markChunkOf is how many ids one --mark-read call carries for an app.
// Inoreader goes one at a time: it spends a round trip per id, and the site's
// latency swings from under a second to over a minute, so a batched chunk
// times out as a whole and the ids that did land are sent again next round.
func markChunkOf(app string) int {
	if app == "inoreader" {
		return 1
	}
	return markChunk
}

// markFlusher carries read marks from the cache to the apps themselves, so the
// page can record one instantly and the TUI still agrees about it later. The
// cache is the write-ahead log: anything read but unsynced is retried until it
// lands, across restarts included.
type markFlusher struct {
	root  string
	cache *feedCache
	mark  markFunc
	wake  chan struct{}

	mu sync.Mutex
	// extra holds marks for items the cache never saw: a card read off a page
	// the sweep has since pruned out from under has nothing here to hang its
	// mark on, so it is flushed straight through.
	extra map[string][]string
}

func newMarkFlusher(root string, cache *feedCache) *markFlusher {
	return &markFlusher{
		root: root, cache: cache, mark: subprocessMark(root),
		wake: make(chan struct{}, 1), extra: map[string][]string{},
	}
}

func (f *markFlusher) kick() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// push queues ids that are not in the cache, then asks for a flush.
func (f *markFlusher) push(app string, ids []string) {
	if len(ids) == 0 {
		return
	}
	f.mu.Lock()
	f.extra[app] = append(f.extra[app], ids...)
	f.mu.Unlock()
	f.kick()
}

func (f *markFlusher) run(ctx context.Context) {
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		case <-t.C:
		}
		f.flushOnce(ctx)
	}
}

func (f *markFlusher) flushOnce(ctx context.Context) {
	pending := f.cache.unsynced()
	f.mu.Lock()
	for app, ids := range f.extra {
		pending[app] = append(pending[app], ids...)
	}
	f.extra = map[string][]string{}
	f.mu.Unlock()

	for app, ids := range pending {
		done, err := markInChunks(ctx, f.mark, app, ids)
		if len(done) > 0 {
			f.cache.markSynced(app, done)
			if serr := f.cache.save(); serr != nil {
				logf("save cache: %v", serr)
			}
		}
		if err != nil && ctx.Err() == nil {
			logf("%s: %d of %d read marks flushed, retrying: %v", app, len(done), len(ids), err)
		}
	}
}
