package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// renderPage / renderCard execute the embedded page template the way handleAll
// does, so the tests cover the real markup. xTab is which x timeline the page is
// showing: "foryou" renders it as the For You chip's page.
func renderPage(t *testing.T, items []core.Item, apps, failed []string, xTab, warn string) string {
	t.Helper()
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := loader.load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	in := pageInput{items: items, total: len(items), apps: apps, failed: failed, now: time.Now(), warn: warn, updated: time.Now()}
	if xTab == "foryou" {
		in.sel = feedSel{Kind: "x", Key: "foryou"}
	}
	if err := tmpl.Execute(&b, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// renderInput renders one hand-built pageInput, for the cases a fixed helper
// can't reach (a windowed backlog, a sweep in flight).
func renderInput(t *testing.T, in pageInput) string {
	t.Helper()
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := loader.load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// renderSavedPage renders the saved list the way the ?saved=1 route does.
func renderSavedPage(t *testing.T, store *savedStore) string {
	t.Helper()
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := loader.load()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var b strings.Builder
	items := store.list(now)
	in := pageInput{
		items: items, total: len(items), now: now, saved: store, savedView: true,
		query: url.Values{"saved": {"1"}}, // the route's own param, which the chips keep
	}
	if err := tmpl.Execute(&b, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// renderSwipePage renders the feed the way --swipe serves it, for the given
// authed apps.
func renderSwipePage(t *testing.T, items []core.Item, apps ...string) string {
	t.Helper()
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := loader.load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if len(apps) == 0 {
		apps = []string{"x", "reddit"}
	}
	in := pageInput{items: items, total: len(items), apps: apps, now: time.Now(), swipe: true}
	if err := tmpl.Execute(&b, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func renderCard(t *testing.T, it core.Item) string {
	t.Helper()
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := loader.load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := tmpl.ExecuteTemplate(&b, "card", buildCard(it, false, listClips)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderCardEscapes(t *testing.T) {
	// Title differs from body here, so the card shows a title + body.
	it := core.Item{
		App:    "reddit",
		ID:     "a<b\"c",
		Title:  "<script>alert(1)</script> & \"quotes\"",
		Body:   "safe <body> & text",
		Source: "r/golang",
		Author: "bob",
		URL:    "https://example.com/?a=1&b=2",
		Age:    "2h",
	}
	out := renderCard(t, it)
	if strings.Contains(out, "<script>") {
		t.Fatal("script tag not escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatal("expected escaped script")
	}
	if !strings.Contains(out, "&amp;") {
		t.Fatal("expected & escaped")
	}
	// A title block and the open link in the footer are present.
	if !strings.Contains(out, "class=\"ctitle\"") {
		t.Fatalf("expected title block: %s", out)
	}
	if !strings.Contains(out, "class=\"open\"") {
		t.Fatalf("expected footer open link: %s", out)
	}
}

func TestRenderCardDedupTitle(t *testing.T) {
	// x posts carry the full text as both title and body: no duplicate title,
	// and the original post is opened only via the footer link.
	it := core.Item{
		App:    "x",
		ID:     "tweet1",
		Title:  "Hello world this is a tweet",
		Body:   "Hello world this is a tweet",
		Source: "@alice",
		URL:    "https://x.com/alice/status/tweet1",
		Age:    "5m",
	}
	out := renderCard(t, it)
	if strings.Count(out, "class=\"ctitle\"") != 0 {
		t.Fatalf("expected no duplicate title, got: %s", out)
	}
	if !strings.Contains(out, "href=\"https://x.com/alice/status/tweet1\"") {
		t.Fatal("expected the original post URL on the open link")
	}
	// Nothing else on the card links out: only the footer link.
	if strings.Count(out, "target=\"_blank\"") != 1 {
		t.Fatalf("expected exactly one external link (footer), got: %s", out)
	}
}

func TestRenderCardExpandOnlyWhenLong(t *testing.T) {
	short := core.Item{App: "reddit", ID: "1", Title: "T", Body: "short", Source: "r/g", URL: "https://e.com", Age: "1m"}
	if strings.Contains(renderCard(t, short), "expand") {
		t.Fatal("expand should not appear for short content")
	}
	long := core.Item{App: "reddit", ID: "2", Title: "T", Body: strings.Repeat("word ", 200), Source: "r/g", URL: "https://e.com", Age: "1m"}
	out := renderCard(t, long)
	// Expanding is the word count in the text; the footer only carries the
	// collapse button, hidden until something is expanded.
	if !strings.Contains(out, `class="expand hid" type="button">less`) {
		t.Fatal("expand should appear for long content")
	}
	if !strings.Contains(out, "class=\"full hid\"") {
		t.Fatal("expected a hidden full-content panel")
	}
}

// The ellipsis says there is more; the badge after it says how much more.
func TestRenderCardRemainingWordCount(t *testing.T) {
	// 200 words of 5 runes each: the 220-rune clip keeps exactly 44 of them.
	long := core.Item{App: "reddit", ID: "1", Title: "T", Body: strings.Repeat("word ", 200), Source: "r/g", URL: "https://e.com", Age: "1m"}
	out := renderCard(t, long)
	if !strings.Contains(out, `…<button class="rest" type="button" title="show the rest">+156 words</button>`) {
		t.Fatalf("expected the clipped preview to report the words left: %s", out)
	}
	if n := strings.Count(out, `class="rest"`); n != 1 {
		t.Errorf("only the clipped preview carries the badge, got %d: %s", n, out)
	}
	short := core.Item{App: "reddit", ID: "2", Title: "T", Body: "short", Source: "r/g", URL: "https://e.com", Age: "1m"}
	if out := renderCard(t, short); strings.Contains(out, `class="rest"`) {
		t.Errorf("nothing is clipped, so no word count: %s", out)
	}
}

func TestCountWords(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"one", 1},
		{"  two  words  ", 2},
		{"hi, there", 2},
		{"don't stop", 2},
		{"你好，世界", 4},       // no spaces: each rune a word, the comma none
		{"go 语言 rocks", 4}, // mixed script
	}
	for _, c := range cases {
		if got := countWords(c.in); got != c.want {
			t.Errorf("countWords(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Swipe mode deals one card at a time, so a body that the feed would have
// clipped often fits whole — but the cap still exists.
func TestSwipeClipsLater(t *testing.T) {
	body := strings.Repeat("word ", 60) // 300 runes: past the feed's clip, inside the deck's
	if !strings.Contains(renderCard(t, core.Item{App: "reddit", ID: "1", Title: "T", Body: body}), `class="rest"`) {
		t.Fatal("the feed should still clip a 300-rune body")
	}
	deck := buildCard(core.Item{App: "reddit", ID: "1", Title: "T", Body: body}, false, swipeClips)
	if strings.Contains(string(deck.PreviewBody), `class="rest"`) {
		t.Errorf("a swiped card has room for this whole body: %s", deck.PreviewBody)
	}
	if deck.Expand {
		t.Error("nothing is clipped, so the card needs no expand control")
	}
	long := buildCard(core.Item{App: "reddit", ID: "2", Title: "T", Body: strings.Repeat("word ", 200)}, false, swipeClips)
	if !strings.Contains(string(long.PreviewBody), `class="rest"`) {
		t.Errorf("1000 runes is past even the deck's cap: %s", long.PreviewBody)
	}
}

// The deck wraps the cards and keeps the footer actions; the feed does neither.
func TestRenderPageSwipeDeck(t *testing.T) {
	items := []core.Item{{App: "x", ID: "1", Title: "a"}, {App: "reddit", ID: "2", Title: "b"}}
	deck := renderSwipePage(t, items)
	if !strings.Contains(deck, `<div class="deckwrap"><div class="deck" id="deck">`) {
		t.Fatalf("expected the cards wrapped in a centered deck: %s", deck)
	}
	if !strings.Contains(deck, `data-swipe="true"`) {
		t.Error("the page should tell its script it is in swipe mode")
	}
	if strings.Count(deck, `class="share"`) != 2 {
		t.Error("every card keeps its footer actions in the deck")
	}
	if !strings.Contains(deck, `class="markall fab"`) {
		t.Errorf("mark-all-read should float in a deck: %s", deck)
	}
	// The end of the deck says so and nothing more: where to go next is the chip
	// row's business now, not a link buried in an end state.
	if !strings.Contains(deck, `id="deckEnd">Deck's empty — that's every card.</div>`) {
		t.Errorf("expected a plain end state: %s", deck)
	}
	feed := renderPage(t, items, []string{"x", "reddit"}, nil, "following", "")
	if strings.Contains(feed, `class="deck"`) || strings.Contains(feed, `id="deckEnd"`) {
		t.Errorf("the scrolling feed should render no deck: %s", feed)
	}
	if !strings.Contains(feed, `data-swipe="false"`) {
		t.Errorf("the feed should tell its script swipe mode is off: %s", feed)
	}
}

// The deck's two corner buttons: one tick on the left for this card, two on the
// right for the lot. A thumb that would rather tap than throw needs the same
// two things the gestures already do.
func TestDeckCornerTicks(t *testing.T) {
	items := []core.Item{{App: "x", ID: "1", Title: "a"}, {App: "reddit", ID: "2", Title: "b"}}
	deck := renderSwipePage(t, items)
	if !strings.Contains(deck, `id="markOne" class="markall fab" type="button" title="mark this card read and deal the next"><svg`) {
		t.Fatalf("expected a single-tick button for the card on screen: %s", deck)
	}
	// Drawn, not typeset: one path for the single, two for the double, and the
	// same stroke on both — which is the whole reason they aren't ✓ and ✓✓.
	if strings.Count(deck, `<path d="M5 12.5L10 17.5L19 6.5"/>`) != 1 {
		t.Errorf("expected one drawn tick on the left button: %s", deck)
	}
	if !strings.Contains(deck, `<path d="M2 12.5L7 17.5L16 6.5"/><path d="M11 12.5L16 17.5L25 6.5"/>`) {
		t.Errorf("expected two drawn ticks on the right button: %s", deck)
	}
	if strings.Count(deck, `stroke-width="2.4"`) != 2 {
		t.Errorf("both marks have to carry the same stroke or the pair looks wrong: %s", deck)
	}
	// Height is what's pinned, so the wider double keeps the single's stroke.
	if !strings.Contains(deck, ".markall.fab svg{display:block;height:22px;width:auto}") {
		t.Errorf("the two marks have to be scaled off their height: %s", deck)
	}
	if strings.Contains(deck, "mark all read") {
		t.Error("the deck's fab has no room for words; the title attribute carries them")
	}
	if !strings.Contains(deck, `title="mark the whole deck read"`) {
		t.Errorf("a wordless button still has to say what it does on a long press: %s", deck)
	}
	// Both hide together when the deck is dealt out: neither has anything left
	// to act on.
	if !strings.Contains(deck, `if (oneBtn) oneBtn.classList.toggle('hid', done);`) {
		t.Error("the single tick should go away with the deck")
	}
	if !strings.Contains(deck, ".markall.fab#markOne{left:14px}") || !strings.Contains(deck, ".markall.fab#markAll{right:14px}") {
		t.Errorf("the two ticks belong in opposite corners: %s", deck)
	}

	// The scrolling feed keeps its wordy button: there is no card on screen
	// there to mark, and the button sits in the flow where a label fits.
	feed := renderPage(t, items, []string{"x", "reddit"}, nil, "following", "")
	if strings.Contains(feed, `id="markOne"`) {
		t.Error("a scrolling feed has no current card, so no single-tick button")
	}
	if !strings.Contains(feed, "mark all read") {
		t.Errorf("the feed's mark-all keeps its label: %s", feed)
	}
}

// x's For You is a chip of its own, in black so the two x chips are told apart:
// the blue one is the cached Following backlog, the black one is fetched on the
// spot. It replaces the end-of-feed offers, which are gone.
func TestForYouIsAChip(t *testing.T) {
	items := []core.Item{{App: "x", ID: "1", Title: "a"}}
	feed := renderPage(t, items, []string{"x"}, nil, "following", "")
	if !strings.Contains(feed, `href="/?x=foryou" data-kind="x" data-key="foryou" data-on="0"`) {
		t.Errorf("expected a For You chip the page is not on: %s", feed)
	}
	if !strings.Contains(feed, `style="background:#000000"`) {
		t.Errorf("the For You chip should be black: %s", feed)
	}
	// It is scraped, not cached, so from here there is nothing to count: the
	// chip is the icon alone.
	if !strings.Contains(feed, `style="background:#000000">𝕏</span></a>`) {
		t.Errorf("the For You chip carries no count until it has fetched: %s", feed)
	}
	// It sits beside x's own chip, which keeps its own color and count.
	i, j := strings.Index(feed, `data-key="x"`), strings.Index(feed, `data-key="foryou"`)
	if i < 0 || j < i {
		t.Errorf("the For You chip belongs next to x's own: %s", feed)
	}
	// On For You the chip is the one that is on, and tapping it again asks for
	// another round — the link back to the whole list is the clear chip.
	on := renderPage(t, items, []string{"x"}, nil, "foryou", "")
	if !strings.Contains(on, `href="/?x=foryou" data-kind="x" data-key="foryou" data-on="1"`) {
		t.Errorf("For You should render as the picked chip, still good for another round: %s", on)
	}
	// Fetched: now it has a number, next to the icon like any other chip's, and
	// it is that round's own — green, because the fetch landed.
	if !strings.Contains(on, `style="background:#000000">𝕏</span><span class="fn ok">1</span>`) {
		t.Errorf("a fetched round should state how much it brought: %s", on)
	}
	if failed := renderPage(t, nil, []string{"x"}, []string{"x"}, "foryou", ""); !strings.Contains(failed, `style="background:#000000">𝕏</span><span class="fn bad">0</span>`) {
		t.Errorf("a round that failed should say so in red: %s", failed)
	}
	if !strings.Contains(on, `class="fchip fclear" href="/" id="fclear"`) {
		t.Errorf("expected a way back to the whole list: %s", on)
	}
	// No x at all: no chip.
	if p := renderPage(t, items, []string{"reddit"}, nil, "following", ""); strings.Contains(p, `data-key="foryou"`) {
		t.Errorf("no x, no For You chip: %s", p)
	}
	// The header still counts the backlog, which the round is no part of, so
	// reading a round leaves that number alone rather than dropping it to
	// somewhere the next load would undo.
	if !strings.Contains(on, `if(SEL === 'x:foryou') return;`) {
		t.Errorf("reading a For You round should not move the backlog count: %s", on)
	}
	// The end-of-feed offers are gone; the chip is the only way in.
	for _, p := range []string{feed, renderSwipePage(t, items)} {
		if strings.Contains(p, `id="forYouNote"`) || strings.Contains(p, "Continue with For You") {
			t.Errorf("the end-of-feed offer should be gone: %s", p)
		}
	}
}

// Following a link that refetches covers the page it left behind.
func TestLoadingCover(t *testing.T) {
	p := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, []string{"x"}, nil, "following", "")
	if !strings.Contains(p, `id="loading" class="loading hid"`) {
		t.Errorf("expected a hidden loading cover: %s", p)
	}
}

func TestSavedViewNeverSwipes(t *testing.T) {
	in := pageInput{items: []core.Item{{App: "x", ID: "1", Title: "a"}}, now: time.Now(), savedView: true, swipe: true}
	if buildPageData(in).Swipe {
		t.Error("the saved list is for re-reading, so it stays a scrolling list")
	}
}

func TestRenderCardShareButton(t *testing.T) {
	it := core.Item{App: "x", ID: "1", Title: "hello", Body: "hello", Source: "@a", URL: "https://x.com/a/status/1", Age: "1m"}
	if out := renderCard(t, it); !strings.Contains(out, `class="share"`) {
		t.Fatalf("expected a share button in the footer: %s", out)
	}
}

func TestRenderPageMarkAll(t *testing.T) {
	with := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}, {App: "reddit", ID: "2", Title: "b"}}, []string{"x", "reddit"}, nil, "following", "")
	if !strings.Contains(with, "mark all read") {
		t.Fatal("expected mark-all-read button when there are items")
	}
	without := renderPage(t, nil, []string{"x"}, nil, "following", "")
	if strings.Contains(without, "mark all read") {
		t.Fatal("mark-all-read should be absent when the feed is empty")
	}
	// The 'all' wordmark is removed.
	if strings.Contains(with, "<h1>all</h1>") {
		t.Fatal("expected the 'all' title to be removed")
	}
}

func TestRenderPageWarn(t *testing.T) {
	p := renderPage(t, nil, []string{"inoreader"}, []string{"inoreader"}, "following", "Inoreader session is stale — re-run `tui inoreader --auth`.")
	if !strings.Contains(p, "session is stale") || !strings.Contains(p, `class="warn"`) {
		t.Fatalf("expected a warn banner: %s", p)
	}
	// No warning message → no banner.
	p2 := renderPage(t, nil, []string{"x"}, nil, "following", "")
	if strings.Contains(p2, `class="warn"`) {
		t.Fatal("warn banner should be absent when there's no warning")
	}
}

func TestLinkify(t *testing.T) {
	cases := []struct{ in, wantSub string }{
		// A bare URL on its own line becomes a clickable link.
		{"https://preview.redd.it/wtlijoe96phh1.png?width=964", `href="https://preview.redd.it/wtlijoe96phh1.png?width=964"`},
		// Multiple URLs each link, separated by the newlines.
		{"https://a.example/1\nhttps://b.example/2\n", "https://b.example/2"},
		// Surrounding prose is kept and links are inserted around it.
		{"see https://x.com/oops.<b>x</b>", `see <a class="link"`},
	}
	for _, c := range cases {
		out := linkify(c.in)
		if !strings.Contains(out, c.wantSub) {
			t.Errorf("linkify(%q) missing %q -> %s", c.in, c.wantSub, out)
		}
	}
	// Prose after a URL is still escaped (no injection).
	if out := linkify("https://x.com/a <b>hi</b>"); !strings.Contains(out, "&lt;b&gt;") {
		t.Fatalf("linkify must escape prose after a URL: %s", out)
	}
	// No URLs -> no links.
	if out := linkify("plain text with no links"); strings.Contains(out, "<a ") {
		t.Fatalf("no URLs should produce no links: %s", out)
	}
}

func TestRenderPageEmptyAndNote(t *testing.T) {
	// No authed apps: the page tells the user to log in.
	p := renderPage(t, nil, nil, nil, "following", "")
	if !strings.Contains(p, "No reader app is logged in") {
		t.Fatal("expected login note, got: " + p)
	}
	// The sort toggle is gone; the order is the server's business.
	if strings.Contains(p, "sortbar") {
		t.Fatal("the sort row should not be rendered: " + p)
	}
	// Authed but zero items: inbox zero.
	p2 := renderPage(t, nil, []string{"x"}, nil, "following", "")
	if !strings.Contains(p2, "Inbox zero") {
		t.Fatal("expected inbox-zero message")
	}
}

// A source chip's count is that service's status light: green when the last
// sweep of it worked, red when it didn't and the number is therefore stale.
func TestSourceChipCarriesServiceHealth(t *testing.T) {
	p := renderPage(t,
		[]core.Item{{App: "x", ID: "1", Title: "a"}, {App: "reddit", ID: "2", Title: "b"}},
		[]string{"x", "reddit"}, []string{"reddit"}, "following", "")
	if !strings.Contains(p, `title="x: live"`) || !strings.Contains(p, `class="fn ok">1<`) {
		t.Fatal("expected a green count on the healthy service's chip: " + p)
	}
	if !strings.Contains(p, `title="reddit: failed to load"`) || !strings.Contains(p, `class="fn bad">1<`) {
		t.Fatal("expected a red count on the failed service's chip: " + p)
	}

	// A service that failed has nothing on the page, and is exactly the one
	// worth seeing: it still gets a chip, reading zero, in red.
	p = renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, []string{"x", "folo"}, []string{"folo"}, "following", "")
	if !strings.Contains(p, `data-key="folo"`) || !strings.Contains(p, `class="fn bad">0<`) {
		t.Fatal("a service with nothing to show should still report itself: " + p)
	}

	// No logged-in services: nothing to draw.
	if strings.Contains(renderPage(t, nil, nil, nil, "following", ""), `id="filters"`) {
		t.Fatal("no logged-in service means no chips")
	}

	// Items cached before a logout are still items to filter by, so their app
	// keeps a chip — just without a status, having no session to report on.
	p = renderPage(t,
		[]core.Item{{App: "x", ID: "1", Title: "a"}, {App: "douban", ID: "2", Title: "b"}},
		[]string{"x"}, nil, "following", "")
	if !strings.Contains(p, `data-kind="app" data-key="douban" data-on="0">`) {
		t.Fatal("a logged-out app with cached items should keep its chip: " + p)
	}
	if strings.Contains(p, `title="douban: live"`) {
		t.Fatal("a logged-out app has no session to call live: " + p)
	}

	// The saved list is read off disk: no service to be up or down, so its
	// chips carry no state at all.
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	now := time.Now()
	for _, it := range []core.Item{
		{App: "x", ID: "1", Title: "a"},
		{App: "reddit", ID: "2", Title: "b"},
	} {
		if err := store.add(it, now); err != nil {
			t.Fatal(err)
		}
	}
	saved := renderSavedPage(t, store)
	if !strings.Contains(saved, `data-kind="app" data-key="x"`) {
		t.Fatal("the saved list still gets source chips: " + saved)
	}
	if strings.Contains(saved, `class="fn ok"`) || strings.Contains(saved, `class="fn bad"`) {
		t.Fatal("a saved-list chip has no service health to report: " + saved)
	}
}

// The chip row is one wrapping line of chips: a nested flex box per group broke
// the line at the group boundary and left the rest of it empty.
func TestChipGroupsShareOneRow(t *testing.T) {
	p := renderPage(t,
		[]core.Item{{App: "x", ID: "1", Title: "a"}, {App: "x", ID: "2", Title: "b", Audio: "https://ex.com/e.mp3"}},
		[]string{"x"}, nil, "following", "")
	if !strings.Contains(p, ".fgroup{display:contents}") {
		t.Error("groups should not be flex boxes of their own")
	}
	if !strings.Contains(p, ".fgroup+.fgroup>.fchip:first-child{margin-left:8px}") {
		t.Error("a later group needs its gap on its first chip, not on the group box")
	}
	// Both groups are there to share the row: two types on this page.
	if !strings.Contains(p, `data-kind="type" data-key="audio"`) {
		t.Fatal("expected the type group alongside the source group: " + p)
	}
}

// The deck takes h/l as well as the arrows, the vim spelling of sideways.
// Left walks back, right deals the next: the direction you are travelling, not
// the direction the card is thrown.
func TestDeckTakesVimKeys(t *testing.T) {
	p := renderSwipePage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, "x")
	if !strings.Contains(p, `k === 'ArrowLeft' || k === 'h' || k === 'H'`) {
		t.Error("h should walk back, like the left arrow")
	}
	if !strings.Contains(p, `k === 'ArrowRight' || k === 'l' || k === 'L'`) {
		t.Error("l should deal the next card, like the right arrow")
	}
	// j/k are the terminal's vertical pair; up and down stay the page's scroll,
	// so the deck must not claim them.
	if strings.Contains(p, `=== 'j'`) || strings.Contains(p, `=== 'k'`) {
		t.Error("the deck should not bind the vertical keys")
	}
}

// A read that never reached the server leaves the page lying to you: the card
// is greyed and off the counts, the server still has it unread. Swiping on from
// there only widens the gap, so the page repairs itself — one retry for a blip,
// then a reload, which comes back as whatever the server actually has.
func TestALostReadResyncs(t *testing.T) {
	p := renderSwipePage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, "x")
	// The ids stay queued: the retry needs something to send.
	if !strings.Contains(p, `pending[app] = ids.concat(pending[app]); failed = true; }`) {
		t.Error("a failed mark should go back on the queue, not be dropped")
	}
	if !strings.Contains(p, "Promise.all(calls).then(function(){ if(failed) resync(); })") {
		t.Error("a flush that lost a mark should resync")
	}
	// Retry once, reload after that — not the other way round, or a dropped
	// packet mid-swipe would throw the deck away.
	if !strings.Contains(p, "flushTimer = setTimeout(flushPending, 1500)") {
		t.Error("the first failure should retry, not reload")
	}
	if !strings.Contains(p, "location.reload();\n}") {
		t.Error("a second failure should reload")
	}
	// Marking read again after a recovery gets its own retry.
	if !strings.Contains(p, "decCount(ids.length); retried = false;") {
		t.Error("a landed mark should clear the retry, so the next blip gets one too")
	}
	// A page that reloads under you should say why, once, in passing.
	if !strings.Contains(p, `sessionStorage.setItem('tui:resync', '1')`) ||
		!strings.Contains(p, `toast("reloaded: the server didn't take some reads")`) {
		t.Error("the fresh page should explain the reload")
	}
	// A hung request is the quiet version of a refused one.
	if !strings.Contains(p, "AbortSignal.timeout") {
		t.Error("a mark that never answers should time out so the resync happens")
	}
}

// A swiped card starts where the chips end. Centring it in the leftover
// viewport put a short card a long way down and moved every card as the next
// one's height changed.
func TestDeckIsNotCentred(t *testing.T) {
	p := renderSwipePage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, "x")
	if strings.Contains(p, "100dvh") {
		t.Error("the deck should not reserve a viewport's worth of height to centre in")
	}
	if strings.Contains(p, "margin:auto 0") {
		t.Error("the deck should be top-aligned, not centred")
	}
	if !strings.Contains(p, ".deckwrap .deck{width:100%}") {
		t.Error("the deck should still fill the width")
	}
}

// Reading takes the chip counts down with the header's, so a chip never claims
// more than it would leave.
func TestChipCountsFollowReading(t *testing.T) {
	p := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, []string{"x"}, nil, "following", "")
	if !strings.Contains(p, "function recountChips()") {
		t.Error("expected the chip recount")
	}
	if !strings.Contains(p, "scheduleRecount();") {
		t.Error("marking a card read should schedule a recount")
	}
	if !strings.Contains(p, "article.card:not(.read)") {
		t.Error("the recount should count unread cards")
	}
}

// The page is served from the backlog cache, so it says how old that is and
// offers a fetch on the spot. Both live on the unread count itself, which is
// the only header element the number belongs next to.
func TestRenderPageFreshness(t *testing.T) {
	p := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, []string{"x"}, nil, "following", "")
	if !strings.Contains(p, `id="refresh"`) {
		t.Fatal("expected the tap-to-fetch control: " + p)
	}
	if !strings.Contains(p, `id="upd">just now`) {
		t.Fatal("expected the freshness label: " + p)
	}

	// A sweep in flight says so rather than showing an age about to change.
	in := pageInput{
		items: []core.Item{{App: "x", ID: "1", Title: "a"}}, total: 1,
		apps: []string{"x"}, now: time.Now(),
		updated: time.Now().Add(-9 * time.Minute), fetching: true,
	}
	if got := renderInput(t, in); !strings.Contains(got, `id="upd">fetching…`) {
		t.Fatal("a sweep in flight should say so: " + got)
	}

	// Nothing swept yet (a cold cache): no age to state.
	in.updated, in.fetching = time.Time{}, false
	if got := renderInput(t, in); strings.Contains(got, `id="upd"`) {
		t.Fatal("a never-swept cache has no age to show: " + got)
	}
}

// A page carries a window of the backlog, not all of it: the count is the whole
// thing, the button says what it is actually about to clear, and it flags that
// a reload brings more.
func TestRenderPageWindowedBacklog(t *testing.T) {
	items := []core.Item{{App: "x", ID: "1", Title: "a"}, {App: "x", ID: "2", Title: "b"}}
	deep := feedTally{apps: map[string]int{"x": 812}, types: map[string]int{"text": 812}}
	in := pageInput{items: items, total: 812, apps: []string{"x"}, now: time.Now(), tally: &deep, updated: time.Now()}
	p := renderInput(t, in)
	if !strings.Contains(p, `<span id="unreadn">812</span>`) {
		t.Fatal("the count is the whole backlog, not the window: " + p)
	}
	if !strings.Contains(p, `data-more="1"`) || !strings.Contains(p, `mark these <span id="markn">2</span> read`) {
		t.Fatal("expected a windowed mark-all button: " + p)
	}
	// The two numbers have to add up, or the button reads as a bug next to the
	// header's count.
	if !strings.Contains(p, `<span class="behind">810 more behind</span>`) {
		t.Fatal("a windowed button should say how much is behind it: " + p)
	}
	if !strings.Contains(p, "function relabelMarkAll()") {
		t.Error("the button's number should come down as cards are read")
	}

	// The whole backlog on one page: plain "mark all read", no number to keep
	// honest and nothing more behind.
	in.total = len(items)
	p = renderInput(t, in)
	if !strings.Contains(p, `data-more="0"`) || !strings.Contains(p, `mark all read`) {
		t.Fatal("expected the unwindowed mark-all button: " + p)
	}
	if strings.Contains(p, `id="markn"`) || strings.Contains(p, "more behind") {
		t.Fatal("an unwindowed page has nothing behind it to explain: " + p)
	}
}

// A chip is a link, so tapping one leaves the page — which is exactly when a
// mark that hasn't reached the server yet would be lost. The click is
// intercepted so the queue goes first.
func TestChipTapFlushesBeforeLeaving(t *testing.T) {
	p := renderPage(t,
		[]core.Item{{App: "x", ID: "1", Title: "a"}, {App: "reddit", ID: "2", Title: "b"}},
		[]string{"x", "reddit"}, nil, "following", "")
	if !strings.Contains(p, "flushPending().then(function(){ location.assign(href); })") {
		t.Error("a chip should flush pending marks and then navigate")
	}
	if !strings.Contains(p, "return Promise.all(calls)") {
		t.Error("flushPending has to be awaitable for that to be safe")
	}
}

// A capped count is short of the truth (a drain stopped at its round cap), so
// it renders the way the picker's does: "N+".
func TestRenderPageCappedCount(t *testing.T) {
	deep := feedTally{apps: map[string]int{"inoreader": 400}, types: map[string]int{"text": 400}}
	in := pageInput{
		items: []core.Item{{App: "inoreader", ID: "1", Title: "a"}}, total: 400,
		apps: []string{"inoreader"}, now: time.Now(), tally: &deep,
		updated: time.Now(), capped: true,
	}
	if got := renderInput(t, in); !strings.Contains(got, `<span id="unreadn">400</span>+ unread`) {
		t.Fatal("expected a capped count: " + got)
	}
}

// A bilibili post carries only a watch page: the player is this server's own
// route, which resolves the stream when it is first asked for.
func TestRenderCardBilibiliVideo(t *testing.T) {
	it := core.Item{
		App: "bilibili", ID: "1000000000000000001",
		Title:   "我做了一个东西",
		Body:    "花了三个月做这个",
		Source:  "何同学 · 1.2万播放",
		URL:     "https://www.bilibili.com/video/BV1GJ411x7h7",
		Poster:  "https://i2.hdslb.com/bfs/archive/cover.jpg",
		VidSecs: 754,
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `src="/bili?id=BV1GJ411x7h7"`) {
		t.Fatalf("expected the player to point at the bilibili route: %s", out)
	}
	if !strings.Contains(out, `poster="https://i2.hdslb.com/bfs/archive/cover.jpg"`) || !strings.Contains(out, `<span class="vlen">12:34</span>`) {
		t.Fatalf("the cover and the running time come from the feed: %s", out)
	}
	if !strings.Contains(out, `href="/bili?id=BV1GJ411x7h7&amp;dl=1"`) {
		t.Fatalf("keep should ask the same route for an attachment: %s", out)
	}
	if strings.Contains(out, "/dl?") {
		t.Fatalf("nothing here is a direct mp4 for the /dl proxy: %s", out)
	}
	if !strings.Contains(out, `data-type="video"`) {
		t.Fatalf("the saved list should be able to slice it as a video: %s", out)
	}
	if strings.Contains(out, `class="rgv"`) {
		t.Fatalf("a bilibili card is not a redgifs card: %s", out)
	}

	// A series episode names no bvid, so there is nothing to resolve and the card
	// stays a link out.
	it.URL = "https://www.bilibili.com/bangumi/play/ep123456"
	if out = renderCard(t, it); strings.Contains(out, "<video") || strings.Contains(out, "/bili?") {
		t.Fatalf("a bvid-less post should offer no player: %s", out)
	}
}

func TestRenderCardVideo(t *testing.T) {
	it := core.Item{
		App: "x", ID: "50", Title: "watch this", Body: "watch this",
		Source: "@vera", URL: "https://x.com/vera/status/50",
		Video:   "https://video.twimg.com/ext_tw_video/50/pu/vid/avc1/720x1280/high.mp4",
		Poster:  "https://pbs.twimg.com/ext_tw_video_thumb/50/pu/img/poster.jpg",
		VidSecs: 95,
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `<video class="vid"`) || !strings.Contains(out, `src="https://video.twimg.com/ext_tw_video/50/pu/vid/avc1/720x1280/high.mp4"`) {
		t.Fatalf("expected an inline video player: %s", out)
	}
	if !strings.Contains(out, `poster="https://pbs.twimg.com/ext_tw_video_thumb/50/pu/img/poster.jpg"`) {
		t.Fatalf("expected the poster frame: %s", out)
	}
	// Footer gains the video controls: speed + mute toggles and a save link
	// through the /dl proxy.
	if !strings.Contains(out, `class="speed"`) || !strings.Contains(out, `class="mute"`) || !strings.Contains(out, `class="rot"`) {
		t.Fatalf("expected speed, mute, and rotate controls: %s", out)
	}
	if !strings.Contains(out, `href="/dl?n=x-50.mp4&amp;u=https%3A%2F%2Fvideo.twimg.com`) {
		t.Fatalf("expected a /dl download link: %s", out)
	}
	// Download is 'keep', so it doesn't read as a second copy of the star button.
	if !strings.Contains(out, "<span>keep</span>") || strings.Count(out, ">save</button>") != 1 {
		t.Fatalf("the download link must not also be labelled save: %s", out)
	}
	// Repeat is the last of the footer's actions, and off until asked for.
	if !strings.Contains(out, `<button class="loop" type="button" data-on="0"`) {
		t.Fatalf("expected a loop toggle, off by default: %s", out)
	}
	if !strings.HasSuffix(strings.TrimSuffix(out, "</article>"), ">loop</button></div>") {
		t.Fatalf("loop should come last in the footer: %s", out)
	}
	// No video -> no player, no controls.
	it.Video, it.Poster = "", ""
	out = renderCard(t, it)
	if strings.Contains(out, "<video") || strings.Contains(out, `class="speed"`) || strings.Contains(out, "/dl?") {
		t.Fatal("player and controls should be absent without a video")
	}
	if strings.Contains(out, `class="loop"`) {
		t.Fatal("nothing to repeat without a video")
	}
}

// A redgifs link post has nothing playable in the feed JSON, so the card offers
// a video button instead of a player: the stream is resolved only when tapped.
func TestRenderCardRedgif(t *testing.T) {
	it := core.Item{
		App: "reddit", ID: "1abc", Title: "massage day",
		Source: "r/jav", Author: "someone",
		URL:    "https://www.redgifs.com/watch/ElementaryHoarseFlea",
		Images: []string{"https://preview.redd.it/abc.jpg"},
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `<button class="rgv" type="button" data-on="0" data-id="elementaryhoarseflea"`) {
		t.Fatalf("expected the footer video button, off until tapped: %s", out)
	}
	// Nothing plays yet, so none of the player controls are rendered: the button
	// brings them along when it swaps itself for the player.
	if strings.Contains(out, "<video") || strings.Contains(out, `class="speed"`) {
		t.Fatalf("no player until the button is tapped: %s", out)
	}

	// The link can also sit in the body of a self post.
	it.URL = "https://old.reddit.com/r/jav/comments/1abc/massage_day/"
	it.Body = "mirror: https://redgifs.com/ifr/elementaryhoarseflea"
	if out := renderCard(t, it); !strings.Contains(out, `data-id="elementaryhoarseflea"`) {
		t.Fatalf("a redgifs link in the body should offer the same button: %s", out)
	}

	// Any other link is left alone.
	it.Body = "see https://example.com/watch/nothing"
	if out := renderCard(t, it); strings.Contains(out, `class="rgv"`) {
		t.Fatalf("only redgifs links get the button: %s", out)
	}
}

func TestRedgifID(t *testing.T) {
	cases := map[string]string{
		"https://www.redgifs.com/watch/elementaryhoarseflea":       "elementaryhoarseflea",
		"https://redgifs.com/watch/ElementaryHoarseFlea":           "elementaryhoarseflea",
		"https://v3.redgifs.com/watch/abc?rel=user%3Asomeone":      "abc",
		"http://www.redgifs.com/ifr/elementaryhoarseflea":          "elementaryhoarseflea",
		"look at https://www.redgifs.com/watch/somename, it's ok":  "somename",
		"https://www.redgifs.com/users/someone":                    "",
		"https://notredgifs.example.com/watch/elementaryhoarsefle": "",
		"https://media.redgifs.com/ElementaryHoarseFlea.mp4":       "",
	}
	for in, want := range cases {
		if got := redgifID(in); got != want {
			t.Errorf("redgifID(%q) = %q, want %q", in, got, want)
		}
	}
	// The first link on the card wins; blank texts are skipped.
	if got := redgifID("", "", "https://www.redgifs.com/watch/first and https://www.redgifs.com/watch/second"); got != "first" {
		t.Errorf("got %q, want the first clip linked", got)
	}
}

func TestRenderCardAudio(t *testing.T) {
	it := core.Item{
		App: "inoreader", ID: "77", Title: "Why Adults Are Getting Cancer at a Younger Age",
		Body: "Show notes", Source: "The Daily", URL: "https://ex.com/thedaily",
		Audio: "https://dts.podtrac.com/redirect.mp3/x/default.mp3?aid=rss_feed",
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `<audio class="aud" controls preload="metadata" src="https://dts.podtrac.com/redirect.mp3/x/default.mp3?aid=rss_feed"`) {
		t.Fatalf("expected an inline audio player: %s", out)
	}
	// An episode plays at the shared 2× like any other player, but sound is the
	// whole point of it, so it gets no mute toggle — nor rotate, nor /dl, which
	// only x's video CDN is allowed through.
	if !strings.Contains(out, `class="speed"`) {
		t.Fatalf("expected the speed control: %s", out)
	}
	if strings.Contains(out, `class="mute"`) || strings.Contains(out, `class="rot"`) || strings.Contains(out, "/dl?") {
		t.Fatalf("audio should carry no mute, rotate, or download: %s", out)
	}
	if strings.Contains(out, `class="loop"`) {
		t.Fatalf("an episode is not something to repeat: %s", out)
	}
	// No episode -> no player, no controls.
	it.Audio = ""
	if out := renderCard(t, it); strings.Contains(out, "<audio") || strings.Contains(out, `class="speed"`) {
		t.Fatalf("player and controls should be absent without audio: %s", out)
	}
}

// A podcast item that also has a video keeps the full video control set, with
// one speed button driving both players.
func TestRenderCardAudioAndVideoShareOneSpeed(t *testing.T) {
	it := core.Item{
		App: "inoreader", ID: "78", Title: "both", Body: "both", Source: "feed",
		Video: "https://video.twimg.com/ext_tw_video/78/high.mp4",
		Audio: "https://ex.com/ep.mp3",
	}
	out := renderCard(t, it)
	if strings.Count(out, `class="speed"`) != 1 {
		t.Errorf("speed is global, so one button: %s", out)
	}
	if !strings.Contains(out, `class="mute"`) || !strings.Contains(out, `class="rot"`) {
		t.Errorf("the video's own controls must survive: %s", out)
	}
}

func TestRenderCardVideoLength(t *testing.T) {
	it := core.Item{
		App: "x", ID: "50", Title: "watch this", Body: "watch this", Source: "@vera",
		Video:   "https://video.twimg.com/ext_tw_video/50/high.mp4",
		Poster:  "https://pbs.twimg.com/ext_tw_video_thumb/50/poster.jpg",
		VidSecs: 95,
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `<span class="vlen">1:35</span>`) {
		t.Fatalf("expected a length badge over the poster: %s", out)
	}
	// A quoted post's player carries its own badge.
	it.Quote = &core.Quote{Source: "@eve", Video: "https://video.twimg.com/ext_tw_video/30/q.mp4", VidSecs: 12}
	if out := renderCard(t, it); !strings.Contains(out, `<span class="vlen">0:12</span>`) {
		t.Errorf("expected a badge on the quoted video too: %s", out)
	}

	// Unknown length -> no badge at all, rather than a wrong 0:00.
	it.Quote, it.VidSecs = nil, 0
	if out := renderCard(t, it); strings.Contains(out, "vlen") {
		t.Errorf("no duration should mean no badge: %s", out)
	}
}

func TestVidLen(t *testing.T) {
	cases := []struct {
		secs int
		want string
	}{
		{0, ""}, {-3, ""}, {7, "0:07"}, {60, "1:00"}, {95, "1:35"},
		{600, "10:00"}, {3600, "1:00:00"}, {3903, "1:05:03"},
	}
	for _, c := range cases {
		if got := vidLen(c.secs); got != c.want {
			t.Errorf("vidLen(%d) = %q, want %q", c.secs, got, c.want)
		}
	}
}

func TestRenderCardQuote(t *testing.T) {
	it := core.Item{
		App: "x", ID: "3", Title: "see this", Body: "see this",
		Source: "@dave", URL: "https://x.com/dave/status/3", Age: "2h",
		Quote: &core.Quote{
			Source: "@eve", Author: "Eve", Text: "the <quoted> post",
			URL: "https://x.com/eve/status/30",
		},
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `class="quote"`) {
		t.Fatalf("expected an embedded quote block: %s", out)
	}
	if !strings.Contains(out, "@eve") || !strings.Contains(out, "Eve") {
		t.Errorf("quote should show its own author: %s", out)
	}
	if !strings.Contains(out, `href="https://x.com/eve/status/30"`) {
		t.Errorf("quote handle should link to the quoted post: %s", out)
	}
	if !strings.Contains(out, "the &lt;quoted&gt; post") {
		t.Errorf("quote text must be escaped: %s", out)
	}
	// The parent's own text is not repeated inside the quote box, and a short
	// quote needs no expand toggle.
	if strings.Contains(out, "quoting @eve") {
		t.Errorf("the quote must not also be appended to the body: %s", out)
	}
	if strings.Contains(out, `class="expand`) || strings.Contains(out, `class="rest"`) {
		t.Errorf("a short post + short quote needs no expand toggle: %s", out)
	}

	// A long quote alone is enough to earn the expand toggle: the count in the
	// quote's own text expands the card, and the quote gets its own
	// preview/full pair so expanding reveals all of it.
	it.Quote.Text = strings.Repeat("word ", 100)
	out = renderCard(t, it)
	if !strings.Contains(out, `class="rest"`) {
		t.Errorf("a clipped quote should offer its own expand control: %s", out)
	}
	if !strings.Contains(out, `class="expand hid"`) {
		t.Errorf("a clipped quote should get the footer collapse button: %s", out)
	}
	if strings.Count(out, `class="full hid"`) != 2 {
		t.Errorf("expected a full panel for both the body and the quote: %s", out)
	}

	// No quote -> no quote block.
	it.Quote = nil
	if out := renderCard(t, it); strings.Contains(out, `class="quote"`) {
		t.Errorf("quote block should be absent without a quote: %s", out)
	}
}

// doubanio serves a picture only to a request whose Referer is a douban page,
// which the browser cannot produce, so those images load through /img while
// every other host is fetched straight from the source.
func TestRenderCardImageProxy(t *testing.T) {
	it := core.Item{
		App: "douban", ID: "chart:36808876", Title: "#1 ↑ 奥德赛", Body: "8.3 ★",
		Source: "一周口碑电影榜",
		Images: []string{"https://img9.doubanio.com/view/photo/m/public/p2933569626.jpg"},
		Quote:  &core.Quote{Source: "好讨厌平台", Images: []string{"https://img9.doubanio.com/view/group_topic/l/public/p742204311.jpg"}},
	}
	out := renderCard(t, it)
	// the card's own picture and the embed's both route through the server
	if strings.Count(out, `data-src="/img?u=https%3A%2F%2Fimg9.doubanio.com`) != 2 {
		t.Errorf("douban images should load through /img: %s", out)
	}
	if strings.Contains(out, `data-src="https://img9.doubanio.com`) {
		t.Errorf("no douban image should be requested by the browser: %s", out)
	}

	it.Quote = nil
	it.App, it.Images = "x", []string{"https://pbs.twimg.com/media/one.jpg"}
	if out := renderCard(t, it); !strings.Contains(out, `data-src="https://pbs.twimg.com/media/one.jpg"`) {
		t.Errorf("a host that serves the browser directly is not proxied: %s", out)
	}
}

// The proxy exists to look like the page load it stands in for. doubanio
// answers 418 to a request with no Referer, and 418 again to Go's own user
// agent, so a picture that arrives with either missing never renders.
func TestImageRequestHeaders(t *testing.T) {
	u, err := parseImageURL("https://img9.doubanio.com/view/status/small/public/x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	req, err := imageRequest(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Referer"); !strings.Contains(got, "douban.com") {
		t.Errorf("Referer = %q, want a douban page", got)
	}
	ua := req.Header.Get("User-Agent")
	if !strings.HasPrefix(ua, "Mozilla/") {
		t.Errorf("User-Agent = %q, want a browser's", ua)
	}
	if strings.Contains(ua, "Go-http-client") {
		t.Errorf("User-Agent = %q; douban turns Go's own away", ua)
	}
}

// /img must not become an open proxy for anything on the internet.
func TestParseImageURL(t *testing.T) {
	ok := []string{
		"https://img9.doubanio.com/view/photo/m/public/p1.jpg",
		"https://img3.doubanio.com/view/status/small/public/x.jpg",
	}
	for _, u := range ok {
		if _, err := parseImageURL(u); err != nil {
			t.Errorf("parseImageURL(%q) = %v, want it admitted", u, err)
		}
	}
	bad := []string{
		"",
		"http://img9.doubanio.com/p.jpg", // plaintext
		"https://evil.com/p.jpg",         // another host
		"https://img9.doubanio.com.evil.com/p.jpg",            // suffix lookalike
		"https://evil.com/?x=https://img9.doubanio.com/p.jpg", // the real host in a query
		"file:///etc/passwd",
		"https://127.0.0.1/p.jpg",
	}
	for _, u := range bad {
		if _, err := parseImageURL(u); err == nil {
			t.Errorf("parseImageURL(%q) was admitted; it must be refused", u)
		}
	}
}

// A douban reshare embeds the discussion it passed along, so the box is not
// x's alone: the headline links out and the cover hides behind the same toggle.
func TestRenderCardEmbedNonX(t *testing.T) {
	it := core.Item{
		App: "douban", ID: "9457779094", Title: "。。。", Body: "。。。",
		Source: "竹子哟竹子✨ 转发了 生活组 的讨论", Age: "1h",
		URL: "https://www.douban.com/people/2298386/status/9457779094/",
		Quote: &core.Quote{
			Source: "好讨厌平台", Text: "我们在抖音上卖东西，平台抽佣8%，",
			URL:    "https://douc.cc/8wFqXD",
			Images: []string{"https://img9.doubanio.com/view/status/small/public/iLZUXK.jpg"},
		},
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `class="quote"`) {
		t.Fatalf("expected the embedded post block: %s", out)
	}
	if !strings.Contains(out, `href="https://douc.cc/8wFqXD"`) || !strings.Contains(out, "好讨厌平台 ↗") {
		t.Errorf("the discussion headline should link out: %s", out)
	}
	if !strings.Contains(out, `data-src="/img?u=https%3A%2F%2Fimg9.doubanio.com%2Fview%2Fstatus%2Fsmall%2Fpublic%2FiLZUXK.jpg"`) {
		t.Errorf("the embedded cover should be lazy-loaded inside the box: %s", out)
	}
	if !strings.Contains(out, `<button class="img"`) {
		t.Errorf("an embed-only image still needs the footer toggle: %s", out)
	}
}

func TestRenderCardQuoteVideo(t *testing.T) {
	it := core.Item{
		App: "x", ID: "3", Title: "see this", Body: "see this",
		Source: "@dave", URL: "https://x.com/dave/status/3",
		Quote: &core.Quote{
			Source: "@eve", Text: "watch",
			Video:  "https://video.twimg.com/ext_tw_video/30/quoted.mp4",
			Poster: "https://pbs.twimg.com/ext_tw_video_thumb/30/poster.jpg",
		},
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `src="https://video.twimg.com/ext_tw_video/30/quoted.mp4"`) {
		t.Fatalf("expected a player for the quoted post's video: %s", out)
	}
	// The shared playback controls appear even though the parent has no video.
	if !strings.Contains(out, `class="speed"`) || !strings.Contains(out, `class="rot"`) {
		t.Errorf("expected the video controls for a quote-only video: %s", out)
	}
	if !strings.Contains(out, `href="/dl?n=x-3-quote.mp4&u=https%3a%2f%2fvideo.twimg.com`) {
		t.Errorf("expected a /dl link for the quoted video: %s", out)
	}
	if !strings.Contains(out, "<span>keep</span>") {
		t.Errorf("the quoted video download should be labelled keep: %s", out)
	}
	// The parent has no video of its own, so no parent download link.
	if strings.Contains(out, "/dl?n=x-3.mp4") {
		t.Errorf("parent has no video; it must not offer one: %s", out)
	}
}

func TestRenderCardImages(t *testing.T) {
	it := core.Item{
		App: "x", ID: "60", Title: "two shots", Body: "two shots",
		Source: "@alice", URL: "https://x.com/alice/status/60",
		Images: []string{"https://pbs.twimg.com/media/one.jpg", "https://pbs.twimg.com/media/two.jpg"},
	}
	out := renderCard(t, it)
	if !strings.Contains(out, `<button class="img"`) {
		t.Fatalf("expected an image toggle in the footer: %s", out)
	}
	// Collapsed by default, and unopened images cost no bandwidth: the URL sits
	// in data-src until the toggle promotes it.
	if !strings.Contains(out, `<div class="imgbox hid">`) {
		t.Errorf("images should start collapsed: %s", out)
	}
	if strings.Contains(out, `<img src=`) {
		t.Errorf("images must not load before the toggle: %s", out)
	}
	for _, u := range it.Images {
		if !strings.Contains(out, `data-src="`+u+`"`) {
			t.Errorf("missing image %s: %s", u, out)
		}
	}

	// A quoted post's images get the same treatment, and one toggle covers both.
	it.Images = nil
	it.Quote = &core.Quote{Source: "@eve", Text: "look", Images: []string{"https://pbs.twimg.com/media/q.jpg"}}
	out = renderCard(t, it)
	if !strings.Contains(out, `<button class="img"`) {
		t.Errorf("a quote-only image should still offer the toggle: %s", out)
	}
	if !strings.Contains(out, `data-src="https://pbs.twimg.com/media/q.jpg"`) {
		t.Errorf("expected the quoted post's image: %s", out)
	}

	// No images anywhere -> no toggle, no image block.
	it.Quote = nil
	out = renderCard(t, it)
	if strings.Contains(out, `class="img"`) || strings.Contains(out, "imgbox") {
		t.Errorf("image toggle should be absent without images: %s", out)
	}
}

func TestSavedQuoteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.json")
	s := loadSaved(path)
	it := core.Item{
		App: "x", ID: "3", Title: "see this", Body: "see this", Source: "@dave",
		Quote: &core.Quote{Source: "@eve", Author: "Eve", Text: "quoted body", URL: "https://x.com/eve/status/30"},
	}
	if err := s.add(it, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := loadSaved(path).list(time.Now())[0]
	if got.Quote == nil || got.Quote.Source != "@eve" || got.Quote.Text != "quoted body" {
		t.Fatalf("quote lost in the saved round trip: %+v", got.Quote)
	}
}

func TestDownloadGuards(t *testing.T) {
	for _, bad := range []string{
		"", "http://video.twimg.com/a.mp4", "https://evil.com/a.mp4",
		"https://video.twimg.com.evil.com/a.mp4", "://nope",
	} {
		if _, err := parseVideoURL(bad); err == nil {
			t.Errorf("parseVideoURL(%q) should be rejected", bad)
		}
	}
	if _, err := parseVideoURL("https://video.twimg.com/amplify_video/1/vid/avc1/720x1280/x.mp4?tag=14"); err != nil {
		t.Errorf("real CDN URL rejected: %v", err)
	}
	if got := dlName("x-123.mp4"); got != "x-123.mp4" {
		t.Errorf("dlName = %q", got)
	}
	if got := dlName(`../../etc/passwd"`); got != "....etcpasswd" {
		t.Errorf("dlName should strip separators and quotes, got %q", got)
	}
	if got := dlName(""); got != "video.mp4" {
		t.Errorf("empty name should default, got %q", got)
	}
}

func TestSavedStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.json")
	s := loadSaved(path)
	if s.count() != 0 {
		t.Fatalf("fresh store should be empty, got %d", s.count())
	}

	it := core.Item{
		App: "x", ID: "1", Title: "a post", Body: "body text",
		Source: "@alice", URL: "https://x.com/alice/status/1",
		At:    time.Now().Add(-72 * time.Hour),
		Video: "https://video.twimg.com/a.mp4",
	}
	if err := s.add(it, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.add(it, time.Now()); err != nil { // saving twice must not duplicate
		t.Fatal(err)
	}
	if s.count() != 1 || !s.has("x", "1") {
		t.Fatalf("expected exactly one saved item, got %d", s.count())
	}

	// A second process (or a restart) sees the same list from disk.
	reloaded := loadSaved(path)
	if reloaded.count() != 1 || !reloaded.has("x", "1") {
		t.Fatalf("store did not persist: %d items", reloaded.count())
	}
	got := reloaded.list(time.Now())[0]
	if got.Title != "a post" || got.Video != "https://video.twimg.com/a.mp4" {
		t.Errorf("item lost fields in the round trip: %+v", got)
	}
	// Age is recomputed from the publish time, not frozen at save time.
	if got.Age != "3d ago" {
		t.Errorf("age = %q, want it recomputed to 3d ago", got.Age)
	}

	if err := reloaded.remove("x", "1"); err != nil {
		t.Fatal(err)
	}
	if reloaded.count() != 0 || loadSaved(path).count() != 0 {
		t.Fatal("unsave should persist too")
	}
}

// The saved list is ordered by when you saved things, newest first — the
// publish dates are all over the place and are not what you came back for.
func TestSavedListOrderedBySaveTime(t *testing.T) {
	s := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	// Saved oldest-first, but published newest-first: the two orders disagree.
	for i, id := range []string{"first", "second", "third"} {
		it := core.Item{App: "x", ID: id, Title: id, At: base.Add(time.Duration(-i) * time.Hour)}
		if err := s.add(it, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, it := range s.list(base.Add(time.Hour)) {
		got = append(got, it.ID)
	}
	want := []string{"third", "second", "first"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("saved order = %v, want most recently saved first %v", got, want)
	}
	// Re-saving an item moves it back to the top.
	if err := s.add(core.Item{App: "x", ID: "first", Title: "first"}, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if top := s.list(base.Add(2 * time.Hour))[0].ID; top != "first" {
		t.Errorf("re-saved item should lead the list, got %q", top)
	}
}

func TestSavedPageAndButton(t *testing.T) {
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	empty := renderSavedPage(t, store)
	if !strings.Contains(empty, "Nothing saved yet") {
		t.Fatalf("expected an empty-state note: %s", empty)
	}
	// The saved view never offers to mark things read.
	if strings.Contains(empty, "mark all read") {
		t.Fatal("mark-all-read must not appear in the saved view")
	}
	if !strings.Contains(empty, `data-savedview="true"`) {
		t.Fatal("saved view should flag itself so scroll-to-read stays off")
	}

	if err := store.add(core.Item{App: "reddit", ID: "7", Title: "kept", Source: "r/go"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	page := renderSavedPage(t, store)
	if !strings.Contains(page, "kept") {
		t.Fatalf("saved item missing from the saved view: %s", page)
	}
	// Cards in the saved view render as already saved.
	if !strings.Contains(page, `data-saved="1"`) || !strings.Contains(page, ">saved</button>") {
		t.Fatalf("expected an unsave-able button: %s", page)
	}

	// The feed view links to the saved list and shows its count.
	feed := renderPage(t, nil, []string{"x"}, nil, "following", "")
	if !strings.Contains(feed, `href="/?saved=1"`) {
		t.Fatalf("expected a saved link in the header: %s", feed)
	}
	if !strings.Contains(feed, `<span id="unreadn">`) {
		t.Fatal("unread count needs its own span so the saved link survives updates")
	}
}

func TestCardType(t *testing.T) {
	cases := []struct {
		name string
		it   core.Item
		want string
	}{
		{"plain post", core.Item{App: "reddit", Title: "hi", Body: "there"}, "text"},
		{"attached video", core.Item{App: "x", Video: "https://video.twimg.com/a.mp4"}, "video"},
		{"quoted video", core.Item{App: "x", Quote: &core.Quote{Video: "https://video.twimg.com/a.mp4"}}, "video"},
		{"redgifs link", core.Item{App: "reddit", URL: "https://www.redgifs.com/watch/elementaryhoarseflea"}, "video"},
		// The player is built in the browser from the link, but the item is a
		// video all the same.
		{"youtube link", core.Item{App: "folo", Body: "clip: https://youtu.be/aqz-KE-bpKQ"}, "video"},
		{"podcast", core.Item{App: "inoreader", Audio: "https://ex.com/ep.mp3"}, "audio"},
		{"both", core.Item{App: "x", Video: "https://video.twimg.com/a.mp4", Audio: "https://ex.com/ep.mp3"}, "video"},
	}
	for _, c := range cases {
		if got := buildCard(c.it, false, listClips).Type; got != c.want {
			t.Errorf("%s: type = %q, want %q", c.name, got, c.want)
		}
		if out := renderCard(t, c.it); !strings.Contains(out, `data-type="`+c.want+`"`) {
			t.Errorf("%s: card should carry its type: %s", c.name, out)
		}
	}
}

func TestCardFilters(t *testing.T) {
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	now := time.Now()
	items := []core.Item{
		{App: "reddit", ID: "1", Title: "read me", Source: "r/go"},
		{App: "reddit", ID: "2", Title: "clip", URL: "https://www.redgifs.com/watch/elementaryhoarseflea"},
		{App: "x", ID: "3", Title: "post", Source: "@vera"},
		{App: "inoreader", ID: "4", Title: "episode", Audio: "https://ex.com/ep.mp3"},
	}
	for _, it := range items {
		if err := store.add(it, now); err != nil {
			t.Fatal(err)
		}
	}
	page := renderSavedPage(t, store)
	for _, want := range []string{
		`<div class="filters" id="filters">`,
		`data-kind="app" data-key="reddit" data-on="0"`,
		`data-kind="app" data-key="x"`,
		`data-kind="app" data-key="inoreader"`,
		`data-kind="type" data-key="text"`,
		`data-kind="type" data-key="video"`,
		`data-kind="type" data-key="audio"`,
		// A pick is a page load, and on the saved list it stays on the saved list.
		`href="/?app=reddit&amp;saved=1"`,
		`href="/?saved=1&amp;type=video"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("saved page missing %s:\n%s", want, page)
		}
	}
	// The busiest source leads, and every chip states what it would leave.
	if i, j := strings.Index(page, `data-key="reddit"`), strings.Index(page, `data-key="x"`); i > j {
		t.Error("reddit has two saved items, so its chip should come first")
	}
	if !strings.Contains(page, `<span class="fn">2</span>`) {
		t.Errorf("expected a count on the reddit chip: %s", page)
	}

	// The feed is served from the backlog cache, so it arrives whole too and
	// gets the same cloud: same groups, same counts, same markup.
	feed := renderPage(t, items, []string{"reddit", "x"}, nil, "following", "")
	for _, want := range []string{
		`<div class="filters" id="filters">`,
		`data-kind="app" data-key="reddit" data-on="0"`,
		`data-kind="type" data-key="video"`,
		`data-kind="type" data-key="audio"`,
		`href="/?app=reddit"`,
		`href="/?type=video"`,
	} {
		if !strings.Contains(feed, want) {
			t.Errorf("feed page missing %s:\n%s", want, feed)
		}
	}
	// Nothing is on, so there is nothing to clear.
	if strings.Contains(feed, `id="fclear"`) {
		t.Errorf("the clear chip belongs to a page that picked something: %s", feed)
	}
}

// A pick is served, not hidden: the page carries that chip's items alone, and
// the chip counts still come from the whole list so each one says what picking
// it would bring.
func TestPickIsServedNotHidden(t *testing.T) {
	items := []core.Item{
		{App: "reddit", ID: "1", Title: "read me"},
		{App: "reddit", ID: "2", Title: "clip", Video: "https://video.twimg.com/a.mp4"},
		{App: "x", ID: "3", Title: "post"},
	}
	all := tallyItems(items)
	picked := selectItems(items, feedSel{Kind: "app", Key: "reddit"})
	if len(picked) != 2 || picked[0].ID != "1" || picked[1].ID != "2" {
		t.Fatalf("an app pick should leave that app's items: %+v", picked)
	}
	if v := selectItems(items, feedSel{Kind: "type", Key: "video"}); len(v) != 1 || v[0].ID != "2" {
		t.Fatalf("a type pick should leave that kind: %+v", v)
	}
	// For You is fetched, not sliced out of anything here.
	if f := selectItems(items, feedSel{Kind: "x", Key: "foryou"}); len(f) != 0 {
		t.Fatalf("the For You pick takes no cached items: %+v", f)
	}

	p := renderInput(t, pageInput{
		items: picked, total: len(picked), apps: []string{"reddit", "x"}, now: time.Now(),
		sel: feedSel{Kind: "app", Key: "reddit"}, tally: &all, query: url.Values{"app": {"reddit"}},
	})
	if !strings.Contains(p, `data-kind="app" data-key="reddit" data-on="1"`) {
		t.Errorf("the picked chip should render as on: %s", p)
	}
	// x still counts its one item, though this page shows none of it.
	if !strings.Contains(p, `class="fn ok">1<`) {
		t.Errorf("the chips count the whole list, not this page: %s", p)
	}
	// The header counts every source, or it would just repeat the picked chip
	// and nothing on the page would state the whole.
	if !strings.Contains(p, `<span id="unreadn">3</span>`) {
		t.Errorf("the header should count all three, not the two picked: %s", p)
	}
	// The chip that is on links back to everything, as does the clear chip.
	if !strings.Contains(p, `href="/" data-kind="app" data-key="reddit" data-on="1"`) {
		t.Errorf("tapping the picked chip should put the whole list back: %s", p)
	}
	if !strings.Contains(p, `class="fchip fclear" href="/"`) {
		t.Errorf("expected a clear chip: %s", p)
	}
	// One at a time: picking another app replaces this pick rather than adding to it.
	if !strings.Contains(p, `href="/?app=x" data-kind="app" data-key="x"`) {
		t.Errorf("another chip should replace the pick, not stack with it: %s", p)
	}
	// Nothing left after a pick says so, rather than claiming inbox zero.
	empty := renderInput(t, pageInput{
		items: nil, total: 0, apps: []string{"reddit", "x"}, now: time.Now(),
		sel: feedSel{Kind: "app", Key: "reddit"}, tally: &all, query: url.Values{"app": {"reddit"}},
	})
	if !strings.Contains(empty, "Nothing unread from that chip.") {
		t.Errorf("an emptied pick is not inbox zero: %s", empty)
	}
}

// One chip at a time, so the request names one and the links swap it rather
// than piling params up. For You wins the tie: it is the one pick that is not a
// slice of the backlog.
func TestChipQueryIsOnePick(t *testing.T) {
	sels := []struct {
		q    string
		want feedSel
	}{
		{"", feedSel{}},
		{"app=reddit", feedSel{Kind: "app", Key: "reddit"}},
		{"type=video", feedSel{Kind: "type", Key: "video"}},
		{"type=nonsense", feedSel{}},
		{"x=foryou", feedSel{Kind: "x", Key: "foryou"}},
		{"x=following", feedSel{}},
		{"x=foryou&app=reddit&type=video", feedSel{Kind: "x", Key: "foryou"}},
		{"app=reddit&type=video", feedSel{Kind: "app", Key: "reddit"}},
	}
	for _, c := range sels {
		q, err := url.ParseQuery(c.q)
		if err != nil {
			t.Fatal(err)
		}
		if got := parseSel(q); got != c.want {
			t.Errorf("parseSel(%q) = %+v, want %+v", c.q, got, c.want)
		}
	}

	hrefs := []struct {
		q    string
		sel  feedSel
		want string
	}{
		{"app=reddit", feedSel{Kind: "app", Key: "x"}, "/?app=x"},
		{"type=video", feedSel{Kind: "app", Key: "x"}, "/?app=x"},
		{"app=reddit", feedSel{}, "/"},
		{"x=foryou", feedSel{}, "/"},
		// The sort order and the saved view are not picks, so they ride along.
		{"order=desc&app=x", feedSel{Kind: "type", Key: "audio"}, "/?order=desc&type=audio"},
		{"saved=1", feedSel{Kind: "app", Key: "x"}, "/?app=x&saved=1"},
	}
	for _, c := range hrefs {
		q, err := url.ParseQuery(c.q)
		if err != nil {
			t.Fatal(err)
		}
		if got := chipHref(q, c.sel); got != c.want {
			t.Errorf("chipHref(%q, %+v) = %q, want %q", c.q, c.sel, got, c.want)
		}
	}
}

// The header's far end says which way the feed runs and turns it around, over
// the whole backlog rather than the window of it this page carries — so it is a
// link the server answers, and the order it lands on is always spelled out.
func TestOrderToggle(t *testing.T) {
	hrefs := []struct {
		q    string
		asc  bool
		want string
	}{
		{"", true, "/?order=desc"},
		{"", false, "/?order=asc"},
		{"order=asc", true, "/?order=desc"},
		{"order=desc", false, "/?order=asc"},
		// A pick rides along: turning the feed around should not clear the chip.
		{"order=desc&app=reddit", false, "/?app=reddit&order=asc"},
		{"x=foryou", true, "/?order=desc&x=foryou"},
	}
	for _, c := range hrefs {
		q, err := url.ParseQuery(c.q)
		if err != nil {
			t.Fatal(err)
		}
		if got := orderHref(q, c.asc); got != c.want {
			t.Errorf("orderHref(%q, asc=%t) = %q, want %q", c.q, c.asc, got, c.want)
		}
	}

	feed := func(asc bool) string {
		return renderInput(t, pageInput{
			items: []core.Item{{App: "x", ID: "1", Title: "a"}}, total: 1,
			apps: []string{"x"}, now: time.Now(), asc: asc, query: url.Values{},
		})
	}
	// The word is the order the page is already in, not the one a tap would bring.
	if p := feed(true); !strings.Contains(p, `href="/?order=desc"`) || !strings.Contains(p, `>oldest</a>`) {
		t.Errorf("oldest-first should say so and offer newest: %s", p)
	}
	if p := feed(false); !strings.Contains(p, `href="/?order=asc"`) || !strings.Contains(p, `>newest</a>`) {
		t.Errorf("newest-first should say so and offer oldest: %s", p)
	}
	// The saved and blocked lists are ordered by when you saved or blocked
	// something, which the toggle has no say over, so it isn't drawn there.
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	if p := renderSavedPage(t, store); strings.Contains(p, `id="sortlink"`) {
		t.Errorf("the saved list has an order of its own: %s", p)
	}
	// Nor with nothing logged in, where there is no feed to turn around.
	if p := renderPage(t, nil, nil, nil, "following", ""); strings.Contains(p, `id="sortlink"`) {
		t.Errorf("no apps, no feed, no toggle: %s", p)
	}
}

// Reading is scrolling past a card, so a page that arrives already scrolled -- a
// Firefox reload restoring the offset, a back button -- must not count the whole
// list above it as read.
func TestScrollMarkIgnoresRestoredScroll(t *testing.T) {
	feed := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, []string{"x"}, nil, "following", "")
	if !strings.Contains(feed, "history.scrollRestoration = 'manual'") {
		t.Error("where the feed starts is ours to set, not the browser's to restore")
	}
	if !strings.Contains(feed, "if(el.dataset.behind) return;") {
		t.Error("a card that was never on screen cannot have been scrolled past")
	}
	if !strings.Contains(feed, "if(en.isIntersecting){ delete el.dataset.behind; return; }") {
		t.Error("a card that comes into view is readable again from there on")
	}
	if !strings.Contains(feed, "navigator.sendBeacon('/mark', fd)") {
		t.Error("a refresh inside the queue's window must not lose the reads")
	}
}

// The deck gets the same chips as the feed, and nothing client-side is left to
// hide cards with: the page it was served is already the pick.
func TestDeckChipsAreLinksToo(t *testing.T) {
	page := renderSwipePage(t,
		[]core.Item{
			{App: "x", ID: "1", Title: "post"},
			{App: "reddit", ID: "2", Title: "clip", Video: "https://video.twimg.com/a.mp4"},
		}, "x", "reddit")
	if !strings.Contains(page, `id="filters"`) {
		t.Fatalf("the deck gets the same chips as the feed:\n%s", page)
	}
	if strings.Contains(page, "fout") {
		t.Error("nothing filters cards in the browser any more")
	}
	if strings.Contains(page, "onFilterChange") {
		t.Error("the deck deals from the page it was served")
	}
	// Every card on the page is a card in the deck, so the end of it is the end.
	if !strings.Contains(page, "endEl.classList.toggle('hid', !done)") {
		t.Error("the end note should follow the end of the deck")
	}
}

// Mark-all clears what is on screen, which with a chip on is that chip's items
// and nothing else — the page carries no others to clear by accident.
func TestMarkAllClearsThePage(t *testing.T) {
	page := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}}, []string{"x"}, nil, "following", "")
	if !strings.Contains(page, `document.querySelectorAll('article.card:not(.read)').forEach`) {
		t.Error("mark-all should clear every unread card on the page")
	}
	// A windowed page always has more behind it, chip or no chip, and the chip
	// rides along in the URL, so the reload comes back to the same pick.
	if !strings.Contains(page, `if (markAllBtn.dataset.more === '1') {`) {
		t.Error("a windowed page should reload for the next window unconditionally")
	}
}

// A group whose chips would all stay lit filters nothing, so it isn't drawn —
// and a saved list of one kind from one app gets no cloud at all.
func TestCardFiltersSkipPointlessGroups(t *testing.T) {
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	now := time.Now()
	for _, it := range []core.Item{
		{App: "reddit", ID: "1", Title: "one", Source: "r/go"},
		{App: "reddit", ID: "2", Title: "two", Source: "r/go"},
	} {
		if err := store.add(it, now); err != nil {
			t.Fatal(err)
		}
	}
	if page := renderSavedPage(t, store); strings.Contains(page, `id="filters"`) {
		t.Errorf("one source, one type: nothing to filter by:\n%s", page)
	}

	if err := store.add(core.Item{App: "reddit", ID: "3", Title: "clip", Video: "https://video.twimg.com/a.mp4"}, now); err != nil {
		t.Fatal(err)
	}
	page := renderSavedPage(t, store)
	if !strings.Contains(page, `data-kind="type"`) {
		t.Errorf("a mixed-type list should offer the type group: %s", page)
	}
	if strings.Contains(page, `data-kind="app"`) {
		t.Errorf("everything is from reddit, so the source group is noise: %s", page)
	}
}

// Where you left off is kept with the saved item, so it survives the page, a
// restart, and the trip to another device through the synced store.
func TestSavedPlaybackPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.json")
	store := loadSaved(path)
	ep := core.Item{App: "inoreader", ID: "9", Title: "episode", Audio: "https://ex.com/ep.mp3"}
	if err := store.add(ep, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Nothing to resume until something reports a position.
	if page := renderSavedPage(t, store); strings.Contains(page, "data-pos=") {
		t.Errorf("a fresh save has no position: %s", page)
	}

	kept, err := store.setPos("inoreader", "9", "https://ex.com/ep.mp3", 615.5)
	if err != nil || !kept {
		t.Fatalf("setPos = %v, %v; want it kept", kept, err)
	}
	page := renderSavedPage(t, store)
	if !strings.Contains(page, `data-pos="615.5" data-pos-key="https://ex.com/ep.mp3"`) {
		t.Errorf("card should carry the resume point: %s", page)
	}

	// It is on disk, not just in this process.
	reloaded := loadSaved(path)
	if at, src := reloaded.pos("inoreader", "9"); at != 615.5 || src != "https://ex.com/ep.mp3" {
		t.Errorf("after reload: %v %q, want the stored position", at, src)
	}

	// Re-saving refreshes the item, not where you got to in it.
	if err := reloaded.add(ep, time.Now()); err != nil {
		t.Fatal(err)
	}
	if at, _ := reloaded.pos("inoreader", "9"); at != 615.5 {
		t.Errorf("re-saving dropped the position (%v)", at)
	}

	// Finishing (or starting over) forgets it, and the attribute goes with it.
	if _, err := reloaded.setPos("inoreader", "9", "https://ex.com/ep.mp3", 0); err != nil {
		t.Fatal(err)
	}
	if at, src := reloaded.pos("inoreader", "9"); at != 0 || src != "" {
		t.Errorf("zero should clear the position, got %v %q", at, src)
	}
	if page := renderSavedPage(t, reloaded); strings.Contains(page, "data-pos=") {
		t.Errorf("a cleared position should not render: %s", page)
	}

	// An item nobody saved has nowhere to keep one.
	if kept, err := reloaded.setPos("x", "404", "https://video.twimg.com/a.mp4", 30); kept || err != nil {
		t.Errorf("setPos on an unsaved item = %v, %v; want it dropped quietly", kept, err)
	}
}

// A YouTube embed's position is keyed "yt:<id>", which is not a URL — and the
// attribute must not be named as if it were one. html/template treats any
// attribute ending in "src" as a URL context and rewrites anything with an
// unknown scheme to #ZgotmplZ, which silently broke resuming for every embed.
func TestSavedPositionKeySurvivesTheTemplate(t *testing.T) {
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	if err := store.add(core.Item{
		App: "inoreader", ID: "77", Title: "an episode with a clip",
		Body: "watch: https://www.youtube.com/watch?v=aqz-KE-bpKQ",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.setPos("inoreader", "77", "yt:aqz-KE-bpKQ", 615); err != nil {
		t.Fatal(err)
	}
	page := renderSavedPage(t, store)
	if strings.Contains(page, "ZgotmplZ") {
		t.Fatalf("the position key was sanitized away: %s", page)
	}
	if !strings.Contains(page, `data-pos="615" data-pos-key="yt:aqz-KE-bpKQ"`) {
		t.Fatalf("expected the embed's resume key intact: %s", page)
	}
}

// The feed shows it too: a card starred earlier resumes where it was left.
func TestFeedCardCarriesSavedPosition(t *testing.T) {
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	it := core.Item{App: "x", ID: "50", Title: "clip", Video: "https://video.twimg.com/a.mp4"}
	if err := store.add(it, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.setPos("x", "50", "https://video.twimg.com/a.mp4", 42); err != nil {
		t.Fatal(err)
	}
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := loader.load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	in := pageInput{items: []core.Item{it}, apps: []string{"x"}, now: time.Now(), saved: store}
	if err := tmpl.Execute(&b, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `data-pos="42"`) {
		t.Errorf("a saved item in the feed should resume too: %s", b.String())
	}
}

func TestPosHandler(t *testing.T) {
	store := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	if err := store.add(core.Item{App: "x", ID: "50", Title: "clip"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	post := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/pos", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handlePos(rec, r, store)
		return rec
	}

	rec := post(url.Values{"app": {"x"}, "id": {"50"}, "src": {"https://video.twimg.com/a.mp4"}, "secs": {"42.5"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("got %d %s, want the position kept", rec.Code, rec.Body.String())
	}
	if at, src := store.pos("x", "50"); at != 42.5 || src != "https://video.twimg.com/a.mp4" {
		t.Errorf("stored %v %q", at, src)
	}

	// Reported from the feed for something never starred: accepted, not stored.
	rec = post(url.Values{"app": {"reddit"}, "id": {"nope"}, "src": {"https://x/a.mp4"}, "secs": {"9"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Errorf("an unsaved item should be answered, not an error: %d %s", rec.Code, rec.Body.String())
	}

	// Junk is refused rather than written into the store.
	for _, bad := range []url.Values{
		{"id": {"50"}, "secs": {"1"}},                              // no app
		{"app": {"x"}, "secs": {"1"}},                              // no id
		{"app": {"x"}, "id": {"50"}, "secs": {"soon"}},             // not a number
		{"app": {"x"}, "id": {"50"}, "secs": {"-5"}},               // before the start
		{"app": {"x"}, "id": {"50"}, "secs": {"NaN"}},              // not a position
		{"app": {"x"}, "id": {"50"}, "secs": {"Inf"}},              // nor is this
		{"app": {"x"}, "id": {"50"}, "secs": {"1"}, "src": {long}}, // an unbounded src
	} {
		if rec := post(bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%v gave %d, want 400", bad, rec.Code)
		}
	}
	if at, _ := store.pos("x", "50"); at != 42.5 {
		t.Errorf("a refused report changed the stored position to %v", at)
	}
}

var long = strings.Repeat("u", maxPosSrc+1)

// Every logged-in service gets a chip, using the same label its cards do:
// there is one row of sources now, not a row of chips and a row of dots.
func TestEveryLoggedInServiceGetsAChip(t *testing.T) {
	p := renderPage(t, nil, []string{"inoreader", "reddit", "douban", "folo"}, nil, "following", "")
	for _, app := range []string{"inoreader", "reddit", "douban", "folo"} {
		if !strings.Contains(p, `data-kind="app" data-key="`+app+`"`) {
			t.Errorf("expected a chip for %s: %s", app, p)
		}
		if !strings.Contains(p, `>`+appLabel(app)+`<`) {
			t.Errorf("expected %s's chip to carry its card label %q", app, appLabel(app))
		}
	}
	if strings.Contains(p, `class="health"`) || strings.Contains(p, "hdot") {
		t.Error("the separate row of status dots should be gone")
	}
}

func TestRenderedItemsEviction(t *testing.T) {
	r := newRenderedItems()
	r.put([]core.Item{{App: "x", ID: "1", Title: "one"}})
	if _, ok := r.get("x", "1"); !ok {
		t.Fatal("rendered item should be retrievable")
	}
	if _, ok := r.get("x", "nope"); ok {
		t.Fatal("unknown item should miss")
	}
	// Overflowing the cap drops the oldest entries, not the newest.
	many := make([]core.Item, 0, maxRendered+10)
	for i := 0; i < maxRendered+10; i++ {
		many = append(many, core.Item{App: "reddit", ID: strconv.Itoa(i)})
	}
	r.put(many)
	if _, ok := r.get("x", "1"); ok {
		t.Error("oldest entry should have been evicted")
	}
	if _, ok := r.get("reddit", strconv.Itoa(maxRendered+9)); !ok {
		t.Error("newest entry should survive")
	}
}

// A source that arrives already sorted into streams — reddit's subreddits,
// inoreader's feeds — gets a second row of chips under the source row, which
// narrows the pick it sits beneath rather than replacing it.
func TestSubcategoryRow(t *testing.T) {
	items := []core.Item{
		{App: "reddit", ID: "1", Source: "r/golang", Title: "a"},
		{App: "reddit", ID: "2", Source: "r/ChatGPTCoding", Title: "b"},
		{App: "reddit", ID: "3", Source: "r/ChatGPTCoding", Title: "c"},
		{App: "reddit", ID: "4", Source: "r/rust", Title: "d"},
		{App: "x", ID: "5", Source: "@someone", Title: "e"},
	}
	tally := tallyItems(items)
	q := url.Values{"app": {"reddit"}}

	// Nothing to draw until a source that has them is the chip that is on.
	if got := subChips(tally, feedSel{}, url.Values{}); got != nil {
		t.Errorf("no pick, no second row: %+v", got)
	}
	if got := subChips(tally, feedSel{Kind: "app", Key: "x"}, url.Values{"app": {"x"}}); got != nil {
		t.Errorf("x's sources are people, not streams to pick between: %+v", got)
	}
	if got := subChips(tally, feedSel{Kind: "type", Key: "video"}, url.Values{}); got != nil {
		t.Errorf("a type pick has no source to break down: %+v", got)
	}

	chips := subChips(tally, feedSel{Kind: "app", Key: "reddit"}, q)
	// Busiest first, alphabetical between ties, so the ones worth a tap are the
	// ones that survive the row being trimmed to a single line.
	want := []struct {
		key   string
		count int
	}{{"r/ChatGPTCoding", 2}, {"r/golang", 1}, {"r/rust", 1}}
	if len(chips) != len(want) {
		t.Fatalf("got %d chips, want %d: %+v", len(chips), len(want), chips)
	}
	for i, w := range want {
		if chips[i].Key != w.key || chips[i].Count != w.count {
			t.Errorf("chip %d = %s/%d, want %s/%d", i, chips[i].Key, chips[i].Count, w.key, w.count)
		}
		if chips[i].On {
			t.Errorf("nothing is picked yet, so %s should be off", chips[i].Key)
		}
	}
	// The link stacks on the source pick rather than replacing it.
	if chips[0].Href != "/?app=reddit&sub=r%2FChatGPTCoding" {
		t.Errorf("subcategory link should keep the source: %s", chips[0].Href)
	}

	// With one on, it is the way back to the whole source.
	on := subChips(tally, feedSel{Kind: "app", Key: "reddit", Sub: "r/golang"}, url.Values{"app": {"reddit"}, "sub": {"r/golang"}})
	for _, c := range on {
		if (c.Key == "r/golang") != c.On {
			t.Errorf("%s: On = %t", c.Key, c.On)
		}
		if c.On && c.Href != "/?app=reddit" {
			t.Errorf("tapping the one that is on hands the source back: %s", c.Href)
		}
	}
	// Its count is still the whole subcategory's, not what the narrowed page
	// carries, so the numbers hold still across a pick.
	if on[0].Count != 2 {
		t.Errorf("counts come from before the pick: %+v", on[0])
	}

	// One subcategory narrows nothing, so no row.
	lone := tallyItems([]core.Item{{App: "reddit", ID: "1", Source: "r/golang"}})
	if got := subChips(lone, feedSel{Kind: "app", Key: "reddit"}, q); got != nil {
		t.Errorf("a row of one is no choice at all: %+v", got)
	}
}

// The second layer is a query param of its own, read and written alongside the
// source pick it hangs off.
func TestSubcategoryQuery(t *testing.T) {
	sels := []struct {
		q    string
		want feedSel
	}{
		{"app=reddit&sub=r%2Fgolang", feedSel{Kind: "app", Key: "reddit", Sub: "r/golang"}},
		{"app=inoreader&sub=Hacker+News", feedSel{Kind: "app", Key: "inoreader", Sub: "Hacker News"}},
		// A source with no streams of its own has nothing for it to mean.
		{"app=x&sub=r%2Fgolang", feedSel{Kind: "app", Key: "x"}},
		{"sub=r%2Fgolang", feedSel{}},
		{"type=video&sub=r%2Fgolang", feedSel{Kind: "type", Key: "video"}},
	}
	for _, c := range sels {
		q, err := url.ParseQuery(c.q)
		if err != nil {
			t.Fatal(err)
		}
		if got := parseSel(q); got != c.want {
			t.Errorf("parseSel(%q) = %+v, want %+v", c.q, got, c.want)
		}
	}

	// Picking anything else drops it: it belonged to the source that is going.
	q := url.Values{"app": {"reddit"}, "sub": {"r/golang"}, "order": {"desc"}}
	if got := chipHref(q, feedSel{Kind: "app", Key: "inoreader"}); got != "/?app=inoreader&order=desc" {
		t.Errorf("another source, another set of streams: %s", got)
	}
	if got := chipHref(q, feedSel{}); got != "/?order=desc" {
		t.Errorf("clear should clear both layers: %s", got)
	}
	// ...but turning the feed around keeps it, the way it keeps the source.
	if got := orderHref(q, true); got != "/?app=reddit&order=desc&sub=r%2Fgolang" {
		t.Errorf("the order toggle is not a pick: %s", got)
	}

	// And it narrows the list.
	items := []core.Item{
		{App: "reddit", ID: "1", Source: "r/golang"},
		{App: "reddit", ID: "2", Source: "r/rust"},
		{App: "x", ID: "3", Source: "r/golang"}, // same name, another service
	}
	got := selectItems(items, feedSel{Kind: "app", Key: "reddit", Sub: "r/golang"})
	if len(got) != 1 || got[0].ID != "1" {
		t.Errorf("a subcategory only ever narrows its own source: %+v", got)
	}
}

// The row renders under the source row it narrows, with every chip sent and the
// trimming left to the page — only the laid-out row knows how many fit.
func TestSubcategoryRowRenders(t *testing.T) {
	items := []core.Item{
		{App: "reddit", ID: "1", Source: "r/golang", Title: "a"},
		{App: "reddit", ID: "2", Source: "r/ChatGPTCoding", Title: "b"},
	}
	page := renderInput(t, pageInput{
		items: items, total: len(items), apps: []string{"reddit"}, now: time.Now(),
		sel: feedSel{Kind: "app", Key: "reddit"}, query: url.Values{"app": {"reddit"}},
	})
	subs := strings.Index(page, `id="subs"`)
	if subs < 0 {
		t.Fatalf("no second row:\n%s", page)
	}
	if filters := strings.Index(page, `id="filters"`); filters > subs {
		t.Error("the second row belongs under the row it narrows")
	}
	for _, want := range []string{`data-kind="sub"`, `data-key="r/golang"`, `id="fmore"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s:\n%s", want, page)
		}
	}
	// A card says which stream it came from, so reading takes that chip's number
	// down with the rest.
	if !strings.Contains(page, `data-sub="r/golang"`) {
		t.Errorf("cards should carry their subcategory:\n%s", page)
	}

	// Without a source pick there is no second row to draw.
	whole := renderInput(t, pageInput{
		items: items, total: len(items), apps: []string{"reddit"}, now: time.Now(), query: url.Values{},
	})
	if strings.Contains(whole, `id="subs"`) {
		t.Errorf("the whole list has no one source to break down:\n%s", whole)
	}
}

// An empty pick still draws the chips, and they come first: the note explains
// why the page is empty, and the row is what you act on to fix it.
func TestEmptyNoteSitsUnderTheChips(t *testing.T) {
	page := renderInput(t, pageInput{
		total: 0, apps: []string{"reddit", "x"}, now: time.Now(),
		sel: feedSel{Kind: "app", Key: "reddit"}, query: url.Values{"app": {"reddit"}},
	})
	chips, note := strings.Index(page, `id="filters"`), strings.Index(page, "Nothing unread from that chip.")
	if chips < 0 || note < 0 {
		t.Fatalf("want both a chip row and a note:\n%s", page)
	}
	if note < chips {
		t.Error("the note belongs under the chips, not over them")
	}
}

// The trimmed row opens and closes again: the button that showed the rest is
// the one that puts them back.
func TestSubcategoryRowFoldsBothWays(t *testing.T) {
	page := renderInput(t, pageInput{
		items: []core.Item{
			{App: "reddit", ID: "1", Source: "r/golang", Title: "a"},
			{App: "reddit", ID: "2", Source: "r/rust", Title: "b"},
		},
		total: 2, apps: []string{"reddit"}, now: time.Now(),
		sel: feedSel{Kind: "app", Key: "reddit"}, query: url.Values{"app": {"reddit"}},
	})
	if !strings.Contains(page, `id="fmore"`) {
		t.Fatalf("no fold control:\n%s", page)
	}
	for _, want := range []string{`more.textContent = 'less'`, `'+' + (chips.length - cut) + ' more'`} {
		if !strings.Contains(page, want) {
			t.Errorf("the button should say both things; missing %s", want)
		}
	}
}

// A page narrowed to one stream cannot be recounted into a number about the
// whole source, so the source chip walks down from what the server sent by
// what has been read since — otherwise reading moves the header and the
// subcategory chip and leaves the source chip stuck.
func TestSourceChipFollowsReadingUnderASubcategory(t *testing.T) {
	items := []core.Item{
		{App: "reddit", ID: "1", Source: "r/golang", Title: "a"},
		{App: "reddit", ID: "2", Source: "r/golang", Title: "b"},
	}
	deep := tallyItems(append(items,
		core.Item{App: "reddit", ID: "3", Source: "r/rust", Title: "c"},
		core.Item{App: "reddit", ID: "4", Source: "r/rust", Title: "d"},
	))
	page := renderInput(t, pageInput{
		items: items, total: len(items), apps: []string{"reddit"}, now: time.Now(),
		sel:   feedSel{Kind: "app", Key: "reddit", Sub: "r/golang"},
		tally: &deep, query: url.Values{"app": {"reddit"}, "sub": {"r/golang"}},
	})
	// The source chip states the whole source, not the stream on the page.
	if !strings.Contains(page, `data-key="reddit" data-on="1" title="reddit: live"><span class="chip" style="background:#ff6b33">rdt</span><span class="fn ok">4</span>`) {
		t.Errorf("the source chip counts the whole source:\n%s", page)
	}
	if !strings.Contains(page, `data-sub="r/golang"`) {
		t.Errorf("the page should know which stream it is:\n%s", page)
	}
	for _, want := range []string{
		"chip.dataset.n0 = n.textContent", // the baseline, stashed before anything writes over it
		"article.card.read",               // ...and what comes off it
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s from the recount", want)
		}
	}
}
