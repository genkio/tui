package main

import (
	"strings"
	"testing"

	"github.com/genkio/tui/core"
)

// xFeed builds an allModel whose feed holds the given items, for exercising the
// "continue on x For You" offer logic.
func xFeed(t *testing.T, items []core.Item) allModel {
	t.Helper()
	m := newAllModel("/tmp")
	th := core.NewTheme(true)
	m.feed = core.NewFeed(th, true)
	m.feed.SetItems(items, true)
	return m
}

func TestOfferableX(t *testing.T) {
	mk := func(apps []string, xTab string, items []core.Item) allModel {
		m := xFeed(t, items)
		m.apps = apps
		m.xTab = xTab
		return m
	}
	key := func(app, id string) core.Item {
		return core.Item{App: app, ID: id, Title: app + id}
	}

	// x on Following, feed empty → offerable.
	if m := mk([]string{"x", "reddit"}, "following", nil); !m.offerableX() {
		t.Fatal("expected offerable when following and feed empty")
	}
	// Already on For You → not offerable.
	if m := mk([]string{"x"}, "foryou", nil); m.offerableX() {
		t.Fatal("must not offer when already on For You")
	}
	// x not authed → not offerable.
	if m := mk([]string{"reddit"}, "following", nil); m.offerableX() {
		t.Fatal("must not offer when x isn't authed")
	}
	// Some unread remains → not offerable.
	item := key("x", "1")
	m := mk([]string{"x", "reddit"}, "following", []core.Item{item})
	if m.offerableX() {
		t.Fatal("must not offer while something is unread")
	}
	// Everything read → offerable.
	m.feed.MarkRead(item.Key())
	if !m.offerableX() {
		t.Fatal("expected offerable once everything is read")
	}
	// Marking read shows the hint once, and only once.
	m2 := mk([]string{"x"}, "following", []core.Item{key("x", "9")})
	m2.feed.MarkRead(key("x", "9").Key())
	m2.maybeOfferX()
	if !strings.Contains(m2.status, "press f") {
		t.Fatalf("expected the offer hint in status, got %q", m2.status)
	}
	m2.maybeOfferX() // second call must not re-show (status unchanged is fine, just no overwrite)
	if !strings.Contains(m2.status, "press f") {
		t.Fatal("hint should persist, not be cleared")
	}
}
