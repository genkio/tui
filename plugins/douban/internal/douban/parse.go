package douban

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/genkio/tui/core"
)

// cst is Douban's wall-clock zone: created_at titles come as a bare
// "2006-01-02 15:04:05" string in Beijing time.
var cst = time.FixedZone("CST", 8*3600)

// parseHome scrapes the 友邻广播 stream out of the logged-in desktop homepage.
// Each div.new-status wrapper is one timeline entry: its first div.status-item
// is the followed user's status, and a reshare nests the original inside a
// div.status-real-wrapper sibling. What a status passed along comes out as its
// Embed, so renderers can draw it apart from the words the resharer added.
// now anchors the relative age strings.
func parseHome(body []byte, now time.Time) ([]Status, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing douban homepage: %w", err)
	}
	var out []Status
	for _, wrapper := range findAll(doc, func(n *html.Node) bool {
		return n.Data == "div" && hasClass(n, "new-status")
	}) {
		main := findFirst(wrapper, isStatusItem)
		if main == nil {
			continue
		}
		p := parseItem(main)
		if p.sid == "" {
			continue
		}

		var embed *core.Quote
		// a reshare carries the original status in a sibling wrapper; embed it
		if real := findFirst(wrapper, func(n *html.Node) bool {
			return n.Data == "div" && hasClass(n, "status-real-wrapper")
		}); real != nil {
			if orig := findFirst(real, isStatusItem); orig != nil {
				embed = parseItem(orig).embed()
			}
		}
		// resharing a discussion brings no original wrapper: the card itself is
		// the post being passed along, so it embeds rather than joining the text
		own := p.card
		if embed == nil && isReshare(p.activity) {
			if q := p.card.embed(); q != nil {
				embed, own = q, nil
			}
		}

		var parts []string
		if p.saying != "" {
			parts = append(parts, p.saying)
		}
		parts = append(parts, own.textParts()...)

		s := Status{
			ID:       p.sid,
			Author:   p.author,
			Activity: p.activity,
			Text:     strings.Join(parts, "\n\n"),
			URL:      stripQuery(p.url),
			Images:   append(p.images, own.images()...),
			Embed:    embed,
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
	card     *card  // the subject/topic block it points at, if any
	images   []string
}

// card is the block a status points at: a book, a movie, a group discussion.
// Its pictures are held apart from the status's own so a reshared discussion
// carries them into the embed instead of into the resharer's stills.
type card struct {
	title string
	owner string // who wrote the discussion, when the card says
	url   string
	desc  string // the preview paragraph
	pics  []string
}

// embed shapes a reshared original into the block a renderer nests inside the
// card: who wrote it, what they said (their own card folded in), where it lives.
func (p item) embed() *core.Quote {
	var parts []string
	if p.saying != "" {
		parts = append(parts, p.saying)
	}
	parts = append(parts, p.card.textParts()...)
	q := &core.Quote{
		Source: strings.TrimSpace(p.author + " " + p.activity),
		Text:   strings.Join(parts, "\n\n"),
		URL:    stripQuery(p.url),
		Images: append(p.images, p.card.images()...),
	}
	if q.Source == "" && q.Text == "" && len(q.Images) == 0 {
		return nil
	}
	return q
}

// embed draws a reshared discussion as that same nested block: its title is the
// headline linking out, its preview paragraph the body. A card with no title
// has no headline to lead with, so it stays text.
func (c *card) embed() *core.Quote {
	if c == nil || c.title == "" {
		return nil
	}
	return &core.Quote{Source: c.title, Author: c.owner, Text: c.desc, URL: c.url, Images: c.pics}
}

// textParts is the card flattened into content lines, for when it is what the
// status is itself about rather than something it passed along.
func (c *card) textParts() []string {
	if c == nil {
		return nil
	}
	var parts []string
	if c.title != "" {
		parts = append(parts, "→ "+c.title)
	}
	if c.url != "" {
		parts = append(parts, c.url)
	}
	if c.desc != "" {
		parts = append(parts, c.desc)
	}
	return parts
}

func (c *card) images() []string {
	if c == nil {
		return nil
	}
	return c.pics
}

// isCardBlock matches either markup douban serves a card in: .block for a
// subject (a book, a movie), .topic-card for a discussion passed along.
func isCardBlock(n *html.Node) bool {
	return n.Data == "div" && (hasClass(n, "block") || hasClass(n, "topic-card"))
}

// isReshare reports whether the status is passing along someone else's post:
// douban words that activity 转发 ("转发", "转发了 X 的讨论"). A 想读/看过 mark
// points at a subject instead, whose card is the status's own content.
func isReshare(activity string) bool { return strings.Contains(activity, "转发") }

func isStatusItem(n *html.Node) bool { return n.Data == "div" && hasClass(n, "status-item") }

func parseItem(n *html.Node) item {
	p := item{sid: attr(n, "data-sid")}
	// the card a status points at, found first so nothing inside it is mistaken
	// for the status's own words or pictures
	block := findFirst(n, isCardBlock)

	if hd := findFirst(n, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "hd") }); hd != nil {
		p.url = attr(hd, "data-status-url")
		if txt := findFirst(hd, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "text") }); txt != nil {
			var who, quote, when *html.Node
			if who = findFirst(txt, func(c *html.Node) bool { return c.Data == "a" && hasClass(c, "lnk-people") }); who != nil {
				p.author = textOf(who)
			}
			if quote = findFirst(txt, func(c *html.Node) bool { return c.Data == "blockquote" }); quote != nil {
				p.saying = textOf(quote)
			}
			// newer statuses date themselves inside .text; that stamp is not part
			// of what the user did, so it never belongs in the activity
			when = findFirst(txt, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "pubtime") })
			// the activity is the loose text left in .text: author link, stamp and
			// blockquote excluded, trailing colon dropped ("转发：" -> "转发")
			p.activity = strings.TrimRight(textExcluding(txt, who, quote, when), ":： ")
		}
	}

	// the topic (动态) variant keeps the saying in .bd .content instead of
	// .hd .text, so fall back to a blockquote anywhere outside the card (a
	// topic-card quotes the discussion, which is not what this user wrote)
	if p.saying == "" {
		if quote := findOutside(n, block, func(c *html.Node) bool { return c.Data == "blockquote" }); quote != nil {
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

	if block != nil {
		cd := &card{}
		if title := findFirst(block, func(c *html.Node) bool {
			return c.Data == "div" && (hasClass(c, "title") || hasClass(c, "topic-card-title"))
		}); title != nil {
			if a := findFirst(title, func(c *html.Node) bool { return c.Data == "a" }); a != nil {
				cd.title = textOf(a)
				cd.url = attr(a, "href")
			}
		}
		// a discussion card names who wrote it ("momo 说：")
		if owner := findFirst(block, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "topic-card-owner") }); owner != nil {
			if a := findFirst(owner, func(c *html.Node) bool { return c.Data == "a" }); a != nil {
				cd.owner = textOf(a)
			}
		}
		if cd.url == "" {
			cd.url = strings.TrimSpace(attr(block, "data-url"))
		}
		if desc := findFirst(block, func(c *html.Node) bool { return c.Data == "p" }); desc != nil {
			cd.desc = clip(textOf(desc), 200)
		}
		cd.pics = photos(block, nil)
		cd.url = stripTracking(unwrapLink2(cd.url))
		p.card = cd
	} else if title := findFirst(n, func(c *html.Node) bool { return c.Data == "div" && hasClass(c, "title") }); title != nil {
		// group topics have no block div: the linked title in .content is the
		// content itself. A title link with no text (the 动态 variant) adds
		// nothing, so it's dropped rather than emitting a bare tracking URL.
		if a := findFirst(title, func(c *html.Node) bool { return c.Data == "a" }); a != nil {
			if t := textOf(a); t != "" {
				p.card = &card{title: t, url: stripTracking(unwrapLink2(attr(a, "href")))}
			}
		}
	}
	p.images = photos(n, block)
	return p
}

// photos collects what a status attached: uploaded pictures, and a card's cover
// unless that block is skipped (the card keeps its own cover). The header
// carries the poster's avatar, which is the row's chrome rather than its
// content, so that subtree is skipped too. Douban lazy-loads, so the real URL
// is usually in data-src with a placeholder left in src.
func photos(n, skip *html.Node) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(*html.Node)
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	walk = func(c *html.Node) {
		if c == skip {
			return
		}
		if c.Type == html.ElementNode && (hasClass(c, "hd") || hasClass(c, "usr-pic")) {
			return
		}
		if c.Type == html.ElementNode && c.Data == "script" {
			for _, u := range scriptPhotos(textOf(c)) {
				add(u)
			}
			return
		}
		if c.Type == html.ElementNode && c.Data == "img" {
			u := imageURL(attr(c, "data-src"))
			if u == "" {
				u = imageURL(attr(c, "src"))
			}
			add(u)
		}
		for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return out
}

// rePhotosJSON pulls the list douban hands its client-side gallery. Pictures
// attached to a status (and to a reshared discussion) reach the page only as
// that script's JSON, never as an <img>, so a scrape that reads tags alone
// comes back empty-handed.
var rePhotosJSON = regexp.MustCompile(`(?s)var\s+photos\s*=\s*(\[.*?\]);`)

func scriptPhotos(js string) []string {
	m := rePhotosJSON.FindStringSubmatch(js)
	if m == nil {
		return nil
	}
	var list []struct {
		Image struct {
			Normal struct {
				URL string `json:"url"`
			} `json:"normal"`
			Large struct {
				URL string `json:"url"`
			} `json:"large"`
		} `json:"image"`
	}
	if err := json.Unmarshal([]byte(m[1]), &list); err != nil {
		return nil
	}
	var out []string
	for _, ph := range list {
		u := ph.Image.Large.URL
		if u == "" {
			u = ph.Image.Normal.URL
		}
		if u = imageURL(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// unwrapLink2 resolves douban's /link2/ redirect wrapper to the address it
// hides, so a card links to the discussion rather than to the bounce.
func unwrapLink2(raw string) string {
	if !strings.Contains(raw, "/link2/") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if target := u.Query().Get("url"); target != "" {
		return target
	}
	return raw
}

// reImageShard matches the numbered hosts douban spreads its pictures over.
var reImageShard = regexp.MustCompile(`^https://img\d+\.doubanio\.com/`)

// imageURL normalizes a douban picture. The img1/img2/img3/img9 hosts are not
// mirrors of one CDN but four: the low-numbered ones resolve to mainland edges
// (UPYUN, Alibaba) that answer 403 Forbidden to a request from outside China,
// while img9 is the global edge and serves the same path anywhere. The digit
// carries no meaning beyond which CDN, so every picture is asked of img9.
func imageURL(raw string) string {
	return reImageShard.ReplaceAllString(core.ImageURL(raw), "https://img9.doubanio.com/")
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
	return findOutside(n, nil, pred)
}

// findOutside is findFirst with one subtree left out, for the pieces a card
// owns rather than the status around it.
func findOutside(n, skip *html.Node, pred func(*html.Node) bool) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c == skip {
			continue
		}
		if c.Type == html.ElementNode && pred(c) {
			return c
		}
		if hit := findOutside(c, skip, pred); hit != nil {
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
