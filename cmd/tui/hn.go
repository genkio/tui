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

var hnItemURLRe = regexp.MustCompile(`news\.ycombinator\.com/item\?id=(\d+)`)

// hnRefOf reports which HN thread an item hangs off, and whether it is one at
// all. A story links the article it is about, so the thread is named in the body
// the feed writes ("Comments URL: …"); an Ask HN has no article and links the
// thread itself. A comment's own link is the comment.
func hnRefOf(it core.Item) (gistRef, bool) {
	src := strings.TrimSpace(it.Source)
	if !strings.HasPrefix(src, "Hacker News") {
		return gistRef{}, false
	}
	if strings.Contains(strings.ToLower(src), "comment") {
		if m := hnItemURLRe.FindStringSubmatch(it.URL); m != nil {
			return gistRef{Service: gistHN, ID: m[1], Kind: "comment"}, true
		}
		return gistRef{}, false
	}
	if m := hnItemURLRe.FindStringSubmatch(it.Body); m != nil {
		return gistRef{Service: gistHN, ID: m[1], Kind: "story"}, true
	}
	if m := hnItemURLRe.FindStringSubmatch(it.URL); m != nil {
		return gistRef{Service: gistHN, ID: m[1], Kind: "story"}, true
	}
	return gistRef{}, false
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

func fetchHNThread(ctx context.Context, ref gistRef) (discussion, error) {
	ctx, cancel := context.WithTimeout(ctx, threadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hnItemAPI+ref.ID, nil)
	if err != nil {
		return discussion{}, err
	}
	req.Header.Set("User-Agent", "tui-feed")
	resp, err := threadHTTP.Do(req)
	if err != nil {
		return discussion{}, fmt.Errorf("could not reach Hacker News: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return discussion{}, fmt.Errorf("hacker news said %s about that thread", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, threadMaxBody))
	if err != nil {
		return discussion{}, err
	}
	var root hnAPIItem
	if err := json.Unmarshal(raw, &root); err != nil {
		return discussion{}, fmt.Errorf("hacker news answered with something unreadable: %w", err)
	}
	return hnThreadOf(ref, root), nil
}

func hnThreadOf(ref gistRef, root hnAPIItem) discussion {
	th := discussion{
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
	th.Count = discCount(th.Replies)
	return th
}

// hnKids keeps the shape of the tree while dropping what is not there to read.
// A deleted comment is not a comment, but the replies under it are still
// answering something, so they move up rather than going with it.
func hnKids(in []hnAPIItem) []discNode {
	out := make([]discNode, 0, len(in))
	for _, c := range in {
		kids := hnKids(c.Children)
		text := hnText(c.Text)
		if text == "" {
			out = append(out, kids...)
			continue
		}
		out = append(out, discNode{Author: hnStr(c.Author), Text: text, At: c.CreatedAt, Kids: kids})
	}
	return out
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
	return strings.TrimSpace(gapRe.ReplaceAllString(out, "\n\n"))
}

// hnStoryAPI is the official Firebase API, used for this one lookup and nothing
// else: all a comment's briefing wants from its story is the title and the link,
// and Algolia would answer that with the story's whole comment tree — a megabyte
// to read one field. A var so a test can point it elsewhere.
var hnStoryAPI = "https://hacker-news.firebaseio.com/v0/item/"

// fetchHNStory looks up the story a comment sits under, for its title and the
// article it points at. Best-effort, like the article fetch it feeds, and on
// the same short deadline.
func fetchHNStory(ctx context.Context, id string) (title, url string, err error) {
	ctx, cancel := context.WithTimeout(ctx, articleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hnStoryAPI+id+".json", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "tui-feed")
	resp, err := articleHTTP.Do(req)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, threadMaxBody)).Decode(&out); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(out.Title), strings.TrimSpace(out.URL), nil
}
