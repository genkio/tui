package douban

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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
	body, err := c.get(ctx, "https://www.douban.com/")
	if err != nil {
		return nil, err
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
	c.unclip(ctx, statuses)
	return statuses, nil
}

// get fetches one douban page as the captured browser session and returns its
// body, mapping the ways douban says no onto errors the user can act on.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
	return body, nil
}

// maxUnclip caps how many status pages one refresh will open. Each is a second
// request douban did not ask for, and the WAF counts them; a timeline rarely
// holds more than a couple of clipped 动态 anyway.
const maxUnclip = 8

// unclip replaces every clipped saying with the whole text, read off the page
// the （全文） link points at. Those pages are login-walled, so this is the only
// place the rest of a long 动态 can come from. A page that will not load leaves
// its status as douban served it: a short read beats no feed.
func (c *Client) unclip(ctx context.Context, statuses []Status) {
	type job struct {
		status *Status
		clip   Clip
	}
	var jobs []job
	for i := range statuses {
		for _, cl := range statuses[i].Clips {
			if cl.URL == "" || cl.Text == "" {
				continue
			}
			jobs = append(jobs, job{&statuses[i], cl})
		}
	}
	if len(jobs) > maxUnclip {
		jobs = jobs[:maxUnclip]
	}

	full := make([]string, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := c.get(ctx, j.clip.URL)
			if err != nil {
				return
			}
			if text, err := parseFull(body); err == nil {
				full[i] = text
			}
		}()
	}
	wg.Wait()

	for i, j := range jobs {
		if full[i] == "" || full[i] == j.clip.Text {
			continue
		}
		// the saying rides in the status text and, for a reshare, in the embed;
		// whichever holds this clip is the one that gets the whole version
		j.status.Text = strings.Replace(j.status.Text, j.clip.Text, full[i], 1)
		if j.status.Embed != nil {
			j.status.Embed.Text = strings.Replace(j.status.Embed.Text, j.clip.Text, full[i], 1)
		}
	}
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
