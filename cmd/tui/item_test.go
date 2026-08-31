package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// getItem serves /item the way the route does, against the given stores.
func getItem(t *testing.T, query string, cache *feedCache, saved *savedStore, rendered *renderedItems) *httptest.ResponseRecorder {
	t.Helper()
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	if rendered == nil {
		rendered = newRenderedItems()
	}
	rec := httptest.NewRecorder()
	handleItem(rec, httptest.NewRequest(http.MethodGet, "/item?"+query, nil), loader, cache, saved, nil, rendered)
	return rec
}

func TestItemViewServesOneCard(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{
		{App: "reddit", ID: "77", Title: "a post of its own", Body: "the whole thing"},
		{App: "x", ID: "88", Title: "someone else"},
	}, time.Now())

	rec := getItem(t, "app=reddit&id=77", cache, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-itemview="true"`) {
		t.Error("the page should tell its script this is one item's own page")
	}
	if !strings.Contains(body, `data-id="77"`) || !strings.Contains(body, "a post of its own") {
		t.Errorf("the asked-for item should be on the page: %s", body)
	}
	if strings.Contains(body, `data-id="88"`) {
		t.Error("nothing but the asked-for item belongs on it")
	}
	if !strings.Contains(body, "<title>a post of its own — tui</title>") {
		t.Error("a URL that gets sent to somebody should name what it opens")
	}
	// The list's own furniture has nothing to act on here.
	if strings.Contains(body, `id="markAll"`) || strings.Contains(body, `id="decklink"`) {
		t.Error("no bulk mark and no layout toggle over a single card")
	}
	// Its own page is where the card already is, so it carries no link to it.
	if strings.Contains(body, "/item?app=reddit") {
		t.Error("the item view should not link to itself")
	}
	// Following a link is not reading the feed.
	if n := cache.unreadCount(); n != 2 {
		t.Errorf("unread = %d, want 2: opening an item's page must not mark it read", n)
	}
}

// An item the backlog has let go of is still reachable when it was saved, which
// is the whole point of a URL that outlives the page it came from.
func TestItemViewFallsBackToSavedAndRendered(t *testing.T) {
	db, err := openFeedDB(filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	store, err := loadSavedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.add(core.Item{App: "folo", ID: "9", Title: "kept"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cache := newTestCache(t)

	rec := getItem(t, "app=folo&id=9", cache, store, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "kept") {
		t.Fatalf("saved item = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data-saved="1"`) {
		t.Error("an item that is saved should say so on its own page")
	}

	// x's For You is never cached, so the only record of one of its posts is
	// what this process rendered.
	rendered := newRenderedItems()
	rendered.put([]core.Item{{App: "x", ID: "live", Title: "from For You"}})
	rec = getItem(t, "app=x&id=live", cache, store, rendered)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from For You") {
		t.Fatalf("rendered item = %d %s", rec.Code, rec.Body.String())
	}
}

func TestItemViewMissingAndUnknown(t *testing.T) {
	cache := newTestCache(t)
	if rec := getItem(t, "app=x", cache, nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no id status = %d, want 400", rec.Code)
	}
	if rec := getItem(t, "id=1", cache, nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no app status = %d, want 400", rec.Code)
	}
	if rec := getItem(t, "app=x&id=gone", cache, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown item status = %d, want 404", rec.Code)
	}
}

func TestItemViewJSON(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "x", ID: "5", Title: "one"}}, time.Now())
	rec := getItem(t, "app=x&id=5&json=1", cache, nil, nil)
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), `"id":"5"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// Every card in a list offers the way to its own page, so the URL is there to
// copy or send without having to build it by hand.
func TestCardCarriesItsOwnLink(t *testing.T) {
	page := renderPage(t, []core.Item{{App: "reddit", ID: "a/c", Title: "linkable"}}, []string{"reddit"}, nil, "", "")
	// html/template escapes the & in the href, so match what the page actually
	// carries rather than the raw URL.
	want := `href="` + strings.ReplaceAll(itemHref("reddit", "a/c"), "&", "&amp;") + `"`
	if !strings.Contains(page, want) {
		t.Errorf("card should link to %s:\n%s", want, page)
	}
	if got := itemHref("reddit", "a b/c"); got != "/item?app=reddit&id=a+b%2Fc" {
		t.Errorf("itemHref = %q", got)
	}
	if itemHref("", "1") != "" || itemHref("x", "") != "" {
		t.Error("a half-named item has no page")
	}
}

func TestHeadTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		it   core.Item
		want string
	}{
		{"title", core.Item{Title: "a headline"}, "a headline — tui"},
		{"first line of a post with no title of its own", core.Item{Title: "hi\nthere", Body: "hi\nthere"}, "hi — tui"},
		{"source when there is nothing else", core.Item{Source: "r/go"}, "r/go — tui"},
		{"nothing at all", core.Item{}, "item — tui"},
		{"clipped", core.Item{Title: strings.Repeat("x", 90)}, strings.Repeat("x", 70) + "… — tui"},
	} {
		if got := headTitle(tc.it); got != tc.want {
			t.Errorf("%s: headTitle = %q, want %q", tc.name, got, tc.want)
		}
	}
}
