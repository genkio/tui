package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/genkio/tui/core"
)

// Hacker News arrives here through Inoreader, as two feeds that read very
// differently: "Hacker News: Best" is a story, whose body is a stub pointing at
// the article and at the thread, and "Hacker News: Best Comments" is one comment
// out of a thread, whose body is the whole of it. Neither carries what makes a
// thread worth the trip — what the room said back — so a card that wants
// summarizing has to go and get the comments first.
//
// They come from Algolia's HN API rather than the official Firebase one, which
// answers a request per comment and would mean hundreds of them for one thread;
// this returns the whole subtree in a single call.
// hnItemAPI is a var so a test can point it at a server of its own; nothing
// else writes it.
var hnItemAPI = "https://hn.algolia.com/api/v1/items/"

const (
	// A thread of a thousand comments is a megabyte of JSON over a public API
	// with no key, which is slower than any other fetch this server makes.
	hnFetchTimeout = 45 * time.Second
	hnMaxBody      = 32 << 20
	// Per-comment text budget, the same bargain the backlog briefings strike: a
	// thread is read whole, however deep, and the one bound is on how much of any
	// single comment goes in, so a lone essay cannot spend the prompt.
	hnTextRunes = 700
)

var hnHTTP = &http.Client{Timeout: hnFetchTimeout}

// hnRef is the HN item a card is about, and which of the two kinds it is: a
// story, whose comments are the discussion, or a comment, whose replies are.
type hnRef struct {
	ID   string
	Kind string // "story" or "comment"
}

var hnItemURLRe = regexp.MustCompile(`news\.ycombinator\.com/item\?id=(\d+)`)

// hnRefOf reports which HN thread an item hangs off, and whether it is one at
// all. A story links the article it is about, so the thread is named in the body
// the feed writes ("Comments URL: …"); an Ask HN has no article and links the
// thread itself. A comment's own link is the comment.
func hnRefOf(it core.Item) (hnRef, bool) {
	src := strings.TrimSpace(it.Source)
	if !strings.HasPrefix(src, "Hacker News") {
		return hnRef{}, false
	}
	if strings.Contains(strings.ToLower(src), "comment") {
		if m := hnItemURLRe.FindStringSubmatch(it.URL); m != nil {
			return hnRef{ID: m[1], Kind: "comment"}, true
		}
		return hnRef{}, false
	}
	if m := hnItemURLRe.FindStringSubmatch(it.Body); m != nil {
		return hnRef{ID: m[1], Kind: "story"}, true
	}
	if m := hnItemURLRe.FindStringSubmatch(it.URL); m != nil {
		return hnRef{ID: m[1], Kind: "story"}, true
	}
	return hnRef{}, false
}

// hnNode is one comment and the ones under it, kept as a tree rather than a
// list: who is answering whom is most of what a thread means, and a flat pile of
// opinions is not the same thing.
type hnNode struct {
	Author string
	Text   string
	At     time.Time
	Kids   []hnNode
}

// hnThread is what a card's button goes and gets: the item the feed pointed at,
// and everything said under it.
type hnThread struct {
	Ref     hnRef
	Title   string // the story's title; blank under a comment, which has none
	URL     string // the article the story points at
	Author  string
	Text    string // the story's own text (Ask HN), or the comment being replied to
	Points  int
	Replies []hnNode
	Count   int // comments in Replies, at any depth
}

// hnAPIItem mirrors Algolia's shape. Everything but the id and the children is
// nullable there: a deleted comment keeps its place in the tree with no author
// and no text, and a story has no text of its own unless it is an Ask HN.
type hnAPIItem struct {
	ID        int         `json:"id"`
	Author    *string     `json:"author"`
	Title     *string     `json:"title"`
	URL       *string     `json:"url"`
	Text      *string     `json:"text"`
	Points    *int        `json:"points"`
	Type      string      `json:"type"`
	CreatedAt time.Time   `json:"created_at"`
	Children  []hnAPIItem `json:"children"`
}

func fetchHNThread(ctx context.Context, ref hnRef) (hnThread, error) {
	ctx, cancel := context.WithTimeout(ctx, hnFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hnItemAPI+ref.ID, nil)
	if err != nil {
		return hnThread{}, err
	}
	req.Header.Set("User-Agent", "tui-feed")
	resp, err := hnHTTP.Do(req)
	if err != nil {
		return hnThread{}, fmt.Errorf("could not reach Hacker News: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hnThread{}, fmt.Errorf("hacker news said %s about that thread", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, hnMaxBody))
	if err != nil {
		return hnThread{}, err
	}
	var root hnAPIItem
	if err := json.Unmarshal(raw, &root); err != nil {
		return hnThread{}, fmt.Errorf("hacker news answered with something unreadable: %w", err)
	}
	return hnThreadOf(ref, root), nil
}

func hnThreadOf(ref hnRef, root hnAPIItem) hnThread {
	th := hnThread{
		Ref:     ref,
		Title:   hnStr(root.Title),
		URL:     hnStr(root.URL),
		Author:  hnStr(root.Author),
		Text:    hnText(root.Text),
		Replies: hnKids(root.Children),
	}
	if root.Points != nil {
		th.Points = *root.Points
	}
	th.Count = hnCount(th.Replies)
	return th
}

// hnKids keeps the shape of the tree while dropping what is not there to read.
// A deleted comment is not a comment, but the replies under it are still
// answering something, so they move up rather than going with it.
func hnKids(in []hnAPIItem) []hnNode {
	out := make([]hnNode, 0, len(in))
	for _, c := range in {
		kids := hnKids(c.Children)
		text := hnText(c.Text)
		if text == "" {
			out = append(out, kids...)
			continue
		}
		out = append(out, hnNode{Author: hnStr(c.Author), Text: text, At: c.CreatedAt, Kids: kids})
	}
	return out
}

func hnCount(nodes []hnNode) int {
	n := 0
	for _, c := range nodes {
		n += 1 + hnCount(c.Kids)
	}
	return n
}

func hnStr(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

var (
	hnParaRe = regexp.MustCompile(`(?i)<p>|</p>|<br\s*/?>`)
	hnTagRe  = regexp.MustCompile(`(?s)<[^>]+>`)
	hnGapRe  = regexp.MustCompile(`\n{3,}`)
)

// hnText flattens a comment's HTML to what it says. HN allows a short list of
// tags — paragraphs, italics, links, a code block — so breaking on the block
// ones and dropping the rest loses nothing a summary wants.
func hnText(s *string) string {
	if s == nil {
		return ""
	}
	out := hnParaRe.ReplaceAllString(*s, "\n\n")
	out = hnTagRe.ReplaceAllString(out, "")
	out = html.UnescapeString(out)
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	out = strings.Join(lines, "\n")
	return strings.TrimSpace(hnGapRe.ReplaceAllString(out, "\n\n"))
}

// hnWrite prints one comment and everything under it, depth-first, which is the
// order a reader would go through the page in. The depth is stated rather than
// drawn as indentation: a comment carries newlines of its own, and an indent
// that only holds for the first line says nothing.
func hnWrite(b *strings.Builder, nodes []hnNode, depth int, n *int) {
	for _, c := range nodes {
		*n++
		author := c.Author
		if author == "" {
			author = "someone"
		}
		fmt.Fprintf(b, "--- comment %d · depth %d · by %s\n%s\n\n", *n, depth, author, clipRunes(c.Text, hnTextRunes))
		hnWrite(b, c.Kids, depth+1, n)
	}
}
