package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// feedCache is the web view's backlog: every unread item the sweeper has ever
// fetched, kept whole on disk so serving a page is a file read instead of six
// concurrent scrapes. It is the counterpart of savedStore and lives beside it,
// so --state-dir carries both.
//
// Two things fall out of accumulating rather than re-fetching. The unread count
// becomes real: a service that only ever hands over its newest page (or, for
// Inoreader, cannot page at all) still adds to a total that grows across
// sweeps. And read state becomes ours: an entry carries whether you have read
// it and whether that mark has reached the app it came from, so a mark is
// recorded instantly and flushed upstream in the background.
//
// One process writes it. The server owns the file while it runs; the launcher
// only ever reads it.
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
}

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

const (
	// feedCacheKeep is how long a read entry lingers before pruning. It has to
	// outlast a re-fetch: an app whose --json still lists an item we have read
	// would otherwise resurrect it as unread.
	feedCacheKeep = 14 * 24 * time.Hour
	// maxFeedEntries bounds the file. It is a whole-file rewrite on every sweep
	// and it may sit in a synced folder, so the backlog is deliberately finite.
	maxFeedEntries = 6000
)

// loadFeedCache reads the cache, or the default location when path is empty. A
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
	return c
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
func (c *feedCache) unread(now time.Time, skipApp string) []core.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]core.Item, 0, len(c.entries))
	for _, e := range c.entries {
		if e.Read || e.App == skipApp {
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

// unreadCount is the whole backlog; unreadApp is one service's share of it,
// with whether that service's own count is known to be short.
func (c *feedCache) unreadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if !e.Read {
			n++
		}
	}
	return n
}

func (c *feedCache) unreadApp(app string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if !e.Read && e.App == app {
			n++
		}
	}
	return n, c.status[app].Capped
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
			warn = trimJoin(warn, a+" session is stale — re-run `tui "+a+" --auth`.")
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

// save writes the cache atomically. It marshals under the lock and writes
// outside it, so a multi-megabyte rewrite doesn't hold up a page render; a
// revision check drops a snapshot that a newer one has already overtaken.
func (c *feedCache) save() error {
	if c.path == "" {
		return nil
	}
	c.mu.Lock()
	dropped := c.prune()
	c.rev++
	rev := c.rev
	f := feedFile{Items: c.entries, Status: c.status}
	if !c.swept.IsZero() {
		f.Swept = c.swept.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(f)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if dropped > 0 {
		logf("cache full: dropped %d unread item(s) to stay under %d", dropped, maxFeedEntries)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if rev <= c.written {
		return nil // a later snapshot already landed
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

// prune bounds the file: read entries past feedCacheKeep go first, then the
// oldest read ones, and only then unread ones. It reports how many unread
// entries it had to drop, which is worth saying out loud — for a drained
// service that is backlog nobody can fetch again. Caller holds the lock.
func (c *feedCache) prune() int {
	cut := time.Now().Add(-feedCacheKeep).UTC().Format(time.RFC3339)
	kept := c.entries[:0]
	for _, e := range c.entries {
		if e.Read && e.Synced && e.ReadAt != "" && e.ReadAt < cut {
			delete(c.byKey, core.Key(e.App, e.ID))
			continue
		}
		kept = append(kept, e)
	}
	c.entries = kept

	over := len(c.entries) - maxFeedEntries
	if over <= 0 {
		return 0
	}
	// Oldest first, and read before unread within that: entries are in the
	// order they were first seen, so two passes over the slice do it.
	droppedUnread := 0
	for _, readPass := range []bool{true, false} {
		for i := 0; i < len(c.entries) && over > 0; i++ {
			e := c.entries[i]
			if e == nil || e.Read != readPass {
				continue
			}
			if !e.Read {
				droppedUnread++
			}
			delete(c.byKey, core.Key(e.App, e.ID))
			c.entries[i] = nil
			over--
		}
	}
	kept = c.entries[:0]
	for _, e := range c.entries {
		if e != nil {
			kept = append(kept, e)
		}
	}
	c.entries = kept
	return droppedUnread
}
