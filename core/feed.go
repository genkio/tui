package core

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// column widths for the aligned left edge of every row, so titles line up even
// as apps interleave. chip holds the source tag, src the handle/feed.
const (
	chipCol = 4
	srcCol  = 16
)

// Feed is the scrolling list shared by every app and the merged "all" view: a
// cursor selects a row, expanding it shows the body and link inline. The items
// are already the unread set, so reading is tracked in place: a row you scroll
// past, expand, or mark greys out but stays put until the next refresh drops
// it. ShowChip draws the per-source tag (𝕏/ino/folo) for the merged view; a
// single-source app leaves it off.
type Feed struct {
	ShowChip bool

	items    []Item
	expanded map[string]bool // item key -> body shown
	read     map[string]bool // item key -> greyed read this session
	kept     map[string]bool // item key -> pinned unread this session
	showSrc  bool
	cursor   int
	yoff     int

	starts []int // first rendered line of each item, rebuilt on render
	total  int

	vp     viewport.Model
	th     Theme
	width  int
	height int
}

func NewFeed(th Theme, showChip bool) Feed {
	return Feed{
		ShowChip: showChip,
		expanded: map[string]bool{},
		read:     map[string]bool{},
		kept:     map[string]bool{},
		showSrc:  true,
		th:       th,
		vp:       viewport.New(),
	}
}

// SetItems replaces the list. resetCursor jumps back to the top and collapses
// everything (a manual refresh wants this; a background auto-refresh keeps the
// reader's position). The read overlay is left alone; call ClearRead when a
// fetch is a fresh unread baseline.
func (f *Feed) SetItems(items []Item, resetCursor bool) {
	f.items = items
	if resetCursor {
		f.expanded = map[string]bool{}
		f.kept = map[string]bool{}
		f.cursor = 0
		f.yoff = 0
	}
	f.clampCursor()
	f.render()
}

// ClearRead drops the greyed-read overlay, for when a fresh fetch is the new
// unread baseline (the server already dropped what we marked).
func (f *Feed) ClearRead() { f.read = map[string]bool{}; f.render() }

// SetBody replaces one item's body in place, for a body fetched lazily after
// the list loaded (folo). Cursor and scroll are untouched.
func (f *Feed) SetBody(key, body string) {
	for i := range f.items {
		if f.items[i].Key() == key {
			f.items[i].Body = body
			f.render()
			return
		}
	}
}

func (f *Feed) SetTheme(th Theme) { f.th = th; f.render() }

func (f *Feed) ToggleSource() { f.showSrc = !f.showSrc; f.render() }

func (f *Feed) SetSize(w, h int) {
	f.width, f.height = w, h
	f.vp.SetWidth(w)
	f.vp.SetHeight(h)
	f.render()
}

func (f *Feed) MarkRead(key string) { f.read[key] = true; f.render() }

// Unmark undoes a mark whose persist failed (or an optimistic mark the caller
// revokes), so the row returns to unread.
func (f *Feed) Unmark(key string) { delete(f.read, key); f.render() }

// RevertKeep drops a keep pin and greys the row back, for when the server
// refused the mark-unread that a keep on an already-read row requires: the UI
// shouldn't promise an unread that won't survive the next refresh.
func (f *Feed) RevertKeep(key string) { delete(f.kept, key); f.read[key] = true; f.render() }

func (f Feed) IsRead(key string) bool { return f.read[key] && !f.kept[key] }

func (f Feed) IsKept(key string) bool { return f.kept[key] }

func (f Feed) IsExpanded(key string) bool { return f.expanded[key] }

func (f Feed) Cursor() int { return f.cursor }

func (f Feed) Len() int { return len(f.items) }

// Items is the currently displayed slice (read-only); callers must not mutate.
func (f Feed) Items() []Item { return f.items }

// ToggleKeep pins the cursored row unread (and back). Pinning also un-greys it
// so scrolling won't re-mark it read; the caller cancels any queued store mark.
// The pin lasts until the next refresh. Reports the new state, false ok when
// nothing is selected.
func (f *Feed) ToggleKeep() (kept, ok bool) {
	it, sel := f.Selected()
	if !sel {
		return false, false
	}
	k := it.Key()
	if f.kept[k] {
		delete(f.kept, k)
	} else {
		f.kept[k] = true
		delete(f.read, k)
	}
	f.render()
	return f.kept[k], true
}

func (f *Feed) MoveCursor(delta int) {
	if len(f.items) == 0 {
		return
	}
	old := f.cursor
	f.cursor += delta
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

func (f *Feed) ToTop() {
	old := f.cursor
	f.cursor = 0
	f.leaveExpanded(old)
	f.render()
}

func (f *Feed) ToBottom() {
	old := f.cursor
	f.cursor = len(f.items) - 1
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

// leaveExpanded collapses row i once the cursor moves off it, so only the
// cursored row stays open.
func (f *Feed) leaveExpanded(i int) {
	if i == f.cursor || i < 0 || i >= len(f.items) {
		return
	}
	delete(f.expanded, f.items[i].Key())
}

func (f *Feed) clampCursor() {
	if f.cursor >= len(f.items) {
		f.cursor = len(f.items) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

func (f Feed) Selected() (Item, bool) {
	if f.cursor < 0 || f.cursor >= len(f.items) {
		return Item{}, false
	}
	return f.items[f.cursor], true
}

// ToggleCursor expands or collapses the cursored row, reporting whether it
// ended up expanded (false also when nothing is selected).
func (f *Feed) ToggleCursor() bool {
	it, ok := f.Selected()
	if !ok {
		return false
	}
	f.expanded[it.Key()] = !f.expanded[it.Key()]
	f.render()
	return f.expanded[it.Key()]
}

// CollapseCursor collapses the cursored row if expanded, reporting whether it
// did, so esc backs out of an expansion.
func (f *Feed) CollapseCursor() bool {
	it, ok := f.Selected()
	if !ok || !f.expanded[it.Key()] {
		return false
	}
	delete(f.expanded, it.Key())
	f.render()
	return true
}

func (f Feed) View() string { return f.vp.View() }

// Update forwards a message to the inner viewport (mouse wheel, etc.).
func (f *Feed) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.vp, cmd = f.vp.Update(msg)
	return cmd
}

func (f *Feed) render() {
	if f.width <= 0 {
		return
	}
	var lines []string
	f.starts = make([]int, len(f.items))
	for i, it := range f.items {
		f.starts[i] = len(lines)
		expanded := f.expanded[it.Key()]
		lines = append(lines, f.renderItem(it, i == f.cursor, expanded, f.IsRead(it.Key()))...)
		if expanded {
			lines = append(lines, "")
		}
	}
	f.total = len(lines)
	f.vp.SetContent(strings.Join(lines, "\n"))
	f.scrollToCursor()
}

func (f Feed) renderItem(it Item, selected, expanded, read bool) []string {
	th := f.th
	gutter := "  "
	if selected {
		gutter = th.SelGutter.Render("▌ ")
	}

	chipSeg, chipSegW := "", 0
	if f.ShowChip {
		chip := th.Chip(it.App)
		if read { // a greyed row dims everything, chip included
			chip = th.Read.Render(PlainChip(it.App))
		}
		chipSeg = chip + strings.Repeat(" ", max(1, chipCol-lipgloss.Width(chip)))
		chipSegW = lipgloss.Width(chipSeg)
	}

	srcSeg, srcSegW := "", 0
	if f.showSrc && it.Source != "" {
		srcSt := th.Source
		if read {
			srcSt = th.Read
		}
		src := padRight(truncate(it.Source, srcCol), srcCol)
		srcSeg = srcSt.Render(src) + " "
		srcSegW = lipgloss.Width(srcSeg)
	}

	age := th.Time.Render(it.Age)
	if read {
		age = th.Read.Render(it.Age)
	}

	titleSt := th.Title
	switch {
	case read:
		titleSt = th.Read
	case selected:
		titleSt = th.TitleSel
	}

	fixed := lipgloss.Width(gutter) + chipSegW + srcSegW + 1 + lipgloss.Width(age)
	avail := f.width - fixed
	if avail < 8 {
		avail = 8
	}
	oneLine := strings.Join(strings.Fields(it.Title), " ")
	if oneLine == "" {
		oneLine = "(no title)"
	}
	title := titleSt.Render(truncate(oneLine, avail))

	used := lipgloss.Width(gutter) + chipSegW + srcSegW + lipgloss.Width(title) + lipgloss.Width(age)
	pad := f.width - used
	if pad < 1 {
		pad = 1
	}
	header := gutter + chipSeg + srcSeg + title + strings.Repeat(" ", pad) + age
	lines := []string{header}

	if expanded {
		lines = append(lines, f.renderBody(it)...)
	}
	return lines
}

func (f Feed) renderBody(it Item) []string {
	th := f.th
	textWidth := f.width - 4
	if textWidth < 10 {
		textWidth = 10
	}

	var byline string
	if f.ShowChip {
		byline = th.Chip(it.App)
	}
	if it.Source != "" {
		if byline != "" {
			byline += "  "
		}
		byline += th.Source.Render(it.Source)
	}
	if it.Author != "" && it.Author != it.Source {
		byline += th.Time.Render("  · " + it.Author)
	}
	lines := []string{""}
	if strings.TrimSpace(byline) != "" {
		lines = append(lines, "    "+byline)
	}
	lines = append(lines, "")

	body := it.Body
	if strings.TrimSpace(body) == "" {
		body = it.Title
	}
	if strings.TrimSpace(body) == "" {
		lines = append(lines, "    "+th.Empty.Render("(no text; press o to read in carbonyl, b for browser)"))
	} else {
		for _, ln := range wrapText(body, textWidth) {
			lines = append(lines, "    "+th.Text.Render(ln))
		}
	}
	if it.URL != "" {
		lines = append(lines, "", "    "+th.Link.Render(it.URL))
	}
	return lines
}

// cursorBlock returns the first and last rendered line of the selected row.
func (f Feed) cursorBlock() (top, bottom int) {
	top = f.starts[f.cursor]
	bottom = f.total - 1
	if f.cursor+1 < len(f.starts) {
		bottom = f.starts[f.cursor+1] - 1
	}
	return top, bottom
}

// ScrollExpanded consumes one down/up press as line scrolling inside the
// expanded selected row while its body overflows the viewport. Reports false
// when the body is already fully on screen (or not expanded), meaning the
// cursor should move instead.
func (f *Feed) ScrollExpanded(delta int) bool {
	it, ok := f.Selected()
	if !ok || !f.expanded[it.Key()] || len(f.starts) == 0 {
		return false
	}
	top, bottom := f.cursorBlock()
	if delta > 0 {
		if f.yoff >= bottom-f.vp.Height()+1 {
			return false
		}
		f.yoff++
	} else {
		if f.yoff <= top {
			return false
		}
		f.yoff--
	}
	f.vp.SetYOffset(f.yoff)
	return true
}

// scrollToCursor positions the viewport for the selected row. A fitting row is
// pinned at the half-screen line so the items ahead stay in view and the list
// scrolls under the cursor; near the top the cursor rides above the line (the
// offset floors at 0) and near the end the remaining items rise to meet it (the
// offset caps at the last page). A selection taller than the viewport keeps its
// sticky offset instead, so a long expanded body scrolls line by line
// (ScrollExpanded) without snapping back to centre.
func (f *Feed) scrollToCursor() {
	if len(f.starts) == 0 {
		f.yoff = 0
		f.vp.SetYOffset(0)
		return
	}
	h := f.vp.Height()
	top, bottom := f.cursorBlock()

	if bottom-top+1 > h {
		lo, hi := top, bottom-h+1
		off := f.yoff
		if off < lo {
			off = lo
		}
		if off > hi {
			off = hi
		}
		f.setOffset(off)
		return
	}

	off := top - h/2
	if lo := bottom - h + 1; off < lo { // keep a multi-line selection fully on screen
		off = lo
	}
	if off > top {
		off = top
	}
	f.setOffset(off)
}

func (f *Feed) setOffset(off int) {
	if maxOff := f.total - f.vp.Height(); off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	f.yoff = off
	f.vp.SetYOffset(off)
}

func padRight(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// truncate shortens s to at most max display cells (CJK-aware), adding an
// ellipsis when cut.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > max {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// wrapText word-wraps s to the given display width, hard-breaking runs with no
// spaces (URLs, CJK text) so nothing overflows the viewport.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		lineW := 0
		for _, word := range words {
			ww := lipgloss.Width(word)
			for ww > width {
				if line != "" {
					out = append(out, line)
					line, lineW = "", 0
				}
				head, rest := cutWidth(word, width)
				out = append(out, head)
				word = rest
				ww = lipgloss.Width(word)
			}
			switch {
			case line == "":
				line, lineW = word, ww
			case lineW+1+ww > width:
				out = append(out, line)
				line, lineW = word, ww
			default:
				line += " " + word
				lineW += 1 + ww
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func Center(s string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

// ForceHeight pads or trims s to exactly h lines so a footer stays pinned.
func ForceHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// cutWidth splits s at the longest prefix whose display width is <= max.
func cutWidth(s string, max int) (string, string) {
	w := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max {
			return s[:i], s[i:]
		}
		w += rw
	}
	return s, ""
}
