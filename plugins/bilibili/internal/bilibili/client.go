package bilibili

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultUA mimics the browser the session was captured in. bilibili's web APIs
// answer a logged-in browser; a bare programmatic agent is risk-controlled
// instead.
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// feedAPI is the endpoint t.bilibili.com's own page calls for the 动态 timeline.
// type=video asks for the video posts only, which is the whole point of this
// app: text statuses and picture albums are not what a reader comes to bilibili
// for.
const feedAPI = "https://api.bilibili.com/x/polymer/web-dynamic/v1/feed/all"

// maxPages bounds a fetch: one page carries about a dozen dynamics, so five is
// deep enough for any sane max while a runaway offset cannot walk the whole
// timeline.
const maxPages = 5

// Client reads the authenticated user's following 动态 (the video posts of the
// uploaders you follow).
type Client struct {
	hc     *http.Client
	cookie string // the whole bilibili.com cookie set, joined for the Cookie header
	ua     string
}

// New builds a client from the captured browser session cookie set. An empty ua
// falls back to the default browser agent.
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

// Feed returns up to limit video posts from the following 动态, newest first,
// paging until it has enough (bilibili hands out about a dozen per page).
func (c *Client) Feed(ctx context.Context, limit int) ([]Video, error) {
	if limit <= 0 {
		limit = 40
	}
	now := time.Now()
	var out []Video
	offset := ""
	for page := 1; page <= maxPages && len(out) < limit; page++ {
		got, err := c.page(ctx, page, offset, now)
		if err != nil {
			// A later page failing still leaves a usable timeline; only the first
			// one is the fetch.
			if page == 1 {
				return nil, err
			}
			break
		}
		out = append(out, got.Videos...)
		if !got.HasMore || got.Offset == "" || got.Offset == offset {
			break
		}
		offset = got.Offset
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// page fetches one page of the feed. offset is what the previous page reported;
// empty starts at the top.
func (c *Client) page(ctx context.Context, page int, offset string, now time.Time) (feedPage, error) {
	q := url.Values{
		"type":            {"video"},
		"platform":        {"web"},
		"page":            {strconv.Itoa(page)},
		"timezone_offset": {"-480"}, // bilibili's own web client sends UTC+8
		"features":        {"itemOpusStyle"},
	}
	if offset != "" {
		q.Set("offset", offset)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedAPI+"?"+q.Encode(), nil)
	if err != nil {
		return feedPage{}, err
	}
	req.Header.Set("user-agent", c.ua)
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("accept-language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("referer", "https://t.bilibili.com/")
	req.Header.Set("origin", "https://t.bilibili.com")
	if c.cookie != "" {
		req.Header.Set("cookie", c.cookie)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return feedPage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return feedPage{}, err
	}

	switch {
	// 412 is bilibili's risk control: the request looked automated, or this
	// address is on its list. Nothing about the session is wrong, so re-auth is
	// the wrong advice.
	case resp.StatusCode == http.StatusPreconditionFailed:
		return feedPage{}, errors.New("bilibili risk-controlled this request (HTTP 412); wait a while and refresh")
	case resp.StatusCode == http.StatusTooManyRequests:
		return feedPage{}, errors.New("bilibili rate limit hit (HTTP 429); wait a bit before refreshing")
	case resp.StatusCode != http.StatusOK:
		return feedPage{}, fmt.Errorf("bilibili returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}

	got, err := parseFeed(body, now)
	if err != nil {
		return feedPage{}, err
	}
	if err := apiError(got.Code, got.Message); err != nil {
		return feedPage{}, err
	}
	return got, nil
}

// apiError maps a reply code to an error the user can act on. bilibili answers
// HTTP 200 with a code in the body, so this is where a dead session surfaces.
func apiError(code int, message string) error {
	switch code {
	case 0:
		return nil
	// -101 账号未登录 / -6 both mean the cookie no longer authenticates.
	case -101, -6:
		return errors.New("bilibili session is stale: the saved cookie expired. Re-run 'tui bilibili --auth' to refresh it")
	// The query this app sends is fixed, so 请求错误 in practice means the cookie
	// itself is not one bilibili will read.
	case -400:
		return errors.New("bilibili session is stale or malformed (code -400): re-run 'tui bilibili --auth' to capture it again")
	case -352:
		return errors.New("bilibili risk-controlled this request (code -352); wait a while and refresh")
	case -509:
		return errors.New("bilibili rate limit hit (code -509); wait a bit before refreshing")
	default:
		if message == "" {
			message = "no message"
		}
		return fmt.Errorf("bilibili 动态 feed returned code %d: %s", code, message)
	}
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
