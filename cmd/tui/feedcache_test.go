package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

func newTestCache(t *testing.T) *feedCache {
	t.Helper()
	return loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
}

func item(app, id, title string) core.Item {
	return core.Item{App: app, ID: id, Title: title}
}

// A sweep accumulates rather than replaces: that is what makes the count real
// for a service that only ever hands over its newest page.
func TestFeedCacheAccumulates(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()

	if n := c.upsert([]core.Item{item("x", "1", "a"), item("x", "2", "b")}, now); n != 2 {
		t.Fatalf("first sweep: got %d new, want 2", n)
	}
	// Second sweep overlaps the first; only the genuinely new item counts.
	if n := c.upsert([]core.Item{item("x", "2", "b"), item("x", "3", "c")}, now); n != 1 {
		t.Fatalf("second sweep: got %d new, want 1", n)
	}
	if got := c.unreadCount(); got != 3 {
		t.Fatalf("backlog is %d, want 3", got)
	}
}

// Re-fetching something already triaged must not resurrect it, however many
// times the service keeps listing it.
func TestFeedCacheUpsertKeepsReadState(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a")}, now)
	c.markRead("x", []string{"1"}, now)
	c.markSynced("x", []string{"1"})

	c.upsert([]core.Item{item("x", "1", "a, edited")}, now)
	if got := c.unreadCount(); got != 0 {
		t.Fatalf("a re-fetch resurrected a read item: unread is %d", got)
	}
	// The content is refreshed, though: a body can arrive on a later pass.
	it, ok := c.item("x", "1", now)
	if !ok || it.Title != "a, edited" {
		t.Fatalf("expected refreshed content, got %+v (ok=%v)", it, ok)
	}
}

// markRead reports back the ids it has never seen, so the caller can hand them
// to the app directly instead of dropping them (x's uncached For You).
func TestFeedCacheMarkReadReportsUnknown(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a")}, now)

	unknown := c.markRead("x", []string{"1", "999"}, now)
	if len(unknown) != 1 || unknown[0] != "999" {
		t.Fatalf("unknown ids = %v, want [999]", unknown)
	}
	if got := c.unreadCount(); got != 0 {
		t.Fatalf("unread is %d, want 0", got)
	}
}

func TestFeedCacheMarkUnreadKeepsUpstreamState(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a")}, now)
	c.markRead("x", []string{"1"}, now)
	c.markSynced("x", []string{"1"})

	if !c.markUnread("x", "1") {
		t.Fatal("a read item should become unread")
	}
	e := c.byKey[core.Key("x", "1")]
	if e.Read || e.ReadAt != "" || !e.Synced {
		t.Fatalf("unread entry = %+v, want local unread with upstream state preserved", e)
	}
	if c.markUnread("x", "1") {
		t.Fatal("marking an unread item unread should be a no-op")
	}

	c.markRead("x", []string{"1"}, now)
	if pending := c.unsynced(); len(pending) != 0 {
		t.Fatalf("an upstream read that already landed should not be resent: %v", pending)
	}
}

// A read mark is queued for its app until it lands, and a drained entry needs
// no queueing at all: the service was told at fetch time.
func TestFeedCacheUnsynced(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a"), item("inoreader", "2", "b")}, now)

	// Inoreader's entry was drained: upstream already treats it as read.
	c.markSynced("inoreader", []string{"2"})
	c.markRead("x", []string{"1"}, now)
	c.markRead("inoreader", []string{"2"}, now)

	pending := c.unsynced()
	if len(pending) != 1 || len(pending["x"]) != 1 || pending["x"][0] != "1" {
		t.Fatalf("pending = %v, want only x/1", pending)
	}

	c.markSynced("x", []string{"1"})
	if pending := c.unsynced(); len(pending) != 0 {
		t.Fatalf("nothing should be pending: %v", pending)
	}
}

// The window and the count are different numbers, and each app's share is its
// own, because the picker asks per app.
func TestFeedCacheUnreadPerApp(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a"), item("inoreader", "2", "b"), item("inoreader", "3", "c")}, now)
	c.markRead("inoreader", []string{"3"}, now)

	if n, capped := c.unreadApp("inoreader"); n != 1 || capped {
		t.Fatalf("inoreader = %d (capped %v), want 1 uncapped", n, capped)
	}
	c.setStatus("inoreader", appStatus{Capped: true})
	if _, capped := c.unreadApp("inoreader"); !capped {
		t.Fatal("a capped sweep should carry into the count")
	}
	if n, _ := c.unreadApp("folo"); n != 0 {
		t.Fatalf("folo = %d, want 0", n)
	}
}

// unread can leave one app out, which is how For You is served: the backlog
// minus x, plus a live look at x.
func TestFeedCacheUnreadSkipsApp(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a"), item("folo", "2", "b")}, now)
	got := c.unread(now, "x")
	if len(got) != 1 || got[0].App != "folo" {
		t.Fatalf("got %+v, want only the folo item", got)
	}
}

// An age is recomputed from the publish time, so something cached last week
// doesn't still read "2h".
func TestFeedCacheRecomputesAge(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	it := item("x", "1", "a")
	it.At = now.Add(-50 * time.Hour)
	it.Age = "2h" // what the service said when it was fetched
	c.upsert([]core.Item{it}, now)

	got := c.unread(now, "")
	if len(got) != 1 || got[0].Age != "2d ago" {
		t.Fatalf("age = %q, want \"2d ago\"", got[0].Age)
	}
}

// The file round-trips, and a save is atomic (no .tmp left behind).
func TestFeedCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feed.json")
	now := time.Now()

	c := loadFeedCache(path)
	c.upsert([]core.Item{item("x", "1", "a"), item("folo", "2", "b")}, now)
	c.markRead("folo", []string{"2"}, now)
	c.setStatus("x", appStatus{At: now.UTC().Format(time.RFC3339)})
	c.setSwept(now)
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("a temp file survived the save")
	}

	back := loadFeedCache(path)
	if got := back.unreadCount(); got != 1 {
		t.Fatalf("unread after reload = %d, want 1", got)
	}
	if back.sweptAt().Unix() != now.Unix() {
		t.Fatalf("swept = %v, want %v", back.sweptAt(), now)
	}
	if back.statusOf("x").At == "" {
		t.Fatal("per-app status did not survive the reload")
	}
	if _, ok := back.item("x", "1", now); !ok {
		t.Fatal("the cached item did not survive the reload")
	}
}

// A corrupt or missing file is an empty cache, never an error: the worst case
// is one slow sweep before the page has anything to serve.
func TestFeedCacheToleratesRubbish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.json")
	if err := os.WriteFile(path, []byte("{{{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadFeedCache(path).unreadCount(); got != 0 {
		t.Fatalf("unread = %d, want 0", got)
	}
	if got := loadFeedCache(filepath.Join(t.TempDir(), "nope.json")).unreadCount(); got != 0 {
		t.Fatal("a missing file should read as an empty cache")
	}
}

func TestFeedCacheRetainsOldAndLargeHistory(t *testing.T) {
	const formerLimit = 6000
	c := newTestCache(t)
	now := time.Now()
	items := make([]core.Item, 0, formerLimit+1)
	for i := range formerLimit + 1 {
		items = append(items, item("x", fmt.Sprint(i), "post"))
	}
	c.upsert(items, now)
	old := now.Add(-30 * 24 * time.Hour)
	c.markRead("x", []string{"0"}, old)
	c.markSynced("x", []string{"0"})
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if got := len(c.entries); got != formerLimit+1 {
		t.Fatalf("kept %d entries, want %d", got, formerLimit+1)
	}
	if _, ok := c.item("x", "0", now); !ok {
		t.Fatal("old read entry was deleted")
	}
}

// trouble is what the header's dots and warning are built from.
func TestFeedCacheTrouble(t *testing.T) {
	c := newTestCache(t)
	c.setStatus("x", appStatus{At: "2026-01-01T00:00:00Z"})
	c.setStatus("folo", appStatus{Err: "boom"})
	c.setStatus("inoreader", appStatus{Err: "401", Stale: true, Capped: true})

	failed, warn, capped := c.trouble([]string{"x", "folo", "inoreader"})
	if len(failed) != 2 {
		t.Fatalf("failed = %v, want folo and inoreader", failed)
	}
	if !strings.Contains(warn, "inoreader session is stale") {
		t.Fatalf("warn = %q, want the re-auth hint", warn)
	}
	if strings.Contains(warn, "folo session") {
		t.Fatalf("warn = %q: a plain failure is not a stale session", warn)
	}
	if !capped {
		t.Fatal("a capped service should be reported")
	}
	// An app that isn't logged in isn't asked about.
	if failed, _, _ := c.trouble([]string{"x"}); len(failed) != 0 {
		t.Fatalf("failed = %v, want none", failed)
	}
}

// The on-disk shape is what the launcher reads to answer for a drained service,
// so it is worth pinning.
func TestFeedCacheFileShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.json")
	now := time.Now()
	c := loadFeedCache(path)
	c.upsert([]core.Item{item("inoreader", "1", "a")}, now)
	c.markSynced("inoreader", []string{"1"})
	c.setSwept(now)
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f feedFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 1 || f.Items[0].App != "inoreader" || !f.Items[0].Synced || f.Items[0].Read {
		t.Fatalf("unexpected entry: %+v", f.Items[0])
	}
	if f.Items[0].FirstSeen == "" {
		t.Error("an entry should record when it was first seen")
	}
	if f.Swept == "" {
		t.Error("the file should record when it was last swept")
	}
}
