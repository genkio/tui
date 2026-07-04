package main

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// column widths for the aligned left edge of every row, so titles line up even
// as apps interleave. chip holds the source tag, src the handle/feed.
const (
	chipCol = 4
	srcCol  = 16
)

// feed renders the merged "all" timeline as one scrolling list (newest first).
// A cursor selects a row; expanding it shows the body and link inline. Because
// the underlying items are already the unread set, reading is tracked in place:
// a row you scroll past, expand, or mark greys out but stays put until the next
// refresh drops it.
type feed struct {
	items    []item
	expanded map[string]bool // item key -> body shown
	read     map[string]bool // item key -> greyed read this session
	kept     map[string]bool // item key -> pinned unread this session
	showSrc  bool
	cursor   int
	yoff     int

	starts []int // first rendered line of each item, rebuilt on render
	total  int

	vp     viewport.Model
	th     theme
	width  int
	height int
}

func newFeed(th theme) feed {
	return feed{
		expanded: map[string]bool{},
		read:     map[string]bool{},
		kept:     map[string]bool{},
		showSrc:  true,
		th:       th,
		vp:       viewport.New(),
	}
}

// setItems replaces the list, jumping back to the newest row and collapsing
// everything (a fetch or manual refresh always resets the view).
func (f *feed) setItems(items []item) {
	f.items = items
	f.expanded = map[string]bool{}
	f.kept = map[string]bool{}
	f.cursor = 0
	f.yoff = 0
	f.clampCursor()
	f.render()
}

func (f *feed) setTheme(th theme) { f.th = th; f.render() }

func (f *feed) toggleSource() { f.showSrc = !f.showSrc; f.render() }

func (f *feed) setSize(w, h int) {
	f.width, f.height = w, h
	f.vp.SetWidth(w)
	f.vp.SetHeight(h)
	f.render()
}

func (f *feed) markRead(key string) { f.read[key] = true; f.render() }

func (f feed) isRead(key string) bool { return f.read[key] && !f.kept[key] }

func (f feed) isKept(key string) bool { return f.kept[key] }

// toggleKeep pins the cursored row unread (and back). Pinning also un-greys it
// so scrolling won't re-mark it read; the caller cancels any queued store mark.
// The pin lasts until the next refresh. Reports the new state, false ok when
// nothing is selected.
func (f *feed) toggleKeep() (kept, ok bool) {
	it, sel := f.selected()
	if !sel {
		return false, false
	}
	k := it.key()
	if f.kept[k] {
		delete(f.kept, k)
	} else {
		f.kept[k] = true
		delete(f.read, k)
	}
	f.render()
	return f.kept[k], true
}

func (f *feed) moveCursor(delta int) {
	if len(f.items) == 0 {
		return
	}
	old := f.cursor
	f.cursor += delta
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

func (f *feed) toTop() {
	old := f.cursor
	f.cursor = 0
	f.leaveExpanded(old)
	f.render()
}

func (f *feed) toBottom() {
	old := f.cursor
	f.cursor = len(f.items) - 1
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

// leaveExpanded collapses row i once the cursor moves off it, so only the
// cursored row stays open.
func (f *feed) leaveExpanded(i int) {
	if i == f.cursor || i < 0 || i >= len(f.items) {
		return
	}
	delete(f.expanded, f.items[i].key())
}

func (f *feed) clampCursor() {
	if f.cursor >= len(f.items) {
		f.cursor = len(f.items) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

func (f feed) selected() (item, bool) {
	if f.cursor < 0 || f.cursor >= len(f.items) {
		return item{}, false
	}
	return f.items[f.cursor], true
}

// toggleCursor expands or collapses the cursored row, reporting whether it
// ended up expanded (false also when nothing is selected).
func (f *feed) toggleCursor() bool {
	it, ok := f.selected()
	if !ok {
		return false
	}
	f.expanded[it.key()] = !f.expanded[it.key()]
	f.render()
	return f.expanded[it.key()]
}

// collapseCursor collapses the cursored row if expanded, reporting whether it
// did, so esc backs out of an expansion.
func (f *feed) collapseCursor() bool {
	it, ok := f.selected()
	if !ok || !f.expanded[it.key()] {
		return false
	}
	delete(f.expanded, it.key())
	f.render()
	return true
}

func (f feed) View() string { return f.vp.View() }

func (f *feed) render() {
	if f.width <= 0 {
		return
	}
	var lines []string
	f.starts = make([]int, len(f.items))
	for i, it := range f.items {
		f.starts[i] = len(lines)
		expanded := f.expanded[it.key()]
		lines = append(lines, f.renderItem(it, i == f.cursor, expanded, f.isRead(it.key()))...)
		if expanded {
			lines = append(lines, "")
		}
	}
	f.total = len(lines)
	f.vp.SetContent(strings.Join(lines, "\n"))
	f.scrollToCursor()
}

func (f feed) renderItem(it item, selected, expanded, read bool) []string {
	th := f.th
	gutter := "  "
	if selected {
		gutter = th.selGutter.Render("▌ ")
	}

	chip := th.chip(it.App)
	if read { // a greyed row dims everything, chip included
		chip = th.read.Render(plainChipLabel(it.App))
	}
	chipSeg := chip + strings.Repeat(" ", max(1, chipCol-lipgloss.Width(chip)))

	srcSeg, srcSegW := "", 0
	if f.showSrc {
		srcSt := th.source
		if read {
			srcSt = th.read
		}
		src := padRight(truncate(it.Source, srcCol), srcCol)
		srcSeg = srcSt.Render(src) + " "
		srcSegW = lipgloss.Width(srcSeg)
	}

	age := th.time.Render(it.Age)
	if read {
		age = th.read.Render(it.Age)
	}

	titleSt := th.title
	switch {
	case read:
		titleSt = th.read
	case selected:
		titleSt = th.titleSel
	}

	fixed := lipgloss.Width(gutter) + lipgloss.Width(chipSeg) + srcSegW + 1 + lipgloss.Width(age)
	avail := f.width - fixed
	if avail < 8 {
		avail = 8
	}
	oneLine := strings.Join(strings.Fields(it.Title), " ")
	if oneLine == "" {
		oneLine = "(no title)"
	}
	title := titleSt.Render(truncate(oneLine, avail))

	used := lipgloss.Width(gutter) + lipgloss.Width(chipSeg) + srcSegW + lipgloss.Width(title) + lipgloss.Width(age)
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

func (f feed) renderBody(it item) []string {
	th := f.th
	textWidth := f.width - 4
	if textWidth < 10 {
		textWidth = 10
	}

	byline := th.chip(it.App)
	if it.Source != "" {
		byline += "  " + th.source.Render(it.Source)
	}
	if it.Author != "" && it.Author != it.Source {
		byline += th.time.Render("  · " + it.Author)
	}
	lines := []string{"", "    " + byline, ""}

	body := it.Body
	if strings.TrimSpace(body) == "" {
		body = it.Title
	}
	if strings.TrimSpace(body) == "" {
		lines = append(lines, "    "+th.empty.Render("(no text; press o to read in carbonyl, b for browser)"))
	} else {
		for _, ln := range wrapText(body, textWidth) {
			lines = append(lines, "    "+th.text.Render(ln))
		}
	}
	if it.URL != "" {
		lines = append(lines, "", "    "+th.link.Render(it.URL))
	}
	return lines
}

// cursorBlock returns the first and last rendered line of the selected row.
func (f feed) cursorBlock() (top, bottom int) {
	top = f.starts[f.cursor]
	bottom = f.total - 1
	if f.cursor+1 < len(f.starts) {
		bottom = f.starts[f.cursor+1] - 1
	}
	return top, bottom
}

// scrollExpanded consumes one down/up press as line scrolling inside the
// expanded selected row while its body overflows the viewport. Reports false
// when the body is already fully on screen (or not expanded), meaning the
// cursor should move instead.
func (f *feed) scrollExpanded(delta int) bool {
	it, ok := f.selected()
	if !ok || !f.expanded[it.key()] || len(f.starts) == 0 {
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

// scrollToCursor clamps the viewport so the selected row stays visible, keeping
// a fully-fitting selection on screen and letting a taller one scroll.
func (f *feed) scrollToCursor() {
	if len(f.starts) == 0 {
		f.yoff = 0
		f.vp.SetYOffset(0)
		return
	}
	top, bottom := f.cursorBlock()
	lo, hi := top, bottom-f.vp.Height()+1
	if hi < lo {
		lo, hi = hi, lo
	}
	off := f.yoff
	if off > hi {
		off = hi
	}
	if off < lo {
		off = lo
	}
	if maxOff := f.total - f.vp.Height(); off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	f.yoff = off
	f.vp.SetYOffset(off)
}

func plainChipLabel(app string) string {
	if l := map[string]string{"x": "𝕏", "inoreader": "ino", "folo": "folo"}[app]; l != "" {
		return l
	}
	return app
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

func center(s string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

// forceHeight pads or trims s to exactly h lines so the footer stays pinned.
func forceHeight(s string, h int) string {
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
