package core

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// newTestFeed returns a small-viewport feed whose first item's body overflows.
func newTestFeed(t *testing.T) Feed {
	t.Helper()
	f := NewFeed(NewTheme(true), false)
	f.SetSize(40, 6)
	long := strings.TrimSpace(strings.Repeat("lorem ipsum dolor sit amet ", 30))
	f.SetItems([]Item{
		{App: "t", ID: "1", Title: "first", Body: long},
		{App: "t", ID: "2", Title: "second", Body: "short"},
	}, true)
	return f
}

func TestScrollExpandedReadsLongBodyLineByLine(t *testing.T) {
	f := newTestFeed(t)
	k1 := Key("t", "1")

	if f.ScrollExpanded(1) {
		t.Fatal("collapsed row must not consume the keypress")
	}

	f.ToggleCursor() // expand first row
	_, bottom := f.cursorBlock()
	if bottom < f.vp.Height() {
		t.Fatalf("fixture body does not overflow: bottom=%d height=%d", bottom, f.vp.Height())
	}

	if !f.ScrollExpanded(1) || f.yoff != 1 {
		t.Fatalf("first j should scroll one line, yoff=%d", f.yoff)
	}

	steps := 0
	for f.ScrollExpanded(1) {
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
	f.MarkRead(k1)
	if want := bottom - f.vp.Height() + 1; f.yoff != want {
		t.Fatalf("re-render reset reading offset: yoff=%d, want %d", f.yoff, want)
	}

	if !f.ScrollExpanded(-1) {
		t.Fatal("k should scroll back up from the tail")
	}

	// Tail visible again: the next j must hand over to cursor movement.
	f.ScrollExpanded(1)
	if f.ScrollExpanded(1) {
		t.Fatal("j past the tail should move the cursor, not scroll")
	}
	f.MoveCursor(1)
	if f.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", f.cursor)
	}
	if f.expanded[k1] {
		t.Fatal("leaving the row should collapse it")
	}
}

func TestToggleCollapsesMidBody(t *testing.T) {
	f := newTestFeed(t)
	f.ToggleCursor() // expand
	for i := 0; i < 3; i++ {
		if !f.ScrollExpanded(1) {
			t.Fatal("expected room to scroll")
		}
	}

	f.ToggleCursor() // space mid-body collapses
	if f.expanded[Key("t", "1")] {
		t.Fatal("row still expanded")
	}
	if f.yoff != f.starts[0] {
		t.Fatalf("viewport not back on the header: yoff=%d", f.yoff)
	}
}

func TestKeepPinsRowUnread(t *testing.T) {
	f := newTestFeed(t)
	k1 := Key("t", "1")
	f.MarkRead(k1)
	if kept, ok := f.ToggleKeep(); !ok || !kept {
		t.Fatal("ToggleKeep should pin the cursored row")
	}
	if f.IsRead(k1) {
		t.Fatal("keeping should restore the row to unread")
	}
	if kept, _ := f.ToggleKeep(); kept {
		t.Fatal("second toggle should unlock")
	}

	f.ToggleKeep()
	f.SetItems(f.items, false) // auto-refresh keeps position and pins
	if !f.IsKept(k1) {
		t.Fatal("auto-refresh dropped the keep")
	}
	f.SetItems(f.items, true) // manual refresh clears keeps
	if f.IsKept(k1) {
		t.Fatal("manual refresh should clear keeps")
	}
}

func TestScrollExpandedShortBodyHandsOver(t *testing.T) {
	f := NewFeed(NewTheme(true), false)
	f.SetSize(40, 10)
	f.SetItems([]Item{
		{App: "t", ID: "1", Title: "a", Body: "tiny"},
		{App: "t", ID: "2", Title: "b", Body: "tiny"},
	}, true)
	f.ToggleCursor()
	if f.ScrollExpanded(1) {
		t.Fatal("fully visible body should not consume j")
	}
}
