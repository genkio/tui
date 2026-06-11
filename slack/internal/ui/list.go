package ui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genkio/slack-tui/internal/slack"
)

// convItem adapts a Conversation to the bubbles list.Item interface.
type convItem struct{ conv slack.Conversation }

func (i convItem) FilterValue() string { return i.conv.Name }

func convItems(convs []slack.Conversation) []list.Item {
	items := make([]list.Item, len(convs))
	for i, c := range convs {
		items[i] = convItem{c}
	}
	return items
}

// convDelegate renders one conversation row: a type badge, the name, and the
// unread count right-aligned.
type convDelegate struct{}

func (convDelegate) Height() int                         { return 1 }
func (convDelegate) Spacing() int                        { return 0 }
func (convDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (convDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(convItem)
	if !ok {
		return
	}
	c := it.conv

	badge := badgeFor(c.Type)
	count := countStyle.Render(fmt.Sprintf("%d", c.UnreadCount))

	cursor := "  "
	nameSt := nameStyle
	if index == m.Index() {
		cursor = cursorStyle.Render("▌ ")
		nameSt = nameSelStyle
	}

	avail := m.Width() - 2 - lipgloss.Width(badge) - 1 - lipgloss.Width(count) - 1
	if avail < 4 {
		avail = 4
	}
	name := truncate(c.Name, avail)
	pad := avail - lipgloss.Width(name)
	if pad < 0 {
		pad = 0
	}

	fmt.Fprintf(w, "%s%s %s%s %s", cursor, badge, nameSt.Render(name), strings.Repeat(" ", pad), count)
}

func badgeFor(t slack.ChannelType) string {
	label := fmt.Sprintf("%-8s", t.Label())
	switch t {
	case slack.TypeDM, slack.TypeGroupDM:
		return badgeDMStyle.Render(label)
	default:
		return badgeChanStyle.Render(label)
	}
}

// truncate shortens s to at most max runes, adding an ellipsis when cut.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
