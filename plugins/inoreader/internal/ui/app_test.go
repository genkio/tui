package ui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/inoreader/internal/inoreader"
)

func testModel() Model {
	m := Model{feed: core.NewFeed(core.NewTheme(true), false), keys: defaultKeys()}
	m.feed.SetSize(40, 10)
	m.feed.SetItems(inoreader.ToItems([]inoreader.Article{{ID: "1", Title: "a"}, {ID: "2", Title: "b"}}), true)
	return m
}

func key1() string { return core.Key("inoreader", "1") }

func TestKeptArticleResistsMarking(t *testing.T) {
	m := testModel()
	m.feed.ToggleKeep() // pin the cursored article (id "1")

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = got.(Model)
	if cmd != nil || m.feed.IsRead(key1()) {
		t.Fatal("r must not mark a kept article read")
	}

	got, cmd = m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = got.(Model)
	if cmd != nil || m.feed.IsRead(key1()) {
		t.Fatal("space must not mark a kept article read")
	}
	got, _ = m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "}) // collapse again
	m = got.(Model)

	got, cmd = m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("j past a kept article must not fire markRead")
	}
	if m.feed.IsRead(key1()) {
		t.Fatal("j past a kept article greyed it")
	}
	if m.feed.Cursor() != 1 {
		t.Fatalf("cursor = %d, want 1", m.feed.Cursor())
	}
}

func TestKeepOnReadArticleMarksServerUnread(t *testing.T) {
	m := testModel()

	// Pinning an article that is still unread needs no server call.
	got, cmd := m.handleKey(tea.KeyPressMsg{Code: 'K', Text: "K"})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("pinning an unread article must stay local")
	}
	got, _ = m.handleKey(tea.KeyPressMsg{Code: 'K', Text: "K"}) // unpin
	m = got.(Model)

	// Pinning a greyed article must also restore it to unread on the server,
	// or a refresh drops it.
	m.feed.MarkRead(key1())
	got, cmd = m.handleKey(tea.KeyPressMsg{Code: 'K', Text: "K"})
	m = got.(Model)
	if cmd == nil {
		t.Fatal("pinning a read article must fire a server mark-unread")
	}
	if m.feed.IsRead(key1()) || !m.feed.IsKept(key1()) {
		t.Fatal("article should be unread and pinned locally")
	}

	upd, _ := m.Update(unmarkedMsg{id: "1", err: errors.New("boom")})
	m = upd.(Model)
	if !m.feed.IsRead(key1()) || m.feed.IsKept(key1()) {
		t.Fatal("failed server mark-unread must re-grey and unpin")
	}
}

func TestUpMarksLeavingArticleRead(t *testing.T) {
	m := testModel()
	m.feed.MoveCursor(1) // cursor -> 1 (id "2")

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = got.(Model)
	if cmd == nil || !m.feed.IsRead(core.Key("inoreader", "2")) {
		t.Fatal("k should mark the article it leaves read")
	}
	if m.feed.Cursor() != 0 {
		t.Fatalf("cursor = %d, want 0", m.feed.Cursor())
	}

	// Already at the top: the cursor cannot leave, so nothing is marked.
	got, cmd = m.handleKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = got.(Model)
	if cmd != nil || m.feed.IsRead(key1()) {
		t.Fatal("k at the top must not mark")
	}
}

func TestExpandMarksArticleRead(t *testing.T) {
	m := testModel()

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = got.(Model)
	if cmd == nil || !m.feed.IsRead(key1()) {
		t.Fatal("expanding should mark the article read")
	}
	if !m.feed.IsExpanded(key1()) {
		t.Fatal("article did not expand")
	}

	got, cmd = m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("collapsing must not fire another markRead")
	}
	if m.feed.IsExpanded(key1()) {
		t.Fatal("article did not collapse")
	}
}
