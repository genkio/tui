package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
	// Nothing set aside yet is nothing to point at, so the chip is drawn but
	// hidden: it has to be able to appear the moment a card is set aside, and a
	// chip that only arrived on the next page load would make asking for a
	// discussion look like it did nothing.
	plain := tallyItems([]core.Item{{App: "hn", ID: "2", Title: "still going"}})
	page = renderInput(t, pageInput{
		items: items[1:2], total: 1, apps: []string{"hn"}, now: time.Now(),
		tally: &plain, query: url.Values{},
	})
	if !strings.Contains(page, `<a class="fchip hid" href="/?gist=1" data-kind="gist"`) {
		t.Errorf("an empty gist chip should be drawn hidden: %s", page)
	}
	if !strings.Contains(page, `if(chip.dataset.kind === 'gist') chip.classList.toggle('hid', count === 0);`) {
		t.Error("the chip should show itself as soon as it holds something")
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
	if !strings.Contains(page, `if (GIST_PILE) buttons().forEach(`) {
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

// Asking for a discussion sets the item aside. The gesture is "not now, and not
// without the thread": you tap gist and carry straight on down the deck, so the
// item has to leave the feed on the asking rather than on the answer. Before
// this it stayed in the feed, was marked read by the very next tap, and had
// dropped out of the backlog by the time its gist landed — which is why the chip
// it was meant to fill was never drawn.
func TestAskingForAGistSetsTheItemAside(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{{
		App: "inoreader", ID: "1", Source: "Hacker News: Best", Title: "a story",
		URL: "https://news.ycombinator.com/item?id=1",
	}, {App: "x", ID: "2", Title: "something else"}}, now)
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "what the room said", nil
	})
	sum.thread = func(context.Context, gistRef) (discussion, error) {
		return discussion{Count: 3, Ref: gistRef{Service: gistHN, Kind: "story"}, Title: "a story"}, nil
	}
	sum.article = nil

	if rec := post(t, sum, "app=inoreader&id=1"); rec.Code != http.StatusAccepted {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body)
	}
	// Out of the feed and into the pile the moment the ask is taken, not when the
	// reading finishes: the whole minute in between is time you spend elsewhere.
	if got := cache.unreadCount(); got != 1 {
		t.Fatalf("unread = %d, want 1: the asked-for item should have left the feed", got)
	}
	if pile := cache.gisting(now); len(pile) != 1 || pile[0].ID != "1" {
		t.Fatalf("the gist pile holds %+v, want the item asked about", pile)
	}
	// And it is still unread, which is the point: it is waiting, not gone.
	for _, it := range cache.unread(now, "") {
		if it.ID == "1" {
			t.Fatal("a set-aside item should not be back in the feed")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(sum.gisted()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the gist never landed: %+v", sum.states())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := cache.gistingCount(); got != 1 {
		t.Fatalf("the pile is %d after the reading landed, want 1", got)
	}
	// Reading it out of the chip is what finally marks it read.
	cache.markRead("inoreader", []string{"1"}, now)
	if got := cache.gistingCount(); got != 0 {
		t.Fatalf("the pile is %d after reading it there, want 0", got)
	}
}

// A thread nobody could read is no reason to lose the item: it goes back to the
// feed rather than sitting in the chip with nothing to show and no way out.
func TestAFailedGistHandsTheItemBack(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{{
		App: "inoreader", ID: "1", Source: "Hacker News: Best", Title: "a story",
		URL: "https://news.ycombinator.com/item?id=1",
	}}, now)
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "", errors.New("the model fell over")
	})
	sum.thread = func(context.Context, gistRef) (discussion, error) {
		return discussion{Count: 3, Ref: gistRef{Service: gistHN, Kind: "story"}, Title: "a story"}, nil
	}
	sum.article = nil
	if rec := post(t, sum, "app=inoreader&id=1"); rec.Code != http.StatusAccepted {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for cache.gistingCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("a failed reading left the item set aside for good")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := cache.unreadCount(); got != 1 {
		t.Fatalf("unread = %d, want the item back in the feed", got)
	}
}

// A gist is a minute of somebody else's time, asked for so it can be come back
// to. A restart that emptied the chip would have set the item aside for nothing,
// so both the pile and the prose are on disk.
func TestGistsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.db")
	db, err := openFeedDB(path)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cache.upsert([]core.Item{{
		App: "inoreader", ID: "1", Source: "Hacker News: Best", Title: "a story",
		URL: "https://news.ycombinator.com/item?id=1",
	}}, now)
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "what the room said", nil
	})
	sum.thread = func(context.Context, gistRef) (discussion, error) {
		return discussion{Count: 3, Ref: gistRef{Service: gistHN, Kind: "story"}, Title: "a story"}, nil
	}
	sum.article = nil
	if rec := post(t, sum, "app=inoreader&id=1&lang=en"); rec.Code != http.StatusAccepted {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(sum.gisted()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the gist never landed: %+v", sum.states())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if err := db.close(); err != nil {
		t.Fatal(err)
	}

	// The server comes back up over the same database.
	again, err := openFeedDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.close()
	cache2, err := loadFeedCacheDB(again)
	if err != nil {
		t.Fatal(err)
	}
	if got := cache2.gistingCount(); got != 1 {
		t.Fatalf("the pile is %d after a restart, want 1", got)
	}
	sum2 := newSummarizer(cache2)
	if !sum2.gisted()[core.Key("inoreader", "1")] {
		t.Fatalf("the chip is empty after a restart: %v", sum2.gisted())
	}
	j, ok := sum2.job(summaryKey("inoreader", "1"))
	if !ok || j.State != "done" || !strings.Contains(j.HTML, "what the room said") {
		t.Fatalf("the prose did not survive: %+v (ok=%v)", j, ok)
	}
	if j.Count != 3 {
		t.Errorf("comment count = %d, want 3", j.Count)
	}
}

// What the page does with a card once its discussion has been asked for: gone
// from the feed's counts, but never reported read, because it has not been.
func TestTheFeedLetsGoOfAnAskedForCard(t *testing.T) {
	p := renderSwipePage(t, []core.Item{{
		App: "inoreader", ID: "1", Source: "Hacker News: Best", Title: "a",
		URL: "https://news.ycombinator.com/item?id=1",
	}}, "inoreader")
	if !strings.Contains(p, `.then(function(){ setAside(card); watch(); })`) {
		t.Error("a taken ask should set the card aside at once, not on the next load")
	}
	if !strings.Contains(p, `card.dataset.gisting = '1';`) || !strings.Contains(p, `card.classList.add('read');`) {
		t.Error("the card should leave the feed's counts, flagged apart from a read one")
	}
	// The next tap in the deck moves on without claiming the item was read.
	if !strings.Contains(p, `if(g && g.dataset.state === 'running') return false;`) {
		t.Error("moving on past a card whose thread is being read must not mark it read")
	}
	if !strings.Contains(p, `var aside = document.querySelectorAll('article.card[data-gisting]').length;`) {
		t.Error("the gist chip should count what has just been set aside")
	}
}

// The pile is written out with the rest of the backlog, so a server that
// stopped between a reading landing and the next write would come back holding
// prose for an item that had wandered back into the feed. The stored gist is
// enough to put it where it belongs.
func TestARestoredGistPutsItsItemBackInThePile(t *testing.T) {
	db, err := openFeedDB(filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	it := core.Item{App: "hn", ID: "1", Title: "a story", URL: "https://news.ycombinator.com/item?id=1"}
	cache.upsert([]core.Item{it}, now)
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if err := db.putGist(storedGist{
		App: "hn", ID: "1", Lang: "en", HTML: "<p>what the room said</p>",
		Count: 2, Generated: now.UTC().Format(time.RFC3339), Item: it.Wire(),
	}); err != nil {
		t.Fatal(err)
	}
	// The item is unread and in the feed: nothing wrote the set-aside out.
	if cache.gistingCount() != 0 || cache.unreadCount() != 1 {
		t.Fatalf("setup: pile=%d unread=%d", cache.gistingCount(), cache.unreadCount())
	}

	newSummarizer(cache)
	if got := cache.gistingCount(); got != 1 {
		t.Fatalf("pile = %d after restore, want the item with a gist behind it", got)
	}
	if got := cache.unreadCount(); got != 0 {
		t.Fatalf("unread = %d, want the set-aside item out of the feed", got)
	}
}
