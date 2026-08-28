package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		items    []core.Item
		apps     []string
		updated  time.Time
		fetching bool
		capped   bool
		note     string // non-fatal trouble, e.g. "couldn't load: folo"
	}
	markFlushedMsg struct {
		app string
		ids []string
		err error
	}
	unmarkFlushedMsg struct {
		item core.Item
		err  error
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

type feedAPIResponse struct {
	Items    []core.Wire `json:"items"`
	Apps     []string    `json:"apps,omitempty"`
	Failed   []string    `json:"failed,omitempty"`
	Warn     string      `json:"warn,omitempty"`
	Updated  string      `json:"updated,omitempty"`
	Fetching bool        `json:"fetching,omitempty"`
	Capped   bool        `json:"capped,omitempty"`
}

var feedServerHTTP = &http.Client{Timeout: 110 * time.Second}
var feedMutationHTTP = &http.Client{Timeout: 15 * time.Second}

func fetchAll(server, xTab string) tea.Cmd {
	return func() tea.Msg {
		feed, err := fetchServerFeed(server, xTab)
		if err != nil {
			return errMsg{err: err}
		}
		items := make([]core.Item, 0, len(feed.Items))
		now := time.Now()
		for _, wire := range feed.Items {
			items = append(items, wire.Item(now))
		}
		note := ""
		if len(feed.Failed) > 0 {
			note = "couldn't load: " + strings.Join(feed.Failed, ", ")
		}
		note = trimJoin(note, feed.Warn)
		updated, _ := time.Parse(time.RFC3339, feed.Updated)
		return allItemsMsg{
			items: items, apps: feed.Apps, updated: updated,
			fetching: feed.Fetching, capped: feed.Capped, note: note,
		}
	}
}

func fetchServerFeed(server, xTab string) (feedAPIResponse, error) {
	u, err := serverEndpoint(server, "/")
	if err != nil {
		return feedAPIResponse{}, err
	}
	q := u.Query()
	q.Set("json", "1")
	q.Set("order", "desc")
	if xTab == "foryou" {
		q.Set("x", "foryou")
	}
	u.RawQuery = q.Encode()
	res, err := feedServerHTTP.Get(u.String())
	if err != nil {
		return feedAPIResponse{}, fmt.Errorf("cannot reach feed server %s; start `tui serve` or pass --server: %w", server, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return feedAPIResponse{}, serverResponseError(server, res)
	}
	var feed feedAPIResponse
	if err := json.NewDecoder(res.Body).Decode(&feed); err != nil {
		return feedAPIResponse{}, fmt.Errorf("invalid response from feed server %s: %w", server, err)
	}
	return feed, nil
}

func flushMarks(server, app string, ids []string) tea.Cmd {
	return func() tea.Msg {
		return markFlushedMsg{app: app, ids: ids, err: markServerRead(server, app, ids)}
	}
}

func markServerRead(server, app string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	u, err := serverEndpoint(server, "/mark")
	if err != nil {
		return err
	}
	form := url.Values{"app": {app}, "json": {"1"}}
	for _, id := range ids {
		form.Add("id", id)
	}
	res, err := feedMutationHTTP.PostForm(u.String(), form)
	if err != nil {
		return fmt.Errorf("cannot send reads to feed server %s: %w", server, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return serverResponseError(server, res)
	}
	return nil
}

func unmarkServerRead(server, app, id string) error {
	u, err := serverEndpoint(server, "/unmark")
	if err != nil {
		return err
	}
	res, err := feedMutationHTTP.PostForm(u.String(), url.Values{"app": {app}, "id": {id}})
	if err != nil {
		return fmt.Errorf("cannot restore unread through feed server %s: %w", server, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return serverResponseError(server, res)
	}
	return nil
}

func unmarkServer(server string, item core.Item) tea.Cmd {
	return func() tea.Msg {
		return unmarkFlushedMsg{item: item, err: unmarkServerRead(server, item.App, item.ID)}
	}
}

func serverEndpoint(server, path string) (*url.URL, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid feed server URL %q", server)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func serverResponseError(server string, res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = res.Status
	}
	return fmt.Errorf("feed server %s: %s", server, message)
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
