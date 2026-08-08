package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

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

type pageData struct {
	Unread      int    // shown only when HasApps; JS decrements it in place
	Clock       string // local HH:MM of this page load
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
	Expand      bool
}

// buildPageData shapes the fetched items into what page.tmpl renders: header
// meta, per-service health dots, and one card per item.
func buildPageData(items []core.Item, apps, failed []string, now time.Time, asc bool, xTab, warn string) pageData {
	xAuth := false
	for _, a := range apps {
		if a == "x" {
			xAuth = true
			break
		}
	}

	bad := map[string]bool{}
	for _, f := range failed {
		bad[f] = true
	}
	var health []healthEntry
	for _, a := range apps {
		label := appLabels[a]
		if label == "" {
			label = a
		}
		h := healthEntry{Label: label, Title: a + ": live", OK: true}
		if bad[a] {
			h.Title, h.OK = a+": failed to load", false
		}
		health = append(health, h)
	}

	cards := make([]cardData, 0, len(items))
	for _, it := range items {
		cards = append(cards, buildCard(it))
	}

	return pageData{
		Unread:  len(items),
		Clock:   now.Local().Format("15:04"),
		XAuth:   xAuth,
		XTab:    xTab,
		Health:  health,
		Asc:     asc,
		Warn:    warn,
		HasApps: len(apps) > 0,
		// With x authed on Following and nothing left to read, give a direct
		// way into For You right from the empty state.
		OfferForYou: len(items) == 0 && xAuth && xTab == "following",
		Cards:       cards,
	}
}

func buildCard(it core.Item) cardData {
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
		Expand: needsExpand(body, title),
	}
	// Two content panels: a clipped preview and a full version the footer's
	// expand toggle reveals. linkify escapes, so the HTML is safe as-is.
	if body != "" {
		c.PreviewBody = template.HTML(linkify(clip(body, 220)))
		c.FullBody = template.HTML(linkify(body))
	}
	return c
}

// needsExpand reports whether anything is clipped, i.e. there is more to reveal.
func needsExpand(body, title string) bool {
	return len([]rune(body)) > 220 || len([]rune(title)) > 220
}

// clip truncates s to at most n runes, adding an ellipsis when cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
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
		App    string `json:"app"`
		ID     string `json:"id"`
		Title  string `json:"title"`
		Body   string `json:"body,omitempty"`
		Source string `json:"source,omitempty"`
		Author string `json:"author,omitempty"`
		URL    string `json:"url,omitempty"`
		Age    string `json:"age,omitempty"`
		TS     string `json:"ts,omitempty"`
	}
	out := make([]wireItem, 0, len(items))
	for _, it := range items {
		wi := wireItem{
			App:    it.App,
			ID:     it.ID,
			Title:  it.Title,
			Body:   it.Body,
			Source: it.Source,
			Author: it.Author,
			URL:    it.URL,
			Age:    it.Age,
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
