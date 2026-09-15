package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/genkio/tui/core"
)

// A reddit post is its own thread: the id the app filed it under is all the
// comments endpoint needs. Anything else is somebody else's item, and an id
// that is not base36 is not an id.
func TestRedditRefOfPicksThePost(t *testing.T) {
	tests := []struct {
		name string
		item core.Item
		want gistRef
		ok   bool
	}{
		{
			name: "a post the reddit app filed",
			item: core.Item{App: "reddit", ID: "1wgjbh4", Source: "r/codex", Title: "a post"},
			want: gistRef{Service: gistReddit, ID: "1wgjbh4", Kind: "post"},
			ok:   true,
		},
		{
			name: "a fullname keeps only the id",
			item: core.Item{App: "reddit", ID: "t3_1wgjbh4", Source: "r/codex"},
			want: gistRef{Service: gistReddit, ID: "1wgjbh4", Kind: "post"},
			ok:   true,
		},
		{
			name: "another service's item, however reddit it looks",
			item: core.Item{App: "folo", ID: "1wgjbh4", Source: "r/codex"},
		},
		{
			name: "an id that would not be one",
			item: core.Item{App: "reddit", ID: "../../secrets", Source: "r/codex"},
		},
		{
			name: "no id at all",
			item: core.Item{App: "reddit", Source: "r/codex"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := redditRefOf(tc.item)
			if ok != tc.ok || got != tc.want {
				t.Errorf("redditRefOf() = %+v, %t; want %+v, %t", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// Reddit answers a comments page with two listings: the post, then the tree.
// A comment with no replies carries "" where the others carry a Listing, which
// is the one thing about the shape that cannot be read straight.
const redditThreadJSON = `[
  {"kind": "Listing", "data": {"children": [
    {"kind": "t3", "data": {"id": "1wgjbh4", "title": "Go 1.30 is out", "selftext": "",
      "url": "https://go.dev/blog/go1.30", "is_self": false, "author": "op", "score": 261}}
  ]}},
  {"kind": "Listing", "data": {"children": [
    {"kind": "t1", "data": {"author": "alice", "body": "first &amp; best", "created_utc": 1789427775.0,
      "replies": {"kind": "Listing", "data": {"children": [
        {"kind": "t1", "data": {"author": "[deleted]", "body": "[removed]", "replies": {"kind": "Listing", "data": {"children": [
          {"kind": "t1", "data": {"author": "carol", "body": "orphaned by a delete", "replies": ""}}
        ]}}}},
        {"kind": "more", "data": {"count": 41}}
      ]}}}},
    {"kind": "t1", "data": {"author": "bob", "body": "unconvinced", "replies": ""}}
  ]}}
]`

func TestRedditThreadReadsTheTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1wgjbh4.json" {
			t.Errorf("asked for %q, want the post's own id", r.URL.Path)
		}
		if r.URL.Query().Get("raw_json") != "1" {
			t.Errorf("query = %q, want raw_json=1", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(redditThreadJSON))
	}))
	defer srv.Close()
	was := redditItemAPIs
	redditItemAPIs = []string{srv.URL + "/"}
	defer func() { redditItemAPIs = was }()

	th, err := fetchRedditThread(context.Background(), gistRef{Service: gistReddit, ID: "1wgjbh4", Kind: "post"})
	if err != nil {
		t.Fatal(err)
	}
	if th.Title != "Go 1.30 is out" || th.URL != "https://go.dev/blog/go1.30" || th.Points != 261 {
		t.Errorf("thread = %+v, want the post's own details", th)
	}
	// Three comments: the deleted one is not a comment and the "more" stub is not
	// one either.
	if th.Count != 3 {
		t.Errorf("count = %d, want 3", th.Count)
	}
	// ...but what was said under the deleted one is still an answer to something,
	// so it moves up rather than going with it.
	if len(th.Replies) != 2 || len(th.Replies[0].Kids) != 1 || th.Replies[0].Kids[0].Author != "carol" {
		t.Fatalf("replies = %+v, want the deleted comment's child kept", th.Replies)
	}
	if th.Replies[0].Text != "first & best" {
		t.Errorf("text = %q, want the entities back as themselves", th.Replies[0].Text)
	}
}

// A self post is its own article: there is no page to go and fetch, and the
// text is already in the prompt.
func TestRedditThreadLinksNoArticleForASelfPost(t *testing.T) {
	raw := []byte(`[{"kind":"Listing","data":{"children":[{"kind":"t3","data":{
	  "title":"what are you working on?","selftext":"tell me","is_self":true,
	  "url":"https://www.reddit.com/r/golang/comments/1wgjbh4/what/","score":12}}]}},
	  {"kind":"Listing","data":{"children":[]}}]`)
	th, err := redditThreadOf(gistRef{Service: gistReddit, ID: "1wgjbh4", Kind: "post"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if th.URL != "" || th.Text != "tell me" {
		t.Errorf("thread = %+v, want its own text and no article", th)
	}
}

func TestRedditThreadReportsWhatTheAPISaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	was := redditItemAPIs
	redditItemAPIs = []string{srv.URL + "/"}
	defer func() { redditItemAPIs = was }()

	if _, err := fetchRedditThread(context.Background(), gistRef{Service: gistReddit, ID: "1", Kind: "post"}); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want the status in it", err)
	}
}

// The other half of the gist: a reddit post's prompt says which room it is, and
// calls the votes what reddit calls them.
func TestItemSummaryPromptOfARedditPost(t *testing.T) {
	th := discussion{
		Ref:   gistRef{Service: gistReddit, ID: "1wgjbh4", Kind: "post"},
		Title: "Go 1.30 is out", URL: "https://go.dev/blog/go1.30", Points: 261,
		Replies: []discNode{{Author: "alice", Text: "at last", Kids: []discNode{{Author: "bob", Text: "no"}}}},
	}
	th.Count = discCount(th.Replies)
	p := itemSummaryPrompt(core.Item{App: "reddit", ID: "1wgjbh4", Source: "r/golang", Title: "Go 1.30 is out"}, th, "", "en")
	for _, want := range []string{
		"Summarize the Reddit discussion under a post",
		"Post: Go 1.30 is out",
		"Subreddit: r/golang",
		"Article: https://go.dev/blog/go1.30",
		"Upvotes: 261",
		"2 comments follow",
		"--- comment 1 · depth 1\nat last",
		"--- comment 2 · depth 2\nno",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
	for _, gone := range []string{"Hacker News", "alice", "bob"} {
		if strings.Contains(p, gone) {
			t.Errorf("prompt should not carry %q:\n%s", gone, p)
		}
	}
}
