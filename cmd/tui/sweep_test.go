package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// The interval is spread either way so the sweeps don't arrive on a
// machine-perfect cadence, but it stays recognisably the interval.
func TestJitterStaysInBand(t *testing.T) {
	const every = 10 * time.Minute
	lo := time.Duration(float64(every) * (1 - sweepJitter))
	hi := time.Duration(float64(every) * (1 + sweepJitter))
	seen := map[time.Duration]bool{}
	for range 200 {
		d := jitter(every)
		if d < lo || d > hi {
			t.Fatalf("jitter(%s) = %s, outside [%s, %s]", every, d, lo, hi)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatal("jitter returned one value 200 times; it isn't spreading anything")
	}
	if got := jitter(0); got != 0 {
		t.Fatalf("jitter(0) = %s, want 0 (on-demand only)", got)
	}
}

// Only a service that cannot page is drained. Getting this wrong would tell a
// service its unread list is read when we never needed to.
func TestOnlyInoreaderDrains(t *testing.T) {
	if !drainApps["inoreader"] {
		t.Error("inoreader cannot page, so it has to be drained")
	}
	for _, app := range []string{"x", "folo", "reddit", "douban", "bilibili", "slack"} {
		if drainApps[app] {
			t.Errorf("%s should not be drained: it pages, or has no server-side read state", app)
		}
	}
}

func TestItemIDsSkipsBlanks(t *testing.T) {
	got := itemIDs([]core.Item{{ID: "a"}, {ID: ""}, {ID: "b"}})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("itemIDs = %v, want [a b]", got)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("boom\nstack\ntrace"); got != "boom" {
		t.Fatalf("firstLine = %q, want \"boom\"", got)
	}
	if got := firstLine("one line"); got != "one line" {
		t.Fatalf("firstLine = %q", got)
	}
}

// fakeMark records what it was asked to mark, and can be told to start failing
// after a given number of calls.
type fakeMark struct {
	mu       sync.Mutex
	calls    [][]string
	failFrom int // 0 = never fail
	onCall   func(call int)
}

func (m *fakeMark) fn(_ context.Context, _ string, ids []string) error {
	m.mu.Lock()
	m.calls = append(m.calls, append([]string(nil), ids...))
	n := len(m.calls)
	hook := m.onCall
	m.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	if m.failFrom > 0 && n >= m.failFrom {
		return errors.New("upstream said no")
	}
	return nil
}

func (m *fakeMark) marked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, c := range m.calls {
		out = append(out, c...)
	}
	return out
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.Itoa(i)
	}
	return out
}

// A backlog goes upstream a chunk at a time, because a service that spends a
// round trip per id cannot answer for hundreds inside one call.
func TestMarkInChunksSplitsWork(t *testing.T) {
	m := &fakeMark{}
	want := ids(markChunk*2 + 3)
	done, err := markInChunks(context.Background(), m.fn, "inoreader", want)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != len(want) {
		t.Fatalf("marked %d of %d", len(done), len(want))
	}
	if len(m.calls) != 3 {
		t.Fatalf("made %d calls for %d ids, want 3 of at most %d", len(m.calls), len(want), markChunk)
	}
	for i, c := range m.calls {
		if len(c) > markChunk {
			t.Fatalf("call %d carried %d ids, over the %d cap", i, len(c), markChunk)
		}
	}
}

// A failure partway reports what already landed, so the retry doesn't repeat it.
func TestMarkInChunksReportsPartialProgress(t *testing.T) {
	m := &fakeMark{failFrom: 3}
	done, err := markInChunks(context.Background(), m.fn, "inoreader", ids(markChunk*4))
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	if len(done) != markChunk*2 {
		t.Fatalf("reported %d ids as done, want the %d that landed before the failure", len(done), markChunk*2)
	}
}

func TestMarkInChunksStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &fakeMark{}
	done, err := markInChunks(ctx, m.fn, "inoreader", []string{"a", "b"})
	if err == nil {
		t.Fatal("expected the cancellation to surface")
	}
	if len(done) != 0 || len(m.calls) != 0 {
		t.Fatalf("marked %v after cancellation", done)
	}
}

// newTestSweeper wires a sweeper to canned pages instead of subprocesses. Each
// call to fetch hands back the next page.
func newTestSweeper(t *testing.T, drain bool, pages [][]core.Item) (*sweeper, *feedCache, *fakeMark, *int) {
	t.Helper()
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	m := &fakeMark{}
	fetches := 0
	s := newSweeper(t.TempDir(), c, nil, nil, drain, 0)
	s.mark = m.fn
	s.fetch = func(context.Context, string, string, int, time.Time) ([]core.Item, bool, error) {
		if fetches >= len(pages) {
			return nil, false, nil
		}
		p := pages[fetches]
		fetches++
		return p, false, nil
	}
	return s, c, m, &fetches
}

func page(app string, from, n int) []core.Item {
	out := make([]core.Item, 0, n)
	for i := from; i < from+n; i++ {
		out = append(out, core.Item{App: app, ID: strconv.Itoa(i), Title: "article " + strconv.Itoa(i)})
	}
	return out
}

// A drain walks the backlog: mark the page we have, ask again, until a round
// brings nothing new. Everything it collected is unread for the reader and
// already synced upstream.
func TestDrainWalksTheBacklog(t *testing.T) {
	s, c, m, fetches := newTestSweeper(t, true, [][]core.Item{
		page("inoreader", 0, 3),
		page("inoreader", 3, 3),
		page("inoreader", 6, 2),
		{}, // the stream is exhausted
	})
	s.sweepApp(context.Background(), "inoreader")

	if got := c.unreadCount(); got != 8 {
		t.Fatalf("collected %d articles, want 8 across three pages", got)
	}
	if *fetches != 4 {
		t.Fatalf("fetched %d times, want 4 (three pages plus the empty one)", *fetches)
	}
	if got := len(m.marked()); got != 8 {
		t.Fatalf("told inoreader about %d articles, want 8", got)
	}
	// Read upstream, unread here: that is the whole trick.
	if pending := c.unsynced(); len(pending) != 0 {
		t.Fatalf("a drained article should need no later flush: %v", pending)
	}
	if st := c.statusOf("inoreader"); st.Capped {
		t.Error("the drain finished, so nothing is capped")
	}
}

// Reading a drained article later costs no second round trip: the service was
// already told.
func TestDrainedArticleNeedsNoSecondCall(t *testing.T) {
	s, c, m, _ := newTestSweeper(t, true, [][]core.Item{page("inoreader", 0, 2), {}})
	s.sweepApp(context.Background(), "inoreader")
	before := len(m.marked())

	c.markRead("inoreader", []string{"0"}, time.Now())
	if pending := c.unsynced(); len(pending) != 0 {
		t.Fatalf("reading a drained article queued a flush: %v", pending)
	}
	if len(m.marked()) != before {
		t.Fatal("reading a drained article should say nothing upstream")
	}
}

// A drain that runs out of rounds says so, so the count can render as "N+"
// rather than claiming to be the whole truth.
func TestDrainStopsAtRoundCapAndSaysSo(t *testing.T) {
	pages := make([][]core.Item, 0, drainRounds+2)
	for i := range drainRounds + 2 {
		pages = append(pages, page("inoreader", i*2, 2))
	}
	s, c, _, _ := newTestSweeper(t, true, pages)
	s.sweepApp(context.Background(), "inoreader")

	if st := c.statusOf("inoreader"); !st.Capped {
		t.Fatal("a drain that hit its round cap should report a capped count")
	}
	if got := c.unreadCount(); got != (drainRounds+1)*2 {
		t.Fatalf("collected %d, want the first %d rounds' worth", got, drainRounds)
	}
}

// The items are on disk before a word goes upstream. An article Inoreader has
// been told we read but that never reached the cache is gone for good.
func TestDrainSavesBeforeMarking(t *testing.T) {
	s, c, m, _ := newTestSweeper(t, true, [][]core.Item{page("inoreader", 0, 3), {}})
	m.onCall = func(int) {
		// Whatever is being marked must already be readable from the file.
		onDisk := loadFeedCache(c.path)
		if got := onDisk.unreadCount(); got == 0 {
			t.Error("marked articles read upstream before they reached the cache file")
		}
	}
	s.sweepApp(context.Background(), "inoreader")
}

// A failed mark stops the drain rather than pressing on: the next sweep can try
// again, and nothing is lost either way.
func TestDrainStopsOnMarkFailure(t *testing.T) {
	s, c, m, fetches := newTestSweeper(t, true, [][]core.Item{
		page("inoreader", 0, 2), page("inoreader", 2, 2), page("inoreader", 4, 2),
	})
	m.failFrom = 1
	s.sweepApp(context.Background(), "inoreader")

	if *fetches != 1 {
		t.Fatalf("fetched %d times; a failed mark should stop the walk", *fetches)
	}
	if got := c.unreadCount(); got != 2 {
		t.Fatalf("kept %d articles, want the 2 already fetched", got)
	}
}

// A service that pages honestly is fetched and left alone: nothing is said
// upstream until the reader actually reads something.
func TestNonDrainerIsNeverMarked(t *testing.T) {
	s, c, m, fetches := newTestSweeper(t, true, [][]core.Item{page("folo", 0, 4), page("folo", 4, 4)})
	s.sweepApp(context.Background(), "folo")

	if *fetches != 1 {
		t.Fatalf("fetched %d times, want 1: folo pages on its own", *fetches)
	}
	if got := m.marked(); len(got) != 0 {
		t.Fatalf("said %v to folo; it has its own unread list to keep", got)
	}
	if got := c.unreadCount(); got != 4 {
		t.Fatalf("collected %d, want 4", got)
	}
	if st := c.statusOf("folo"); st.At == "" || st.Err != "" || st.Capped {
		t.Fatalf("expected a clean, uncapped status, got %+v", st)
	}
}

// A page that came back full has more behind it, so the count renders as "N+"
// rather than claiming to be everything.
func TestFullPageIsCapped(t *testing.T) {
	s, c, _, _ := newTestSweeper(t, true, [][]core.Item{page("folo", 0, sweepMax)})
	s.sweepApp(context.Background(), "folo")
	if st := c.statusOf("folo"); !st.Capped {
		t.Fatalf("a full page should be reported as capped, got %+v", st)
	}
}

// --web-drain=false leaves Inoreader's own unread list intact, at the cost of
// only ever seeing its first page.
func TestDrainOffLeavesInoreaderAlone(t *testing.T) {
	s, c, m, fetches := newTestSweeper(t, false, [][]core.Item{page("inoreader", 0, 3), page("inoreader", 3, 3)})
	s.sweepApp(context.Background(), "inoreader")

	if *fetches != 1 || len(m.marked()) != 0 {
		t.Fatalf("fetched %d times and marked %v; --web-drain=false should do neither", *fetches, m.marked())
	}
	if got := c.unreadCount(); got != 3 {
		t.Fatalf("collected %d, want the single page", got)
	}
}

// A failed fetch is recorded as such, keeps the previous success time, and
// carries the stale-session flag the page turns into a re-auth hint.
func TestSweepAppRecordsFailure(t *testing.T) {
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	s := newSweeper(t.TempDir(), c, nil, nil, true, 0)
	s.mark = (&fakeMark{}).fn
	c.setStatus("x", appStatus{At: "2026-01-01T00:00:00Z"})
	s.fetch = func(context.Context, string, string, int, time.Time) ([]core.Item, bool, error) {
		return nil, true, errors.New("session is stale")
	}
	s.sweepApp(context.Background(), "x")

	st := c.statusOf("x")
	if st.Err == "" || !st.Stale {
		t.Fatalf("status = %+v, want a stale-session failure", st)
	}
	if st.At != "2026-01-01T00:00:00Z" {
		t.Fatalf("At = %q; a failure should not erase the last success", st.At)
	}
}

// The flusher carries read marks to their apps and stops retrying once they
// land, including marks for items the cache never saw (x's For You).
func TestFlusherFlushesAndClears(t *testing.T) {
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	now := time.Now()
	c.upsert([]core.Item{{App: "x", ID: "1", Title: "a"}}, now)
	c.markRead("x", []string{"1"}, now)

	m := &fakeMark{}
	f := newMarkFlusher(t.TempDir(), c)
	f.mark = m.fn
	f.push("x", []string{"live-2"}) // never cached: For You
	f.flushOnce(context.Background())

	got := m.marked()
	if len(got) != 2 {
		t.Fatalf("flushed %v, want both the cached and the uncached mark", got)
	}
	if pending := c.unsynced(); len(pending) != 0 {
		t.Fatalf("a flushed mark should not be retried: %v", pending)
	}
	f.flushOnce(context.Background())
	if len(m.marked()) != 2 {
		t.Fatal("a second flush repeated work that had already landed")
	}
}

// A mark that could not be delivered stays queued, so it is retried rather than
// lost — across a restart too, since the cache is where it lives.
func TestFlusherRetriesWhatDidNotLand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.json")
	c := loadFeedCache(path)
	now := time.Now()
	c.upsert([]core.Item{{App: "x", ID: "1", Title: "a"}}, now)
	c.markRead("x", []string{"1"}, now)
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	f := newMarkFlusher(t.TempDir(), c)
	f.mark = (&fakeMark{failFrom: 1}).fn
	f.flushOnce(context.Background())
	if pending := c.unsynced(); len(pending["x"]) != 1 {
		t.Fatalf("a failed flush should stay queued: %v", pending)
	}

	// A restart reads the same pending mark back off disk.
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if pending := loadFeedCache(path).unsynced(); len(pending["x"]) != 1 {
		t.Fatalf("a pending mark should survive a restart: %v", pending)
	}
}

func TestFlusherPushIgnoresNothing(t *testing.T) {
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	f := newMarkFlusher(t.TempDir(), c)
	f.push("x", nil)
	f.mu.Lock()
	n := len(f.extra)
	f.mu.Unlock()
	if n != 0 {
		t.Fatal("pushing nothing should queue nothing")
	}
}

// kick never blocks, however many times it is called: an extra request while a
// sweep runs is dropped, because the one in flight is already the answer.
func TestKickDoesNotBlock(t *testing.T) {
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	s := newSweeper(t.TempDir(), c, nil, nil, false, time.Minute)
	done := make(chan struct{})
	go func() {
		for range 100 {
			s.kick()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("kick blocked")
	}
}

func TestSweepRunsAfterHook(t *testing.T) {
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	s := newSweeper(t.TempDir(), c, nil, nil, false, 0)
	called := 0
	s.after = func() error {
		called++
		return nil
	}
	s.sweep(context.Background(), true)
	if called != 1 {
		t.Fatalf("after hook called %d times, want 1", called)
	}
}
