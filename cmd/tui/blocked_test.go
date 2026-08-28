package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// newTestBlocker is a block list on temp files, with the given keywords already
// in it.
func newTestBlocker(t *testing.T, words ...string) *blocker {
	t.Helper()
	dir := t.TempDir()
	b := loadBlocker(filepath.Join(dir, "keywords.json"), filepath.Join(dir, "blocked.json"))
	if len(words) > 0 {
		if err := b.setKeywords(words); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// renderBlockedPage renders the blocked list the way the ?blocked=1 route does.
func renderBlockedPage(t *testing.T, b *blocker) string {
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
	items := b.list(now)
	var out strings.Builder
	in := pageInput{
		items: items, total: len(items), now: now, block: b, blockedView: true,
		query: url.Values{"blocked": {"1"}}, // the route's own param, which the chips keep
	}
	if err := tmpl.Execute(&out, buildPageData(in)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The textarea is the whole list, so what it means has to be exactly what you
// can see in it: a line is a keyword, a blank line is nothing, and a repeat is
// still one keyword however it was typed.
func TestParseKeywords(t *testing.T) {
	got := parseKeywords(" crypto \n\n NFT\r\nnft\n\t\nCRYPTO\n国足\n")
	want := []string{"crypto", "NFT", "国足"}
	if len(got) != len(want) {
		t.Fatalf("parseKeywords = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseKeywords = %q, want %q", got, want)
		}
	}
	if got := parseKeywords("   \n\n"); got != nil {
		t.Fatalf("a textarea of whitespace gave %q, want no keywords", got)
	}
}

func TestValidKeywords(t *testing.T) {
	if err := validKeywords([]string{"a", "b"}); err != nil {
		t.Fatalf("a short list was refused: %v", err)
	}
	long := make([]string, maxKeywords+1)
	for i := range long {
		long[i] = string(rune('a' + i%26))
	}
	if err := validKeywords(long); err == nil {
		t.Error("a list past the cap should be refused, not silently trimmed")
	}
	if err := validKeywords([]string{strings.Repeat("x", maxKeywordLen+1)}); err == nil {
		t.Error("a keyword past the length cap should be refused")
	}
}

// Only the title is read, and only a title a card would actually show: an x
// post carries its whole text as its title, and blocking on that would be
// blocking on the body by another name.
func TestBlockerMatchesTitleOnly(t *testing.T) {
	b := newTestBlocker(t, "crypto", "国足")

	cases := []struct {
		name string
		it   core.Item
		want string
	}{
		{"title carries it", core.Item{App: "reddit", ID: "1", Title: "Why CRYPTO is back"}, "crypto"},
		{"CJK substring", core.Item{App: "douban", ID: "2", Title: "国足又输了"}, "国足"},
		{"body alone doesn't", core.Item{App: "folo", ID: "3", Title: "Markets", Body: "crypto everywhere"}, ""},
		{"untitled x post", core.Item{App: "x", ID: "4", Title: "crypto is back", Body: "crypto is back"}, ""},
		{"no title at all", core.Item{App: "x", ID: "5", Body: "crypto is back"}, ""},
		{"nothing matches", core.Item{App: "reddit", ID: "6", Title: "Go 1.25 is out"}, ""},
	}
	for _, c := range cases {
		if got := b.match(itemTitle(c.it)); got != c.want {
			t.Errorf("%s: matched %q, want %q", c.name, got, c.want)
		}
	}

	// An empty list blocks nothing, whatever the titles say.
	none := newTestBlocker(t)
	items := []core.Item{{App: "reddit", ID: "1", Title: "Why CRYPTO is back"}}
	if keep, caught := none.split(items, time.Now()); len(keep) != 1 || len(caught) != 0 {
		t.Errorf("no keywords blocked %d item(s)", len(caught))
	}
}

// A blocked post is fetched again every sweep — nothing upstream knows we don't
// want it — so filing it twice has to be filing it once.
func TestBlockerFilesEachPostOnce(t *testing.T) {
	b := newTestBlocker(t, "crypto")
	now := time.Now()
	items := []core.Item{
		{App: "reddit", ID: "1", Title: "crypto again", Source: "r/all"},
		{App: "reddit", ID: "2", Title: "Go 1.25 is out"},
	}

	keep, caught := b.split(items, now)
	if len(keep) != 1 || keep[0].ID != "2" {
		t.Fatalf("kept %v, want the unblocked item alone", keep)
	}
	fresh, err := b.file(caught)
	if err != nil {
		t.Fatal(err)
	}
	if fresh != 1 || b.count() != 1 {
		t.Fatalf("filed %d fresh, holding %d; want 1 and 1", fresh, b.count())
	}

	_, caught = b.split(items, now)
	fresh, err = b.file(caught)
	if err != nil {
		t.Fatal(err)
	}
	if fresh != 0 || b.count() != 1 {
		t.Fatalf("the same post filed again: %d fresh, holding %d", fresh, b.count())
	}
	if got := b.caughtBy("reddit", "1"); got != "crypto" {
		t.Errorf("caughtBy = %q, want the keyword that blocked it", got)
	}
}

func TestBlockerRetainsRowsPastFormerLimit(t *testing.T) {
	const formerLimit = 2000
	b := loadBlocker(filepath.Join(t.TempDir(), "keywords.json"), filepath.Join(t.TempDir(), "blocked.json"))
	items := make([]blockedItem, 0, formerLimit+1)
	for i := range formerLimit + 1 {
		items = append(items, blockedItem{Wire: item("reddit", fmt.Sprint(i), "blocked").Wire()})
	}
	if fresh, err := b.file(items); err != nil || fresh != len(items) {
		t.Fatalf("filed %d of %d items: %v", fresh, len(items), err)
	}
	if got := b.count(); got != formerLimit+1 {
		t.Fatalf("kept %d blocked items, want %d", got, formerLimit+1)
	}
}

// Both files outlive the process: the words because they are the list, and the
// posts because the feed they came from is long gone by the time you look.
func TestBlockerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	words, blocked := filepath.Join(dir, "keywords.json"), filepath.Join(dir, "blocked.json")

	b := loadBlocker(words, blocked)
	if err := b.setKeywords([]string{"crypto"}); err != nil {
		t.Fatal(err)
	}
	_, caught := b.split([]core.Item{{
		App: "reddit", ID: "1", Title: "crypto again", Source: "r/all", URL: "https://x.test/1",
	}}, time.Now())
	if _, err := b.file(caught); err != nil {
		t.Fatal(err)
	}

	again := loadBlocker(words, blocked)
	if again.keywordCount() != 1 || again.keywordText() != "crypto" {
		t.Fatalf("keywords came back as %q", again.keywordText())
	}
	if again.count() != 1 {
		t.Fatalf("blocked list came back with %d items", again.count())
	}
	got := again.list(time.Now())
	if len(got) != 1 || got[0].Title != "crypto again" || got[0].URL != "https://x.test/1" {
		t.Fatalf("blocked item came back as %+v", got)
	}

	// Junk on disk is an empty list, not a page that refuses to load.
	if err := os.WriteFile(blocked, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := loadBlocker(words, blocked).count(); n != 0 {
		t.Fatalf("a corrupt file yielded %d items", n)
	}
}

// The newest block is what you came to check, so it is at the top.
func TestBlockedListNewestFirst(t *testing.T) {
	b := newTestBlocker(t, "crypto")
	base := time.Now()
	for i, id := range []string{"old", "new"} {
		_, caught := b.split([]core.Item{{App: "reddit", ID: id, Title: "crypto " + id}}, base.Add(time.Duration(i)*time.Hour))
		if _, err := b.file(caught); err != nil {
			t.Fatal(err)
		}
	}
	got := b.list(base)
	if len(got) != 2 || got[0].ID != "new" {
		t.Fatalf("list = %v, want the most recent block first", got)
	}
}

// newBlockingSweeper wires a sweeper to canned pages and a block list, the way
// newTestSweeper does for the plain case.
func newBlockingSweeper(t *testing.T, drain bool, b *blocker, pages [][]core.Item) (*sweeper, *feedCache) {
	t.Helper()
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	s := newSweeper(t.TempDir(), c, nil, b, drain, 0)
	s.mark = (&fakeMark{}).fn
	fetches := 0
	s.fetch = func(context.Context, string, string, int, time.Time) ([]core.Item, bool, error) {
		if fetches >= len(pages) {
			return nil, false, nil
		}
		p := pages[fetches]
		fetches++
		return p, false, nil
	}
	return s, c
}

// A blocked post never becomes backlog: the sweep files it and the cache never
// hears about it, so there is nothing to triage away later.
func TestSweepKeepsBlockedOutOfTheCache(t *testing.T) {
	b := newTestBlocker(t, "crypto")
	s, c := newBlockingSweeper(t, false, b, [][]core.Item{{
		{App: "reddit", ID: "1", Title: "crypto again"},
		{App: "reddit", ID: "2", Title: "Go 1.25 is out"},
	}})
	s.sweepApp(context.Background(), "reddit")

	if got := c.unreadCount(); got != 1 {
		t.Fatalf("cache took %d items, want the one that isn't blocked", got)
	}
	if _, ok := c.item("reddit", "1", time.Now()); ok {
		t.Error("a blocked post reached the backlog cache")
	}
	if b.count() != 1 {
		t.Fatalf("blocked list holds %d, want the one post that was caught", b.count())
	}
}

// A drain stops when a round brings nothing new. A round that brought only
// blocked posts did bring something, and calling it exhausted would leave the
// rest of the backlog unreachable.
func TestDrainCountsBlockedAsProgress(t *testing.T) {
	b := newTestBlocker(t, "crypto")
	s, c := newBlockingSweeper(t, true, b, [][]core.Item{
		{{App: "inoreader", ID: "1", Title: "keep me"}},
		{{App: "inoreader", ID: "2", Title: "crypto again"}}, // nothing for the cache
		{{App: "inoreader", ID: "3", Title: "keep me too"}},  // ...but the drain went on
		{},
	})
	s.sweepApp(context.Background(), "inoreader")

	if got := c.unreadCount(); got != 2 {
		t.Fatalf("cache holds %d, want both unblocked articles from either side of the blocked round", got)
	}
	if b.count() != 1 {
		t.Fatalf("blocked list holds %d, want 1", b.count())
	}
}

// A keyword is added because of something on the screen right now, so saving it
// has to clear that too — not just what arrives on the next sweep.
func TestKeywordsHandlerPurgesTheBacklog(t *testing.T) {
	b := newTestBlocker(t)
	c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	now := time.Now()
	c.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "crypto again"},
		{App: "reddit", ID: "2", Title: "Go 1.25 is out"},
		{App: "x", ID: "3", Title: "crypto is back", Body: "crypto is back"}, // untitled
	}, now)

	post := func(words string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"words": {words}}
		r := httptest.NewRequest(http.MethodPost, "/keywords", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handleKeywords(rec, r, b, c)
		return rec
	}

	rec := post("crypto\n\ncrypto\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"keywords":1`) || !strings.Contains(rec.Body.String(), `"moved":1`) {
		t.Fatalf("answered %s, want one keyword and one post moved", rec.Body.String())
	}
	if got := c.unreadCount(); got != 2 {
		t.Fatalf("backlog holds %d, want the article and the untitled x post", got)
	}
	if _, ok := c.item("reddit", "1", now); ok {
		t.Error("the blocked article is still in the backlog")
	}
	if b.count() != 1 {
		t.Fatalf("blocked list holds %d, want the purged article", b.count())
	}

	// Emptying the list is allowed, and takes nothing else with it.
	if rec := post(""); rec.Code != http.StatusOK || b.keywordCount() != 0 {
		t.Fatalf("clearing the list gave %d, leaving %d keywords", rec.Code, b.keywordCount())
	}
	if b.count() != 1 || c.unreadCount() != 2 {
		t.Error("clearing the keywords should not un-block or re-block anything on its own")
	}

	if rec := post(strings.Repeat("x", maxKeywordLen+1)); rec.Code != http.StatusBadRequest {
		t.Errorf("an over-long keyword gave %d, want 400", rec.Code)
	}
}

// The blocked list is a list of titles: the content is the part you asked not
// to see, and leaving it out is what makes the list worth scrolling.
func TestBlockedPageIsTitlesOnly(t *testing.T) {
	b := newTestBlocker(t, "crypto")

	empty := renderBlockedPage(t, b)
	if !strings.Contains(empty, "Nothing blocked yet") {
		t.Fatalf("expected an empty-state note: %s", empty)
	}
	if !strings.Contains(empty, `data-blockedview="true"`) {
		t.Fatal("the blocked view should flag itself so scroll-to-read stays off")
	}
	if strings.Contains(empty, "mark all read") {
		t.Fatal("nothing on the blocked list is unread, so there is nothing to mark")
	}

	_, caught := b.split([]core.Item{{
		App: "reddit", ID: "1", Title: "crypto again", Body: "a body nobody asked for",
		Source: "r/all", URL: "https://x.test/1", Video: "https://video.twimg.com/a.mp4",
		Images: []string{"https://img.test/a.jpg"},
	}}, time.Now())
	if _, err := b.file(caught); err != nil {
		t.Fatal(err)
	}

	page := renderBlockedPage(t, b)
	if !strings.Contains(page, "crypto again") {
		t.Fatalf("the title should be on the row: %s", page)
	}
	for _, gone := range []string{
		"a body nobody asked for",
		`class="save"`,
		`class="share"`,
		`class="vid"`,
		`class="imgbox`,
		`class="full hid"`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("a blocked row should carry none of %q: %s", gone, page)
		}
	}
	// ...but it still says where it came from, why it's here, and links out.
	if !strings.Contains(page, `class="kw"`) || !strings.Contains(page, ">crypto</span>") {
		t.Errorf("the row should name the keyword that caught it: %s", page)
	}
	if !strings.Contains(page, `href="https://x.test/1"`) {
		t.Errorf("the row should still open the original: %s", page)
	}
	if !strings.Contains(page, `<span id="blockedn">1</span> blocked`) {
		t.Errorf("the header should count the list: %s", page)
	}
}

// The keywords modal is the browser's own dialog, pre-filled with the list as
// the server has it, and only on the page it belongs to.
func TestKeywordsModal(t *testing.T) {
	b := newTestBlocker(t, "crypto", "国足")
	page := renderBlockedPage(t, b)
	if !strings.Contains(page, `<dialog id="kwdlg"`) {
		t.Fatalf("expected a native dialog: %s", page)
	}
	if !strings.Contains(page, "crypto\n国足</textarea>") {
		t.Fatalf("the textarea should hold the list, one per line: %s", page)
	}
	if !strings.Contains(page, `<span id="kwn">2</span> keywords`) {
		t.Errorf("the header should count the keywords: %s", page)
	}
	if !strings.Contains(page, `id="kwopen"`) {
		t.Error("the count should be the control that opens the dialog")
	}
	// Anything smaller and mobile Safari zooms the page the moment you tap in.
	if !strings.Contains(page, "font:16px/1.6 ui-monospace") {
		t.Error("the textarea has to be at least 16px or focusing it zooms iOS")
	}

	feed := renderPage(t, nil, []string{"x"}, nil, "following", "")
	if strings.Contains(feed, `<dialog id="kwdlg"`) {
		t.Error("the block list is edited from the blocked view, not from the feed")
	}
}

// Every view links to the blocked list, and the blocked view links back.
func TestBlockedLinkInTheHeader(t *testing.T) {
	feed := renderPage(t, nil, []string{"x"}, nil, "following", "")
	if !strings.Contains(feed, `href="/?blocked=1"`) {
		t.Fatalf("expected a blocked link in the feed header: %s", feed)
	}
	if !strings.Contains(feed, `href="/?saved=1"`) {
		t.Error("the saved link should still be there beside it")
	}
	blocked := renderBlockedPage(t, newTestBlocker(t))
	if !strings.Contains(blocked, `<a class="viewlink" href="/">unread</a>`) {
		t.Errorf("the blocked view should link back to the feed: %s", blocked)
	}
}

func TestClearBlockedKeepsKeywordsAndReferencedItems(t *testing.T) {
	db, err := openFeedDB(filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.db.Close()
	b, err := loadBlockerDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.setKeywords([]string{"crypto"}); err != nil {
		t.Fatal(err)
	}
	_, caught := b.split([]core.Item{
		{App: "reddit", ID: "1", Title: "crypto shared"},
		{App: "reddit", ID: "2", Title: "crypto orphan"},
	}, time.Now())
	if _, err := b.file(caught); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSavedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.add(core.Item{App: "reddit", ID: "1", Title: "crypto shared"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	page := renderBlockedPage(t, b)
	if !strings.Contains(page, `id="clearBlocked"`) || !strings.Contains(page, `data-total="2"`) ||
		!strings.Contains(page, `window.confirm('Delete all ' + total + ' blocked '`) {
		t.Fatalf("blocked history should offer a counted, confirmed clear: %s", page)
	}
	rec := httptest.NewRecorder()
	handleClearBlocked(rec, b)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cleared":2`) {
		t.Fatalf("clear answered %d %s", rec.Code, rec.Body.String())
	}
	if b.count() != 0 || b.keywordCount() != 1 {
		t.Fatalf("after clear: blocked=%d keywords=%d, want 0 and 1", b.count(), b.keywordCount())
	}
	var blockedRows, itemRows int
	if err := db.db.QueryRow(`SELECT count(*) FROM blocked_items`).Scan(&blockedRows); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM items`).Scan(&itemRows); err != nil {
		t.Fatal(err)
	}
	if blockedRows != 0 || itemRows != 1 {
		t.Fatalf("database after clear: blocked=%d items=%d, want 0 and shared saved item only", blockedRows, itemRows)
	}
	if page := renderBlockedPage(t, b); strings.Contains(page, `id="clearBlocked"`) {
		t.Fatal("an empty blocked history should have no clear action")
	}
}

// A chip narrows the blocked list the way it narrows the saved one, and the
// pick has to stay inside the view it was made in.
func TestBlockedChipsKeepTheView(t *testing.T) {
	q := url.Values{"blocked": {"1"}}
	if got := chipHref(q, feedSel{Kind: "app", Key: "reddit"}); !strings.Contains(got, "blocked=1") {
		t.Fatalf("chipHref = %q, want the blocked view kept", got)
	}
	if got := chipHref(q, feedSel{}); !strings.Contains(got, "blocked=1") {
		t.Fatalf("the clear link = %q, want the blocked view kept", got)
	}
}

// The deck is for triage, and there is none to do here.
func TestBlockedViewNeverSwipes(t *testing.T) {
	b := newTestBlocker(t, "crypto")
	_, caught := b.split([]core.Item{{App: "reddit", ID: "1", Title: "crypto again"}}, time.Now())
	if _, err := b.file(caught); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	items := b.list(now)
	d := buildPageData(pageInput{
		items: items, total: len(items), now: now, block: b, blockedView: true, swipe: true,
	})
	if d.Swipe {
		t.Error("the blocked list should stay a scrolling list even with the deck asked for")
	}
}
