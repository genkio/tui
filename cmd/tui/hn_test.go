package main

import (
	"context"
	"io"
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
	p := itemSummaryPrompt(core.Item{Title: "A story", Source: "Hacker News: Best"}, th, "", "en")
	for _, want := range []string{
		"Story: A story",
		"Article: https://example.com/a",
		"Points: 191",
		"2 comments follow",
		"--- comment 1 · depth 1\nfirst",
		"--- comment 2 · depth 2\nreply",
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
	p := itemSummaryPrompt(core.Item{Title: `New comment by zahlman in "A story"`, Source: "Hacker News: Best Comments"}, th, "", "zh")
	for _, want := range []string{
		"replies to one Hacker News comment",
		`Thread: New comment by zahlman in "A story"`,
		"The comment:\nthe comment",
		"1 replies follow",
		"简体中文",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
}

// Handles are dropped on the way in, not asked to be dropped on the way out:
// the model is never given a name it could put in the summary.
func TestItemSummaryPromptNamesNobody(t *testing.T) {
	th := hnThreadOf(hnRef{ID: "1", Kind: "story"}, hnAPIItem{
		Title: strptr("A story"), Author: strptr("op"), Text: strptr("<p>asking</p>"),
		Children: []hnAPIItem{{Author: strptr("nunez"), Text: strptr("<p>works for me</p>")}},
	})
	p := itemSummaryPrompt(core.Item{Title: "A story", Source: "Hacker News: Best"}, th, "", "en")
	for _, gone := range []string{"nunez", "by op", "the handles"} {
		if strings.Contains(p, gone) {
			t.Errorf("prompt should not carry %q:\n%s", gone, p)
		}
	}
	if !strings.Contains(p, "Never name a commenter") {
		t.Errorf("prompt should forbid naming commenters:\n%s", p)
	}
}

// The article is the first thing the summary is about, when there is one to
// read; a fetch that came back empty leaves the discussion opener alone.
func TestItemSummaryPromptOpensWithTheArticle(t *testing.T) {
	th := hnThreadOf(hnRef{ID: "1", Kind: "story"}, hnAPIItem{
		Title: strptr("A story"), URL: strptr("https://example.com/a"),
		Children: []hnAPIItem{{Author: strptr("alice"), Text: strptr("<p>first</p>")}},
	})
	it := core.Item{Title: "A story", Source: "Hacker News: Best"}
	with := itemSummaryPrompt(it, th, "The article says a thing.", "en")
	for _, want := range []string{"The article's text", "The article says a thing.", "what the article itself says"} {
		if !strings.Contains(with, want) {
			t.Errorf("prompt is missing %q:\n%s", want, with)
		}
	}
	without := itemSummaryPrompt(it, th, "  ", "en")
	if strings.Contains(without, "what the article itself says") {
		t.Errorf("with no article there is nothing to open with:\n%s", without)
	}
}

// A comment is arguing about the same article the story is, so its briefing
// opens on the article too — it just has to be told which story it sits in.
func TestItemSummaryPromptOfACommentCarriesTheArticle(t *testing.T) {
	th := hnThreadOf(hnRef{ID: "2", Kind: "comment"}, hnAPIItem{
		Author: strptr("zahlman"), Text: strptr("<p>the comment</p>"), StoryID: intptr(1),
		Children: []hnAPIItem{{Author: strptr("bob"), Text: strptr("<p>no</p>")}},
	})
	if th.StoryID != "1" {
		t.Fatalf("StoryID = %q, want the story the comment sits under", th.StoryID)
	}
	th.StoryURL = "https://example.com/a"
	p := itemSummaryPrompt(core.Item{Title: "A comment", Source: "Hacker News: Best Comments"}, th,
		"The article says a thing.", "en")
	for _, want := range []string{
		"Article the thread is about: https://example.com/a",
		"The article says a thing.",
		"what the article itself says",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
}

func TestFetchHNStoryReadsTitleAndLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.json" {
			t.Errorf("asked for %q, want the story's own id", r.URL.Path)
		}
		io.WriteString(w, `{"id":1,"type":"story","title":"A story","url":"https://example.com/a"}`)
	}))
	defer srv.Close()
	was := hnStoryAPI
	hnStoryAPI = srv.URL + "/"
	defer func() { hnStoryAPI = was }()

	title, url, err := fetchHNStory(context.Background(), "1")
	if err != nil || title != "A story" || url != "https://example.com/a" {
		t.Errorf("fetchHNStory() = %q, %q, %v", title, url, err)
	}
}

func TestFetchHNArticleReadsThePageAsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><title>t</title><style>p{}</style></head>`+
			`<body><nav>menu here</nav><script>var x = 1;</script>`+
			`<p>First paragraph.</p><p>Second &amp; last.</p></body></html>`)
	}))
	defer srv.Close()
	got, err := fetchHNArticle(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchHNArticle: %v", err)
	}
	for _, want := range []string{"First paragraph.", "Second & last."} {
		if !strings.Contains(got, want) {
			t.Errorf("article text is missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"menu here", "var x", "p{}"} {
		if strings.Contains(got, gone) {
			t.Errorf("article text should not carry %q:\n%s", gone, got)
		}
	}
}

// An Ask HN links its own thread, and a PDF is not something to feed a prompt:
// neither is fetched, and neither is an error.
func TestFetchHNArticleSkipsWhatIsNotAnArticle(t *testing.T) {
	got, err := fetchHNArticle(context.Background(), "https://news.ycombinator.com/item?id=49500001")
	if err != nil || got != "" {
		t.Errorf("got %q, %v; want the thread link skipped", got, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		io.WriteString(w, "%PDF-1.7")
	}))
	defer srv.Close()
	if got, err := fetchHNArticle(context.Background(), srv.URL); err != nil || got != "" {
		t.Errorf("got %q, %v; want the pdf skipped", got, err)
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
