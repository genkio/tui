package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	gohtml "golang.org/x/net/html"

	"github.com/genkio/tui/core"
)

// A gist is the discussion under one card, which is the half of the item its
// feed does not carry: a Hacker News story is a stub pointing at an article, a
// reddit post is a title and a link, and what the room said back is somewhere
// else entirely. The button goes and gets it.
//
// Two services answer that today, from two different APIs, and this file is
// what they have in common: which item a card is about, the shape of a thread
// once it has been read, and the article fetch both hang off. hn.go and
// reddit.go are the halves that know one service each.

const (
	gistHN     = "hn"
	gistReddit = "reddit"
)

// gistRef is the discussion a card is about: whose it is, which item, and what
// kind of item — a Hacker News story is read through its comments and a
// comment through its replies, which are the same run over different pieces of
// one tree.
type gistRef struct {
	Service string // gistHN or gistReddit
	ID      string
	Kind    string // hn: "story" or "comment"; reddit: "post"
}

// gistRefOf reports which discussion an item hangs off, and whether it has one
// at all. Everything a card's gist button does starts here, so a service that
// is not read yet simply never grows the button.
func gistRefOf(it core.Item) (gistRef, bool) {
	if ref, ok := hnRefOf(it); ok {
		return ref, true
	}
	return redditRefOf(it)
}

// gistRefusal is what a card with no discussion behind it is told, in the
// handler that refuses the ask and in the worker that would have run it.
const gistRefusal = "only Hacker News and Reddit items carry a discussion to summarize"

// nothingSaid is the empty thread, named for what was empty: a story nobody
// answered and a comment nobody answered are different disappointments.
func nothingSaid(ref gistRef) string {
	switch {
	case ref.Kind == "comment":
		return "nobody has replied to that comment yet"
	case ref.Service == gistReddit:
		return "nothing has been said under that post yet"
	}
	return "nothing has been said under that story yet"
}

// discussion is what a gist reads: the item the feed pointed at, and everything
// said under it.
type discussion struct {
	Ref    gistRef
	Title  string // the story's or post's title; blank under a comment, which has none
	URL    string // the article it points at, blank when the item is its own text
	Author string
	Text   string // the item's own text (an Ask HN, a self post), or the comment being replied to
	Points int
	// Replies is what was said under it, and Count is how much of that there is,
	// at any depth.
	Replies []discNode
	Count   int
	// Under a Hacker News comment: the story it sits in, which is where the
	// article a reply is arguing about actually lives. A comment carries neither
	// title nor link of its own, so this is looked up separately.
	StoryID    string
	StoryTitle string
	StoryURL   string
}

// discNode is one comment and the ones under it, kept as a tree rather than a
// list: who is answering whom is most of what a thread means, and a flat pile
// of opinions is not the same thing.
type discNode struct {
	Author string
	Text   string
	At     time.Time
	Kids   []discNode
}

func discCount(nodes []discNode) int {
	n := 0
	for _, c := range nodes {
		n += 1 + discCount(c.Kids)
	}
	return n
}

// fetchDiscussion is the card's button, server side: the thread behind a ref,
// from whichever service holds it.
func fetchDiscussion(ctx context.Context, ref gistRef) (discussion, error) {
	switch ref.Service {
	case gistHN:
		return fetchHNThread(ctx, ref)
	case gistReddit:
		return fetchRedditThread(ctx, ref)
	}
	return discussion{}, fmt.Errorf("nothing here reads %q discussions", ref.Service)
}

const (
	// A thread of a thousand comments is a megabyte of JSON over a public API
	// with no key, which is slower than any other fetch this server makes.
	threadTimeout = 45 * time.Second
	threadMaxBody = 32 << 20
	// Per-comment text budget, the same bargain the backlog briefings strike: a
	// thread is read whole, however deep, and the one bound is on how much of any
	// single comment goes in, so a lone essay cannot spend the prompt.
	commentRunes = 700
	// The linked article is a page off the open web rather than an API answer, so
	// it gets a tighter deadline than the thread: it is a nice-to-have the
	// briefing goes ahead without.
	articleTimeout = 20 * time.Second
	articleMaxBody = 8 << 20
	articleRunes   = 12000
)

var (
	threadHTTP  = &http.Client{Timeout: threadTimeout}
	articleHTTP = &http.Client{Timeout: articleTimeout}
)

// fetchArticle reads the page an item points at, as text. What the room said
// back only means something next to what it said back about, and no feed
// carries the article — the item's body is a stub and the model is not allowed
// out to the network, so the server goes and gets it.
//
// Best-effort by design: a paywall, a PDF, a login wall or a dead host is
// ordinary here, and the discussion summary is worth writing without it.
func fetchArticle(ctx context.Context, rawURL string) (string, error) {
	if !fetchableArticle(rawURL) {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, articleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := articleHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach the article: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the article said %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "html") {
		return "", nil // a PDF or a video is not something to feed a prompt
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, articleMaxBody))
	if err != nil {
		return "", err
	}
	return articleText(raw)
}

// A browser's, because a plain script UA is what most of the web's bot walls
// answer 403 to — reddit's own JSON API included — and the fetch is one page a
// person asked for.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// fetchableArticle keeps the fetch to pages worth fetching: a discussion that
// links itself (an Ask HN, a reddit self post or gallery) is already in the
// prompt, and anything that is not plain http is not a page.
func fetchableArticle(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false
	}
	return !hnItemURLRe.MatchString(u) && !redditHosted(u)
}

// gapRe collapses the blank lines a stripped page or comment leaves behind.
var gapRe = regexp.MustCompile(`\n{3,}`)

var articleSkip = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true, "head": true,
	"nav": true, "footer": true, "header": true, "aside": true, "form": true,
	"iframe": true, "template": true,
}

var articleBreak = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "section": true,
	"article": true, "blockquote": true, "pre": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true,
}

// articleText flattens a page to the prose on it. This is not readability:
// the chrome that survives the skip list costs the prompt a few hundred words,
// which is cheaper than being wrong about which div the article lives in.
func articleText(raw []byte) (string, error) {
	doc, err := gohtml.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var walk func(*gohtml.Node)
	walk = func(n *gohtml.Node) {
		if n.Type == gohtml.ElementNode && articleSkip[n.Data] {
			return
		}
		if n.Type == gohtml.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteString(" ")
			}
		}
		brk := n.Type == gohtml.ElementNode && articleBreak[n.Data]
		if brk {
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if brk {
			b.WriteString("\n")
		}
	}
	walk(doc)
	lines := strings.Split(b.String(), "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimSpace(ln)
	}
	out := strings.Join(lines, "\n")
	return strings.TrimSpace(gapRe.ReplaceAllString(out, "\n\n")), nil
}

// writeComments prints one comment and everything under it, depth-first, which
// is the order a reader would go through the page in. The depth is stated
// rather than drawn as indentation: a comment carries newlines of its own, and
// an indent that only holds for the first line says nothing.
//
// No author: the summary is not allowed to name commenters, and the surest way
// to keep handles out of it is to never hand it any.
func writeComments(b *strings.Builder, nodes []discNode, depth int, n *int) {
	for _, c := range nodes {
		*n++
		fmt.Fprintf(b, "--- comment %d · depth %d\n%s\n\n", *n, depth, clipRunes(c.Text, commentRunes))
		writeComments(b, c.Kids, depth+1, n)
	}
}
