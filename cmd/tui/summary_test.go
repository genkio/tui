package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// testSummarizer is the real thing with the codex call replaced: a test has no
// business spending minutes on a model to find out whether a handler picks the
// right items. Its worker runs for the length of the test.
func testSummarizer(t *testing.T, cache *feedCache, codex func(context.Context, string) (string, error)) *summarizer {
	t.Helper()
	sum := newSummarizer(cache)
	sum.codex = codex
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sum.serve(ctx) }()
	t.Cleanup(func() {
		stop()
		// Bounded: a test that failed mid-run may have left the stub blocked on a
		// channel nobody will close, and a hung cleanup hides the real failure.
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	return sum
}

func post(t *testing.T, sum *summarizer, form string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/summarize", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	startSummary(rec, req, sum)
	return rec
}

func get(t *testing.T, sum *summarizer, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	showSummary(rec, httptest.NewRequest(http.MethodGet, "/summarize?"+query, nil), sum)
	return rec
}

// settled waits for a source's job to stop running, which is what the page's own
// polling does.
func settled(t *testing.T, sum *summarizer, app string) summaryJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := sum.job(app); ok && j.State != "running" {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s never settled", app)
	return summaryJob{}
}

func TestSummarizeBriefsOneSourcesBacklog(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "go 1.30 is out", Source: "r/golang", At: now.Add(-2 * time.Hour)},
		{App: "reddit", ID: "2", Title: "another release post", Source: "r/golang", At: now.Add(-1 * time.Hour)},
		{App: "folo", ID: "9", Title: "nothing to do with reddit"},
	}, now)

	prompts := make(chan string, 1)
	sum := testSummarizer(t, cache, func(_ context.Context, p string) (string, error) {
		prompts <- p
		return "## releases\n\n- [go 1.30](/item?app=reddit&id=1) landed", nil
	})

	// Starting one answers at once with the job, not the summary: the wait is
	// minutes, and the chip that asked has a spinner to get on with.
	rec := post(t, sum, "app=reddit")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", rec.Code, rec.Body.String())
	}
	var started summaryJob
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.State != "running" {
		t.Errorf("state = %q, want running", started.State)
	}

	if j := settled(t, sum, "reddit"); j.State != "done" {
		t.Fatalf("job = %+v, want done", j)
	}
	rec = get(t, sum, "app=reddit")
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch = %d %s", rec.Code, rec.Body.String())
	}
	var got summaryJob
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 2 || got.Generated == "" {
		t.Errorf("job = %+v, want the two reddit items and a time", got)
	}
	// The Markdown is rendered here, so the browser has nothing to parse and the
	// model's output cannot bring HTML of its own with it.
	if !strings.Contains(got.HTML, "<h2") || !strings.Contains(got.HTML, `href="/item?app=reddit&amp;id=1"`) {
		t.Errorf("html = %q: want rendered Markdown with the item link intact", got.HTML)
	}

	// Every item the source holds, and none another one does, each with the URL
	// of its own page for the model to cite rather than build.
	prompt := <-prompts
	if !strings.Contains(prompt, "go 1.30 is out") || !strings.Contains(prompt, "another release post") {
		t.Errorf("prompt should carry the source's items:\n%s", prompt)
	}
	if strings.Contains(prompt, "nothing to do with reddit") {
		t.Error("another source's backlog is no part of this briefing")
	}
	if !strings.Contains(prompt, "page: "+itemHref("reddit", "1")) {
		t.Errorf("prompt should hand over each item's own page:\n%s", prompt)
	}

	// Being told what is in a batch is not having read it.
	if n := cache.unreadCount(); n != 3 {
		t.Errorf("unread = %d, want 3: summarizing must not mark anything read", n)
	}
}

// A briefing is about a backlog as it stood, and a sweep lands every quarter of
// an hour, so what it never read is counted for it: by id, since reading some
// and fetching some leaves a total where it was.
func TestSummaryCountsWhatArrivedSince(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "one"},
		{App: "reddit", ID: "2", Title: "two"},
		{App: "folo", ID: "9", Title: "another source"},
	}, now)
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) { return "a briefing", nil })

	if rec := post(t, sum, "app=reddit"); rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	settled(t, sum, "reddit")

	fetched := func() summaryJob {
		t.Helper()
		var j summaryJob
		if err := json.Unmarshal(get(t, sum, "app=reddit").Body.Bytes(), &j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	if j := fetched(); j.New != 0 {
		t.Errorf("new = %d, want 0: the briefing has just read the whole backlog", j.New)
	}

	// A sweep lands two more, and one of the summarized items is read. The
	// briefing is behind by the two, not by the arithmetic on the count.
	cache.upsert([]core.Item{
		{App: "reddit", ID: "3", Title: "three"},
		{App: "reddit", ID: "4", Title: "four"},
		{App: "folo", ID: "10", Title: "not this source's"},
	}, now)
	cache.markRead("reddit", []string{"1"}, now)
	if j := fetched(); j.New != 2 {
		t.Errorf("new = %d, want 2", j.New)
	}
	// The chips poll the listing, so it says the same thing.
	var listing struct {
		Jobs map[string]summaryJob `json:"jobs"`
	}
	if err := json.Unmarshal(get(t, sum, "").Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Jobs["reddit"].New != 2 {
		t.Errorf("listing = %+v, want 2 new", listing.Jobs["reddit"])
	}

	// ...and running it again is what clears it: the new briefing read them.
	if rec := post(t, sum, "app=reddit"); rec.Code != http.StatusAccepted {
		t.Fatalf("again = %d %s", rec.Code, rec.Body.String())
	}
	settled(t, sum, "reddit")
	if j := fetched(); j.New != 0 || j.Count != 3 {
		t.Errorf("job = %+v, want 3 items and nothing since", j)
	}
}

// The states listing is what the chips poll, so it carries their whole state
// machine and none of the prose that would make polling expensive.
func TestSummaryStatesListing(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "one"},
		{App: "folo", ID: "2", Title: "two"},
	}, time.Now())

	release := make(chan struct{})
	sum := testSummarizer(t, cache, func(_ context.Context, p string) (string, error) {
		// By the item's own page, not by a word: the instructions are prose and
		// carry plenty of ordinary words with them.
		if strings.Contains(p, itemHref("folo", "2")) {
			<-release
		}
		return "a briefing", nil
	})

	if rec := post(t, sum, "app=reddit"); rec.Code != http.StatusAccepted {
		t.Fatalf("reddit = %d %s", rec.Code, rec.Body.String())
	}
	settled(t, sum, "reddit")
	if rec := post(t, sum, "app=folo"); rec.Code != http.StatusAccepted {
		t.Fatalf("folo = %d %s", rec.Code, rec.Body.String())
	}

	rec := get(t, sum, "")
	var listing struct {
		Jobs map[string]summaryJob `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Jobs["reddit"].State != "done" {
		t.Errorf("reddit = %+v, want done", listing.Jobs["reddit"])
	}
	if listing.Jobs["reddit"].HTML != "" {
		t.Error("a client polling for what is ready has no use for every briefing's prose")
	}
	if listing.Jobs["folo"].State != "running" {
		t.Errorf("folo = %+v, want running", listing.Jobs["folo"])
	}
	close(release)

	// ...and the full one is still there to be fetched by name.
	if rec := get(t, sum, "app=reddit"); !strings.Contains(rec.Body.String(), "a briefing") {
		t.Errorf("full fetch = %s", rec.Body.String())
	}
	if rec := get(t, sum, "app=douban"); rec.Code != http.StatusNotFound {
		t.Errorf("unasked source = %d, want 404", rec.Code)
	}
}

// Several sources can be asked for at once — that is the whole point of firing
// one and carrying on reading — but only one may actually be running: codex is a
// subprocess costing minutes and tokens, and a handful racing finishes no sooner.
func TestSummarizeRunsOneAtATime(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "one"},
		{App: "folo", ID: "2", Title: "two"},
		{App: "x", ID: "3", Title: "three"},
	}, time.Now())

	var live atomic.Int32
	peak := make(chan int32, 8)
	release := make(chan struct{})
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		peak <- live.Add(1)
		<-release
		live.Add(-1)
		return "a briefing", nil
	})

	for _, app := range []string{"reddit", "folo", "x"} {
		if rec := post(t, sum, "app="+app); rec.Code != http.StatusAccepted {
			t.Fatalf("%s = %d %s", app, rec.Code, rec.Body.String())
		}
	}
	// All three are asked for, and all three chips spin.
	states := sum.states()
	for _, app := range []string{"reddit", "folo", "x"} {
		if states[app].State != "running" {
			t.Errorf("%s = %+v, want running", app, states[app])
		}
	}
	if n := <-peak; n != 1 {
		t.Errorf("%d runs at once, want 1", n)
	}
	close(release)
	for _, app := range []string{"reddit", "folo", "x"} {
		if j := settled(t, sum, app); j.State != "done" {
			t.Errorf("%s settled as %+v, want done", app, j)
		}
	}

	// Asking again for one already going changes nothing: the tap that started it
	// said the same thing. Queued without a worker, so what the queue holds is
	// the whole of what was asked for.
	idle := newSummarizer(cache)
	for i := 0; i < 3; i++ {
		if err := idle.start("reddit", "", "en", false); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(idle.queue); n != 1 {
		t.Errorf("queue holds %d, want asking again not to have doubled it", n)
	}

	// The bound is against a stuck queue, not something anyone should meet.
	for i := 0; i < summaryQueue; i++ {
		_ = idle.start("app"+strconv.Itoa(i), "", "en", false)
	}
	if err := idle.start("one too many", "", "en", false); err == nil {
		t.Error("a full queue should say so rather than growing forever")
	}
}

func TestSummarizeRefusals(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "x", ID: "1", Title: "one"}}, time.Now())
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "", errors.New("codex failed: stream error: unknown model")
	})

	if rec := post(t, sum, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no app status = %d, want 400", rec.Code)
	}
	// A chip should not spin for a minute to be told there was nothing behind it.
	if rec := post(t, sum, "app=douban"); rec.Code != http.StatusNotFound {
		t.Errorf("empty backlog status = %d, want 404", rec.Code)
	}

	// What went wrong is kept on the job, so the page that polls it can say so
	// rather than leaving a chip spinning forever.
	if rec := post(t, sum, "app=x"); rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	j := settled(t, sum, "x")
	if j.State != "failed" || !strings.Contains(j.Err, "unknown model") {
		t.Errorf("job = %+v, want the failure and its reason", j)
	}
}

// The whole backlog, however deep: a briefing covering only the newest slice
// would have a hole in it that nothing on the page could tell you about.
func TestSummaryItemsTakesTheWholeBacklog(t *testing.T) {
	now := time.Now()
	var backlog []core.Item
	for i := 0; i < 640; i++ {
		backlog = append(backlog, core.Item{
			App: "inoreader", ID: strconv.Itoa(i), Title: "post " + strconv.Itoa(i),
			At: now.Add(-time.Duration(i) * time.Minute), // 0 is the newest
		})
	}
	backlog = append(backlog, core.Item{App: "folo", ID: "elsewhere", Title: "another source"})

	got, _ := summaryItems(backlog, "inoreader", false)
	if len(got) != 640 {
		t.Fatalf("items = %d, want all 640", len(got))
	}
	// Publication order, so the briefing reads as a sequence.
	if got[0].ID != "639" || got[len(got)-1].ID != "0" {
		t.Errorf("runs %s..%s, want oldest (639) to newest (0)", got[0].ID, got[len(got)-1].ID)
	}
	for _, it := range got {
		if it.App != "inoreader" {
			t.Fatalf("%s leaked into another source's briefing", it.App)
		}
	}
}

func TestSummaryPromptShapesEachItem(t *testing.T) {
	items := []core.Item{{
		App: "x", ID: "42", Source: "@someone", Author: "Some One", Age: "3h",
		Title: "the whole post as its own title", Body: "the whole post as its own title",
		Quote: &core.Quote{Source: "@else", Text: "quoted bit"},
	}, {
		App: "x", ID: "43", Title: "a title", Body: strings.Repeat("x", summaryBodyRunes+40),
		Video: "https://x/v.mp4",
	}}
	p := summaryPrompt("x", "en", items)

	if !strings.Contains(p, "2 unread items follow, oldest first") {
		t.Errorf("prompt should say how much it covers:\n%s", p)
	}
	// An x post is its own title; printing it twice would waste the budget.
	if strings.Count(p, "the whole post as its own title") != 1 {
		t.Errorf("a post that is all body should appear once:\n%s", p)
	}
	if !strings.Contains(p, "quoted bit") {
		t.Error("the post a card embeds is part of what it says")
	}
	if !strings.Contains(p, "carries: video") {
		t.Error("what an item carries is worth knowing before opening it")
	}
	if !strings.Contains(p, " […]") {
		t.Errorf("a long body should be clipped:\n%s", p)
	}
	if strings.Contains(p, strings.Repeat("x", summaryBodyRunes+1)) {
		t.Error("the clip should hold")
	}
}

// Which language a briefing comes back in is the browser's setting, sent with
// the ask, and recorded on the job so the page can tell a briefing in the
// language it is set to now from one in the language it has left.
func TestSummarizeLanguage(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "x", ID: "1", Title: "一条中文推文"}}, time.Now())

	prompts := make(chan string, 1)
	sum := testSummarizer(t, cache, func(_ context.Context, p string) (string, error) {
		prompts <- p
		return "## 概要", nil
	})

	rec := post(t, sum, "app=x&lang=zh")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	// The answer to the ask says which language it is being written in, so a chip
	// is never spinning on a run it cannot account for.
	var started summaryJob
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.Lang != "zh" {
		t.Errorf("started = %+v, want lang zh", started)
	}
	if j := settled(t, sum, "x"); j.Lang != "zh" || j.State != "done" {
		t.Errorf("job = %+v, want a finished Chinese briefing", j)
	}
	if p := <-prompts; !strings.Contains(p, "Simplified Chinese") {
		t.Errorf("prompt should ask for the language picked:\n%s", p)
	}

	// English is what a request naming no language gets, and what an unknown one
	// falls back to: a briefing in the wrong language beats an error where a
	// briefing was asked for.
	for _, form := range []string{"app=x", "app=x&lang=", "app=x&lang=klingon"} {
		sum := testSummarizer(t, cache, func(_ context.Context, p string) (string, error) {
			prompts <- p
			return "## summary", nil
		})
		if rec := post(t, sum, form); rec.Code != http.StatusAccepted {
			t.Fatalf("%s = %d", form, rec.Code)
		}
		if j := settled(t, sum, "x"); j.Lang != summaryLangDefault {
			t.Errorf("%s: lang = %q, want %q", form, j.Lang, summaryLangDefault)
		}
		if p := <-prompts; !strings.Contains(p, "Write in English") {
			t.Errorf("%s: prompt should ask for English:\n%s", form, p)
		}
	}
}

// Whichever language it is written in, the citations have to stay findable and
// readable: one to a row, wearing a label rather than its own URL. A row that
// carries four links is a row nobody can tell apart, and a label that is the
// page path is a link that says nothing about where it goes — both of which the
// model does by default unless told not to.
func TestSummaryPromptShapesTheCitations(t *testing.T) {
	items := []core.Item{{App: "x", ID: "1", Source: "@someone", Title: "a headline"}}
	for _, lang := range []string{"en", "zh"} {
		p := summaryPrompt("x", lang, items)
		for _, want := range []string{
			"Leave titles, names and @handles as they are written",
			"Never put more than one item in a\n  bullet",
			"One link to a\n  bullet",
			"never the page value, never an id,\n  never a bare URL",
		} {
			if !strings.Contains(p, want) {
				t.Errorf("%s: prompt is missing %q:\n%s", lang, want, p)
			}
		}
		if !strings.Contains(p, summaryLangs[lang]) {
			t.Errorf("%s: prompt should carry that language's instruction", lang)
		}
	}
}

// What went wrong has to survive the trip to a toast: the CLI's own words about
// it, not the exit status it dressed them in.
func TestCodexTrouble(t *testing.T) {
	fail := errors.New("exit status 1")
	for _, tc := range []struct {
		name, log, want string
	}{
		{"the line that names it", "workdir: /tmp\nERROR: stream disconnected\ntokens used 400", "ERROR: stream disconnected"},
		{"its last words otherwise", "workdir: /tmp\nnot logged in: run codex login\n", "not logged in: run codex login"},
		{"the exit status when it said nothing", "  \n", "exit status 1"},
	} {
		if got := codexTrouble(tc.log, fail); got != tc.want {
			t.Errorf("%s: codexTrouble = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := codexTrouble(strings.Repeat("z", 400), fail); len([]rune(got)) != 204 {
		t.Errorf("a toast cannot hold a whole log: %d runes", len([]rune(got)))
	}
}

// Every source with a backlog carries the icon that asks for a briefing of it,
// and the source picked also carries the panel the briefing goes in.
func TestSummarizeIconOnEverySourceWithABacklog(t *testing.T) {
	items := []core.Item{
		{App: "reddit", ID: "1", Title: "one", Source: "r/golang"},
		{App: "folo", ID: "2", Title: "two"},
	}
	apps := []string{"reddit", "folo", "douban"}

	whole := renderInput(t, pageInput{items: items, total: 2, apps: apps, now: time.Now()})
	for _, app := range []string{"reddit", "folo"} {
		if !strings.Contains(whole, `data-app="`+app+`"`) {
			t.Errorf("%s has a backlog and should offer a briefing of it:\n%s", app, whole)
		}
	}
	// A chip at zero is still drawn as that service's status light, but there is
	// nothing behind it to brief anybody on.
	if strings.Contains(whole, `id="fsum-douban"`) {
		t.Error("nothing unread is nothing to summarize")
	}
	// The whole feed is a chip of its own now, first in the row, and its cards
	// are what its own briefing goes in place of.
	if !strings.Contains(whole, `<div class="summary" id="summary" data-app="all" data-open="0">`) {
		t.Errorf("the unpicked feed should hold the whole feed's panel, shut:\n%s", whole)
	}
	if !strings.Contains(whole, `id="fsum-all"`) {
		t.Error("the all chip should offer a briefing of everything")
	}

	picked := renderInput(t, pageInput{
		items: items[:1], total: 1, apps: apps, now: time.Now(),
		tally: ptr(tallyItems(items)), sel: feedSel{Kind: "app", Key: "reddit"},
	})
	if !strings.Contains(picked, `<div class="summary" id="summary" data-app="reddit" data-open="0">`) {
		t.Errorf("the picked source's page should hold the panel, shut:\n%s", picked)
	}
	// Every source's icon is still on the row, so a second briefing can be asked
	// for from here without going back to the whole list first.
	if !strings.Contains(picked, `id="fsum-folo"`) {
		t.Error("the chips still offer every source's briefing under a pick")
	}

	// ...and ?summary=1 is the link a finished icon sends you to.
	opened := renderInput(t, pageInput{
		items: items[:1], total: 1, apps: apps, now: time.Now(),
		tally: ptr(tallyItems(items)), sel: feedSel{Kind: "app", Key: "reddit"}, summaryOpen: true,
	})
	if !strings.Contains(opened, `data-open="1"`) {
		t.Error("?summary=1 should open the briefing on arrival")
	}

	// The saved list is read off disk and holds nothing unread, so nothing there
	// is a backlog to summarize.
	saved := renderInput(t, pageInput{
		items: items, total: 2, now: time.Now(), savedView: true,
	})
	if strings.Contains(saved, "button class=\"fsum\"") || strings.Contains(saved, `id="summary"`) {
		t.Error("the saved list holds nothing unread to summarize")
	}
}

// Having read a briefing is a reason to be done with the backlog behind it, so
// mark-all is the one control the briefing does not hide — and while it is open
// the button is about that source, not about everything unread.
func TestSummaryKeepsMarkAllScopedToTheSource(t *testing.T) {
	page := renderInput(t, pageInput{
		items: []core.Item{{App: "reddit", ID: "1", Title: "one"}}, total: 12,
		apps: []string{"reddit", "folo"}, now: time.Now(),
		sel: feedSel{Kind: "app", Key: "reddit"}, summaryOpen: true,
	})
	if strings.Contains(page, "body.sumon #markAll") {
		t.Error("the briefing must not hide mark-all")
	}
	if !strings.Contains(page, `return SUMMARY_ON && app ? ' in ' + app : ' in this feed';`) {
		t.Error("mark-all should name the source whose briefing is open")
	}
	if !strings.Contains(page, "document.body.classList.toggle('sumon', on);\n    labelMarkAll();") {
		t.Error("opening or closing the briefing should relabel mark-all")
	}
	// The request itself is unchanged: it carries the app filter already in the
	// URL, which is the only reason this is one source's backlog and not all of
	// them. A briefing only ever exists under a source pick.
	if !strings.Contains(page, `['app', 'type', 'x', 'sub'].forEach`) {
		t.Error("mark-all should still clear the picked source's backlog only")
	}
}

// A pick is a page of cards, so the briefing flag does not ride along on the
// chips, the sort toggle or the layout toggle.
func TestSummaryFlagDoesNotRideAlong(t *testing.T) {
	q := map[string][]string{"app": {"reddit"}, "summary": {"1"}, "order": {"desc"}}
	for _, tc := range []struct {
		name, got string
	}{
		{"chip", chipHref(q, feedSel{Kind: "app", Key: "folo"})},
		{"clear", chipHref(q, feedSel{})},
		{"order", orderHref(q, false)},
		{"deck", deckHref(q, false)},
	} {
		if strings.Contains(tc.got, "summary") {
			t.Errorf("%s href = %q, should land on the cards", tc.name, tc.got)
		}
	}
}

func ptr(t feedTally) *feedTally { return &t }

// hnItem is a Hacker News story as Inoreader hands it over: a stub pointing at
// the article and at the thread.
func hnItem() core.Item {
	return core.Item{
		App: "inoreader", ID: "77", Source: "Hacker News: Best",
		Title: "Tmp.0ut Volume 5",
		URL:   "https://tmpout.sh/5/",
		Body:  "Article URL: https://tmpout.sh/5/\n\nComments URL: https://news.ycombinator.com/item?id=49516059",
	}
}

// testItemSummarizer is the real thing with both subprocesses gone: no codex,
// and no trip to Hacker News.
func testItemSummarizer(t *testing.T, cache *feedCache, thread hnThread, threadErr error, codex func(context.Context, string) (string, error)) *summarizer {
	t.Helper()
	sum := testSummarizer(t, cache, codex)
	sum.hn = func(_ context.Context, ref hnRef) (hnThread, error) {
		thread.Ref = ref
		return thread, threadErr
	}
	return sum
}

func settledKey(t *testing.T, sum *summarizer, key string) summaryJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := sum.job(key); ok && j.State != "running" {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s never settled", key)
	return summaryJob{}
}

// A Hacker News card carries half the item: the discussion under it is the other
// half, and the button in its footer is what goes and gets it.
func TestSummarizeOneItemsDiscussion(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{hnItem()}, now)

	thread := hnThreadOf(hnRef{}, hnAPIItem{
		Title: strptr("Tmp.0ut Volume 5"), URL: strptr("https://tmpout.sh/5/"), Points: intptr(191),
		Children: []hnAPIItem{{Author: strptr("alice"), Text: strptr("<p>worth reading</p>")}},
	})
	prompts := make(chan string, 1)
	sum := testItemSummarizer(t, cache, thread, nil, func(_ context.Context, p string) (string, error) {
		prompts <- p
		return "The thread is mostly about zines.\n\n- alice liked it", nil
	})

	rec := post(t, sum, "app=inoreader&id=77")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", rec.Code, rec.Body.String())
	}

	key := summaryKey("inoreader", "77")
	if j := settledKey(t, sum, key); j.State != "done" {
		t.Fatalf("job = %+v, want done", j)
	}
	rec = get(t, sum, "app=inoreader&id=77")
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch = %d %s", rec.Code, rec.Body.String())
	}
	var got summaryJob
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// The count is the comments it read, not a backlog, and nothing can overtake
	// a discussion the way a sweep overtakes a briefing.
	if got.Count != 1 || got.New != 0 || got.Generated == "" {
		t.Errorf("job = %+v, want one comment read and no overtaking", got)
	}
	if !strings.Contains(got.HTML, "<li>alice liked it</li>") {
		t.Errorf("html = %q, want the rendered Markdown", got.HTML)
	}
	select {
	case p := <-prompts:
		if !strings.Contains(p, "worth reading") || !strings.Contains(p, "Story: Tmp.0ut Volume 5") {
			t.Errorf("prompt = %q, want the thread in it", p)
		}
	default:
		t.Error("codex was never asked")
	}

	// The source's own briefing is a separate job under a separate key: asking
	// about one must not answer with the other.
	if _, ok := sum.job("inoreader"); ok {
		t.Error("an item's briefing must not stand in for its source's")
	}
}

// Only Hacker News, for now, and only items that are still somewhere: neither
// should cost a spinner and a minute to find out.
func TestSummarizeItemRefusesWhatItCannotRead(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{
		hnItem(),
		{App: "reddit", ID: "1", Title: "not hacker news", Source: "r/golang"},
	}, now)
	sum := testItemSummarizer(t, cache, hnThread{}, nil, func(context.Context, string) (string, error) {
		t.Error("codex should not have been asked")
		return "", nil
	})

	if rec := post(t, sum, "app=reddit&id=1"); rec.Code != http.StatusBadRequest {
		t.Errorf("a reddit item = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if rec := post(t, sum, "app=inoreader&id=nope"); rec.Code != http.StatusNotFound {
		t.Errorf("a missing item = %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// A "best comment" that nobody answered has no discussion under it, and saying
// so is better than a briefing that reads the comment back to you.
func TestSummarizeItemSaysWhenNobodyReplied(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{{
		App: "inoreader", ID: "88", Source: "Hacker News: Best Comments",
		Title: `New comment by zahlman in "A story"`,
		URL:   "https://news.ycombinator.com/item?id=49526135",
		Body:  "a comment nobody answered",
	}}, now)
	sum := testItemSummarizer(t, cache, hnThread{}, nil, func(context.Context, string) (string, error) {
		t.Error("codex should not have been asked")
		return "", nil
	})

	if rec := post(t, sum, "app=inoreader&id=88"); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", rec.Code, rec.Body.String())
	}
	j := settledKey(t, sum, summaryKey("inoreader", "88"))
	if j.State != "failed" || !strings.Contains(j.Err, "replied") {
		t.Errorf("job = %+v, want a failure saying nobody replied", j)
	}
}

// Hacker News being down is the run's failure, in its own words, not a silent
// briefing over nothing.
func TestSummarizeItemCarriesTheFetchFailure(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{hnItem()}, time.Now())
	sum := testItemSummarizer(t, cache, hnThread{}, errors.New("could not reach Hacker News"), func(context.Context, string) (string, error) {
		t.Error("codex should not have been asked")
		return "", nil
	})

	post(t, sum, "app=inoreader&id=77")
	j := settledKey(t, sum, summaryKey("inoreader", "77"))
	if j.State != "failed" || !strings.Contains(j.Err, "could not reach Hacker News") {
		t.Errorf("job = %+v, want the fetch's own words", j)
	}
}

// The whole feed is a briefing like any other source's, and the only one that
// can say what the day was about rather than what one service's day was about.
func TestSummarizeTheWholeFeed(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "go 1.30 is out", Source: "r/golang", At: now.Add(-2 * time.Hour)},
		{App: "folo", ID: "2", Title: "a release post", At: now.Add(-time.Hour)},
		{App: "x", ID: "3", Body: "shipping it", Source: "@someone", At: now.Add(-30 * time.Minute)},
	}, now)

	prompts := make(chan string, 1)
	sum := testSummarizer(t, cache, func(_ context.Context, p string) (string, error) {
		prompts <- p
		return "## releases\n\n- [go 1.30](/item?app=reddit&id=1) landed", nil
	})

	if rec := post(t, sum, "app=all"); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", rec.Code, rec.Body.String())
	}
	j := settled(t, sum, "all")
	if j.State != "done" || j.Count != 3 {
		t.Fatalf("job = %+v, want a briefing over all three", j)
	}
	p := <-prompts
	// Every source at once, oldest first, and told to read it as one feed rather
	// than a section per service.
	for _, want := range []string{
		"Every source at once: the whole feed. 3 unread items follow",
		"This batch is every source at once.",
		"go 1.30 is out", "a release post", "shipping it",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}

	// Overtaken by any source, not just one: a sweep that lands x's items leaves
	// a whole-feed briefing as far behind as one that lands reddit's.
	cache.upsert([]core.Item{{App: "douban", ID: "9", Title: "later", At: now}}, now.Add(time.Minute))
	if got, _ := sum.job("all"); got.New != 1 {
		t.Errorf("new = %d, want the item that arrived after it", got.New)
	}
}

// Ids collide between services, so a whole-feed briefing has to remember which
// service each one came from or an unread item is counted as one it read.
func TestWholeFeedIsNotOvertakenByAnIdItAlreadyRead(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{
		{App: "reddit", ID: "1", Title: "reddit's one", At: now.Add(-time.Hour)},
		{App: "folo", ID: "1", Title: "folo's one", At: now.Add(-time.Hour)},
	}, now)
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "read", nil
	})
	post(t, sum, "app=all")
	if j := settled(t, sum, "all"); j.State != "done" || j.Count != 2 || j.New != 0 {
		t.Errorf("job = %+v, want both items read and nothing left behind", j)
	}
}

// The whole feed's briefing is the one that is capped, and it takes its slice
// from the end the page is reading from: told about the items you are about to
// reach, not the ones you will not get to for days.
func TestWholeFeedBriefingIsCappedFromTheEndYouRead(t *testing.T) {
	now := time.Now()
	var backlog []core.Item
	for i := 0; i < summaryAllCap+40; i++ {
		backlog = append(backlog, core.Item{
			App: "reddit", ID: strconv.Itoa(i), Title: "item " + strconv.Itoa(i),
			At: now.Add(-time.Duration(i) * time.Minute), // 0 is newest
		})
	}

	// Newest first, which is what the feed reads as by default.
	got, deep := summaryItems(backlog, allApp, false)
	if len(got) != summaryAllCap || deep != summaryAllCap+40 {
		t.Fatalf("read %d of %d, want %d of %d", len(got), deep, summaryAllCap, summaryAllCap+40)
	}
	// The slice is the newest ones, and it still reads oldest first inside.
	if got[0].ID != strconv.Itoa(summaryAllCap-1) || got[len(got)-1].ID != "0" {
		t.Errorf("runs %s..%s, want the newest %d oldest-first", got[0].ID, got[len(got)-1].ID, summaryAllCap)
	}

	// Oldest first, and the other end of the same backlog is the one it reads.
	got, _ = summaryItems(backlog, allApp, true)
	if got[0].ID != strconv.Itoa(summaryAllCap+39) || got[len(got)-1].ID != "40" {
		t.Errorf("runs %s..%s, want the oldest %d oldest-first", got[0].ID, got[len(got)-1].ID, summaryAllCap)
	}

	// A source's own backlog is left whole: one service's day is not the sum of
	// every service's, and it is the sum that needed a bound.
	whole, _ := summaryItems(backlog, "reddit", false)
	if len(whole) != summaryAllCap+40 {
		t.Errorf("a source read %d items, want all %d", len(whole), summaryAllCap+40)
	}
}

// ...and the job says so, so the panel can put "200 of 240" over the prose.
func TestCappedBriefingSaysHowDeepTheBacklogWas(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	var items []core.Item
	for i := 0; i < summaryAllCap+3; i++ {
		items = append(items, core.Item{App: "reddit", ID: strconv.Itoa(i), Title: "t", At: now.Add(-time.Duration(i) * time.Minute)})
	}
	cache.upsert(items, now)
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) { return "a briefing", nil })

	post(t, sum, "app=all")
	j := settled(t, sum, allApp)
	if j.Count != summaryAllCap || j.Backlog != summaryAllCap+3 {
		t.Errorf("job = %+v, want %d of %d", j, summaryAllCap, summaryAllCap+3)
	}
	// A briefing that read everything has nothing to say about it.
	post(t, sum, "app=reddit")
	if r := settled(t, sum, "reddit"); r.Backlog != 0 {
		t.Errorf("backlog = %d, want 0 when nothing was left out", r.Backlog)
	}
}
