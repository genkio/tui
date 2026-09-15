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

// The button is offered by the services whose discussions this server can go
// and read, and by no others: a card that cannot answer must not grow a control
// that spins and fails.
func TestOnlyDiscussableCardsOfferAGist(t *testing.T) {
	hn := renderCard(t, core.Item{
		App: "inoreader", ID: "77", Source: "Hacker News: Best",
		Title: "Tmp.0ut Volume 5", URL: "https://tmpout.sh/5/",
		Body: "Comments URL: https://news.ycombinator.com/item?id=49516059",
	})
	if !strings.Contains(hn, `<button class="gist" type="button" data-state="idle"`) {
		t.Errorf("an HN card's footer should offer a gist:\n%s", hn)
	}
	reddit := renderCard(t, core.Item{App: "reddit", ID: "1wgjbh4", Source: "r/golang", Title: "go 1.30"})
	if !strings.Contains(reddit, `<button class="gist" type="button" data-state="idle"`) {
		t.Errorf("a reddit card's footer should offer one too:\n%s", reddit)
	}
	other := renderCard(t, core.Item{App: "x", ID: "1", Source: "@someone", Body: "shipping it"})
	if strings.Contains(other, `class="gist"`) {
		t.Errorf("an x card should not offer one:\n%s", other)
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

func TestFetchArticleReadsThePageAsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><title>t</title><style>p{}</style></head>`+
			`<body><nav>menu here</nav><script>var x = 1;</script>`+
			`<p>First paragraph.</p><p>Second &amp; last.</p></body></html>`)
	}))
	defer srv.Close()
	got, err := fetchArticle(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchArticle: %v", err)
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

// A discussion that links itself — an Ask HN, a reddit self post or gallery —
// is already in the prompt, and a PDF is not something to feed one: none of
// them is fetched, and none of them is an error.
func TestFetchArticleSkipsWhatIsNotAnArticle(t *testing.T) {
	for _, u := range []string{
		"https://news.ycombinator.com/item?id=49500001",
		"https://www.reddit.com/r/golang/comments/1wgjbh4/go_130/",
		"https://i.redd.it/abc.png",
		"ftp://example.com/a.txt",
	} {
		if got, err := fetchArticle(context.Background(), u); err != nil || got != "" {
			t.Errorf("%s: got %q, %v; want it skipped", u, got, err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		io.WriteString(w, "%PDF-1.7")
	}))
	defer srv.Close()
	if got, err := fetchArticle(context.Background(), srv.URL); err != nil || got != "" {
		t.Errorf("got %q, %v; want the pdf skipped", got, err)
	}
}

// An empty thread is named for what was empty: the three are different
// disappointments, and a reddit post is not a story.
func TestNothingSaidNamesWhatWasEmpty(t *testing.T) {
	for _, tc := range []struct {
		ref  gistRef
		want string
	}{
		{gistRef{Service: gistHN, Kind: "story"}, "under that story"},
		{gistRef{Service: gistHN, Kind: "comment"}, "replied to that comment"},
		{gistRef{Service: gistReddit, Kind: "post"}, "under that post"},
	} {
		if got := nothingSaid(tc.ref); !strings.Contains(got, tc.want) {
			t.Errorf("nothingSaid(%+v) = %q, want %q in it", tc.ref, got, tc.want)
		}
	}
}
