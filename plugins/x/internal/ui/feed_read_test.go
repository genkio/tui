package ui

import (
	"path/filepath"
	"testing"

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

func ids(tw []x.Tweet) []string {
	out := make([]string, len(tw))
	for i, t := range tw {
		out[i] = t.ID
	}
	return out
}

func newTestFeed(t *testing.T, unreadOnly bool) feedModel {
	t.Helper()
	f := newFeed(readstore.Load(filepath.Join(t.TempDir(), "read.json")), unreadOnly)
	f.setSize(80, 20)
	return f
}

// Marking a post read while scrolling greys it but keeps it in place; only a
// later fetch (or view toggle) applies the unread-only filter.
func TestMarkReadStaysUntilRefetch(t *testing.T) {
	f := newTestFeed(t, true)
	f.setTweets(testTweets("1", "2", "3"), true)

	f.markReadLocal("2")
	if !f.isRead("2") {
		t.Fatal("post 2 should read after markReadLocal")
	}
	if got := len(f.tweets); got != 3 {
		t.Fatalf("marking read mid-session should not drop the post: len=%d, want 3", got)
	}

	f.setTweets(testTweets("1", "2", "3"), false) // a refresh re-applies the filter
	if got := ids(f.tweets); len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Fatalf("read post 2 should be filtered out after refetch, got %v", got)
	}
}

// Show-all mode never hides read posts; it only greys them.
func TestShowAllKeepsReadVisible(t *testing.T) {
	f := newTestFeed(t, false)
	f.setTweets(testTweets("1", "2", "3"), true)
	f.markReadLocal("2")
	f.setTweets(testTweets("1", "2", "3"), false)
	if got := len(f.tweets); got != 3 {
		t.Fatalf("show-all should keep read posts: len=%d, want 3", got)
	}
	if !f.isRead("2") {
		t.Fatal("post 2 should still read in show-all mode")
	}
}

// toggleUnreadOnly flips filtering live without a refetch.
func TestToggleUnreadOnlyLive(t *testing.T) {
	f := newTestFeed(t, true)
	f.setTweets(testTweets("1", "2", "3"), true)
	f.read.Mark("2")
	f.applyFilter() // simulate a state where 2 is already read at fetch time
	if len(f.tweets) != 2 {
		t.Fatalf("unread-only should hide the read post, got %v", ids(f.tweets))
	}
	if unread := f.toggleUnreadOnly(); unread {
		t.Fatal("toggle should have switched to show-all")
	}
	if len(f.tweets) != 3 {
		t.Fatalf("show-all should reveal the read post, got %v", ids(f.tweets))
	}
}

// Keeping a post unread un-marks it read and pins it so the filter spares it.
func TestKeepUnread(t *testing.T) {
	f := newTestFeed(t, true)
	f.setTweets(testTweets("1", "2", "3"), true)
	f.markReadLocal("2")

	f.cursor = indexOf(f.tweets, "2")
	kept, ok := f.toggleKeep()
	if !ok || !kept {
		t.Fatalf("toggleKeep on post 2 should report kept: kept=%v ok=%v", kept, ok)
	}
	if f.isRead("2") {
		t.Fatal("keeping unread should clear the read mark")
	}

	f.setTweets(testTweets("1", "2", "3"), false) // refetch without reset keeps the pin
	if got := len(f.tweets); got != 3 {
		t.Fatalf("kept post should survive the unread-only filter, got %v", ids(f.tweets))
	}
}

func indexOf(tw []x.Tweet, id string) int {
	for i, t := range tw {
		if t.ID == id {
			return i
		}
	}
	return 0
}
