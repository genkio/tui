package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"

	"github.com/genkio/slack-tui/internal/slack"
)

// Message bodies are clipped to this many wrapped lines so long notifications
// stay scannable; space expands the cursored message to its full text.
const clippedBodyLines = 3

// detailModel shows one conversation's messages in a scrolling viewport. A
// cursor selects a top-level message; selecting a thread root expands its
// replies inline. The viewport is positioned to keep the cursor visible.
type detailModel struct {
	conv         slack.Conversation
	messages     []slack.Message            // sorted oldest -> newest
	expanded     map[string][]slack.Message // thread ts -> replies (including root)
	loadingT     map[string]bool            // thread ts currently being fetched
	bodyExpanded map[string]bool            // message ts whose full text is shown
	cursor       int
	yoff         int
	keepCursorTS string // when set, the next setMessages re-selects this ts instead of the first unread

	vp      viewport.Model
	width   int
	height  int
	loading bool
}

func newDetail() detailModel {
	return detailModel{
		expanded:     map[string][]slack.Message{},
		loadingT:     map[string]bool{},
		bodyExpanded: map[string]bool{},
		vp:           viewport.New(),
	}
}

// open resets the view for a freshly selected conversation.
func (d *detailModel) open(conv slack.Conversation) {
	d.conv = conv
	d.messages = nil
	d.expanded = map[string][]slack.Message{}
	d.loadingT = map[string]bool{}
	d.bodyExpanded = map[string]bool{}
	d.cursor = 0
	d.yoff = 0
	d.loading = true
}

func (d *detailModel) setMessages(msgs []slack.Message) {
	sortByTime(msgs)
	d.messages = msgs
	d.loading = false
	if d.keepCursorTS != "" {
		d.cursor = d.indexOfTS(d.keepCursorTS)
		d.keepCursorTS = ""
	} else {
		d.cursor = d.firstUnreadIndex()
	}
	d.clampCursor()
	d.render()
}

// indexOfTS returns the index of the message with the given ts, or 0 if absent.
func (d detailModel) indexOfTS(ts string) int {
	for i, m := range d.messages {
		if m.ID == ts {
			return i
		}
	}
	return 0
}

func (d *detailModel) setReplies(threadTS string, msgs []slack.Message) {
	sortByTime(msgs)
	delete(d.loadingT, threadTS)
	d.expanded[threadTS] = msgs
	d.render()
}

func (d *detailModel) setSize(w, h int) {
	d.width = w
	d.height = h
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
	d.render()
}

func (d *detailModel) moveCursor(delta int) {
	if len(d.messages) == 0 {
		return
	}
	d.cursor += delta
	d.clampCursor()
	d.render()
}

func (d *detailModel) toTop() {
	d.cursor = 0
	d.render()
}

func (d *detailModel) toBottom() {
	d.cursor = len(d.messages) - 1
	d.clampCursor()
	d.render()
}

func (d *detailModel) clampCursor() {
	if d.cursor < 0 {
		d.cursor = 0
	}
	if d.cursor >= len(d.messages) {
		d.cursor = len(d.messages) - 1
	}
}

func (d *detailModel) selectedMessage() (slack.Message, bool) {
	if d.cursor < 0 || d.cursor >= len(d.messages) {
		return slack.Message{}, false
	}
	return d.messages[d.cursor], true
}

// toggleThread expands or collapses the thread at the cursor. It returns the
// thread ts that needs fetching, or "" if there is nothing to fetch (no thread,
// or it was already shown and is now collapsed).
func (d *detailModel) toggleThread() string {
	m, ok := d.selectedMessage()
	if !ok || !m.IsThreadRoot() {
		return ""
	}
	if _, shown := d.expanded[m.ThreadTS]; shown {
		delete(d.expanded, m.ThreadTS)
		d.render()
		return ""
	}
	d.loadingT[m.ThreadTS] = true
	d.render()
	return m.ThreadTS
}

func (d *detailModel) toggleBody() {
	if m, ok := d.selectedMessage(); ok {
		d.bodyExpanded[m.ID] = !d.bodyExpanded[m.ID]
		d.render()
	}
}

// latestTS is the timestamp of the newest message shown, used as the mark point.
func (d *detailModel) latestTS() string {
	if n := len(d.messages); n > 0 {
		return d.messages[n-1].ID
	}
	return d.conv.Latest
}

func (d *detailModel) selectedMessageURL(base string) string {
	m, ok := d.selectedMessage()
	if !ok {
		return ""
	}
	if m.Permalink != "" {
		return m.Permalink
	}
	if base == "" {
		return ""
	}
	return messageURL(base, d.conv.ID, m.ID, m.ThreadTS)
}

// messageURL reconstructs a Slack archives permalink, which the history API
// omits. The path id is the ts with its dot removed; replies need the thread
// anchor so the link opens in-thread rather than just at the channel.
func messageURL(base, channel, ts, threadTS string) string {
	u := base + "/archives/" + channel + "/p" + strings.ReplaceAll(ts, ".", "")
	if threadTS != "" && threadTS != ts {
		u += "?thread_ts=" + threadTS + "&cid=" + channel
	}
	return u
}

func (d detailModel) View() string {
	return d.vp.View()
}

func (d *detailModel) render() {
	if d.width <= 0 {
		return
	}
	lastRead := slack.ParseTS(d.conv.LastRead)
	dividerShown := d.conv.LastRead == "" // suppress if we don't know the read mark

	var lines []string
	starts := make([]int, len(d.messages))

	for i, m := range d.messages {
		if !dividerShown && m.Time().After(lastRead) {
			lines = append(lines, dividerStyle.Render(divider(d.width, "new")))
			dividerShown = true
		}
		starts[i] = len(lines)

		lines = append(lines, d.renderMessage(m, i == d.cursor, d.bodyExpanded[m.ID], d.width, "")...)

		switch {
		case m.IsThreadRoot() && d.loadingT[m.ThreadTS]:
			lines = append(lines, "    "+threadHintStyle.Render("loading replies…"))
		case m.IsThreadRoot():
			if reps, ok := d.expanded[m.ThreadTS]; ok {
				for _, r := range reps {
					if r.ID == m.ID {
						continue // skip the root; it's already shown above
					}
					lines = append(lines, d.renderMessage(r, false, true, d.width-4, "    ")...)
				}
			}
		}
		lines = append(lines, "")
	}

	d.vp.SetContent(strings.Join(lines, "\n"))
	d.scrollToCursor(starts, len(lines))
}

func (d *detailModel) renderMessage(m slack.Message, selected, fullBody bool, width int, indent string) []string {
	gutter := "  "
	if selected {
		gutter = selGutterStyle.Render("▌ ")
	}

	header := indent + gutter + authorStyle.Render(m.Author()) + "  " + timeStyle.Render(m.Time().Format("Jan 2 15:04"))
	if m.IsThreadRoot() {
		if _, shown := d.expanded[m.ThreadTS]; shown {
			header += "  " + threadHintStyle.Render("[− thread]")
		} else {
			header += "  " + threadHintStyle.Render("[+ thread]")
		}
	}

	textWidth := width - len(indent) - 2
	if textWidth < 10 {
		textWidth = 10
	}

	body := wrapText(m.Text, textWidth)
	clipped := !fullBody && len(body) > clippedBodyLines

	lines := []string{header}
	shown := body
	if clipped {
		shown = body[:clippedBodyLines]
	}
	for _, ln := range shown {
		lines = append(lines, indent+"  "+textStyle.Render(ln))
	}
	if clipped {
		lines = append(lines, indent+"  "+threadHintStyle.Render(fmt.Sprintf("… +%d more (space)", len(body)-clippedBodyLines)))
	}
	if m.Reactions != "" {
		lines = append(lines, indent+"  "+dimStyle.Render(m.Reactions))
	}
	return lines
}

// scrollToCursor positions the viewport so the selected message stays visible,
// always keeping the top of the selected message on screen.
func (d *detailModel) scrollToCursor(starts []int, total int) {
	if len(starts) == 0 {
		d.yoff = 0
		d.vp.SetYOffset(0)
		return
	}
	top := starts[d.cursor]
	bottom := total - 1
	if d.cursor+1 < len(starts) {
		bottom = starts[d.cursor+1] - 1
	}

	h := d.vp.Height()
	off := d.yoff
	if top < off {
		off = top
	} else if bottom >= off+h {
		off = bottom - h + 1
		if off > top {
			off = top
		}
	}
	if maxOff := total - h; off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	d.yoff = off
	d.vp.SetYOffset(off)
}

func (d detailModel) firstUnreadIndex() int {
	if d.conv.LastRead == "" {
		return 0
	}
	lastRead := slack.ParseTS(d.conv.LastRead)
	for i, m := range d.messages {
		if m.Time().After(lastRead) {
			return i
		}
	}
	return 0
}

func sortByTime(msgs []slack.Message) {
	sort.SliceStable(msgs, func(i, j int) bool {
		return msgs[i].Time().Before(msgs[j].Time())
	})
}

func divider(width int, label string) string {
	n := width - len(label) - 3
	if n < 3 {
		n = 3
	}
	return strings.Repeat("─", n) + " " + label
}

// wrapText word-wraps s to the given column width, breaking over-long words
// (e.g. URLs) so nothing overflows the viewport.
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
		for _, word := range words {
			for len(word) > width {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, word[:width])
				word = word[width:]
			}
			switch {
			case line == "":
				line = word
			case len(line)+1+len(word) > width:
				out = append(out, line)
				line = word
			default:
				line += " " + word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
