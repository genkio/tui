package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/inoreader/internal/inoreader"
)

// Messages flowing back into the update loop from background API calls.
type (
	articlesMsg struct {
		items []core.Item
		reset bool // jump cursor to the top (manual refresh / first load), vs keep position
	}
	markedMsg struct {
		id  string
		err error
	}
	unmarkedMsg struct {
		id  string
		err error
	}
	openedMsg         struct{}
	carbonylDoneMsg   struct{}
	carbonylBrowseMsg struct{ url string }
	copiedMsg         struct{}
	autoRefreshMsg    struct{}
	errMsg            struct{ err error }
)

func fetchUnreads(ctx context.Context, c *inoreader.Client, unreadOnly bool, max int, reset bool) tea.Cmd {
	return func() tea.Msg {
		arts, err := c.Unreads(ctx, unreadOnly, max)
		if err != nil {
			return errMsg{err}
		}
		return articlesMsg{items: inoreader.ToItems(arts), reset: reset}
	}
}

// markRead marks one article read in the background. Marking is optimistic in
// the UI (the row greys out immediately), so this just reports the outcome and
// the id, letting the model revert if the server rejected it.
func markRead(ctx context.Context, c *inoreader.Client, id string) tea.Cmd {
	return func() tea.Msg {
		return markedMsg{id: id, err: c.MarkRead(ctx, id)}
	}
}

// markUnread backs the K pin on an already-read article: without restoring the
// article to unread on the server, the next refresh would drop it.
func markUnread(ctx context.Context, c *inoreader.Client, id string) tea.Cmd {
	return func() tea.Msg {
		return unmarkedMsg{id: id, err: c.MarkUnread(ctx, id)}
	}
}

// scheduleRefresh emits an autoRefreshMsg after d; the update loop reschedules
// it to form a recurring timer.
func scheduleRefresh(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return autoRefreshMsg{} })
}

func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		if err := core.OpenInBrowser(url); err != nil {
			return errMsg{err}
		}
		return openedMsg{}
	}
}

func copyToClipboard(s string) tea.Cmd {
	return func() tea.Msg {
		if err := core.CopyOSC52(s); err != nil {
			return errMsg{err}
		}
		return copiedMsg{}
	}
}
