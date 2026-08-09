package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/douban/internal/config"
	"github.com/genkio/tui/plugins/douban/internal/douban"
	"github.com/genkio/tui/plugins/douban/internal/readstore"
)

// Messages flowing back into the update loop from background API calls.
type (
	homeMsg struct {
		statuses []douban.Status
		reset    bool // jump cursor to the top (refresh), vs keep position
	}
	openedMsg         struct{}
	carbonylDoneMsg   struct{}
	carbonylBrowseMsg struct{ url string }
	copiedMsg         struct{}
	autoRefreshMsg    struct{}
	flushReadMsg      struct{} // debounce window elapsed; time to persist read marks
	readSavedMsg      struct{ err error }
	errMsg            struct{ err error }
)

func fetchHome(ctx context.Context, c *douban.Client, cfg config.Config, reset bool) tea.Cmd {
	return func() tea.Msg {
		statuses, err := c.Feed(ctx, cfg.MaxStatuses, cfg.Charts)
		if err != nil {
			return errMsg{err}
		}
		return homeMsg{statuses: statuses, reset: reset}
	}
}

// scheduleRefresh emits an autoRefreshMsg after d; the update loop reschedules
// it to form a recurring timer.
func scheduleRefresh(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return autoRefreshMsg{} })
}

// saveRead persists the read store off the update loop.
func saveRead(s *readstore.Store) tea.Cmd {
	return func() tea.Msg {
		return readSavedMsg{err: s.Save()}
	}
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
