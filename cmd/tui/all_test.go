package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
)

// xFeed builds an allModel whose feed holds the given items, for exercising the
// screen's key handling without a server behind it.
func xFeed(t *testing.T, items []core.Item) allModel {
	t.Helper()
	m := newAllModel("/tmp")
	th := core.NewTheme(true)
	m.feed = core.NewFeed(th, true)
	m.feed.SetItems(items, true)
	return m
}

func TestAllSaveReplacesExplicitMarkKey(t *testing.T) {
	item := core.Item{App: "x", ID: "1", Title: "post"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/save" || r.FormValue("save") != "1" {
			t.Errorf("save request = %s, save=%q", r.URL.Path, r.FormValue("save"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	m := xFeed(t, []core.Item{item})
	m.server = server.URL

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if cmd == nil {
		t.Fatal("s should send the selected item to the server")
	}
	msg, ok := cmd().(savedMsg)
	if !ok || msg.err != nil || msg.item.Key() != item.Key() {
		t.Fatalf("save result = %#v", msg)
	}
	got, _ = got.Update(msg)
	if got.status != "Saved." {
		t.Fatalf("save status = %q", got.status)
	}

	got, cmd = got.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd != nil || got.feed.IsRead(item.Key()) {
		t.Fatal("r should no longer mark an item read")
	}
}

func TestAllHelpKeepsReadBehaviorImplicit(t *testing.T) {
	keys := defaultAllKeys()
	for _, binding := range []key.Binding{keys.Up, keys.Down, keys.Expand} {
		if strings.Contains(binding.Help().Desc, "marks read") {
			t.Fatalf("help still spells out implicit read behavior: %q", binding.Help().Desc)
		}
	}
}
