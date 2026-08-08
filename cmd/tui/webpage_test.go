package main

import (
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// renderPage / renderCard execute the embedded page template the way handleAll
// does, so the tests cover the real markup.
func renderPage(t *testing.T, items []core.Item, apps, failed []string, asc bool, xTab, warn string) string {
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
	if err := tmpl.Execute(&b, buildPageData(items, apps, failed, time.Now(), asc, xTab, warn)); err != nil {
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
	if err := tmpl.ExecuteTemplate(&b, "card", buildCard(it)); err != nil {
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
	if !strings.Contains(out, "class=\"expand\"") {
		t.Fatal("expand should appear for long content")
	}
	if !strings.Contains(out, "class=\"full hid\"") {
		t.Fatal("expected a hidden full-content panel")
	}
}

func TestRenderPageMarkAll(t *testing.T) {
	with := renderPage(t, []core.Item{{App: "x", ID: "1", Title: "a"}, {App: "reddit", ID: "2", Title: "b"}}, []string{"x", "reddit"}, nil, true, "following", "")
	if !strings.Contains(with, "mark all read") {
		t.Fatal("expected mark-all-read button when there are items")
	}
	without := renderPage(t, nil, []string{"x"}, nil, true, "following", "")
	if strings.Contains(without, "mark all read") {
		t.Fatal("mark-all-read should be absent when the feed is empty")
	}
	// The 'all' wordmark is removed.
	if strings.Contains(with, "<h1>all</h1>") {
		t.Fatal("expected the 'all' title to be removed")
	}
}

func TestRenderPageWarn(t *testing.T) {
	p := renderPage(t, nil, []string{"inoreader"}, []string{"inoreader"}, true, "following", "Inoreader session is stale — re-run `tui inoreader --auth`.")
	if !strings.Contains(p, "session is stale") || !strings.Contains(p, `class="warn"`) {
		t.Fatalf("expected a warn banner: %s", p)
	}
	// No warning message → no banner.
	p2 := renderPage(t, nil, []string{"x"}, nil, true, "following", "")
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
	p := renderPage(t, nil, nil, nil, true, "following", "")
	if !strings.Contains(p, "No reader app is logged in") {
		t.Fatal("expected login note, got: " + p)
	}
	// Oldest-first is the default: the sortbar highlights 'oldest'.
	if !strings.Contains(p, "sortbar") || !strings.Contains(p, ">oldest</span>") {
		t.Fatal("expected an oldest-first default sortbar: " + p)
	}
	// Authed but zero items: inbox zero.
	p2 := renderPage(t, nil, []string{"x"}, nil, true, "following", "")
	if !strings.Contains(p2, "Inbox zero") {
		t.Fatal("expected inbox-zero message")
	}
}

func TestRenderPageHealthDots(t *testing.T) {
	// Every logged-in service gets a labeled dot: green when it loaded, red
	// when its fetch failed. The dots replace the refresh button.
	p := renderPage(t, nil, []string{"x", "reddit"}, []string{"reddit"}, true, "following", "")
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
	if strings.Contains(renderPage(t, nil, nil, nil, true, "following", ""), `class="health"`) {
		t.Fatal("health strip should be absent with no logged-in services")
	}
}
