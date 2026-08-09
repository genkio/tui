package douban

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultUA mimics the browser the session was captured in. Douban serves the
// homepage stream to any logged-in browser; a bare programmatic agent gets
// bot-walled instead.
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// Client reads the authenticated user's following timeline (友邻广播) from the
// desktop homepage, which server-renders the stream for a logged-in session.
// The mobile rexxar JSON API answers need_permission to web sessions (it is
// app-only), so scraping the homepage HTML is the reliable route.
type Client struct {
	hc     *http.Client
	cookie string // the whole douban.com cookie set, joined for the Cookie header
	ua     string
}

// New builds a client from the captured browser session cookie set. An empty
// ua falls back to the default browser agent.
func New(cookie, ua string) *Client {
	if strings.TrimSpace(ua) == "" {
		ua = defaultUA
	}
	return &Client{
		hc:     &http.Client{Timeout: 30 * time.Second},
		cookie: cookie,
		ua:     ua,
	}
}

// Feed is what the app shows: the following timeline with the configured 榜单
// charts mixed in by publish time, newest first. The timeline is the feed's
// spine, so its failure is the call's failure, while a chart that will not load
// is simply left out.
func (c *Client) Feed(ctx context.Context, limit int, charts []string) ([]Status, error) {
	statuses, err := c.Home(ctx, limit)
	if err != nil {
		return nil, err
	}
	statuses = append(statuses, c.Charts(ctx, charts, time.Now())...)
	sortByRecency(statuses)
	return statuses, nil
}

// Home fetches up to limit statuses from the following timeline. The session
// cookie is what makes the homepage personal; without one douban serves the
// logged-out landing page, which is reported as a stale session.
func (c *Client) Home(ctx context.Context, limit int) ([]Status, error) {
	if limit <= 0 {
		limit = 50
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.douban.com/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", c.ua)
	req.Header.Set("accept", "text/html,application/xhtml+xml")
	req.Header.Set("accept-language", "zh-CN,zh;q=0.9,en;q=0.8")
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

	// The anti-crawl WAF 302s flagged traffic to a "sorry" page; the block is
	// per-IP and temporary, so tell the user to wait rather than re-auth.
	if resp.Request != nil && strings.Contains(resp.Request.URL.Path, "/misc/sorry") {
		return nil, errors.New("douban temporarily blocked this IP (too many requests); wait a while and refresh")
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("douban rejected the session (HTTP %d): the cookie may be expired. Re-run 'tui douban --auth'", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("douban rate limit hit (HTTP 429); wait a bit before refreshing")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("douban returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}

	statuses, err := parseHome(body, time.Now())
	if err != nil {
		return nil, err
	}
	// A dead session gets the logged-out landing page: no stream, just login
	// links. Surface that as a session problem rather than an empty timeline.
	if len(statuses) == 0 && bytes.Contains(body, []byte("accounts.douban.com")) && !bytes.Contains(body, []byte("status-item")) {
		return nil, errors.New("douban session is stale: the saved cookie expired. Re-run 'tui douban --auth' to refresh it")
	}
	if limit < len(statuses) {
		statuses = statuses[:limit]
	}
	return statuses, nil
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
