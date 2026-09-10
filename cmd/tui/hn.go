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

	gohtml "golang.org/x/net/html"

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
	// The linked article is a page off the open web rather than an API answer, so
	// it gets a tighter deadline than the thread: it is a nice-to-have the
	// briefing goes ahead without.
	hnArticleTimeout = 20 * time.Second
	hnArticleMaxBody = 8 << 20
	hnArticleRunes   = 12000
)

var (
	hnHTTP        = &http.Client{Timeout: hnFetchTimeout}
	hnArticleHTTP = &http.Client{Timeout: hnArticleTimeout}
)

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
	// Under a comment: the story it sits in, which is where the article a reply
	// is arguing about actually lives. A comment carries neither title nor link
	// of its own, so this is looked up separately.
	StoryID    string
	StoryTitle string
	StoryURL   string
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
	StoryID   *int        `json:"story_id"`
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
	if root.StoryID != nil && *root.StoryID != 0 && fmt.Sprint(*root.StoryID) != ref.ID {
		th.StoryID = fmt.Sprint(*root.StoryID)
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

// hnStoryAPI is the official Firebase API, used for this one lookup and nothing
// else: all a comment's briefing wants from its story is the title and the link,
// and Algolia would answer that with the story's whole comment tree — a megabyte
// to read one field. A var so a test can point it elsewhere.
var hnStoryAPI = "https://hacker-news.firebaseio.com/v0/item/"

// fetchHNStory looks up the story a comment sits under, for its title and the
// article it points at. Best-effort, like the article fetch it feeds.
func fetchHNStory(ctx context.Context, id string) (title, url string, err error) {
	ctx, cancel := context.WithTimeout(ctx, hnArticleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hnStoryAPI+id+".json", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "tui-feed")
	resp, err := hnArticleHTTP.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("could not reach Hacker News: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("hacker news said %s about that story", resp.Status)
	}
	var out struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, hnMaxBody)).Decode(&out); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(out.Title), strings.TrimSpace(out.URL), nil
}

// fetchHNArticle reads the page a story points at, as text. What the room said
// back only means something next to what it said back about, and neither feed
// carries the article — the story's body is a stub and the model is not allowed
// out to the network, so the server goes and gets it.
//
// Best-effort by design: a paywall, a PDF, a login wall or a dead host is
// ordinary here, and the discussion summary is worth writing without it.
func fetchHNArticle(ctx context.Context, rawURL string) (string, error) {
	if !hnFetchableArticle(rawURL) {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, hnArticleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", hnArticleUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := hnArticleHTTP.Do(req)
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, hnArticleMaxBody))
	if err != nil {
		return "", err
	}
	return hnArticleText(raw)
}

// A browser's, because a plain script UA is what most of the web's bot walls
// answer 403 to, and the fetch is one page a person asked for.
const hnArticleUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// hnFetchableArticle keeps the fetch to pages worth fetching: an Ask HN links
// the thread itself, and anything that is not plain http is not a page.
func hnFetchableArticle(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false
	}
	return !hnItemURLRe.MatchString(u)
}

var hnArticleSkip = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true, "head": true,
	"nav": true, "footer": true, "header": true, "aside": true, "form": true,
	"iframe": true, "template": true,
}

var hnArticleBreak = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "section": true,
	"article": true, "blockquote": true, "pre": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true,
}

// hnArticleText flattens a page to the prose on it. This is not readability:
// the chrome that survives the skip list costs the prompt a few hundred words,
// which is cheaper than being wrong about which div the article lives in.
func hnArticleText(raw []byte) (string, error) {
	doc, err := gohtml.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var walk func(*gohtml.Node)
	walk = func(n *gohtml.Node) {
		if n.Type == gohtml.ElementNode && hnArticleSkip[n.Data] {
			return
		}
		if n.Type == gohtml.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteString(" ")
			}
		}
		brk := n.Type == gohtml.ElementNode && hnArticleBreak[n.Data]
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
	return strings.TrimSpace(hnGapRe.ReplaceAllString(out, "\n\n")), nil
}

// hnWrite prints one comment and everything under it, depth-first, which is the
// order a reader would go through the page in. The depth is stated rather than
// drawn as indentation: a comment carries newlines of its own, and an indent
// that only holds for the first line says nothing.
//
// No author: the summary is not allowed to name commenters, and the surest way
// to keep handles out of it is to never hand it any.
func hnWrite(b *strings.Builder, nodes []hnNode, depth int, n *int) {
	for _, c := range nodes {
		*n++
		fmt.Fprintf(b, "--- comment %d · depth %d\n%s\n\n", *n, depth, clipRunes(c.Text, hnTextRunes))
		hnWrite(b, c.Kids, depth+1, n)
	}
}
