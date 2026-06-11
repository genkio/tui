package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genkio/inoreader-tui/internal/inoreader"
)

func TestTruncateDisplayWidth(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("no-op truncate: %q", got)
	}
	if got := truncate("hello world", 5); lipgloss.Width(got) > 5 {
		t.Errorf("truncate overflow: %q width %d", got, lipgloss.Width(got))
	}
	// CJK runes are width 2; truncating must respect that, not rune count.
	got := truncate("一二三四五六", 6)
	if w := lipgloss.Width(got); w > 6 {
		t.Errorf("CJK truncate overflow: %q width %d", got, w)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis: %q", got)
	}
}

func TestWrapTextNeverOverflows(t *testing.T) {
	inputs := []string{
		"the quick brown fox jumps over the lazy dog",
		"https://example.com/a/very/long/url/that/cannot/be/broken/on/spaces/at/all",
		"这是一段没有空格的中文文本需要按显示宽度硬换行处理才能正确显示",
		"mixed 中文 and english words together in one line for wrapping",
	}
	const width = 12
	for _, in := range inputs {
		for _, line := range wrapText(in, width) {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("line %q width %d exceeds %d (input %q)", line, w, width, in)
			}
		}
	}
}

func TestWrapTextPreservesParagraphs(t *testing.T) {
	got := wrapText("a\n\nb", 20)
	if len(got) != 3 || got[0] != "a" || got[1] != "" || got[2] != "b" {
		t.Errorf("paragraph break lost: %#v", got)
	}
}

// newTestFeed returns a small-viewport feed whose first article body overflows.
func newTestFeed(t *testing.T) feedModel {
	t.Helper()
	f := newFeed()
	f.setSize(40, 6)
	long := strings.TrimSpace(strings.Repeat("lorem ipsum dolor sit amet ", 30))
	f.setArticles([]inoreader.Article{
		{ID: "1", Title: "first", Content: long},
		{ID: "2", Title: "second", Content: "short"},
	}, true)
	return f
}

func TestScrollExpandedReadsLongBodyLineByLine(t *testing.T) {
	f := newTestFeed(t)

	if f.scrollExpanded(1) {
		t.Fatal("collapsed article must not consume the keypress")
	}

	f.toggleCursor() // expand first article
	_, bottom := f.cursorBlock()
	if bottom < f.vp.Height() {
		t.Fatalf("fixture body does not overflow: bottom=%d height=%d", bottom, f.vp.Height())
	}

	if !f.scrollExpanded(1) || f.yoff != 1 {
		t.Fatalf("first j should scroll one line, yoff=%d", f.yoff)
	}

	steps := 0
	for f.scrollExpanded(1) {
		if steps++; steps > 10_000 {
			t.Fatal("scrolling never reached the tail")
		}
	}
	if want := bottom - f.vp.Height() + 1; f.yoff != want {
		t.Fatalf("tail offset = %d, want %d", f.yoff, want)
	}
	if f.cursor != 0 {
		t.Fatalf("cursor moved while scrolling: %d", f.cursor)
	}

	// Re-render (e.g. mark read) must not lose the reading position.
	f.markReadLocal("1")
	if want := bottom - f.vp.Height() + 1; f.yoff != want {
		t.Fatalf("re-render reset reading offset: yoff=%d, want %d", f.yoff, want)
	}

	if !f.scrollExpanded(-1) {
		t.Fatal("k should scroll back up from the tail")
	}

	// Tail visible again: the next j must hand over to cursor movement.
	f.scrollExpanded(1)
	if f.scrollExpanded(1) {
		t.Fatal("j past the tail should move the cursor, not scroll")
	}
	f.moveCursor(1)
	if f.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", f.cursor)
	}
	if f.expanded["1"] {
		t.Fatal("leaving the article should collapse it")
	}
}

func TestToggleCollapsesMidBody(t *testing.T) {
	f := newTestFeed(t)
	f.toggleCursor() // expand
	for i := 0; i < 3; i++ {
		if !f.scrollExpanded(1) {
			t.Fatal("expected room to scroll")
		}
	}

	f.toggleCursor() // space mid-body collapses
	if f.expanded["1"] {
		t.Fatal("article still expanded")
	}
	if f.yoff != f.starts[0] {
		t.Fatalf("viewport not back on the header: yoff=%d", f.yoff)
	}
}

func TestKeepPinsArticleUnread(t *testing.T) {
	f := newTestFeed(t)
	f.markReadLocal("1")
	if kept, ok := f.toggleKeep(); !ok || !kept {
		t.Fatal("toggleKeep should pin the cursored article")
	}
	if f.isRead("1") {
		t.Fatal("keeping should restore the article to unread")
	}
	if kept, _ := f.toggleKeep(); kept {
		t.Fatal("second toggle should unlock")
	}

	f.toggleKeep()
	f.setArticles(f.articles, false)
	if !f.isKept("1") {
		t.Fatal("auto-refresh dropped the keep")
	}
	f.setArticles(f.articles, true)
	if f.isKept("1") {
		t.Fatal("manual refresh should clear keeps")
	}
}

func TestKeptArticleResistsMarking(t *testing.T) {
	m := Model{feed: newFeed(), keys: defaultKeys()}
	m.feed.setSize(40, 10)
	m.feed.setArticles([]inoreader.Article{{ID: "1", Title: "a"}, {ID: "2", Title: "b"}}, true)
	m.feed.toggleKeep()

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = got.(Model)
	if cmd != nil || m.feed.isRead("1") {
		t.Fatal("r must not mark a kept article read")
	}

	got, cmd = m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = got.(Model)
	if cmd != nil || m.feed.isRead("1") {
		t.Fatal("space must not mark a kept article read")
	}
	got, _ = m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "}) // collapse again
	m = got.(Model)

	got, cmd = m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("j past a kept article must not fire markRead")
	}
	if m.feed.isRead("1") {
		t.Fatal("j past a kept article greyed it")
	}
	if m.feed.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.feed.cursor)
	}
}

func TestKeepOnReadArticleMarksServerUnread(t *testing.T) {
	m := Model{feed: newFeed(), keys: defaultKeys()}
	m.feed.setSize(40, 10)
	m.feed.setArticles([]inoreader.Article{{ID: "1", Title: "a"}, {ID: "2", Title: "b"}}, true)

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
	m.feed.markReadLocal("1")
	got, cmd = m.handleKey(tea.KeyPressMsg{Code: 'K', Text: "K"})
	m = got.(Model)
	if cmd == nil {
		t.Fatal("pinning a read article must fire a server mark-unread")
	}
	if m.feed.isRead("1") || !m.feed.isKept("1") {
		t.Fatal("article should be unread and pinned locally")
	}

	upd, _ := m.Update(unmarkedMsg{id: "1", err: errors.New("boom")})
	m = upd.(Model)
	if !m.feed.isRead("1") || m.feed.isKept("1") {
		t.Fatal("failed server mark-unread must re-grey and unpin")
	}
}

func TestUpMarksLeavingArticleRead(t *testing.T) {
	m := Model{feed: newFeed(), keys: defaultKeys()}
	m.feed.setSize(40, 10)
	m.feed.setArticles([]inoreader.Article{{ID: "1", Title: "a"}, {ID: "2", Title: "b"}}, true)
	m.feed.cursor = 1

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = got.(Model)
	if cmd == nil || !m.feed.isRead("2") {
		t.Fatal("k should mark the article it leaves read")
	}
	if m.feed.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.feed.cursor)
	}

	// Already at the top: the cursor cannot leave, so nothing is marked.
	got, cmd = m.handleKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = got.(Model)
	if cmd != nil || m.feed.isRead("1") {
		t.Fatal("k at the top must not mark")
	}
}

func TestExpandMarksArticleRead(t *testing.T) {
	m := Model{feed: newFeed(), keys: defaultKeys()}
	m.feed.setSize(40, 10)
	m.feed.setArticles([]inoreader.Article{{ID: "1", Title: "a"}, {ID: "2", Title: "b"}}, true)

	got, cmd := m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = got.(Model)
	if cmd == nil || !m.feed.isRead("1") {
		t.Fatal("expanding should mark the article read")
	}
	if !m.feed.expanded["1"] {
		t.Fatal("article did not expand")
	}

	got, cmd = m.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = got.(Model)
	if cmd != nil {
		t.Fatal("collapsing must not fire another markRead")
	}
	if m.feed.expanded["1"] {
		t.Fatal("article did not collapse")
	}
}

func TestScrollExpandedShortBodyHandsOver(t *testing.T) {
	f := newFeed()
	f.setSize(40, 10)
	f.setArticles([]inoreader.Article{
		{ID: "1", Title: "a", Content: "tiny"},
		{ID: "2", Title: "b", Content: "tiny"},
	}, true)
	f.toggleCursor()
	if f.scrollExpanded(1) {
		t.Fatal("fully visible body should not consume j")
	}
}
