package ui

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/x-tui/internal/x"
)

// Messages flowing back into the update loop from background API calls.
type (
	timelineMsg struct {
		tab    x.Tab
		tweets []x.Tweet
		reset  bool // jump cursor to the top (tab switch / manual refresh), vs keep position
	}
	openedMsg      struct{}
	autoRefreshMsg struct{}
	errMsg         struct{ err error }
)

func fetchTimeline(ctx context.Context, c *x.Client, tab x.Tab, max int, reset bool) tea.Cmd {
	return func() tea.Msg {
		tweets, err := c.Timeline(ctx, tab, max)
		if err != nil {
			return errMsg{err}
		}
		return timelineMsg{tab: tab, tweets: tweets, reset: reset}
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
