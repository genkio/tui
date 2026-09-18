package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

// The gist chip: a discussion takes a minute to read, so you fire off a handful
// from the cards and carry on, and the ones that came to something collect
// under a chip of their own to be read one after another.
func TestGistChipCollectsWhatIsReady(t *testing.T) {
	items := []core.Item{
		{App: "reddit", ID: "1", Title: "read already", Gisted: true},
		{App: "hn", ID: "2", Title: "still going"},
		{App: "hn", ID: "3", Title: "read already too", Gisted: true},
	}
	tally := tallyItems(items)
	page := renderInput(t, pageInput{
		items: items, total: len(items), apps: []string{"reddit", "hn"}, now: time.Now(),
		tally: &tally, query: url.Values{},
	})
	if !strings.Contains(page, `<span>gist</span><span class="fn">2</span>`) {
		t.Errorf("the chip should count the discussions waiting: %s", page)
	}
	if !strings.Contains(page, `href="/?gist=1" data-kind="gist"`) {
		t.Error("the chip should link to a page of them")
	}
	// A card whose gist is still running is not on that page.
	got := selectItems(items, feedSel{Kind: "gist", Key: "gist"})
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("the gist pick holds %+v, want the two that are ready", got)
	}
	// Nothing read yet is nothing to point at, so the chip is not drawn.
	plain := tallyItems([]core.Item{{App: "hn", ID: "2", Title: "still going"}})
	page = renderInput(t, pageInput{
		items: items[1:2], total: 1, apps: []string{"hn"}, now: time.Now(),
		tally: &plain, query: url.Values{},
	})
	if strings.Contains(page, `data-kind="gist"`) {
		t.Error("an empty gist chip should not be drawn")
	}
}

// On that page every card opens itself: having waited for the thread once,
// tapping each card again would be the same wait in another shape.
func TestGistPickOpensEveryCard(t *testing.T) {
	page := renderInput(t, pageInput{
		items: []core.Item{{App: "hn", ID: "1", Title: "a", URL: "https://news.ycombinator.com/item?id=1", Gisted: true}},
		total: 1, apps: []string{"hn"}, now: time.Now(),
		sel: feedSel{Kind: "gist", Key: "gist"}, query: url.Values{"gist": {"1"}},
	})
	if !strings.Contains(page, `data-sel="gist:gist"`) {
		t.Error("the page should say which chip it is narrowed to")
	}
	if !strings.Contains(page, `if (SEL === 'gist:gist') buttons().forEach(`) {
		t.Error("the gist pick should open the discussions it collected")
	}
}

// A finished item briefing is what the chip counts, and only a finished one:
// a run still going, or one that came to nothing, has nothing to point at.
func TestSummarizerTracksFinishedGists(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{
		App: "inoreader", ID: "1", Source: "Hacker News: Best", Title: "a story",
		URL: "https://news.ycombinator.com/item?id=1",
	}}, time.Now())
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "what the room said", nil
	})
	sum.thread = func(context.Context, gistRef) (discussion, error) {
		return discussion{Count: 3, Ref: gistRef{Service: gistHN, Kind: "story"}, Title: "a story"}, nil
	}
	sum.article = nil
	if len(sum.gisted()) != 0 {
		t.Fatal("nothing has been read yet")
	}
	if rec := post(t, sum, "app=inoreader&id=1"); rec.Code != http.StatusAccepted {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(sum.gisted()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the gist never landed: %+v", sum.states())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sum.gisted()[core.Key("inoreader", "1")] {
		t.Errorf("gisted = %v, want the item's own feed key", sum.gisted())
	}
}
