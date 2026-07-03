package main

import (
	"testing"
	"time"
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
	if m.badge(0) == allRead {
		t.Fatal("a nonzero count should differ from the all-read badge")
	}
}
