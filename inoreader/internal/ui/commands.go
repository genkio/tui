package ui

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/inoreader-tui/internal/inoreader"
)

// Messages flowing back into the update loop from background API calls.
type (
	articlesMsg struct {
		articles []inoreader.Article
		reset    bool // jump cursor to the top (manual refresh / first load), vs keep position
	}
	markedMsg struct {
		id  string
		err error
	}
	unmarkedMsg struct {
		id  string
		err error
	}
	openedMsg      struct{}
	autoRefreshMsg struct{}
	errMsg         struct{ err error }
)

func fetchUnreads(ctx context.Context, c *inoreader.Client, unreadOnly bool, max int, reset bool) tea.Cmd {
	return func() tea.Msg {
		arts, err := c.Unreads(ctx, unreadOnly, max)
		if err != nil {
			return errMsg{err}
		}
		return articlesMsg{articles: arts, reset: reset}
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
		if err := openInBrowser(url); err != nil {
			return errMsg{err}
		}
		return openedMsg{}
	}
}

func openInBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		// The empty "" is start's window-title argument; without it a quoted URL
		// is mistaken for the title and nothing opens.
		return exec.Command("cmd", "/c", "start", "", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}
