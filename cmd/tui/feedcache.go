package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// feedCache is the web view's backlog: every unread item the sweeper has ever
// fetched, kept in SQLite so serving a page does not wait on six concurrent
// scrapes.
//
// Two things fall out of accumulating rather than re-fetching. The unread count
// becomes real: a service that only ever hands over its newest page (or, for
// Inoreader, cannot page at all) still adds to a total that grows across
// sweeps. And read state becomes ours: an entry carries whether you have read
// it and whether that mark has reached the app it came from, so a mark is
// recorded instantly and flushed upstream in the background.
//
// One server owns the database while it runs; the launcher only reads it.
type feedCache struct {
	mu      sync.Mutex
	entries []*feedEntry
	byKey   map[string]*feedEntry
	status  map[string]appStatus
	swept   time.Time
	rev     int // bumped per mutation; save skips a snapshot already overtaken

	writeMu sync.Mutex
	written int
	path    string
	db      *feedDB
}

func loadFeedCacheDB(db *feedDB) (*feedCache, error) {
	f, err := db.loadFeed()
	if err != nil {
		return nil, err
	}
	c := &feedCache{db: db, path: db.path, byKey: map[string]*feedEntry{}, status: map[string]appStatus{}}
	c.load(f)
	return c, nil
}

// feedEntry is one cached item plus what we know about your relationship to it.
type feedEntry struct {
	core.Wire
	FirstSeen string `json:"first_seen,omitempty"`
	Read      bool   `json:"read,omitempty"`
	ReadAt    string `json:"read_at,omitempty"`
	// Synced reports whether the item's own app already treats it as read. A
	// user's mark starts life unsynced and is flushed to the plugin in the
	// background, so a failed flush is retried instead of lost. A drained
	// service (see drainApps) is told at fetch time, so its entries arrive
	// synced and a later read costs no second call.
	Synced bool `json:"synced,omitempty"`
	// What a sift made of the item: when a run judged it, how likely it is worth
	// reading at all (0 to 1), and which rung of the ladder it landed on (1 to
	// len(siftLevels); 0 is "no run has said"). Every item a run reaches carries
	// all three, the ones it kept as well as the ones it set aside, so a second
	// run spends nothing on what has already been judged and the cut can be
	// moved without asking again. Judged and set aside are not the same thing:
	// see skipped.
	JudgedAt string  `json:"judged_at,omitempty"`
	Worth    float64 `json:"worth,omitempty"`
	Rank     int     `json:"rank,omitempty"`
	// How well the item answers what the reader said they are following at the
	// moment (see interestStore), 0 to 1, or below zero for an item nothing has
	// asked about — which is every item when the list is empty, and every item
	// again the moment the list is edited.
	Interest float64 `json:"interest,omitempty"`
	// Which subject on the list the item answers, as the reader wrote it: the
	// chip on the card in the "for me" pick, so a match can be argued with.
	InterestFor string `json:"interest_for,omitempty"`
	// When the discussion under this item was asked for. Non-empty sets the item
	// aside: out of the feed, into the gist chip, where it waits with its thread
	// read rather than coming round again before the reading is finished.
	GistAt string `json:"gist_at,omitempty"`
}

// matched reports whether the item answers something on the reader's own list.
func (e *feedEntry) matched() bool { return e.Interest >= interestCut }

// judged reports whether a run has answered for this item. Both halves are
// required: a row from before the ladder existed carries a worth and no rung,
// and half a judgment is one the next run should ask again rather than file.
func (e *feedEntry) judged() bool { return e.JudgedAt != "" && e.Rank > 0 }

// skipped reports whether the sift set this item aside: judged, and judged not
// worth the reading. It stays in the backlog table either way — this is a
// bucket to go through and disagree with, not a delete.
// A match is never skipped, whatever the cut made of it. The reader named the
// subject themselves, which outranks a model's opinion about whether there is
// anything in this particular piece of it.
func (e *feedEntry) skipped() bool { return e.judged() && e.Worth < siftCut && !e.matched() }

// gisting reports whether the item is set aside for its discussion: you asked
// for the thread under it and moved on, so it is out of the feed and in the gist
// chip until you read it there.
func (e *feedEntry) gisting() bool { return e.GistAt != "" }

// appStatus is the last thing a sweep learned about one service: enough for the
// header's health dot, the stale-session warning, and whether its backlog is
// deeper than the sweep was willing to go.
type appStatus struct {
	At     string `json:"at,omitempty"`     // when a sweep of it last succeeded
	Err    string `json:"err,omitempty"`    // why the last sweep failed; empty when it didn't
	Stale  bool   `json:"stale,omitempty"`  // that failure is an expired session, fixable by --auth
	Capped bool   `json:"capped,omitempty"` // a drain stopped at its round cap, so there is more upstream
}

type feedFile struct {
	Items  []*feedEntry         `json:"items"`
	Status map[string]appStatus `json:"status,omitempty"`
	Swept  string               `json:"swept,omitempty"`
}

// loadFeedCache reads the legacy JSON cache used by tests and migration. A
// missing or corrupt file yields an empty cache rather than an error: the worst
// case is one slow sweep before the page has anything to serve.
func loadFeedCache(path string) *feedCache {
	if path == "" {
		path = core.StatePath("tui", "feed.json")
	}
	c := &feedCache{path: path, byKey: map[string]*feedEntry{}, status: map[string]appStatus{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var f feedFile
	if json.Unmarshal(data, &f) != nil {
		return c
	}
	c.load(f)
	return c
}

func (c *feedCache) load(f feedFile) {
	for _, e := range f.Items {
		if e == nil || e.App == "" || e.ID == "" {
			continue
		}
		k := core.Key(e.App, e.ID)
		if _, dup := c.byKey[k]; dup {
			continue
		}
		c.entries = append(c.entries, e)
		c.byKey[k] = e
	}
	if f.Status != nil {
		c.status = f.Status
	}
	if t, err := time.Parse(time.RFC3339, f.Swept); err == nil {
		c.swept = t
	}
}

// upsert folds a fetch into the backlog and reports how many of the items were
// new. Content is refreshed (a body can arrive on a later pass) but everything
// we know about your reading of it is kept, so a re-fetch of something already
// triaged cannot resurrect it.
func (c *feedCache) upsert(items []core.Item, now time.Time) int {
	stamp := now.UTC().Format(time.RFC3339)
	c.mu.Lock()
	defer c.mu.Unlock()
	fresh := 0
	for _, it := range items {
		if it.App == "" || it.ID == "" {
			continue
		}
		k := it.Key()
		if e, ok := c.byKey[k]; ok {
			e.Wire = it.Wire()
			continue
		}
		e := &feedEntry{Wire: it.Wire(), FirstSeen: stamp}
		c.entries = append(c.entries, e)
		c.byKey[k] = e
		fresh++
	}
	if fresh > 0 {
		c.rev++
	}
	return fresh
}

// unread returns the whole unread backlog as feed items, unsorted (the caller
// orders it). Ages are recomputed from the publish time so an item cached
// yesterday doesn't still read "2h".
//
// What a sift set aside is not in here, and that is the whole of what a sift
// does: the feed, the chip counts, the header, the briefings and mark-all all
// read the backlog through this, so one judgment takes an item out of every one
// of them at once. It is still in the cache, in the skipped view, waiting to be
// disagreed with.
// An item set aside for its discussion is not in here either, for the same
// reason: you asked for the thread under it rather than read it, and meeting it
// again before the reading is finished is exactly what asking was meant to
// avoid. It is in the gist chip instead.
func (c *feedCache) unread(now time.Time, skipApp string) []core.Item {
	return c.pick(now, func(e *feedEntry) bool {
		return !e.Read && !e.skipped() && !e.gisting() && e.App != skipApp
	})
}

// gisting is the pile waiting on their discussions: asked for, set aside, and
// unread. Whether the thread has actually been read yet is the summarizer's to
// say (see gisted) — the chip holds the item either way, so nothing asked for
// can go missing between the asking and the answer.
func (c *feedCache) gisting(now time.Time) []core.Item {
	return c.pick(now, func(e *feedEntry) bool { return !e.Read && e.gisting() })
}

func (c *feedCache) gistingCount() int {
	return c.count(func(e *feedEntry) bool { return !e.Read && e.gisting() })
}

// setGisting sets one item aside for its discussion. False means there was
// nothing in the feed to take out: an item the cache has never heard of, which
// is a gist asked for from the saved list or from an item's own page, or one
// already read, which is past being set aside for later.
func (c *feedCache) setGisting(app, id string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[core.Key(app, id)]
	if !ok || e.Read || e.gisting() {
		return false
	}
	e.GistAt = now.UTC().Format(time.RFC3339)
	c.rev++
	return true
}

// clearGisting puts one back in the feed, which is what a failed reading has to
// do: an item set aside for a discussion that was never read would otherwise sit
// in the chip with nothing to show.
func (c *feedCache) clearGisting(app, id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[core.Key(app, id)]
	if !ok || !e.gisting() {
		return false
	}
	e.GistAt = ""
	c.rev++
	return true
}

// skipped is the pile the sift set aside, newest judgment first: read like the
// feed is read, which is how a judgment gets checked rather than trusted. The
// worth it was given comes back with it, keyed by feed key, for the chip the
// card wears there.
func (c *feedCache) skipped(now time.Time) ([]core.Item, map[string]float64) {
	worth := map[string]float64{}
	items := c.pick(now, func(e *feedEntry) bool {
		// Asking for the discussion under something the sift set aside is
		// disagreeing with the sift, so the gist chip has it from then on.
		if e.Read || !e.skipped() || e.gisting() {
			return false
		}
		worth[core.Key(e.App, e.ID)] = e.Worth
		return true
	})
	return items, worth
}

// worths is what the sift made of everything it has judged, by feed key: what
// the best-first order is sorted on. An item it has never reached is absent
// rather than zero, which is not the same thing (see itemWorth).
func (c *feedCache) worths() map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]float64, len(c.entries))
	for _, e := range c.entries {
		if !e.judged() {
			continue
		}
		out[core.Key(e.App, e.ID)] = e.Worth
	}
	return out
}

// unjudged is what a sift run has left to do: the unread backlog no run has
// reached yet, oldest first, so a run interrupted halfway is resumed by asking
// again rather than started over.
func (c *feedCache) unjudged(now time.Time) []core.Item {
	items := c.pick(now, func(e *feedEntry) bool { return !e.Read && !e.judged() && !e.gisting() })
	sortItems(items, true)
	return items
}

func (c *feedCache) pick(now time.Time, want func(*feedEntry) bool) []core.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]core.Item, 0, len(c.entries))
	for _, e := range c.entries {
		if !want(e) {
			continue
		}
		out = append(out, itemOf(e, now))
	}
	return out
}

func itemOf(e *feedEntry, now time.Time) core.Item {
	it := e.Wire.Item(now)
	if !it.At.IsZero() {
		it.Age = humanAgo(it.At)
	}
	// The rung rides on the item, the way the age does: it is what the chips
	// group by and what narrows a page to one of them, and both of those are
	// done over items long after the entry they came from is out of reach.
	it.Rank, it.Matched = e.Rank, e.matched()
	if it.Matched {
		it.MatchedFor = e.InterestFor
	}
	return it
}

// lastRead is what has already been gone through, oldest read first, the last
// of them the one read most recently. The deck renders a tail of these behind
// the first unread card so that turning the page - a deck running out and
// reloading for the next window - does not take the way back with it.
func (c *feedCache) lastRead(now time.Time, want func(*feedEntry) bool) []core.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	var got []*feedEntry
	for _, e := range c.entries {
		// An entry read before the timestamp existed has no place in the order,
		// and one without it can only be the oldest kind of read anyway.
		if e.Read && e.ReadAt != "" && want(e) {
			got = append(got, e)
		}
	}
	// Every stamp is UTC RFC3339, so string order is time order.
	sort.SliceStable(got, func(i, j int) bool { return got[i].ReadAt < got[j].ReadAt })
	out := make([]core.Item, 0, len(got))
	for _, e := range got {
		out = append(out, itemOf(e, now))
	}
	return out
}

// judge records what a sift made of these items, by feed key, and returns how
// many of them it set aside. An item read while the run was in flight keeps the
// judgment: it costs nothing to hold, and it is the answer to "why was this one
// not in the feed" long after the fact.
func (c *feedCache) judge(verdicts map[string]siftVerdict, now time.Time) int {
	stamp := now.UTC().Format(time.RFC3339)
	c.mu.Lock()
	defer c.mu.Unlock()
	aside := 0
	for k, v := range verdicts {
		e, ok := c.byKey[k]
		if !ok {
			continue
		}
		e.JudgedAt, e.Worth, e.Rank = stamp, v.Worth, v.Rank
		e.Interest, e.InterestFor = v.Interest, v.InterestFor
		if e.skipped() {
			aside++
		}
		c.rev++
	}
	return aside
}

// forget drops every judgment, which is what editing the interest list has to
// do: an answer about a list is worth nothing once the list has changed, and
// the three questions are asked together anyway. Returns how many it cleared.
func (c *feedCache) forget() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if e.JudgedAt == "" && e.Rank == 0 && e.Interest < 0 {
			continue
		}
		e.JudgedAt, e.Worth, e.Rank = "", 0, 0
		e.Interest, e.InterestFor = -1, ""
		n++
	}
	if n > 0 {
		c.rev++
	}
	return n
}

// matchedCount is how many items answer the reader's own list, which is what
// its chip counts.
func (c *feedCache) matchedCount() int {
	return c.count(func(e *feedEntry) bool { return !e.Read && e.matched() && !e.gisting() })
}

// skippedCount is the size of the pile, for the header's link to it, and
// unjudgedCount is what a run would have to get through.
func (c *feedCache) skippedCount() int {
	return c.count(func(e *feedEntry) bool { return !e.Read && e.skipped() && !e.gisting() })
}

func (c *feedCache) unjudgedCount() int {
	return c.count(func(e *feedEntry) bool { return !e.Read && !e.judged() && !e.gisting() })
}

func (c *feedCache) count(want func(*feedEntry) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if want(e) {
			n++
		}
	}
	return n
}

// unreadCount is the whole backlog; unreadApp is one service's share of it,
// with whether that service's own count is known to be short.
func (c *feedCache) unreadCount() int {
	return c.count(func(e *feedEntry) bool { return !e.Read && !e.skipped() && !e.gisting() })
}

func (c *feedCache) unreadApp(app string) (int, bool) {
	n := c.count(func(e *feedEntry) bool { return !e.Read && !e.skipped() && !e.gisting() && e.App == app })
	c.mu.Lock()
	defer c.mu.Unlock()
	return n, c.status[app].Capped
}

// unreadNew counts the unread items that seen does not know about, which is how
// a briefing tells whether it has been overtaken: comparing totals could not,
// since reading a few and fetching a few leaves the count where it was. seen
// holds the feed keys the briefing was written from, and an empty app is every
// source at once, for the briefing that read the whole feed.
func (c *feedCache) unreadNew(app string, seen map[string]bool) int {
	return c.count(func(e *feedEntry) bool {
		return !e.Read && !e.skipped() && !e.gisting() && (app == "" || e.App == app) && !seen[core.Key(e.App, e.ID)]
	})
}

// drop removes these entries outright and reports how many it found. It is for
// items that should never have been backlog at all — a block keyword added
// after the fact catches what is already cached — rather than for anything you
// have read, which prune deals with in its own time.
func (c *feedCache) drop(keys []string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	gone := map[string]bool{}
	for _, k := range keys {
		if _, ok := c.byKey[k]; ok {
			gone[k] = true
			delete(c.byKey, k)
		}
	}
	if len(gone) == 0 {
		return 0
	}
	kept := c.entries[:0]
	for _, e := range c.entries {
		if gone[core.Key(e.App, e.ID)] {
			continue
		}
		kept = append(kept, e)
	}
	c.entries = kept
	c.rev++
	return len(gone)
}

// has reports whether the cache holds this item at all, read or not. The sweep
// asks it about x's other timeline: a tweet already filed under one of the two
// is not backlog for the other (see dropTwins).
func (c *feedCache) has(app, id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.byKey[core.Key(app, id)]
	return ok
}

// item returns one cached item, for a save button that posts back only app+id.
func (c *feedCache) item(app, id string, now time.Time) (core.Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[core.Key(app, id)]
	if !ok {
		return core.Item{}, false
	}
	return e.Wire.Item(now), true
}

// byKeys returns the cached items under these feed keys, read or not, unsorted.
// Read ones are kept on purpose: this is how a briefing is written a second time
// over the batch it already read, and marking a few of them off in between must
// not quietly change what gets re-read. Ages are recomputed as unread does.
func (c *feedCache) byKeys(keys map[string]bool, now time.Time) []core.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]core.Item, 0, len(keys))
	for k := range keys {
		e, ok := c.byKey[k]
		if !ok {
			continue
		}
		it := e.Wire.Item(now)
		if !it.At.IsZero() {
			it.Age = humanAgo(it.At)
		}
		out = append(out, it)
	}
	return out
}

// markRead records your read of these ids and returns the ones the cache has
// never heard of, which the caller passes straight to the app instead. Synced
// is deliberately untouched: an entry a drain already reported upstream needs
// no second call, and one that doesn't stays queued for the flusher.
func (c *feedCache) markRead(app string, ids []string, now time.Time) []string {
	stamp := now.UTC().Format(time.RFC3339)
	c.mu.Lock()
	defer c.mu.Unlock()
	var unknown []string
	for _, id := range ids {
		e, ok := c.byKey[core.Key(app, id)]
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		if !e.Read {
			e.Read, e.ReadAt = true, stamp
			c.rev++
		}
	}
	return unknown
}

// markUnread puts one cached item back in tui's backlog. Synced stays as it
// was: upstream may already know about the read, but this local queue does not
// need to send the same read again when the item is dealt a second time.
func (c *feedCache) markUnread(app, id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[core.Key(app, id)]
	if !ok || !e.Read {
		return false
	}
	e.Read = false
	e.ReadAt = ""
	c.rev++
	return true
}

// markSynced records that the app itself now has these read marks.
func (c *feedCache) markSynced(app string, ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		if e, ok := c.byKey[core.Key(app, id)]; ok && !e.Synced {
			e.Synced = true
			c.rev++
		}
	}
}

// unsynced groups the read marks the apps have not been told about yet, so the
// flusher can retry them for as long as it takes.
func (c *feedCache) unsynced() map[string][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]string{}
	for _, e := range c.entries {
		if e.Read && !e.Synced {
			out[e.App] = append(out[e.App], e.ID)
		}
	}
	return out
}

func (c *feedCache) setStatus(app string, st appStatus) {
	c.mu.Lock()
	c.status[app] = st
	c.rev++
	c.mu.Unlock()
}

func (c *feedCache) statusOf(app string) appStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status[app]
}

func (c *feedCache) setSwept(t time.Time) {
	c.mu.Lock()
	c.swept = t
	c.rev++
	c.mu.Unlock()
}

func (c *feedCache) sweptAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.swept
}

// trouble reports which of apps a sweep could not reach, the warning line for
// the ones whose session has expired, and whether any service's backlog is
// deeper than the sweep reached.
func (c *feedCache) trouble(apps []string) (failed []string, warn string, capped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range apps {
		st := c.status[a]
		if st.Capped {
			capped = true
		}
		if st.Err == "" {
			continue
		}
		failed = append(failed, a)
		if st.Stale {
			// The login to redo is the plugin's, not the source's: For You has no
			// session of its own to re-run anything for.
			plugin, _ := pluginOf(a)
			warn = trimJoin(warn, appSaying(a)+" session is stale — re-run `tui "+plugin+" --auth`.")
		}
	}
	return failed, warn, capped
}

func trimJoin(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

// save snapshots under the lock and persists outside it, so a large write does
// not hold up a page render. A revision check drops a snapshot that a newer one
// has already overtaken.
func (c *feedCache) save() error {
	if c.path == "" {
		return nil
	}
	c.mu.Lock()
	c.rev++
	rev := c.rev
	items := make([]*feedEntry, 0, len(c.entries))
	for _, e := range c.entries {
		copy := *e
		items = append(items, &copy)
	}
	f := feedFile{Items: items, Status: make(map[string]appStatus, len(c.status))}
	for app, st := range c.status {
		f.Status[app] = st
	}
	if !c.swept.IsZero() {
		f.Swept = c.swept.UTC().Format(time.RFC3339)
	}
	c.mu.Unlock()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if rev <= c.written {
		return nil // a later snapshot already landed
	}
	if c.db != nil {
		if err := c.db.replaceFeed(f); err != nil {
			return err
		}
		c.written = rev
		return nil
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	c.written = rev
	return nil
}
