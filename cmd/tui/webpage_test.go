package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// renderPage / renderCard execute the embedded page template the way handleAll
// does, so the tests cover the real markup.
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
	in := pageInput{items: items, apps: apps, failed: failed, now: time.Now(), xTab: xTab, warn: warn}
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
	in := pageInput{items: store.list(now), now: now, saved: store, savedView: true}
	if err := tmpl.Execute(&b, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// renderSwipePage renders the feed the way --swipe serves it.
func renderSwipePage(t *testing.T, items []core.Item) string {
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
	in := pageInput{items: items, apps: []string{"x", "reddit"}, now: time.Now(), xTab: "following", swipe: true}
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
	// The end of the deck still offers a way back into it.
	if !strings.Contains(deck, `id="deckEnd"`) || !strings.Contains(deck, `id="deckBack"`) {
		t.Errorf("expected an end state with a way back: %s", deck)
	}
	feed := renderPage(t, items, []string{"x", "reddit"}, nil, "following", "")
	if strings.Contains(feed, `class="deck"`) || strings.Contains(feed, `id="deckEnd"`) {
		t.Errorf("the scrolling feed should render no deck: %s", feed)
	}
	if !strings.Contains(feed, `data-swipe="false"`) {
		t.Errorf("the feed should tell its script swipe mode is off: %s", feed)
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

func TestRenderPageHealthDots(t *testing.T) {
	// Every logged-in service gets a labeled dot: green when it loaded, red
	// when its fetch failed. The dots replace the refresh button.
	p := renderPage(t, nil, []string{"x", "reddit"}, []string{"reddit"}, "following", "")
	if !strings.Contains(p, `title="x: live"`) || !strings.Contains(p, `hdot ok`) {
		t.Fatal("expected a green dot for the healthy service")
	}
	if !strings.Contains(p, `title="reddit: failed to load"`) || !strings.Contains(p, `hdot bad`) {
		t.Fatal("expected a red dot for the failed service")
	}
	if strings.Contains(p, `class="refresh"`) {
		t.Fatal("refresh button should be replaced by the health dots")
	}
	// No logged-in services: no health strip at all.
	if strings.Contains(renderPage(t, nil, nil, nil, "following", ""), `class="health"`) {
		t.Fatal("health strip should be absent with no logged-in services")
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
	if !strings.Contains(out, `href="/dl?n=x-50.mp4&u=https%3a%2f%2fvideo.twimg.com`) {
		t.Fatalf("expected a /dl download link: %s", out)
	}
	// Download is 'keep', so it doesn't read as a second copy of the star button.
	if !strings.Contains(out, "<span>keep</span>") || strings.Count(out, ">save</button>") != 1 {
		t.Fatalf("the download link must not also be labelled save: %s", out)
	}
	// No video -> no player, no controls.
	it.Video, it.Poster = "", ""
	out = renderCard(t, it)
	if strings.Contains(out, "<video") || strings.Contains(out, `class="speed"`) || strings.Contains(out, "/dl?") {
		t.Fatal("player and controls should be absent without a video")
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

func TestHealthLabelsAreShort(t *testing.T) {
	p := renderPage(t, nil, []string{"inoreader", "reddit", "douban", "folo"}, nil, "following", "")
	for _, want := range []string{">in<", ">rd<", ">db<", ">fo<"} {
		if !strings.Contains(p, want) {
			t.Errorf("expected two-letter health label %q: %s", want, p)
		}
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
