package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
)

// Messages flowing back into the "all" screen's update loop.
type (
	allItemsMsg struct {
		items []core.Item
		note  string // non-fatal trouble, e.g. "couldn't load: folo"
	}
	markFlushedMsg struct {
		app string
		ids []string
		err error
	}
	flushTickMsg      struct{}
	openedMsg         struct{}
	copiedMsg         struct{}
	carbonylDoneMsg   struct{}
	carbonylBrowseMsg struct{ url string }
	errMsg            struct{ err error }
)

// flushDebounce coalesces a burst of read marks (holding j) into one mark-read
// subprocess per app instead of one per keystroke.
const flushDebounce = 1500 * time.Millisecond

// fetchAll runs each authed app's `make json` concurrently, parses the unread
// items, and returns them merged and sorted newest-first. One app failing (an
// expired cookie, say) drops only that app, noted for the status line.
func fetchAll(root string, apps []string) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		type res struct {
			app   string
			items []core.Item
			err   error
		}
		ch := make(chan res, len(apps))
		for _, app := range apps {
			go func(app string) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, self(), app, "--json")
				cmd.Env = appEnv(filepath.Join(root, "plugins", app))
				out, err := cmd.Output()
				if err != nil {
					ch <- res{app: app, err: err}
					return
				}
				items, perr := core.ParseItems(out, now)
				ch <- res{app: app, items: items, err: perr}
			}(app)
		}
		var all []core.Item
		var failed []string
		for range apps {
			r := <-ch
			if r.err != nil {
				failed = append(failed, r.app)
				continue
			}
			all = append(all, r.items...)
		}
		core.MergeSort(all)
		note := ""
		if len(failed) > 0 {
			note = "couldn't load: " + strings.Join(failed, ", ")
		}
		return allItemsMsg{items: all, note: note}
	}
}

// flushMarks marks read, in one app, every id accumulated since the last flush,
// via the same `make mark-read` contract the count uses. Marking is idempotent,
// so a retried id is harmless.
func flushMarks(root, app string, ids []string) tea.Cmd {
	return func() tea.Msg {
		return markFlushedMsg{app: app, ids: ids, err: runMarkRead(root, app, ids, 90*time.Second)}
	}
}

// runMarkRead pipes ids (one per line) into the app's mark-read subprocess.
func runMarkRead(root, app string, ids []string, timeout time.Duration) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, self(), app, "--mark-read")
	cmd.Env = appEnv(filepath.Join(root, "plugins", app))
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	return cmd.Run()
}

func scheduleFlush() tea.Cmd {
	return tea.Tick(flushDebounce, func(time.Time) tea.Msg { return flushTickMsg{} })
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
