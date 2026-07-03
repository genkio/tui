package ui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/x-tui/internal/readstore"
	"github.com/genkio/x-tui/internal/x"
)

// Messages flowing back into the update loop from background API calls.
type (
	timelineMsg struct {
		tab    x.Tab
		tweets []x.Tweet
		reset  bool // jump cursor to the top (tab switch / manual refresh), vs keep position
	}
	openedMsg       struct{}
	carbonylDoneMsg struct{}
	copiedMsg       struct{}
	autoRefreshMsg  struct{}
	flushReadMsg    struct{} // debounce window elapsed; time to persist read marks
	readSavedMsg    struct{ err error }
	errMsg          struct{ err error }
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

// saveRead persists the read store off the update loop.
func saveRead(s *readstore.Store) tea.Cmd {
	return func() tea.Msg {
		return readSavedMsg{err: s.Save()}
	}
}

func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		if err := openInBrowser(url); err != nil {
			return errMsg{err}
		}
		return openedMsg{}
	}
}

func copyToClipboard(s string) tea.Cmd {
	return func() tea.Msg {
		seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\a"
		// tmux doesn't forward an app's bare OSC52 to the outer terminal; wrap it
		// in DCS passthrough (needs allow-passthrough on) so tmux re-emits it.
		if os.Getenv("TMUX") != "" {
			seq = "\x1bPtmux;\x1b" + seq + "\x1b\\"
		}
		if _, err := os.Stdout.WriteString(seq); err != nil {
			return errMsg{err}
		}
		return copiedMsg{}
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
