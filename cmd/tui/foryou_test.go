package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// For You is served by the x plugin's other tab, so every subprocess it costs —
// the fetch and the read marks both — is x's.
func TestForYouRunsTheXPlugin(t *testing.T) {
	if plugin, tab := pluginOf(xForYouApp); plugin != "x" || tab != "foryou" {
		t.Errorf("pluginOf(%s) = %q/%q, want x/foryou", xForYouApp, plugin, tab)
	}
	if plugin, tab := pluginOf("x"); plugin != "x" || tab != "following" {
		t.Errorf("pluginOf(x) = %q/%q, want x/following", plugin, tab)
	}
	if plugin, tab := pluginOf("reddit"); plugin != "reddit" || tab != "following" {
		t.Errorf("pluginOf(reddit) = %q/%q, want reddit/following", plugin, tab)
	}
	// The plugin says "x" whichever tab it read, so what comes back is filed
	// under the source that asked for it — otherwise For You would land in x's
	// backlog and neither chip could tell you what it holds.
	items := []core.Item{{App: "x", ID: "1"}, {App: "x", ID: "2"}}
	stampApp(items, xForYouApp)
	for _, it := range items {
		if it.App != xForYouApp {
			t.Errorf("item %s filed under %q, want %s", it.ID, it.App, xForYouApp)
		}
	}
}

// Being logged into x is being logged into both its timelines: there is one
// session, and For You is swept off the back of it like any other source.
func TestAuthedFeedAppsSweepsBothXTimelines(t *testing.T) {
	t.Setenv("XTUI_AUTH_TOKEN", "token")
	t.Setenv("XTUI_CT0", "ct0")
	apps := authedFeedApps(t.TempDir())
	at := -1
	for i, a := range apps {
		if a == "x" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("x should be authed with its tokens set: %v", apps)
	}
	if at+1 >= len(apps) || apps[at+1] != xForYouApp {
		t.Fatalf("apps = %v, want %s alongside x", apps, xForYouApp)
	}
}

// A sweep runs the services in parallel but a plugin's own sources in turn: x's
// two timelines are one login, and two scrapes at once is what asks it for a
// rate limit rather than a timeline.
func TestSweepGroupsASourceWithItsPlugin(t *testing.T) {
	got := byPlugin([]string{"inoreader", "x", xForYouApp, "reddit"})
	want := [][]string{{"inoreader"}, {"x", xForYouApp}, {"reddit"}}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], ",") != strings.Join(want[i], ",") {
			t.Fatalf("groups = %v, want %v", got, want)
		}
	}
}

// A tweet is one tweet whichever of x's two windows surfaced it. The timeline
// that cached it first keeps it; the other's sighting is dropped rather than
// filed again under the other name, which would be the same post to read twice.
func TestForYouAndFollowingDoNotDoubleFileATweet(t *testing.T) {
	// A sweeper whose fetch behaves like the subprocess: the same post whichever
	// source asks, filed under the one that did.
	sweeperOver := func(id string) (*sweeper, *feedCache) {
		t.Helper()
		c := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
		s := newSweeper(t.TempDir(), c, nil, nil, false, 0)
		s.mark = (&fakeMark{}).fn
		s.fetch = func(_ context.Context, app string, _ int, _ time.Time) ([]core.Item, bool, error) {
			items := []core.Item{{App: "x", ID: id, Title: "a post from someone you follow"}}
			stampApp(items, app)
			return items, false, nil
		}
		return s, c
	}

	for _, tc := range []struct{ first, second string }{
		{"x", xForYouApp},
		{xForYouApp, "x"},
	} {
		s, cache := sweeperOver("7")
		s.sweepApp(context.Background(), tc.first)
		s.sweepApp(context.Background(), tc.second)
		unread := cache.unread(time.Now(), "")
		if len(unread) != 1 || unread[0].App != tc.first {
			t.Fatalf("%s then %s left %+v, want the one card under %s", tc.first, tc.second, unread, tc.first)
		}
	}

	// Even one already read: having read a post is not a reason to be shown it
	// again wearing the other chip.
	s, cache := sweeperOver("7")
	s.sweepApp(context.Background(), "x")
	cache.markRead("x", []string{"7"}, time.Now())
	s.sweepApp(context.Background(), xForYouApp)
	if n := len(cache.unread(time.Now(), "")); n != 0 {
		t.Fatalf("unread = %d, want a read tweet to stay read under either name", n)
	}

	// Nothing else has a twin, so nothing else is filtered: ids collide between
	// services, and reddit's 7 is not x's.
	other, cacheB := sweeperOver("7")
	other.sweepApp(context.Background(), "x")
	other.sweepApp(context.Background(), "reddit")
	if n := len(cacheB.unread(time.Now(), "")); n != 2 {
		t.Fatalf("unread = %d, want both services' own item kept", n)
	}
}

// A stale session is x's to fix, whichever of its timelines noticed: `tui
// xforyou --auth` is not a command, and a warning that names it is worse than
// none.
func TestForYouStaleSessionNamesTheLoginToRedo(t *testing.T) {
	cache := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	cache.setStatus(xForYouApp, appStatus{Err: "session is stale", Stale: true})
	failed, warn, _ := cache.trouble([]string{"x", xForYouApp})
	if len(failed) != 1 || failed[0] != xForYouApp {
		t.Fatalf("failed = %v, want only %s", failed, xForYouApp)
	}
	if !strings.Contains(warn, "x For You session is stale") || !strings.Contains(warn, "`tui x --auth`") {
		t.Errorf("warn = %q, want x's own login named", warn)
	}
}

// The backlog is what makes the rest of it work: a briefing over For You alone,
// and a mark-all that clears that source and leaves the other timeline be.
func TestForYouBacklogSummarizesAndClearsOnItsOwn(t *testing.T) {
	cache := loadFeedCache(filepath.Join(t.TempDir(), "feed.json"))
	now := time.Now()
	cache.upsert([]core.Item{
		{App: "x", ID: "1", Title: "following"},
		{App: xForYouApp, ID: "2", Title: "for you"},
		{App: xForYouApp, ID: "3", Title: "for you as well"},
	}, now)

	brief, _ := summaryItems(cache.unread(now, ""), xForYouApp, false)
	if len(brief) != 2 {
		t.Fatalf("briefing covers %d items, want For You's two: %+v", len(brief), brief)
	}
	for _, it := range brief {
		if it.App != xForYouApp {
			t.Fatalf("%s leaked into For You's briefing", it.App)
		}
	}
	// ...and it is named in words there, since "xforyou" is nobody's name for it.
	if p := summaryPrompt(xForYouApp, "en", brief); !strings.Contains(p, "Source: x For You.") {
		t.Errorf("the prompt should name the timeline: %s", p)
	}

	flusher := &markFlusher{cache: cache, wake: make(chan struct{}, 1)}
	form := url.Values{"app": {xForYouApp}}
	req := httptest.NewRequest(http.MethodPost, "/mark-all", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleMarkAll(rec, req, cache, flusher, newSummarizer(cache))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"marked":2`) {
		t.Fatalf("mark-all over For You = %d %s", rec.Code, rec.Body.String())
	}
	left := cache.unread(now, "")
	if len(left) != 1 || left[0].App != "x" {
		t.Fatalf("unread after clearing For You = %+v, want x's own backlog untouched", left)
	}
}
