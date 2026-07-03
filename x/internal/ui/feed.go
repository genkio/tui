package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/genkio/x-tui/internal/readstore"
	"github.com/genkio/x-tui/internal/x"
)

// feedModel renders one home timeline as a single scrolling list (newest at the
// top). A cursor selects one post; expanding it shows the full text, counts, and
// link inline, accordion-style. The viewport keeps the cursor visible.
//
// x.com has no server-side read state, so it lives in a local read store: posts
// you scroll past, expand, or mark grey out and, in unread-only mode, drop from
// the list on the next fetch. kept pins a post unread for the session.
type feedModel struct {
	raw        []x.Tweet       // full fetched list, before the unread-only filter
	tweets     []x.Tweet       // visible slice actually rendered
	expanded   map[string]bool // tweet id -> body shown
	read       *readstore.Store
	kept       map[string]bool // tweet id -> pinned unread this session
	unreadOnly bool
	showHandle bool
	cursor     int
	yoff       int

	starts []int // first rendered line of each tweet, rebuilt on render
	total  int

	vp     viewport.Model
	width  int
	height int
}

func newFeed(read *readstore.Store, unreadOnly bool) feedModel {
	return feedModel{
		expanded:   map[string]bool{},
		read:       read,
		kept:       map[string]bool{},
		unreadOnly: unreadOnly,
		showHandle: true,
		vp:         viewport.New(),
	}
}

func (f *feedModel) toggleHandle() { f.showHandle = !f.showHandle; f.render() }

// setTweets replaces the list. resetCursor jumps back to the newest post,
// collapses everything, and drops the session keep pins, which a tab switch or
// manual refresh wants but a background auto-refresh does not.
func (f *feedModel) setTweets(t []x.Tweet, resetCursor bool) {
	f.raw = t
	if resetCursor {
		f.expanded = map[string]bool{}
		f.kept = map[string]bool{}
		f.cursor = 0
		f.yoff = 0
	}
	f.applyFilter()
}

// applyFilter recomputes the visible slice from raw. In unread-only mode a post
// is hidden once it is read, unless pinned unread this session. Marking read
// mid-session only greys a post in place (see markReadLocal); the filter runs on
// the next fetch or an explicit view toggle, so the list never shifts under the
// cursor as you scroll.
func (f *feedModel) applyFilter() {
	if !f.unreadOnly {
		f.tweets = f.raw
	} else {
		vis := make([]x.Tweet, 0, len(f.raw))
		for _, t := range f.raw {
			if f.read.Has(t.ID) && !f.kept[t.ID] {
				continue
			}
			vis = append(vis, t)
		}
		f.tweets = vis
	}
	f.clampCursor()
	f.render()
}

// toggleUnreadOnly flips between hiding read posts and greying them in place,
// re-filtering the current list. Reports the new state.
func (f *feedModel) toggleUnreadOnly() bool {
	f.unreadOnly = !f.unreadOnly
	f.applyFilter()
	return f.unreadOnly
}

// markReadLocal records the post read and re-renders it greyed. It deliberately
// does not re-filter, so a post marked while you read stays put until the next
// fetch or view toggle.
func (f *feedModel) markReadLocal(id string) { f.read.Mark(id); f.render() }

// isRead reports whether a post displays as read: marked and not pinned unread.
func (f feedModel) isRead(id string) bool { return f.read.Has(id) && !f.kept[id] }

func (f feedModel) isKept(id string) bool { return f.kept[id] }

// toggleKeep pins the cursored post unread (and back). Pinning a greyed post
// restores it to unread, in the store too, so a refresh keeps it. Reports the
// new state, and false ok when nothing is selected.
func (f *feedModel) toggleKeep() (kept, ok bool) {
	t, sel := f.selectedTweet()
	if !sel {
		return false, false
	}
	if f.kept[t.ID] {
		delete(f.kept, t.ID)
	} else {
		f.kept[t.ID] = true
		f.read.Unmark(t.ID)
	}
	f.render()
	return f.kept[t.ID], true
}

func (f *feedModel) setSize(w, h int) {
	f.width = w
	f.height = h
	f.vp.SetWidth(w)
	f.vp.SetHeight(h)
	f.render()
}

func (f *feedModel) moveCursor(delta int) {
	if len(f.tweets) == 0 {
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
	f.cursor = len(f.tweets) - 1
	f.clampCursor()
	f.leaveExpanded(old)
	f.render()
}

// leaveExpanded collapses the post at index i once the cursor has moved off it,
// so only the cursored post stays open.
func (f *feedModel) leaveExpanded(i int) {
	if i == f.cursor || i < 0 || i >= len(f.tweets) {
		return
	}
	delete(f.expanded, f.tweets[i].ID)
}

func (f *feedModel) clampCursor() {
	if f.cursor >= len(f.tweets) {
		f.cursor = len(f.tweets) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

func (f feedModel) selectedTweet() (x.Tweet, bool) {
	if f.cursor < 0 || f.cursor >= len(f.tweets) {
		return x.Tweet{}, false
	}
	return f.tweets[f.cursor], true
}

// toggleCursor expands or collapses the cursored post, reporting whether it
// ended up expanded (false also when nothing is selected).
func (f *feedModel) toggleCursor() bool {
	t, ok := f.selectedTweet()
	if !ok {
		return false
	}
	f.expanded[t.ID] = !f.expanded[t.ID]
	f.render()
	return f.expanded[t.ID]
}

// collapseCursor collapses the cursored post if expanded, reporting whether it
// did, so 'q'/esc backs out of an expansion before quitting.
func (f *feedModel) collapseCursor() bool {
	t, ok := f.selectedTweet()
	if !ok || !f.expanded[t.ID] {
		return false
	}
	delete(f.expanded, t.ID)
	f.render()
	return true
}

func (f feedModel) View() string { return f.vp.View() }

func (f *feedModel) render() {
	if f.width <= 0 {
		return
	}
	var lines []string
	f.starts = make([]int, len(f.tweets))
	for i, t := range f.tweets {
		f.starts[i] = len(lines)
		expanded := f.expanded[t.ID]
		lines = append(lines, f.renderTweet(t, i == f.cursor, expanded, f.isRead(t.ID), f.width)...)
		if expanded { // breathing room so an expanded body doesn't run into the next post
			lines = append(lines, "")
		}
	}
	f.total = len(lines)
	f.vp.SetContent(strings.Join(lines, "\n"))
	f.scrollToCursor()
}

func (f feedModel) renderTweet(t x.Tweet, selected, expanded, read bool, width int) []string {
	gutter := "  "
	if selected {
		gutter = selGutterStyle.Render("▌ ") // keep the cursor visible even on a greyed (read) row
	}
	handleSt, textSt := handleStyle, titleStyle
	switch {
	case read:
		handleSt, textSt = readStyle, readStyle
	case selected:
		textSt = titleSelStyle
	}
	age := timeStyle.Render(t.Age)

	handleSeg, handleSegW := "", 0
	if f.showHandle {
		tag := "@" + t.Handle
		if t.RepostBy != "" {
			tag = "🔁 @" + t.Handle
		}
		seg := handleSt.Render(truncate(tag, 24))
		handleSeg = seg + "  "
		handleSegW = lipgloss.Width(seg) + 2
	}

	// Text fills the space between the (optional) handle column and the time.
	fixed := lipgloss.Width(gutter) + handleSegW + 1 + lipgloss.Width(age)
	avail := width - fixed
	if avail < 8 {
		avail = 8
	}
	oneLine := strings.Join(strings.Fields(t.Text), " ")
	if oneLine == "" {
		oneLine = mediaPlaceholder(t)
	}
	text := textSt.Render(truncate(oneLine, avail))

	used := lipgloss.Width(gutter) + handleSegW + lipgloss.Width(text) + lipgloss.Width(age)
	pad := width - used
	if pad < 1 {
		pad = 1
	}
	header := gutter + handleSeg + text + strings.Repeat(" ", pad) + age
	lines := []string{header}

	if expanded {
		textWidth := width - 4
		if textWidth < 10 {
			textWidth = 10
		}
		name := t.Name
		if name == "" {
			name = "@" + t.Handle
		}
		byline := titleSelStyle.Render(name) + " " + handleStyle.Render("@"+t.Handle)
		if t.RepostBy != "" {
			byline += timeStyle.Render("  · reposted by " + t.RepostBy)
		}
		lines = append(lines, "", "    "+byline, "")
		if strings.TrimSpace(t.Text) == "" {
			lines = append(lines, "    "+emptyStyle.Render("(no text; press o to read in carbonyl, b for browser)"))
		} else {
			for _, ln := range wrapText(t.Text, textWidth) {
				lines = append(lines, "    "+textStyle.Render(ln))
			}
		}
		if t.Quoted != nil {
			lines = append(lines, "", "    "+quoteStyle.Render("quoting @"+t.Quoted.Handle+":"))
			for _, ln := range wrapText(t.Quoted.Text, textWidth-2) {
				lines = append(lines, "    "+quoteStyle.Render("┃ "+ln))
			}
		}
		lines = append(lines, "", "    "+helpStyle.Render(statsLine(t)))
		if t.URL != "" {
			lines = append(lines, "", "    "+linkStyle.Render(t.URL))
		}
	}
	return lines
}

// mediaPlaceholder labels a post whose text is empty after stripping the t.co
// link, i.e. it is just an image, video, or a bare quote.
func mediaPlaceholder(t x.Tweet) string {
	if t.Quoted != nil {
		return "[quote]"
	}
	return "[media]"
}

func statsLine(t x.Tweet) string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, humanCount(n)+" "+label)
		}
	}
	add(t.Replies, "replies")
	add(t.Reposts, "reposts")
	add(t.Likes, "likes")
	add(t.Quotes, "quotes")
	if len(parts) == 0 {
		return "no replies yet"
	}
	return strings.Join(parts, " · ")
}

func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// cursorBlock returns the first and last rendered line of the selected post.
func (f feedModel) cursorBlock() (top, bottom int) {
	top = f.starts[f.cursor]
	bottom = f.total - 1
	if f.cursor+1 < len(f.starts) {
		bottom = f.starts[f.cursor+1] - 1
	}
	return top, bottom
}

// scrollExpanded consumes one down/up press as line scrolling inside the
// expanded selected post: down while its tail is below the viewport, up while
// the offset is past its header. Reports false when the body is already fully
// on screen (or not expanded), meaning the cursor should move instead.
func (f *feedModel) scrollExpanded(delta int) bool {
	t, ok := f.selectedTweet()
	if !ok || !f.expanded[t.ID] || len(f.starts) == 0 {
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

// scrollToCursor clamps the viewport so the selected post stays visible. A
// selection that fits is kept fully on screen; one taller than the viewport may
// sit anywhere between top-aligned and tail-visible, so line scrolling through a
// long body survives re-renders.
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
