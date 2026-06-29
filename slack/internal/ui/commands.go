package ui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/slack-tui/internal/config"
	"github.com/genkio/slack-tui/internal/emoji"
	"github.com/genkio/slack-tui/internal/mcp"
	"github.com/genkio/slack-tui/internal/slack"
)

// Messages flowing back into the update loop from background tool calls.
type (
	unreadsMsg struct{ convs []slack.Conversation }
	historyMsg struct {
		convID string
		msgs   []slack.Message
	}
	repliesMsg struct {
		threadTS string
		msgs     []slack.Message
	}
	markedMsg      struct{ label string }
	openedMsg      struct{}
	copiedMsg      struct{}
	autoRefreshMsg struct{}
	errMsg         struct{ err error }

	emojiListMsg struct{ names []string }
	emojiErrMsg  struct{ err error }
	reactedMsg   struct {
		emoji   string
		removed bool
	}
)

func fetchUnreads(ctx context.Context, c *mcp.Client, u config.UnreadsConfig) tea.Cmd {
	return func() tea.Msg {
		convs, err := c.Unreads(ctx, u)
		if err != nil {
			return errMsg{err}
		}
		return unreadsMsg{convs}
	}
}

// scheduleRefresh emits an autoRefreshMsg after d; the update loop reschedules
// it to form a recurring timer.
func scheduleRefresh(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return autoRefreshMsg{} })
}

func fetchHistory(ctx context.Context, c *mcp.Client, convID, limit string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := c.History(ctx, convID, limit)
		if err != nil {
			return errMsg{err}
		}
		return historyMsg{convID: convID, msgs: msgs}
	}
}

func fetchReplies(ctx context.Context, c *mcp.Client, convID, threadTS string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := c.Replies(ctx, convID, threadTS)
		if err != nil {
			return errMsg{err}
		}
		return repliesMsg{threadTS: threadTS, msgs: msgs}
	}
}

func markRead(ctx context.Context, c *mcp.Client, convID, ts, label string) tea.Cmd {
	return func() tea.Msg {
		if err := c.MarkRead(ctx, convID, ts); err != nil {
			return errMsg{err}
		}
		return markedMsg{label: label}
	}
}

func fetchEmojis(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		names, err := emoji.List(ctx)
		if err != nil {
			return emojiErrMsg{err}
		}
		return emojiListMsg{names}
	}
}

// react adds an emoji reaction, toggling it off when it is already present.
// reactions_add reports "already_reacted" when the caller has the reaction, so
// we fall back to reactions_remove to make pressing the same emoji a toggle.
func react(ctx context.Context, c *mcp.Client, convID, ts, name string) tea.Cmd {
	return func() tea.Msg {
		err := c.AddReaction(ctx, convID, ts, name)
		if err == nil {
			return reactedMsg{emoji: name}
		}
		if !strings.Contains(err.Error(), "already_reacted") {
			return errMsg{err}
		}
		if err := c.RemoveReaction(ctx, convID, ts, name); err != nil {
			return errMsg{err}
		}
		return reactedMsg{emoji: name, removed: true}
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
