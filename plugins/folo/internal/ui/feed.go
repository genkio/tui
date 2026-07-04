package ui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/genkio/tui/plugins/folo/internal/folo"
)

// feedModel renders the timeline as a single scrolling list (newest at the
// top). A cursor selects one article; expanding it shows the full body inline,
// accordion-style. Because the list response omits body text, each article's
// content is fetched on first expand and cached here. The viewport is
// positioned to keep the cursor visible.
type feedModel struct {
	articles []folo.Article
	expanded map[string]bool // article id -> body shown
	read     map[string]bool // article id -> marked read locally (greyed, kept until R)
	kept     map[string]bool // article id -> pinned unread; in-memory, cleared by manual refresh
	showFeed bool
	cursor   int
	yoff     int

	// Lazily fetched bodies, keyed by article id.
	content        map[string]string
	contentLoaded  map[string]bool
	contentLoading map[string]bool
	contentErr     map[string]string

	starts []int // first rendered line of each article, rebuilt on render
	total  int   // total rendered lines

	vp     viewport.Model
	width  int
	height int
}

func newFeed() feedModel {
	return feedModel{
		expanded:       map[string]bool{},
		read:           map[string]bool{},
		kept:           map[string]bool{},
		content:        map[string]string{},
		contentLoaded:  map[string]bool{},
		contentLoading: map[string]bool{},
		contentErr:     map[string]string{},
		showFeed:       true,
		vp:             viewport.New(),
	}
}

func (f *feedModel) toggleFeed() { f.showFeed = !f.showFeed; f.render() }

// setArticles replaces the list. A fresh fetch is the unread baseline, so the
// local read overlay is cleared (the server has already dropped what we marked).
// resetCursor jumps back to the top, which a manual refresh wants but a
// background auto-refresh does not; it also drops the body cache so a manual
// refresh re-reads from the server.
func (f *feedModel) setArticles(a []folo.Article, resetCursor bool) {
	f.articles = a
	f.read = map[string]bool{}
	if resetCursor {
		f.expanded = map[string]bool{}
		f.kept = map[string]bool{}
		f.content = map[string]string{}
		f.contentLoaded = map[string]bool{}
		f.contentLoading = map[string]bool{}
		f.contentErr = map[string]string{}
		f.cursor = 0
		f.yoff = 0
	}
	f.clampCursor()
	f.render()
}

func (f *feedModel) markReadLocal(id string) { f.read[id] = true; f.render() }
func (f *feedModel) unmarkRead(id string)    { delete(f.read, id); f.render() }
func (f feedModel) isRead(id string) bool    { return f.read[id] }
func (f feedModel) isKept(id string) bool    { return f.kept[id] }

// needsContent reports whether the body for id still has to be fetched (not
// already loaded and not in flight). A previous failure leaves it eligible, so
// re-expanding retries.
func (f feedModel) needsContent(id string) bool {
	return !f.contentLoaded[id] && !f.contentLoading[id]
}

func (f *feedModel) startContent(id string) {
	f.contentLoading[id] = true
	delete(f.contentErr, id)
	f.render()
}

func (f *feedModel) setContent(id, text string) {
	delete(f.contentLoading, id)
	delete(f.contentErr, id)
	f.contentLoaded[id] = true
	f.content[id] = text
	f.render()
}

func (f *feedModel) setContentErr(id, msg string) {
	delete(f.contentLoading, id)
	f.contentErr[id] = msg
	f.render()
}

// toggleKeep pins the cursored article keep-unread (and back). Pinning a greyed
// article restores it to unread; the pin only lives until a manual refresh.
// Reports the new state, and false ok when nothing is selected.
func (f *feedModel) toggleKeep() (kept, ok bool) {
	a, sel := f.selectedArticle()
	if !sel {
		return false, false
	}
	if f.kept[a.ID] {
		delete(f.kept, a.ID)
	} else {
		f.kept[a.ID] = true
		delete(f.read, a.ID)
	}
	f.render()
	return f.kept[a.ID], true
}

// revertKeep undoes a keep whose server mark-unread failed: the article greys
// back out and loses its pin.
func (f *feedModel) revertKeep(id string) {
	delete(f.kept, id)
	f.read[id] = true
	f.render()
}

func (f *feedModel) setSize(w, h int) {
	f.width = w
	f.height = h
	f.vp.SetWidth(w)
	f.vp.SetHeight(h)
	f.render()
}

func (f *feedModel) moveCursor(delta int) {
	if len(f.articles) == 0 {
		return
	}
	old := f.cursor
	f.cursor += delta
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

func (f *feedModel) toTop() {
	old := f.cursor
	f.cursor = 0
	f.leaveExpanded(old)
	f.render()
}

func (f *feedModel) toBottom() {
	old := f.cursor
	f.cursor = len(f.articles) - 1
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

// leaveExpanded collapses the item at index i once the cursor has moved off it,
// so only the cursored article stays open.
func (f *feedModel) leaveExpanded(i int) {
	if i == f.cursor || i < 0 || i >= len(f.articles) {
		return
	}
	delete(f.expanded, f.articles[i].ID)
}

func (f *feedModel) clampCursor() {
	if f.cursor < 0 {
		f.cursor = 0
	}
	if f.cursor >= len(f.articles) {
		f.cursor = len(f.articles) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

func (f feedModel) selectedArticle() (folo.Article, bool) {
	if f.cursor < 0 || f.cursor >= len(f.articles) {
		return folo.Article{}, false
	}
	return f.articles[f.cursor], true
}

// toggleCursor expands or collapses the cursored article, reporting whether it
// ended up expanded (false also when nothing is selected).
func (f *feedModel) toggleCursor() bool {
	a, ok := f.selectedArticle()
	if !ok {
		return false
	}
	f.expanded[a.ID] = !f.expanded[a.ID]
	f.render()
	return f.expanded[a.ID]
}

// collapseCursor collapses the cursored article if it is expanded, reporting
// whether it did. Used so 'q'/esc backs out of an expansion before quitting.
func (f *feedModel) collapseCursor() bool {
	a, ok := f.selectedArticle()
	if !ok || !f.expanded[a.ID] {
		return false
	}
	delete(f.expanded, a.ID)
	f.render()
	return true
}

func (f feedModel) View() string { return f.vp.View() }

func (f *feedModel) render() {
	if f.width <= 0 {
		return
	}
	var lines []string
	f.starts = make([]int, len(f.articles))
	for i, a := range f.articles {
		f.starts[i] = len(lines)
		expanded := f.expanded[a.ID]
		lines = append(lines, f.renderArticle(a, i == f.cursor, expanded, f.read[a.ID], f.width)...)
		if expanded { // breathing room so an expanded body doesn't run into the next title
			lines = append(lines, "")
		}
	}
	f.total = len(lines)
	f.vp.SetContent(strings.Join(lines, "\n"))
	f.scrollToCursor()
}

func (f feedModel) renderArticle(a folo.Article, selected, expanded, read bool, width int) []string {
	gutter := "  "
	if selected {
		gutter = selGutterStyle.Render("▌ ") // keep the cursor visible even on a greyed (read) row
	}
	feedSt, titleSt := feedTagStyle, titleStyle
	switch {
	case read:
		feedSt, titleSt = readStyle, readStyle
	case selected:
		titleSt = titleSelStyle
	}

	feedSeg, feedSegW := "", 0
	if f.showFeed {
		feed := feedSt.Render(truncate(a.Feed, 22))
		feedSeg = feed + "  "
		feedSegW = lipgloss.Width(feed) + 2
	}
	rel := timeStyle.Render(a.Age)

	titleText := a.Title
	if titleText == "" {
		titleText = "(untitled)"
	}
	// Title fills the space between the (optional) feed tag and the right-aligned time.
	fixed := lipgloss.Width(gutter) + feedSegW + 1 + lipgloss.Width(rel)
	avail := width - fixed
	if avail < 8 {
		avail = 8
	}
	title := titleSt.Render(truncate(titleText, avail))

	used := lipgloss.Width(gutter) + feedSegW + lipgloss.Width(title) + lipgloss.Width(rel)
	pad := width - used
	if pad < 1 {
		pad = 1
	}
	header := gutter + feedSeg + title + strings.Repeat(" ", pad) + rel
	lines := []string{header}

	if expanded {
		lines = append(lines, "")
		if a.Author != "" {
			lines = append(lines, "    "+timeStyle.Render("by "+a.Author))
		}
		lines = append(lines, f.bodyLines(a, width)...)
		if a.URL != "" {
			lines = append(lines, "", "    "+linkStyle.Render(a.URL))
		}
	}
	return lines
}

// bodyLines renders the expanded body: a loading note while the fetch is in
// flight, the error if it failed, or the wrapped text (falling back to the
// list summary, then a no-content note).
func (f feedModel) bodyLines(a folo.Article, width int) []string {
	textWidth := width - 4
	if textWidth < 10 {
		textWidth = 10
	}
	switch {
	case f.contentLoading[a.ID]:
		return []string{"    " + emptyStyle.Render("Loading…")}
	case f.contentErr[a.ID] != "":
		return []string{
			"    " + statusErrStyle.Render(f.contentErr[a.ID]),
			"    " + emptyStyle.Render("(press o to read in carbonyl, b for browser)"),
		}
	}
	body := f.content[a.ID]
	if body == "" {
		body = a.Summary
	}
	if body == "" {
		return []string{"    " + emptyStyle.Render("(no content; press o to read in carbonyl, b for browser)")}
	}
	var out []string
	for _, ln := range wrapText(body, textWidth) {
		out = append(out, "    "+textStyle.Render(ln))
	}
	return out
}

// cursorBlock returns the first and last rendered line of the selected article.
func (f feedModel) cursorBlock() (top, bottom int) {
	top = f.starts[f.cursor]
	bottom = f.total - 1
	if f.cursor+1 < len(f.starts) {
		bottom = f.starts[f.cursor+1] - 1
	}
	return top, bottom
}

// scrollExpanded consumes one down/up press as line scrolling inside the
// expanded selected article: down while its tail is below the viewport, up
// while the offset is past its header. Reports false when the body is already
// fully read through (or not expanded), meaning the cursor should move instead.
func (f *feedModel) scrollExpanded(delta int) bool {
	a, ok := f.selectedArticle()
	if !ok || !f.expanded[a.ID] || len(f.starts) == 0 {
		return false
	}
	top, bottom := f.cursorBlock()
	if delta > 0 {
		if f.yoff >= bottom-f.vp.Height()+1 {
			return false // tail already on screen
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

// scrollToCursor clamps the viewport so the selected article stays visible. A
// selection that fits is kept fully on screen; one taller than the viewport may
// sit anywhere between top-aligned and tail-visible, so line scrolling through
// a long body survives re-renders.
func (f *feedModel) scrollToCursor() {
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
