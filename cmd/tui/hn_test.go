package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/genkio/tui/core"
)

// The two Hacker News feeds name their thread in different places: a story links
// the article and points at the discussion from its body, while a comment's own
// link is the comment. Everything else is somebody else's item.
func TestHNRefOfPicksTheThread(t *testing.T) {
	tests := []struct {
		name string
		item core.Item
		want hnRef
		ok   bool
	}{
		{
			name: "a story points at its thread from the body",
			item: core.Item{
				App: "inoreader", Source: "Hacker News: Best",
				Title: "Tmp.0ut Volume 5",
				URL:   "https://tmpout.sh/5/",
				Body:  "Article URL: https://tmpout.sh/5/\n\nComments URL: https://news.ycombinator.com/item?id=49516059\n\nPoints: 191\n\n# Comments: 38",
			},
			want: hnRef{ID: "49516059", Kind: "story"},
			ok:   true,
		},
		{
			// An Ask HN has no article: the item's own link is the thread.
			name: "a story with no article of its own",
			item: core.Item{
				App: "inoreader", Source: "Hacker News: Best",
				Title: "Ask HN: what are you working on?",
				URL:   "https://news.ycombinator.com/item?id=49500001",
			},
			want: hnRef{ID: "49500001", Kind: "story"},
			ok:   true,
		},
		{
			name: "a comment is its own link",
			item: core.Item{
				App: "inoreader", Source: "Hacker News: Best Comments",
				Title: `New comment by zahlman in "Claude Fable 5.1"`,
				URL:   "https://news.ycombinator.com/item?id=49526135",
				Body:  "There's a huge difference between…",
			},
			want: hnRef{ID: "49526135", Kind: "comment"},
			ok:   true,
		},
		{
			name: "another source's item, however much it talks about HN",
			item: core.Item{
				App: "reddit", Source: "r/programming",
				Body: "see https://news.ycombinator.com/item?id=49526135",
			},
		},
		{
			name: "an HN story the feed gave no thread for",
			item: core.Item{App: "inoreader", Source: "Hacker News: Best", URL: "https://example.com/"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := hnRefOf(tc.item)
			if ok != tc.ok || got != tc.want {
				t.Errorf("hnRefOf() = %+v, %t; want %+v, %t", got, ok, tc.want, tc.ok)
			}
		})
	}
}

const hnThreadJSON = `{
  "id": 1, "type": "story", "title": "A story", "url": "https://example.com/a",
  "author": "op", "points": 191, "text": null, "created_at": "2026-09-01T10:00:00.000Z",
  "children": [
    {"id": 2, "type": "comment", "author": "alice", "text": "<p>first &amp; best</p><p>second line</p>",
     "created_at": "2026-09-01T11:00:00.000Z", "children": [
       {"id": 3, "type": "comment", "author": null, "text": null, "created_at": "2026-09-01T11:30:00.000Z", "children": [
         {"id": 4, "type": "comment", "author": "carol", "text": "orphaned by a delete", "created_at": "2026-09-01T12:00:00.000Z", "children": []}
       ]}
     ]},
    {"id": 5, "type": "comment", "author": "bob", "text": "unconvinced", "created_at": "2026-09-01T13:00:00.000Z", "children": []}
  ]
}`

func TestHNThreadReadsTheTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/49516059" {
			t.Errorf("asked for %q, want the thread's own id", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(hnThreadJSON))
	}))
	defer srv.Close()
	was := hnItemAPI
	hnItemAPI = srv.URL + "/"
	defer func() { hnItemAPI = was }()

	th, err := fetchHNThread(context.Background(), hnRef{ID: "49516059", Kind: "story"})
	if err != nil {
		t.Fatal(err)
	}
	if th.Title != "A story" || th.URL != "https://example.com/a" || th.Points != 191 {
		t.Errorf("thread = %+v, want the story's own details", th)
	}
	// Three comments, not four: the deleted one is not a comment.
	if th.Count != 3 {
		t.Errorf("count = %d, want 3", th.Count)
	}
	// ...but what was said under it is still an answer to something, so it moves
	// up rather than going with it.
	if len(th.Replies) != 2 || len(th.Replies[0].Kids) != 1 || th.Replies[0].Kids[0].Author != "carol" {
		t.Fatalf("replies = %+v, want the deleted comment's child kept", th.Replies)
	}
	// HN's own markup is flattened to what it says: paragraphs break, entities
	// come back as themselves.
	if th.Replies[0].Text != "first & best\n\nsecond line" {
		t.Errorf("text = %q, want the flattened comment", th.Replies[0].Text)
	}
}

func TestHNThreadReportsWhatTheAPISaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	was := hnItemAPI
	hnItemAPI = srv.URL + "/"
	defer func() { hnItemAPI = was }()

	if _, err := fetchHNThread(context.Background(), hnRef{ID: "1", Kind: "story"}); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want the status in it", err)
	}
}

// The prompt carries the whole tree, depth-first with its depths, since who is
// answering whom is most of what a thread means.
func TestItemSummaryPromptCarriesTheDiscussion(t *testing.T) {
	th := hnThreadOf(hnRef{ID: "1", Kind: "story"}, hnAPIItem{
		Title: strptr("A story"), URL: strptr("https://example.com/a"), Author: strptr("op"),
		Points: intptr(191),
		Children: []hnAPIItem{{
			Author: strptr("alice"), Text: strptr("<p>first</p>"),
			Children: []hnAPIItem{{Author: strptr("bob"), Text: strptr("<p>reply</p>")}},
		}},
	})
	p := itemSummaryPrompt(core.Item{Title: "A story", Source: "Hacker News: Best"}, th, "en")
	for _, want := range []string{
		"Story: A story",
		"Article: https://example.com/a",
		"Points: 191",
		"2 comments follow",
		"--- comment 1 · depth 1 · by alice\nfirst",
		"--- comment 2 · depth 2 · by bob\nreply",
		"Write in English",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
}

func TestItemSummaryPromptOfACommentIsAboutItsReplies(t *testing.T) {
	th := hnThreadOf(hnRef{ID: "1", Kind: "comment"}, hnAPIItem{
		Author: strptr("zahlman"), Text: strptr("<p>the comment</p>"),
		Children: []hnAPIItem{{Author: strptr("bob"), Text: strptr("<p>no</p>")}},
	})
	p := itemSummaryPrompt(core.Item{Title: `New comment by zahlman in "A story"`, Source: "Hacker News: Best Comments"}, th, "zh")
	for _, want := range []string{
		"replies to one Hacker News comment",
		`Thread: New comment by zahlman in "A story"`,
		"The comment, by zahlman:\nthe comment",
		"1 replies follow",
		"简体中文",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
}

func strptr(s string) *string { return &s }
func intptr(n int) *int       { return &n }

// The button is a Hacker News thing, for now: every other card's footer is as
// long as it was.
func TestOnlyHackerNewsCardsOfferAGist(t *testing.T) {
	hn := renderCard(t, core.Item{
		App: "inoreader", ID: "77", Source: "Hacker News: Best",
		Title: "Tmp.0ut Volume 5", URL: "https://tmpout.sh/5/",
		Body: "Comments URL: https://news.ycombinator.com/item?id=49516059",
	})
	if !strings.Contains(hn, `<button class="gist" type="button" data-state="idle"`) {
		t.Errorf("an HN card's footer should offer a gist:\n%s", hn)
	}
	other := renderCard(t, core.Item{App: "reddit", ID: "1", Source: "r/golang", Title: "go 1.30"})
	if strings.Contains(other, `class="gist"`) {
		t.Errorf("a reddit card should not offer one:\n%s", other)
	}
}

// A compact saved row hides most of the card, but not a summary that was asked
// for from it: the box only exists because the button was tapped.
func TestGistShowsOnACompactSavedRow(t *testing.T) {
	page := renderPage(t, nil, []string{"inoreader"}, nil, "", "")
	if strings.Contains(page, "compact.expandable>.gistbox") {
		t.Error("the compact row must not hide a summary it asked for")
	}
}
