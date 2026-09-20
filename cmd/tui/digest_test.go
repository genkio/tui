package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// testDigester is the summarizer with the CLI replaced and a pile of its own,
// kept in memory: the store is the same one the server runs, minus the disk.
func testDigester(t *testing.T, cache *feedCache, ask func(context.Context, string) (string, error)) *summarizer {
	t.Helper()
	sum := testSummarizer(t, cache, ask)
	store, err := newDigestStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	sum.digests = store
	sum.ready = nil // no CLI to look for: ask is the stub above
	return sum
}

// settledDigest waits for the run to stop, which is what the page's polling
// does.
func settledDigest(t *testing.T, sum *summarizer) summaryJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := sum.job(digestKey); ok && j.State != "running" {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the digest never settled")
	return summaryJob{}
}

func digestPost(t *testing.T, sum *summarizer, cache *feedCache, form string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/digest", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	startDigest(rec, req, sum, cache, nil)
	return rec
}

func backlogOf(cache *feedCache, n int) {
	items := make([]core.Item, 0, n)
	now := time.Now()
	for i := range n {
		items = append(items, core.Item{
			App: "reddit", ID: strconv.Itoa(i), Title: fmt.Sprintf("post %d", i),
			At: now.Add(-time.Duration(n-i) * time.Minute),
		})
	}
	cache.upsert(items, now)
}

// A fetch summarizes the next batch by itself, and the run stops at the cap:
// the whole point is that a deep backlog is described a couple of hundred items
// at a time rather than in one prompt nothing could hold.
func TestDigestReadsOneCappedBatchPerFetch(t *testing.T) {
	cache := newTestCache(t)
	backlogOf(cache, digestCap+30)

	prompts := make(chan string, 4)
	sum := testDigester(t, cache, func(_ context.Context, p string) (string, error) {
		prompts <- p
		return "## what happened\n\n- [a post](/item?app=reddit&id=1) says something", nil
	})

	sum.digestAuto()
	if j := settledDigest(t, sum); j.State != "done" || j.Count != digestCap {
		t.Fatalf("job = %+v, want a done run over %d items", j, digestCap)
	}
	d, ok := sum.digests.waiting()
	if !ok {
		t.Fatal("nothing waiting after a run")
	}
	if len(d.Items) != digestCap || d.HTML == "" {
		t.Fatalf("digest read %d items, html %q", len(d.Items), d.HTML)
	}
	// Oldest first: the pile works through the backlog from the end that would
	// otherwise never be reached.
	if d.Items[0].ID != "0" {
		t.Errorf("first item = %q, want the oldest", d.Items[0].ID)
	}

	// The next fetch takes what the first one left, and nothing it already read.
	sum.digestAuto()
	if j := settledDigest(t, sum); j.State != "done" || j.Count != 30 {
		t.Fatalf("second job = %+v, want the remaining 30", j)
	}
	if n := sum.digests.waitingCount(); n != 2 {
		t.Fatalf("waiting = %d, want 2", n)
	}
	// ...and a third has nothing to say, so no run is started at all.
	sum.digestAuto()
	if n := sum.digests.waitingCount(); n != 2 {
		t.Fatalf("waiting = %d after a third fetch, want 2", n)
	}
	<-prompts
	second := <-prompts
	if strings.Contains(second, "post 0\n") {
		t.Error("the second batch read an item the first one had already covered")
	}
}

// next is the whole of what a digest can do to the backlog: exactly the items
// it read are marked, whatever else has arrived, and the one behind it is what
// comes up.
func TestDigestNextMarksItsOwnBatchRead(t *testing.T) {
	cache := newTestCache(t)
	backlogOf(cache, 3)
	sum := testDigester(t, cache, func(context.Context, string) (string, error) {
		return "- something", nil
	})
	sum.digestAuto()
	settledDigest(t, sum)

	// An item read in the feed before the digest was cleared: clearing it is a
	// no-op on that one rather than anything to complain about.
	cache.markRead("reddit", []string{"1"}, time.Now())
	// ...and one that arrived after the run, which the digest never read and
	// must not clear.
	cache.upsert([]core.Item{{App: "reddit", ID: "late", Title: "arrived after"}}, time.Now())

	d, _ := sum.digests.waiting()
	rec := digestPost(t, sum, cache, "do=next&id="+strconv.FormatInt(d.ID, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("next = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Marked, Waiting int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Marked != 3 || got.Waiting != 0 {
		t.Errorf("answer = %+v, want the batch cleared and nothing waiting", got)
	}
	left := cache.unread(time.Now(), "")
	if len(left) != 1 || left[0].ID != "late" {
		t.Errorf("backlog = %+v, want only the item that arrived after the run", left)
	}
	// Twice is once: the second tap has nothing left to clear.
	if rec := digestPost(t, sum, cache, "do=next&id="+strconv.FormatInt(d.ID, 10)); rec.Code != http.StatusNotFound {
		t.Errorf("clearing it again = %d, want 404", rec.Code)
	}
}

// retry is the same batch over again, in the same place in the pile: a run the
// model made a mess of is worth another go, and the next run would have moved
// on to items this one never mentioned.
func TestDigestRetryRewritesTheSameBatch(t *testing.T) {
	cache := newTestCache(t)
	backlogOf(cache, 2)
	runs := 0
	sum := testDigester(t, cache, func(context.Context, string) (string, error) {
		runs++
		return fmt.Sprintf("- run %d", runs), nil
	})
	sum.digestAuto()
	settledDigest(t, sum)
	first, _ := sum.digests.waiting()

	rec := digestPost(t, sum, cache, "do=retry&id="+strconv.FormatInt(first.ID, 10))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry = %d %s", rec.Code, rec.Body.String())
	}
	settledDigest(t, sum)
	again, ok := sum.digests.waiting()
	if !ok {
		t.Fatal("the retry lost the digest")
	}
	if again.ID != first.ID {
		t.Errorf("retry made a new digest (%d, was %d)", again.ID, first.ID)
	}
	if len(again.Items) != 2 || !strings.Contains(again.HTML, "run 2") {
		t.Errorf("digest = %+v, want the same two items written again", again)
	}
	if n := sum.digests.waitingCount(); n != 1 {
		t.Errorf("waiting = %d, want the one digest", n)
	}
}

// The chip is the pile's size, and the page it opens is the digest itself
// rather than cards.
func TestDigestChipAndView(t *testing.T) {
	tally := newTally()
	tally.apps["reddit"] = 4
	tally.digests = 2
	var chip *filterChip
	for _, g := range chipRow(tally, []string{"reddit"}, nil, feedSel{}, nil) {
		for i := range g.Chips {
			if g.Chips[i].Kind == "digest" {
				chip = &g.Chips[i]
			}
		}
	}
	if chip == nil {
		t.Fatal("no summary chip in the row")
	}
	if chip.Label != "summary" || chip.Count != 2 || chip.Hidden {
		t.Errorf("chip = %+v, want a visible summary chip counting 2", *chip)
	}
	if chip.Href != "/?digest=1" {
		t.Errorf("href = %q, want /?digest=1", chip.Href)
	}
	if sel := parseSel(map[string][]string{"digest": {"1"}}); sel.Kind != "digest" {
		t.Errorf("parseSel = %+v, want the digest pick", sel)
	}

	// Its page is prose and its own two controls: no cards, and mark-all does
	// not follow it there.
	data := buildPageData(pageInput{
		apps: []string{"reddit"}, tally: &tally, digestView: true,
		digest: &digestData{ID: 3, Count: 200, Waiting: 2, HTML: "<p>hi</p>"},
		block:  &blocker{},
	})
	if !data.DigestView || data.Digest == nil || data.BulkMark {
		t.Errorf("page = %+v, want the digest view without mark-all", data)
	}
	if data.SummaryApp != "" {
		t.Errorf("summary app = %q, want none: the digest is not a source's briefing", data.SummaryApp)
	}
}

// The page the chip opens: the prose, its two buttons, and the note that stands
// in for a pile with nothing in it.
func TestDigestPageRendersProseAndItsTwoControls(t *testing.T) {
	tally := newTally()
	tally.apps["reddit"] = 4
	tally.digests = 1
	in := pageInput{
		apps: []string{"reddit"}, tally: &tally, now: time.Now(), block: &blocker{},
		sel:        feedSel{Kind: "digest", Key: "digest"},
		digestView: true,
		digest:     &digestData{ID: 7, Count: 200, When: "3m ago", Waiting: 1, HTML: "<p>the day in one page</p>"},
	}
	page := renderInput(t, in)
	for _, want := range []string{`id="digest"`, `data-id="7"`, "200 unread items", "summarized 3m ago",
		`id="dignext"`, `id="digretry"`, "the day in one page"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if strings.Contains(page, `id="markAll"`) {
		t.Error("mark all read followed the digest onto its own page")
	}

	in.digest = nil
	in.digestRunning = true
	page = renderInput(t, in)
	if !strings.Contains(page, `data-running="1"`) || !strings.Contains(page, "Reading the backlog") {
		t.Errorf("an empty pile with a run going does not say so:\n%s", page)
	}
}

// The pile is on disk, cleared ones included: a restart that forgot what had
// been summarized would describe the same two hundred items over again.
func TestDigestsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.db")
	db, err := openFeedDB(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newDigestStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job := summaryJob{State: "done", Lang: "en", Count: 2, HTML: "<p>two things</p>", Generated: time.Now().UTC().Format(time.RFC3339)}
	if err := store.add(job, []core.Item{item("reddit", "1", "a"), item("x", "2", "b")}); err != nil {
		t.Fatal(err)
	}
	if err := store.add(job, []core.Item{item("reddit", "3", "c")}); err != nil {
		t.Fatal(err)
	}
	first, _ := store.waiting()
	store.mark(first.ID, time.Now())
	store.setLanguage("zh")
	if err := db.close(); err != nil {
		t.Fatal(err)
	}

	db, err = openFeedDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.close() })
	back, err := newDigestStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if n := back.waitingCount(); n != 1 {
		t.Errorf("waiting = %d, want the one that was never cleared", n)
	}
	d, ok := back.waiting()
	if !ok || len(d.Items) != 1 || d.Items[0].ID != "3" {
		t.Errorf("waiting digest = %+v, want the second batch", d)
	}
	// The cleared one is still there, holding what it read: that is what keeps
	// the next run off the same items.
	covered := back.covered()
	if !covered[core.Key("reddit", "1")] || !covered[core.Key("x", "2")] {
		t.Errorf("covered = %v, want the cleared batch still accounted for", covered)
	}
	if back.language() != "zh" {
		t.Errorf("language = %q, want the one the reader last set", back.language())
	}
}

// The page hands its language to the server, since the runs a fetch starts
// have nobody to ask, and the next run is written in it.
func TestDigestTakesItsLanguageFromThePage(t *testing.T) {
	cache := newTestCache(t)
	backlogOf(cache, 1)
	prompts := make(chan string, 1)
	sum := testDigester(t, cache, func(_ context.Context, p string) (string, error) {
		prompts <- p
		return "- 一件事", nil
	})
	if sum.digests.language() != "en" {
		t.Fatalf("language starts at %q, want en", sum.digests.language())
	}
	if rec := digestPost(t, sum, cache, "do=lang&lang=zh"); rec.Code != http.StatusOK {
		t.Fatalf("setting the language = %d %s", rec.Code, rec.Body.String())
	}
	if sum.digests.language() != "zh" {
		t.Fatalf("language = %q, want zh", sum.digests.language())
	}
	sum.digestAuto()
	if j := settledDigest(t, sum); j.Lang != "zh" {
		t.Errorf("job language = %q, want the one the page set", j.Lang)
	}
	if p := <-prompts; !strings.Contains(p, summaryLangs["zh"]) {
		t.Error("the prompt did not ask for Chinese")
	}
	// ...and the page is told what the server holds, so a browser set to the
	// other one knows to say so.
	data := buildPageData(pageInput{apps: []string{"reddit"}, block: &blocker{}, sumLang: sum.digests.language()})
	if data.SumLang != "zh" {
		t.Errorf("page says %q, want zh", data.SumLang)
	}
}

// Nothing here is marked read by being summarized, and the items stay in the
// feed: a digest describes the backlog, it does not take it away.
func TestDigestLeavesTheBacklogAlone(t *testing.T) {
	cache := newTestCache(t)
	backlogOf(cache, 5)
	sum := testDigester(t, cache, func(context.Context, string) (string, error) {
		return "- something", nil
	})
	sum.digestAuto()
	settledDigest(t, sum)
	if n := cache.unreadCount(); n != 5 {
		t.Errorf("backlog = %d, want all five still unread", n)
	}
}
