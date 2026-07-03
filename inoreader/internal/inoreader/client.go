package inoreader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Client talks to one Inoreader account through the web app's "xajax" RPC
// endpoints, authenticated by the browser session cookie.
type Client struct {
	http   *http.Client
	base   string
	cookie string
	ua     string
}

// New builds a client. base is the site root (e.g. https://www.inoreader.com),
// cookie is the raw browser Cookie header, ua is the User-Agent to send.
func New(base, cookie, ua string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 30 * time.Second},
		base:   strings.TrimRight(strings.TrimSpace(base), "/"),
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

// Unreads fetches up to max articles oldest-first. With unreadOnly it asks for
// only unread items; otherwise it returns the "All articles" view.
//
// print_articles serves only a single page (~20), so we page through it like
// the site's infinite scroll: re-request with the offset set to the count seen
// so far. Dedup across pages doubles as the termination guard: a server that
// ignores the offset adds nothing new, ending the loop instead of looping
// forever or returning duplicates.
func (c *Client) Unreads(ctx context.Context, unreadOnly bool, max int) ([]Article, error) {
	if max <= 0 {
		max = 50
	}

	seen := map[string]bool{}
	var out []Article
	for len(out) < max {
		env, err := c.printArticles(ctx, unreadOnly, len(out))
		if err != nil {
			return nil, err
		}

		loaded := env.articlesLoaded()
		before := len(out)
		add := func(id string) {
			if id == "" || seen[id] || len(out) >= max {
				return
			}
			frag, ok := loaded[id]
			if !ok {
				return
			}
			seen[id] = true
			out = append(out, scrapeArticle(id, frag))
		}
		for _, id := range env.seenIDs() { // display order (oldest first)
			add(id)
		}
		for id := range loaded { // any loaded item not listed in seen_ids
			add(id)
		}

		if len(out) == before { // no new articles: exhausted or offset ignored
			break
		}
	}
	return out, nil
}

// printArticles fetches one page starting at offset (the count of articles
// already shown), mirroring the web app's print_articles xajax call.
func (c *Client) printArticles(ctx context.Context, unreadOnly bool, offset int) (*xjxEnvelope, error) {
	view := 0
	if unreadOnly {
		view = 1
	}
	// articles_order:1 = oldest first; filter all_articles = every feed.
	args := fmt.Sprintf(`{"view_unread":%d,"articles_order":1,"view_style":0,"filter_type":"all_articles","filter_id":0}`, view)
	body := fmt.Sprintf("xjxfun=print_articles&xjxr=1&xjxargs[]=Bfalse&xjxargs[]=N%d&xjxargs[]=%s", offset, url.QueryEscape(args))
	return c.postXajax(ctx, "print_articles", body)
}

// MarkRead marks one article read.
func (c *Client) MarkRead(ctx context.Context, id string) error {
	return c.setReadState(ctx, id, 2)
}

// MarkUnread restores one article to unread (the web app's "mark as unread").
func (c *Client) MarkUnread(ctx context.Context, id string) error {
	return c.setReadState(ctx, id, 1)
}

// setReadState drives the read_article RPC: a {id: state} map plus a small
// view-context object; the large feed-id list the browser includes is optional.
// State 2 = read, 1 = unread, mirroring the site's mark_read JS, which sends
// the state both as the map value and as the trailing argument.
func (c *Client) setReadState(ctx context.Context, id string, state int) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	arg1 := url.QueryEscape(fmt.Sprintf(`{"%s":%d}`, id, state))
	viewCtx := url.QueryEscape(`{"view_unread":"1","articles_order":"1","view_style":"0","filter_type":"all_articles","filter_id":0}`)
	body := fmt.Sprintf("xjxfun=read_article&xjxr=1&xjxargs[]=%s&xjxargs[]=N0&xjxargs[]=%s&xjxargs[]=Bfalse&xjxargs[]=N%d", arg1, viewCtx, state)
	_, err := c.postXajax(ctx, "read_article", body)
	return err
}

func (c *Client) postXajax(ctx context.Context, fn, body string) (*xjxEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/?xjxfun="+fn, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", c.base+"/all_articles")
	req.Header.Set("Accept", "application/json, text/javascript, */*")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, errSession(resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("inoreader %s: HTTP %d", fn, resp.StatusCode)
	}
	// A logged-out session returns the HTML login page, not JSON.
	if t := bytes.TrimSpace(data); len(t) == 0 || t[0] != '{' {
		return nil, errSession(resp.StatusCode)
	}
	var env xjxEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", fn, err)
	}
	return &env, nil
}

func errSession(status int) error {
	return fmt.Errorf("inoreader rejected the session (HTTP %d): the cookie may be invalid or expired", status)
}

// xjxEnvelope is the xajax response: a list of UI commands. We only care about
// the two that carry article data.
type xjxEnvelope struct {
	Obj []xjxCmd `json:"xjxobj"`
}

type xjxCmd struct {
	Cmd  string          `json:"cmd"`
	Func string          `json:"func"`
	Data json.RawMessage `json:"data"`
}

func (e *xjxEnvelope) cmd(fn string) *xjxCmd {
	for i := range e.Obj {
		if e.Obj[i].Func == fn {
			return &e.Obj[i]
		}
	}
	return nil
}

// seenIDs returns the article ids in display order (oldest first).
func (e *xjxEnvelope) seenIDs() []string {
	c := e.cmd("set_seen_ids")
	if c == nil {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(c.Data, &arr); err != nil || len(arr) == 0 {
		return nil
	}
	var nums []json.Number
	if err := json.Unmarshal(arr[0], &nums); err != nil {
		return nil
	}
	ids := make([]string, 0, len(nums))
	for _, n := range nums {
		ids = append(ids, n.String())
	}
	return ids
}

// articlesLoaded maps article id -> its HTML fragment. The command's data is a
// mixed array whose first element is the {id: html} object; the remaining
// elements are other types, so we decode only element 0.
func (e *xjxEnvelope) articlesLoaded() map[string]string {
	c := e.cmd("articles_loaded")
	if c == nil {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(c.Data, &arr); err != nil || len(arr) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(arr[0], &m); err != nil {
		return nil
	}
	return m
}

// scrapeArticle pulls the fields we show out of one article's HTML fragment.
// The selectors mirror Inoreader's web markup, so this is the part most likely
// to need updating if they change their front end.
func scrapeArticle(id, fragment string) Article {
	a := Article{ID: id}
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return a
	}

	var content *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			switch {
			case n.Data == "a" && strings.Contains(class, "article_title_link"):
				if a.Title == "" {
					a.Title = squish(textOf(n))
					a.URL = attr(n, "href")
				}
			case n.Data == "a" && strings.HasPrefix(attr(n, "id"), "article_feed_info_link_"):
				if a.Feed == "" {
					a.Feed = squish(textOf(n))
				}
			case n.Data == "span" && strings.Contains(class, "article-author-"):
				if a.Author == "" {
					a.Author = squish(textOf(n))
				}
			case strings.Contains(class, "article_subtitle_date_wrapper"):
				if a.Age == "" {
					a.Age = squish(textOf(n))
				}
			case content == nil && strings.HasPrefix(attr(n, "id"), "article_contents_inner_"):
				content = n
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)

	if content != nil {
		var sb strings.Builder
		for ch := content.FirstChild; ch != nil; ch = ch.NextSibling {
			html.Render(&sb, ch)
		}
		a.Content = HTMLToText(sb.String())
	}
	return a
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			f(ch)
		}
	}
	f(n)
	return b.String()
}

func squish(s string) string { return strings.Join(strings.Fields(s), " ") }
