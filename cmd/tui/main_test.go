package main

import (
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

func TestParseCountToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"12\n", "12"},
		{"0\n", "0"},
		{"75+\n", "75+"},
		{"  42  \n", "42"},
		{"go: downloading something\n7\n", "7"}, // ignore build chatter, take the count
		{"", ""},
		{"no digits here", ""},
		{"12abc", ""}, // must be a whole count-shaped word
	}
	for _, c := range cases {
		if got := parseCountToken(c.in); got != c.want {
			t.Errorf("parseCountToken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{20 * time.Second, "just now"},
		{90 * time.Second, "1m ago"},
		{25 * time.Minute, "25m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := humanAgo(now.Add(-c.ago)); got != c.want {
			t.Errorf("humanAgo(%s ago) = %q, want %q", c.ago, got, c.want)
		}
	}
}

func TestBadgeStates(t *testing.T) {
	m := newModel(".", 5*time.Minute)
	m.apps = []app{{name: "x"}}
	m.authed = []bool{false}

	if got := m.badge(0); got == "" {
		t.Fatal("unauthed badge should render something")
	}

	m.authed = []bool{true}
	checking := m.badge(0)

	m.counts["x"] = "0"
	allRead := m.badge(0)
	if allRead == checking {
		t.Fatal("count 0 should render differently from the checking state")
	}

	m.counts["x"] = "9"
	unread := m.badge(0)
	if unread == allRead {
		t.Fatal("a nonzero count should differ from the all-read badge")
	}

	// A failed poll is a failed health check: red dot wins over the last count.
	m.countErr["x"] = true
	down := m.badge(0)
	if down == unread || !strings.Contains(down, "unreachable") {
		t.Fatalf("a failed poll should show the service as unreachable, got %q", down)
	}

	// A stale session is called out specifically, pointing at re-auth.
	m.countStale["x"] = true
	stale := m.badge(0)
	if !strings.Contains(stale, "stale") {
		t.Fatalf("a stale session should be labeled, got %q", stale)
	}

	// A later successful poll clears the red dot.
	m.countErr["x"] = false
	m.countStale["x"] = false
	if got := m.badge(0); got != unread {
		t.Fatalf("recovery should restore the count badge, got %q", got)
	}

	// A count that came from the web server's backlog says so: those items are
	// triaged on --web, and opening the app here won't show them.
	m.countWeb["x"] = true
	if got := m.badge(0); !strings.Contains(got, "unread on --web") {
		t.Fatalf("a backlog count should be labeled as such, got %q", got)
	}
}

// A drained service answers zero to its own --count, so the picker asks the
// web server's backlog instead. Nothing else does, and a machine that has
// never run --web is unaffected.
func TestBacklogCount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TUI_STATE_DIR", dir)

	// No cache yet: fall through to the service's own --count.
	if _, ok := backlogCount("inoreader"); ok {
		t.Fatal("an unswept cache should not answer for the service")
	}

	c := loadFeedCache("")
	now := time.Now()
	c.upsert([]core.Item{
		{App: "inoreader", ID: "1", Title: "a"},
		{App: "inoreader", ID: "2", Title: "b"},
		{App: "x", ID: "3", Title: "c"},
	}, now)
	c.markRead("inoreader", []string{"2"}, now)
	c.setSwept(now)
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	got, ok := backlogCount("inoreader")
	if !ok || got != "1" {
		t.Fatalf("backlogCount(inoreader) = %q, %v; want \"1\", true", got, ok)
	}
	// A service that keeps its own unread list answers for itself.
	if _, ok := backlogCount("x"); ok {
		t.Fatal("x is not drained, so its own --count is the truth")
	}

	// A drain that ran out of rounds is short of the truth, and says so.
	c.setStatus("inoreader", appStatus{Capped: true})
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if got, _ := backlogCount("inoreader"); got != "1+" {
		t.Fatalf("a capped backlog should render as \"1+\", got %q", got)
	}
}
