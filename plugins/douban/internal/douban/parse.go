package douban

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// cst is Douban's wall-clock zone: created_at titles come as a bare
// "2006-01-02 15:04:05" string in Beijing time.
var cst = time.FixedZone("CST", 8*3600)

// parseHome scrapes the 友邻广播 stream out of the logged-in desktop homepage.
// Each div.new-status wrapper is one timeline entry: its first div.status-item
// is the followed user's status, and a reshare nests the original inside a
// div.status-real-wrapper sibling. now anchors the relative age strings.
func parseHome(body []byte, now time.Time) ([]Status, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing douban homepage: %w", err)
	}
	var out []Status
	for _, wrapper := range findAll(doc, func(n *html.Node) bool {
		return n.Data == "div" && hasClass(n, "new-status")
	}) {
		main := findFirst(wrapper, func(n *html.Node) bool {
			return n.Data == "div" && hasClass(n, "status-item")
		})
		if main == nil {
			continue
		}
		p := parseItem(main)
		if p.sid == "" {
			continue
		}
		parts := p.textParts()
		// a reshare carries the original status in a sibling wrapper; quote it
		if real := findFirst(wrapper, func(n *html.Node) bool {
			return n.Data == "div" && hasClass(n, "status-real-wrapper")
		}); real != nil {
			if orig := findFirst(real, func(n *html.Node) bool {
				return n.Data == "div" && hasClass(n, "status-item")
			}); orig != nil {
				o := parseItem(orig)
				head := strings.TrimSpace(o.author + " " + o.activity)
				quoted := o.saying
				if quoted == "" && o.cardTitle != "" {
					quoted = o.cardTitle
				}
				parts = append(parts, strings.TrimSpace("↻ "+head+": "+quoted))
				if o.cardTitle != "" && quoted != o.cardTitle {
					parts = append(parts, "→ "+o.cardTitle)
				}
				if o.cardURL != "" {
					parts = append(parts, o.cardURL)
				}
				if o.cardDesc != "" {
					parts = append(parts, o.cardDesc)
				}
			}
		}
		s := Status{
			ID:       p.sid,
			Author:   p.author,
			Activity: p.activity,
			Text:     strings.Join(parts, "\n\n"),
			URL:      stripQuery(p.url),
		}
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", p.created, cst); err == nil {
			s.CreatedAt = t.UTC()
			s.Age = relAge(now.Sub(s.CreatedAt))
		}
		out = append(out, s)
	}
	return out, nil
}

// item is the raw pieces scraped from one div.status-item.
type item struct {
	sid      string
	url      string // data-status-url, tracking params still attached
	author   string
	activity string // e.g. "说", "想读", "转发"; may be empty
	saying   string // blockquote text (what the user wrote)
	created  string // "2006-01-02 15:04:05" wall clock

	cardTitle string // subject/topic block: linked title,
	cardURL   string // its URL,
	cardDesc  string // and the preview paragraph
}

// textParts assembles the item's own content lines (saying, then card).
func (p item) textParts() []string {
	var parts []string
	if p.saying != "" {
		parts = append(parts, p.saying)
	}
	if p.cardTitle != "" {
		parts = append(parts, "→ "+p.cardTitle)
	}
	if p.cardURL != "" {
		parts = append(parts, p.cardURL)
	}
	if p.cardDesc != "" {
		parts = append(parts, p.cardDesc)
	}
	return parts
}

func parseItem(n *html.Node) item {
	p := item{sid: attr(n, "data-sid")}

	if hd := findFirst(n, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "hd") }); hd != nil {
		p.url = attr(hd, "data-status-url")
		if txt := findFirst(hd, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "text") }); txt != nil {
			var who, quote *html.Node
			if who = findFirst(txt, func(c *html.Node) bool { return c.Data == "a" && hasClass(c, "lnk-people") }); who != nil {
				p.author = textOf(who)
			}
			if quote = findFirst(txt, func(c *html.Node) bool { return c.Data == "blockquote" }); quote != nil {
				p.saying = textOf(quote)
			}
			// the activity is the loose text left in .text: author link and
			// blockquote excluded, trailing colon dropped ("转发：" -> "转发")
			p.activity = strings.TrimRight(textExcluding(txt, who, quote), ":： ")
		}
	}

	// the topic (动态) variant keeps the saying in .bd .content instead of
	// .hd .text, so fall back to a blockquote anywhere in the item
	if p.saying == "" {
		if quote := findFirst(n, func(c *html.Node) bool { return c.Data == "blockquote" }); quote != nil {
			p.saying = textOf(quote)
		}
	}

	if at := findFirst(n, func(c *html.Node) bool { return c.Data == "span" && hasClass(c, "created_at") }); at != nil {
		p.created = attr(at, "title")
		// the topic variant has no data-status-url; the timestamp links there
		if p.url == "" {
			if a := findFirst(at, func(c *html.Node) bool { return c.Data == "a" }); a != nil {
				p.url = attr(a, "href")
			}
		}
	}

	// subject/topic card: a movie/book/topic block with a linked title and a
	// preview paragraph
	if card := findFirst(n, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "block") }); card != nil {
		if title := findFirst(card, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "title") }); title != nil {
			if a := findFirst(title, func(c *html.Node) bool { return c.Data == "a" }); a != nil {
				p.cardTitle = textOf(a)
				p.cardURL = attr(a, "href")
			}
		}
		if p.cardURL == "" {
			p.cardURL = strings.TrimSpace(attr(card, "data-url"))
		}
		if desc := findFirst(card, func(c *html.Node) bool { return c.Data == "p" }); desc != nil {
			p.cardDesc = clip(textOf(desc), 200)
		}
	} else if title := findFirst(n, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "title") }); title != nil {
		// group topics have no block div: the linked title in .content is the
		// content itself. A title link with no text (the 动态 variant) adds
		// nothing, so it's dropped rather than emitting a bare tracking URL.
		if a := findFirst(title, func(c *html.Node) bool { return c.Data == "a" }); a != nil {
			if p.cardTitle = textOf(a); p.cardTitle != "" {
				p.cardURL = attr(a, "href")
			}
		}
	}
	p.cardURL = stripTracking(p.cardURL)
	return p
}

// stripTracking drops douban's _spm_id tracking query from a URL, leaving real
// queries (external links) alone.
func stripTracking(u string) string {
	if i := strings.Index(u, "?_spm_id="); i > 0 {
		return u[:i]
	}
	return u
}

// stripQuery drops the tracking query douban appends to status URLs.
func stripQuery(u string) string {
	if i := strings.IndexByte(u, '?'); i > 0 {
		return u[:i]
	}
	return u
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
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

// --- small DOM helpers over x/net/html ---

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, f := range strings.Fields(attr(n, "class")) {
		if f == class {
			return true
		}
	}
	return false
}

// findFirst returns the first element (document order) under n matching pred,
// excluding n itself.
func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && pred(c) {
			return c
		}
		if hit := findFirst(c, pred); hit != nil {
			return hit
		}
	}
	return nil
}

// findAll returns every element under n matching pred, without descending into
// a match (the stream's wrappers never nest).
func findAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && pred(c) {
			out = append(out, c)
			continue
		}
		out = append(out, findAll(c, pred)...)
	}
	return out
}

// textOf is n's concatenated text with whitespace runs collapsed.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for gc := c.FirstChild; gc != nil; gc = gc.NextSibling {
			walk(gc)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// textExcluding is textOf(n) with the given subtrees skipped.
func textExcluding(n *html.Node, skip ...*html.Node) string {
	skipped := func(c *html.Node) bool {
		for _, s := range skip {
			if s != nil && c == s {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if skipped(c) {
			return
		}
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for gc := c.FirstChild; gc != nil; gc = gc.NextSibling {
			walk(gc)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
