package ui

import (
	"path/filepath"
	"testing"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/x/internal/readstore"
	"github.com/genkio/tui/plugins/x/internal/x"
)

func testTweets(ids ...string) []x.Tweet {
	out := make([]x.Tweet, len(ids))
	for i, id := range ids {
		out[i] = x.Tweet{ID: id, Handle: "u" + id, Text: "post " + id}
	}
	return out
}

func itemIDs(f core.Feed) []string {
	out := make([]string, 0, f.Len())
	for _, it := range f.Items() {
		out = append(out, it.ID)
	}
	return out
}

func newTestModel(t *testing.T, unreadOnly bool) Model {
	t.Helper()
	m := Model{
		read:       readstore.Load(filepath.Join(t.TempDir(), "read.json")),
		feed:       core.NewFeed(core.NewTheme(true), false),
		unreadOnly: unreadOnly,
		cache:      map[x.Tab][]x.Tweet{},
		tab:        x.Following,
		keys:       defaultKeys(),
	}
	m.feed.SetSize(80, 20)
	return m
}

func keyX(id string) string { return core.Key("x", id) }

// Marking a post read while scrolling greys it but keeps it in place; only a
// later fetch (or view toggle) applies the unread-only filter.
func TestMarkReadStaysUntilRefetch(t *testing.T) {
	m := newTestModel(t, true)
	m.showTweets(testTweets("1", "2", "3"), true)

	m.markLocal("2")
	if !m.feed.IsRead(keyX("2")) {
		t.Fatal("post 2 should read after markLocal")
	}
	if got := m.feed.Len(); got != 3 {
		t.Fatalf("marking read mid-session should not drop the post: len=%d, want 3", got)
	}

	m.showTweets(testTweets("1", "2", "3"), false) // a refresh re-applies the filter
	if got := itemIDs(m.feed); len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Fatalf("read post 2 should be filtered out after refetch, got %v", got)
	}
}

// Show-all mode never hides read posts; it only greys them.
func TestShowAllKeepsReadVisible(t *testing.T) {
	m := newTestModel(t, false)
	m.showTweets(testTweets("1", "2", "3"), true)
	m.markLocal("2")
	m.showTweets(testTweets("1", "2", "3"), false)
	if got := m.feed.Len(); got != 3 {
		t.Fatalf("show-all should keep read posts: len=%d, want 3", got)
	}
	if !m.feed.IsRead(keyX("2")) {
		t.Fatal("post 2 should still read in show-all mode")
	}
}

// Flipping unread-only re-filters the current list without a refetch.
func TestToggleUnreadOnlyLive(t *testing.T) {
	m := newTestModel(t, true)
	m.cache[m.tab] = testTweets("1", "2", "3")
	m.showTweets(m.cache[m.tab], true)
	m.read.Mark("2")
	m.showTweets(m.cache[m.tab], false) // a state where 2 is already read at fetch time
	if got := m.feed.Len(); got != 2 {
		t.Fatalf("unread-only should hide the read post, got %v", itemIDs(m.feed))
	}
	m.unreadOnly = false
	m.showTweets(m.cache[m.tab], false)
	if got := m.feed.Len(); got != 3 {
		t.Fatalf("show-all should reveal the read post, got %v", itemIDs(m.feed))
	}
}

// Keeping a post unread un-marks it read and pins it so the filter spares it.
func TestKeepUnread(t *testing.T) {
	m := newTestModel(t, true)
	m.cache[m.tab] = testTweets("1", "2", "3")
	m.showTweets(m.cache[m.tab], true)
	m.markLocal("2")

	m.feed.MoveCursor(1) // cursor -> post 2
	kept, ok := m.feed.ToggleKeep()
	if !ok || !kept {
		t.Fatalf("ToggleKeep on post 2 should report kept: kept=%v ok=%v", kept, ok)
	}
	m.read.Unmark("2") // the Keep handler restores unread in the store too
	if m.feed.IsRead(keyX("2")) {
		t.Fatal("keeping unread should clear the read mark")
	}

	m.showTweets(m.cache[m.tab], false) // refetch without reset keeps the pin
	if got := m.feed.Len(); got != 3 {
		t.Fatalf("kept post should survive the unread-only filter, got %v", itemIDs(m.feed))
	}
}
