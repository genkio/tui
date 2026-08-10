package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/genkio/tui/core"
)

// The page markup/CSS/JS lives in page.tmpl, embedded so the release stays a
// single self-contained binary. --dev re-reads the file per request instead.
//
//go:embed page.tmpl
var pageSrc string

// pageLoader hands out the parsed page template: the embedded copy parsed once
// for normal runs, or a fresh re-read of devPath on every call under --dev so
// template edits show up on browser refresh without a rebuild.
type pageLoader struct {
	devPath string
	tmpl    *template.Template
}

func newPageLoader(devPath string) (*pageLoader, error) {
	if devPath != "" {
		return &pageLoader{devPath: devPath}, nil
	}
	t, err := template.New("page").Parse(pageSrc)
	if err != nil {
		return nil, fmt.Errorf("embedded page.tmpl: %w", err)
	}
	return &pageLoader{tmpl: t}, nil
}

func (l *pageLoader) load() (*template.Template, error) {
	if l.devPath == "" {
		return l.tmpl, nil
	}
	src, err := os.ReadFile(l.devPath)
	if err != nil {
		return nil, err
	}
	return template.New("page").Parse(string(src))
}

// pageInput is everything a render needs; a struct because the feed view and
// the saved view fill in different halves of it.
type pageInput struct {
	items     []core.Item
	apps      []string
	failed    []string
	now       time.Time
	asc       bool
	xTab      string
	warn      string
	saved     *savedStore
	savedView bool
}

type pageData struct {
	Unread      int  // shown only when HasApps; JS decrements it in place
	Saved       int  // size of the saved list, in the header and its link
	SavedView   bool // rendering the saved list rather than the live feed
	XAuth       bool
	XTab        string
	Health      []healthEntry
	Asc         bool
	Warn        string
	HasApps     bool
	OfferForYou bool
	Cards       []cardData
}

type healthEntry struct {
	Label string
	Title string
	OK    bool
}

type cardData struct {
	App, ID     string
	Chip, Color string
	Source      string
	Author      string // blank when it duplicates Source
	Age         string
	Title       string // blank when x carries the full text as both title and body
	PreviewBody template.HTML
	FullBody    template.HTML
	URL         string
	Video       string // direct mp4; the card shows an inline player
	Poster      string
	VidLen      string // "1:23" badge over the poster; blank when the length is unknown
	HasVideo    bool   // this card or its quote has a player, so show the shared controls
	Images      []string
	HasImage    bool // this card or its quote has stills, so offer the image toggle
	Quote       *quoteData
	Expand      bool
	Saved       bool // starred: the footer button offers to unsave it
}

// cardImages prepares an app's stills for the card, sending the ones the
// browser cannot fetch itself through this server instead.
func cardImages(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		out = append(out, proxied(u))
	}
	return out
}

// proxied routes an image via /img when its host will not serve the page
// directly. doubanio wants a Referer naming a douban page, which no page here
// can produce, so the server fetches those on the browser's behalf. Everything
// else is loaded straight from its source.
func proxied(u string) string {
	if !strings.Contains(u, ".doubanio.com/") {
		return u
	}
	return "/img?u=" + url.QueryEscape(u)
}

// quoteData is the embedded post drawn inside a card: x's quote box.
type quoteData struct {
	Source      string
	Author      string // blank when it duplicates Source
	PreviewBody template.HTML
	FullBody    template.HTML
	URL         string
	Video       string
	Poster      string
	VidLen      string
	Images      []string
}

// buildPageData shapes the items into what page.tmpl renders: header counts,
// per-service health dots, and one card per item.
func buildPageData(in pageInput) pageData {
	xAuth := false
	for _, a := range in.apps {
		if a == "x" {
			xAuth = true
			break
		}
	}

	bad := map[string]bool{}
	for _, f := range in.failed {
		bad[f] = true
	}
	var health []healthEntry
	for _, a := range in.apps {
		label := healthLabels[a]
		if label == "" {
			label = a
		}
		h := healthEntry{Label: label, Title: a + ": live", OK: true}
		if bad[a] {
			h.Title, h.OK = a+": failed to load", false
		}
		health = append(health, h)
	}

	cards := make([]cardData, 0, len(in.items))
	for _, it := range in.items {
		// In the saved view every card is saved by definition; in the feed ask
		// the store.
		starred := in.savedView || (in.saved != nil && in.saved.has(it.App, it.ID))
		cards = append(cards, buildCard(it, starred))
	}

	savedCount := 0
	if in.saved != nil {
		savedCount = in.saved.count()
	}

	return pageData{
		Unread:    len(in.items),
		Saved:     savedCount,
		SavedView: in.savedView,
		XAuth:     xAuth,
		XTab:      in.xTab,
		Health:    health,
		Asc:       in.asc,
		Warn:      in.warn,
		HasApps:   len(in.apps) > 0,
		// With x authed on Following and nothing left to read, give a direct
		// way into For You right from the empty state.
		OfferForYou: len(in.items) == 0 && xAuth && in.xTab == "following",
		Cards:       cards,
	}
}

func buildCard(it core.Item, starred bool) cardData {
	chip := appLabels[it.App]
	if chip == "" {
		chip = it.App
	}
	color := appColors[it.App]
	if color == "" {
		color = "#4a9eff"
	}

	title := strings.TrimSpace(it.Title)
	body := strings.TrimSpace(it.Body)
	if body != "" && body == title {
		title = "" // x carries the full text as both title and body
	}
	author := it.Author
	if author == it.Source {
		author = ""
	}

	c := cardData{
		App:    it.App,
		ID:     it.ID,
		Chip:   chip,
		Color:  color,
		Source: it.Source,
		Author: author,
		Age:    it.Age,
		Title:  title,
		URL:    it.URL,
		Video:  it.Video,
		Poster: it.Poster,
		VidLen: vidLen(it.VidSecs),
		Images: cardImages(it.Images),
		Saved:  starred,
	}
	// Two content panels: a clipped preview and a full version the footer's
	// expand toggle reveals. linkify escapes, so the HTML is safe as-is.
	if body != "" {
		c.PreviewBody = template.HTML(preview(body, bodyClip))
		c.FullBody = template.HTML(linkify(body))
	}
	if it.Quote != nil {
		c.Quote = buildQuote(*it.Quote)
	}
	c.HasVideo = c.Video != "" || (c.Quote != nil && c.Quote.Video != "")
	c.HasImage = len(c.Images) > 0 || (c.Quote != nil && len(c.Quote.Images) > 0)
	c.Expand = needsExpand(body, title) || (c.Quote != nil && c.Quote.PreviewBody != c.Quote.FullBody)
	return c
}

// buildQuote shapes an embedded post into the nested card, clipped tighter than
// the parent so a long quote can't bury the post that quotes it.
func buildQuote(q core.Quote) *quoteData {
	author := q.Author
	if author == q.Source {
		author = ""
	}
	d := &quoteData{
		Source: q.Source,
		Author: author,
		URL:    q.URL,
		Video:  q.Video,
		Poster: q.Poster,
		VidLen: vidLen(q.VidSecs),
		Images: cardImages(q.Images),
	}
	if q.Text != "" {
		d.PreviewBody = template.HTML(preview(q.Text, quoteClip))
		d.FullBody = template.HTML(linkify(q.Text))
	}
	return d
}

// vidLen formats a clip's length for the badge over its poster ("0:42",
// "1:05:03"), so the cost of a tap is visible before anything downloads. Blank
// when the app reported no duration: no badge is better than a wrong one.
func vidLen(secs int) string {
	if secs <= 0 {
		return ""
	}
	if h := secs / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// How much of a body survives into the clipped preview panel, in runes.
const (
	bodyClip  = 220
	quoteClip = 140
)

// needsExpand reports whether anything is clipped, i.e. there is more to reveal.
func needsExpand(body, title string) bool {
	return len([]rune(body)) > bodyClip || len([]rune(title)) > bodyClip
}

// preview renders the clipped panel: the first n runes, an ellipsis, and how
// many words expanding would still reveal. The count is the expand control
// itself, so the tap lands where the text stops rather than down in the
// footer. Text shorter than the clip is returned whole, which keeps preview
// and full identical for cards that need no expanding at all.
func preview(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return linkify(s)
	}
	return linkify(string(r[:n])) + `…<button class="rest" type="button" title="show the rest">` + restLabel(countWords(string(r[n:]))) + `</button>`
}

func restLabel(n int) string {
	if n == 1 {
		return "+1 word"
	}
	return fmt.Sprintf("+%d words", n)
}

// countWords counts runs of non-space text, except that each CJK rune counts
// on its own: Chinese and Japanese posts have no spaces, so a run-based count
// would report the whole remainder as one word. Stray punctuation never starts
// a word, so "你好,世界" is four and "hi, there" is two.
func countWords(s string) int {
	n, inWord := 0, false
	for _, r := range s {
		switch {
		case cjkRune(r):
			n++
			inWord = false
		case unicode.IsSpace(r):
			inWord = false
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
		default:
			if !inWord {
				n++
				inWord = true
			}
		}
	}
	return n
}

func cjkRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// linkRe matches a URL plus any trailing punctuation (so "see http://x.com."
// renders the period as plain text, not part of the link).
var linkRe = regexp.MustCompile(`(https?://[^\s<>"']+)([.,;:!?)\]}"']*)`)

// linkify HTML-escapes text and turns embedded URLs into clickable links that
// open in a new tab; non-URL text is escaped as before.
func linkify(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range linkRe.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(escape(s[last:m[2]])) // text before the URL
		u := s[m[2]:m[3]]
		b.WriteString(`<a class="link" href="` + escape(u) + `" target="_blank" rel="noopener" title="` + escape(u) + `">` + escape(linkLabel(u)) + `</a>`)
		if m[4] >= 0 { // trailing punctuation kept as plain text
			b.WriteString(escape(s[m[4]:m[5]]))
		}
		last = m[1]
	}
	b.WriteString(escape(s[last:]))
	return b.String()
}

// linkLabel trims the scheme/www and shortens a URL for link text, keeping the
// full address in href and the title tooltip.
func linkLabel(u string) string {
	s := strings.TrimPrefix(u, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// writeJSONItems writes the merged feed as JSON, for API clients.
func writeJSONItems(w io.Writer, items []core.Item, failed []string) {
	type wireItem struct {
		App     string      `json:"app"`
		ID      string      `json:"id"`
		Title   string      `json:"title"`
		Body    string      `json:"body,omitempty"`
		Source  string      `json:"source,omitempty"`
		Author  string      `json:"author,omitempty"`
		URL     string      `json:"url,omitempty"`
		Age     string      `json:"age,omitempty"`
		TS      string      `json:"ts,omitempty"`
		Video   string      `json:"video,omitempty"`
		Poster  string      `json:"poster,omitempty"`
		VidSecs int         `json:"vidsecs,omitempty"`
		Images  []string    `json:"images,omitempty"`
		Quote   *core.Quote `json:"quote,omitempty"`
	}
	out := make([]wireItem, 0, len(items))
	for _, it := range items {
		wi := wireItem{
			App:     it.App,
			ID:      it.ID,
			Title:   it.Title,
			Body:    it.Body,
			Source:  it.Source,
			Author:  it.Author,
			URL:     it.URL,
			Age:     it.Age,
			Video:   it.Video,
			Poster:  it.Poster,
			VidSecs: it.VidSecs,
			Images:  it.Images,
			Quote:   it.Quote,
		}
		if !it.At.IsZero() {
			wi.TS = it.At.UTC().Format(time.RFC3339)
		}
		out = append(out, wi)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":  out,
		"failed": failed,
	})
}
