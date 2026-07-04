package main

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Messages flowing back into the "all" screen's update loop.
type (
	allItemsMsg struct {
		items []item
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
			items []item
			err   error
		}
		ch := make(chan res, len(apps))
		for _, app := range apps {
			go func(app string) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, "make", "-C", filepath.Join(root, "plugins", app), "json").Output()
				if err != nil {
					ch <- res{app: app, err: err}
					return
				}
				items, perr := parseItems(out, now)
				ch <- res{app: app, items: items, err: perr}
			}(app)
		}
		var all []item
		var failed []string
		for range apps {
			r := <-ch
			if r.err != nil {
				failed = append(failed, r.app)
				continue
			}
			all = append(all, r.items...)
		}
		mergeSort(all)
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
	cmd := exec.CommandContext(ctx, "make", "-C", filepath.Join(root, "plugins", app), "mark-read")
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	return cmd.Run()
}

func scheduleFlush() tea.Cmd {
	return tea.Tick(flushDebounce, func(time.Time) tea.Msg { return flushTickMsg{} })
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
		return exec.Command("cmd", "/c", "start", "", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}
