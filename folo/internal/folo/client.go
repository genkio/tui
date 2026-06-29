package folo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// viewArticles is Folo's numeric id for the "Articles" timeline (the view at
// app.folo.is/timeline/articles). The other views (social, pictures, …) render
// quite differently, so this client targets Articles only.
const viewArticles = 0

// Client talks to one Folo account through the web app's HTTP API, authenticated
// by the browser session cookie.
type Client struct {
	http   *http.Client
	api    string // API host, e.g. https://api.folo.is
	web    string // web app origin, sent as Origin/Referer to look like the site
	cookie string
	ua     string
}

// New builds a client. api is the API host (e.g. https://api.folo.is), web is
// the web app origin (e.g. https://app.folo.is), cookie is the raw browser
// Cookie header, ua is the User-Agent to send.
func New(api, web, cookie, ua string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 30 * time.Second},
		api:    strings.TrimRight(strings.TrimSpace(api), "/"),
		web:    strings.TrimRight(strings.TrimSpace(web), "/"),
		cookie: sanitizeCookie(cookie),
		ua:     ua,
	}
}

// sanitizeCookie strips an accidental leading "Cookie:" header name and trims
// surrounding whitespace, so pasting either the value or the whole header works.
func sanitizeCookie(c string) string {
	c = strings.TrimSpace(c)
	if i := strings.IndexByte(c, ':'); i >= 0 && strings.EqualFold(strings.TrimSpace(c[:i]), "cookie") {
		c = strings.TrimSpace(c[i+1:])
	}
	return c
}

// Unreads fetches up to max articles from the Articles timeline. With unreadOnly
// it asks for only the "pending" (unread) entries, mirroring the
// /timeline/articles/all/pending page; otherwise it returns read items too.
func (c *Client) Unreads(ctx context.Context, unreadOnly bool, max int) ([]Article, error) {
	if max <= 0 {
		max = 50
	}
	var out []Article
	cursor := "" // publishedAt of the last item seen; empty asks for the first page
	for len(out) < max {
		limit := max - len(out)
		if limit > 100 { // the API serves at most 100 per page
			limit = 100
		}
		page, next, err := c.entriesPage(ctx, unreadOnly, limit, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		// A short page (or no cursor) means the timeline is exhausted.
		if next == "" || len(page) < limit {
			break
		}
		cursor = next
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// Content fetches one entry's full body, flattened to plain text. The list
// response omits content, so the UI calls this lazily when an article expands.
func (c *Client) Content(ctx context.Context, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", nil
	}
	var env entryGetResponse
	if err := c.do(ctx, http.MethodGet, "/entries?id="+url.QueryEscape(id), nil, &env); err != nil {
		return "", err
	}
	if env.Data == nil {
		return "", nil
	}
	return HTMLToText(env.Data.Entries.Content), nil
}

// MarkRead marks one entry read (the web app's POST /reads).
func (c *Client) MarkRead(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	body, err := json.Marshal(struct {
		EntryIDs []string `json:"entryIds"`
	}{[]string{id}})
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/reads", body, nil)
}

// MarkUnread restores one entry to unread. The web app does this with a DELETE
// to /reads carrying a JSON body, so this is not a plain DELETE.
func (c *Client) MarkUnread(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	body, err := json.Marshal(struct {
		EntryID string `json:"entryId"`
	}{id})
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/reads", body, nil)
}

// entriesPage fetches one page of the Articles timeline and returns the page
// plus the cursor (the last entry's publishedAt) for the next call.
func (c *Client) entriesPage(ctx context.Context, unreadOnly bool, limit int, cursor string) ([]Article, string, error) {
	reqBody := entriesRequest{View: viewArticles, Limit: limit}
	if unreadOnly {
		no := false
		reqBody.Read = &no
	}
	if cursor != "" {
		reqBody.PublishedAfter = cursor
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", err
	}

	var env entriesResponse
	if err := c.do(ctx, http.MethodPost, "/entries", body, &env); err != nil {
		return nil, "", err
	}
	if env.Code != 0 {
		return nil, "", fmt.Errorf("folo /entries: %s", envError(env.Code, env.Message))
	}

	arts := make([]Article, 0, len(env.Data))
	var last string
	for _, it := range env.Data {
		arts = append(arts, it.toArticle())
		last = it.Entries.PublishedAt
	}
	return arts, last, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, rdr)
	if err != nil {
		return err
	}
	c.setHeaders(req, body != nil)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return errSession(resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("folo %s %s: HTTP %d: %s", method, path, resp.StatusCode, snippet(data))
	}
	if out == nil {
		return nil
	}
	// A logged-out session can answer with an HTML page instead of JSON.
	if t := bytes.TrimSpace(data); len(t) == 0 || (t[0] != '{' && t[0] != '[') {
		return errSession(resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s response: %w", path, err)
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request, hasBody bool) {
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	// Folo authenticates by cookie; sending the web origin keeps requests looking
	// like the app and sidesteps any cross-origin checks.
	if c.web != "" {
		req.Header.Set("Origin", c.web)
		req.Header.Set("Referer", c.web+"/")
	}
}

func errSession(status int) error {
	return fmt.Errorf("folo rejected the session (HTTP %d): the cookie may be invalid or expired", status)
}

func envError(code int, msg string) string {
	if msg != "" {
		return fmt.Sprintf("%s (code %d)", msg, code)
	}
	return fmt.Sprintf("code %d", code)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// entriesRequest is the POST /entries body. read is a pointer so unreadOnly can
// send read:false while the "all" view omits it entirely.
type entriesRequest struct {
	View           int    `json:"view"`
	Read           *bool  `json:"read,omitempty"`
	Limit          int    `json:"limit"`
	PublishedAfter string `json:"publishedAfter,omitempty"`
}

type entriesResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    []entryWithFeed `json:"data"`
}

type entryGetResponse struct {
	Code int `json:"code"`
	Data *struct {
		Entries entryObj `json:"entries"`
	} `json:"data"`
}

// entryWithFeed mirrors the API's per-item shape: entry fields nested under
// "entries", the source feed under "feeds".
type entryWithFeed struct {
	Read    bool     `json:"read"`
	Feeds   feedObj  `json:"feeds"`
	Entries entryObj `json:"entries"`
}

type feedObj struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	SiteURL string `json:"siteUrl"`
}

type entryObj struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Content     string `json:"content"` // populated only by the single-entry GET
	PublishedAt string `json:"publishedAt"`
}

func (e entryWithFeed) toArticle() Article {
	a := Article{
		ID:      e.Entries.ID,
		Title:   clean(e.Entries.Title),
		URL:     e.Entries.URL,
		Feed:    feedTitle(e.Feeds),
		Author:  clean(e.Entries.Author),
		Summary: squish(HTMLToText(e.Entries.Description)),
	}
	if t, err := time.Parse(time.RFC3339, e.Entries.PublishedAt); err == nil {
		a.Published = t
		a.Age = relAge(time.Since(t))
	}
	return a
}

// feedTitle prefers the feed's own title, falling back to its host so a row is
// never left without a source label.
func feedTitle(f feedObj) string {
	if t := clean(f.Title); t != "" {
		return t
	}
	for _, raw := range []string{f.SiteURL, f.URL} {
		if h := hostOf(raw); h != "" {
			return h
		}
	}
	return ""
}

func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func relAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
}

func squish(s string) string { return strings.Join(strings.Fields(s), " ") }

// clean decodes HTML entities (RSS titles often carry &amp; and friends) and
// collapses whitespace, for short single-line fields like titles and authors.
func clean(s string) string { return squish(html.UnescapeString(s)) }
