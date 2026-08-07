package reddit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// defaultUA mimics a real browser. Reddit's JSON endpoints reject bare
// programmatic user agents (python-requests, Go-http-client), but serve a
// browser the same way they serve the site.
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// Client reads the authenticated user's home timeline from old.reddit.com's
// JSON API using the captured browser session cookie.
type Client struct {
	hc     *http.Client
	cookie string // the whole reddit.com cookie set, joined for the Cookie header
}

// New builds a client from the captured browser session cookie set.
func New(cookie string) *Client {
	return &Client{
		hc:     &http.Client{Timeout: 30 * time.Second},
		cookie: cookie,
	}
}

// homeEndpoints are the JSON aliases for the logged-in home feed, tried in
// order. Reddit rotates which host serves the legacy JSON API; a path that 404s
// on one may answer on another, so we fall through until one parses.
var homeEndpoints = []string{
	"https://old.reddit.com/.json",
	"https://www.reddit.com/.json",
}

// Home fetches up to limit posts from the authenticated home timeline
// ("the front page") — the personalized feed Reddit shows when logged in. The
// session cookie is what makes it personal; without one every endpoint redirects
// or returns an error page.
func (c *Client) Home(ctx context.Context, limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 25
	}
	// sort=new gives a stable reverse-chronological feed, so a reload doesn't
	// shuffle the pool and old items stop resurfacing (Reddit's default "best"
	// reshuffles every fetch).
	query := "limit=" + url.QueryEscape(fmt.Sprint(limit)) + "&raw_json=1&sort=new"

	var lastErr error
	for _, base := range homeEndpoints {
		posts, err := c.fetch(ctx, base+"?"+query, limit)
		if err == nil {
			return posts, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) fetch(ctx context.Context, endpoint string, limit int) ([]Post, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", defaultUA)
	req.Header.Set("accept", "application/json")
	if c.cookie != "" {
		req.Header.Set("cookie", c.cookie)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("reddit rejected the session (HTTP %d); re-run make auth", resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("reddit returned HTTP 404 for %s", endpoint)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("reddit rate limit hit (HTTP 429); wait a bit before refreshing")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("reddit returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	return parseHome(body)
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
