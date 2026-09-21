package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// testSifter is the real thing with the model call replaced: a test has no
// business spending an API key to find out what a handler does with an answer.
func testSifter(t *testing.T, cache *feedCache, judge func(context.Context, core.Item, []string) (siftVerdict, error)) *sifter {
	t.Helper()
	t.Setenv(typesafeKey, "test-key")
	s := newSifter(cache, &interestStore{}, &mishapLog{})
	s.judge = judge
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.serve(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	return s
}

// worthOf answers with a fixed yes/no number per item id and a rung derived
// from it, which is every judgment most of these tests need to describe.
func worthOf(scores map[string]float64) func(context.Context, core.Item, []string) (siftVerdict, error) {
	return func(_ context.Context, it core.Item, _ []string) (siftVerdict, error) {
		w := scores[it.ID]
		return siftVerdict{Worth: w, Rank: siftRankOf(w * float64(len(siftLevels)-1)), Interest: -1}, nil
	}
}

func settledSift(t *testing.T, s *sifter) siftJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		job := s.state()
		if job.State != "running" && job.State != "" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("sift never settled: %+v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The whole of what a sift does: an item under the cut leaves the feed, the
// count, and everything else that reads the backlog — without being marked read
// and without being deleted.
func TestSiftTakesTheJudgedOutOfTheFeed(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "worth it"), item("x", "2", "gm"), item("x", "3", "also worth it")}, now)

	s := testSifter(t, c, worthOf(map[string]float64{"1": 0.9, "2": 0.02, "3": 0.5}))
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	job := settledSift(t, s)
	if job.State != "done" || job.Done != 3 || job.Aside != 1 {
		t.Fatalf("job = %+v, want done 3 judged 1 aside", job)
	}
	if got := c.unreadCount(); got != 2 {
		t.Fatalf("unread is %d, want 2", got)
	}
	if got := c.skippedCount(); got != 1 {
		t.Fatalf("skipped is %d, want 1", got)
	}
	aside, worth := c.skipped(now)
	if len(aside) != 1 || aside[0].ID != "2" {
		t.Fatalf("set aside %+v, want the one under the cut", aside)
	}
	if got := worth[core.Key("x", "2")]; got != 0.02 {
		t.Fatalf("worth = %v, want the model's own number", got)
	}
	// Set aside is not read: the item is still there to be disagreed with.
	if _, ok := c.item("x", "2", now); !ok {
		t.Error("a skipped item should still be in the cache")
	}
	for _, it := range c.unread(now, "") {
		if it.ID == "2" {
			t.Error("a skipped item is still in the backlog the feed reads")
		}
	}
}

// A fetch sifts itself. Nobody taps anything here: the sweeper asks at the end
// of a sweep, so the feed that comes out of a fetch is already judged.
func TestASweepSiftsWhatItBrought(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "worth it"), item("x", "2", "gm")}, now)
	s := testSifter(t, c, worthOf(map[string]float64{"1": 0.9, "2": 0.02}))

	sw := newSweeper(t.TempDir(), c, nil, nil, false, 0)
	sw.mark = (&fakeMark{}).fn
	sw.fetch = func(context.Context, string, int, time.Time) ([]core.Item, bool, error) {
		return nil, false, nil
	}
	sw.sift = s.auto
	sw.sweep(context.Background(), true)

	if job := settledSift(t, s); job.State != "done" || job.Done != 2 || job.Aside != 1 {
		t.Fatalf("job = %+v, want a run of 2 with 1 set aside", job)
	}
	if got := c.unreadCount(); got != 1 {
		t.Fatalf("unread is %d, want the one the sweep's own sift left", got)
	}
	// And the next sweep has nothing to spend tokens on.
	sw.sweep(context.Background(), true)
	if job := settledSift(t, s); job.Done != 2 {
		t.Fatalf("job = %+v, want the second sweep to judge nothing again", job)
	}
}

// A sweep with no key is silent rather than a failed run on the button: the
// sweeper asks every quarter of an hour, and an answer nobody asked for should
// not be the first thing a page load reports.
func TestAutoSiftWithoutAKeyStaysQuiet(t *testing.T) {
	c := newTestCache(t)
	c.upsert([]core.Item{item("x", "1", "one")}, time.Now())
	s := testSifter(t, c, worthOf(nil))
	t.Setenv(typesafeKey, "")

	s.auto()
	if job := s.state(); job.State != "" {
		t.Fatalf("job = %+v, want no run at all", job)
	}
	if got := c.unjudgedCount(); got != 1 {
		t.Fatalf("unjudged is %d, want the item left for a run that can happen", got)
	}
}

// A second run is for what has arrived since, not for what has already been
// judged: the judgment is kept per item, so the tokens are spent once.
func TestSiftJudgesOnlyWhatIsUnjudged(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "one"), item("x", "2", "two")}, now)

	var mu sync.Mutex
	var asked []string
	s := testSifter(t, c, func(_ context.Context, it core.Item, _ []string) (siftVerdict, error) {
		mu.Lock()
		asked = append(asked, it.ID)
		mu.Unlock()
		return siftVerdict{Worth: 0.9, Rank: 2, Interest: -1}, nil
	})
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	settledSift(t, s)

	c.upsert([]core.Item{item("x", "3", "three")}, now)
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	settledSift(t, s)

	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 3 || asked[2] != "3" {
		t.Fatalf("asked about %v, want the second run to ask about the new item alone", asked)
	}
}

// A model that will not answer is the same answer for every batch behind it, so
// the run stops and says so — keeping whatever had already landed.
func TestSiftStopsAtTheFirstFailure(t *testing.T) {
	c := newTestCache(t)
	c.upsert([]core.Item{item("x", "1", "one")}, time.Now())
	s := testSifter(t, c, func(context.Context, core.Item, []string) (siftVerdict, error) {
		return siftVerdict{}, errors.New("typesafe said 401 Unauthorized: bad key")
	})
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	job := settledSift(t, s)
	if job.State != "failed" || !strings.Contains(job.Err, "401") {
		t.Fatalf("job = %+v, want the service's own words", job)
	}
	if got := c.unjudgedCount(); got != 1 {
		t.Fatalf("unjudged is %d: a failed run must leave the item for the next one", got)
	}
	// ...and on the banner, since nearly every sift is one a fetch asked for
	// with no button being watched.
	m, ok := s.mishaps.latest()
	if !ok || m.Kind != "sift" || !strings.Contains(m.Msg, "401") {
		t.Errorf("mishap = %+v, want the sift's own failure", m)
	}
}

// Nothing to do and no key are both refusals up front rather than a run that
// spins for a minute to say the same thing.
func TestSiftRefusesWhatItCannotDo(t *testing.T) {
	c := newTestCache(t)
	s := testSifter(t, c, worthOf(nil))
	if err := s.start(); err == nil || !strings.Contains(err.Error(), "judged already") {
		t.Fatalf("empty backlog: err = %v, want a refusal", err)
	}
	c.upsert([]core.Item{item("x", "1", "one")}, time.Now())
	t.Setenv(typesafeKey, "")
	if err := s.start(); err == nil || !strings.Contains(err.Error(), typesafeKey) {
		t.Fatalf("no key: err = %v, want a refusal naming the variable", err)
	}
}

// One item to a request, and every question asked of that one item: the state
// is read once per request either way, so a batch shared the reading of
// twenty-five items rather than the reading of one, and paid for it in every
// answer (see typesafeJudge).
func TestSiftRequestCarriesOneItem(t *testing.T) {
	body := siftRequest(core.Item{
		App: "x", ID: "1", Body: "a post with no title of its own", Source: "@someone",
		URL: "https://example.com",
	}, nil)
	if body.Model != typesafeModel {
		t.Fatalf("model = %q", body.Model)
	}
	if len(body.Questions) != 2 {
		t.Fatalf("%d questions, want the cut and the ladder", len(body.Questions))
	}
	if q := body.Questions[siftAskID]; q.Type != "noul" {
		t.Errorf("the cut = %+v, want a noul", q)
	}
	r := body.Questions[siftRankID]
	if r.Type != "score" {
		t.Fatalf("the ladder = %+v, want a score", r)
	}
	rungs, ok := r.Criteria.([]string)
	if !ok || len(rungs) != len(siftLevels) {
		t.Fatalf("rungs = %+v, want one description per level, lowest first", r.Criteria)
	}
	// Nothing to index into and nothing to number: the item is the state.
	state, ok := body.State.(siftState)
	if !ok {
		t.Fatalf("state = %T, want one item", body.State)
	}
	for _, q := range body.Questions {
		if strings.Contains(q.Instructions, "items[") {
			t.Errorf("a question should not point at an index: %q", q.Instructions)
		}
	}
	// An x post is all body; its title is that body over again and is not sent
	// twice.
	if state.Item.Title != "" {
		t.Errorf("x item carried a title of %q", state.Item.Title)
	}
	if state.Item.Text == "" {
		t.Error("x item lost its text")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	// Neither is anything to judge, and both would be spent on every item.
	if strings.Contains(string(raw), "example.com") {
		t.Error("the request should not carry the item's URL")
	}
}

// The status route is what the button polls: how far through, and what a run
// started now would have to get through.
func TestSiftStatusReportsProgress(t *testing.T) {
	c := newTestCache(t)
	c.upsert([]core.Item{item("x", "1", "one"), item("x", "2", "two")}, time.Now())
	s := testSifter(t, c, worthOf(map[string]float64{"1": 0.9, "2": 0.01}))

	rec := httptest.NewRecorder()
	showSift(rec, s)
	var before struct {
		siftJob
		Left    int `json:"left"`
		Skipped int `json:"skipped"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatal(err)
	}
	if before.Left != 2 || before.Skipped != 0 {
		t.Fatalf("before = %+v, want two left and none set aside", before)
	}

	rec = httptest.NewRecorder()
	startSift(rec, s)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /sift = %d, want 202", rec.Code)
	}
	settledSift(t, s)

	rec = httptest.NewRecorder()
	showSift(rec, s)
	var after struct {
		siftJob
		Left    int `json:"left"`
		Skipped int `json:"skipped"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if after.State != "done" || after.Left != 0 || after.Skipped != 1 {
		t.Fatalf("after = %+v, want a finished run with one set aside", after)
	}
}

// The skipped view is read the way the feed is read — full cards, the deck, the
// sort toggle, mark-all — because that is the only way a judgment gets checked.
// What it does not get is a briefing: it is the part you have been told to skip.
func TestSkippedViewReadsLikeTheFeed(t *testing.T) {
	in := pageInput{
		items: []core.Item{{App: "x", ID: "1", Title: "set aside"}}, total: 1,
		apps: []string{"x"}, now: time.Now(), skippedView: true,
		worth: map[string]float64{core.Key("x", "1"): 0.08},
	}
	page := renderInput(t, in)
	if !strings.Contains(page, `data-skippedview="true"`) {
		t.Error("the page should say which view it is")
	}
	if !strings.Contains(page, `id="markAll"`) {
		t.Error("the skipped pile should be clearable in one go")
	}
	if !strings.Contains(page, `id="sortlink"`) {
		t.Error("the skipped pile should sort like the feed")
	}
	if strings.Contains(page, `id="summary"`) {
		t.Error("the skipped pile must not offer a briefing of itself")
	}
	if strings.Contains(page, `id="siftBtn"`) {
		t.Error("a sift is started from the feed, not from its own leavings")
	}
	// The number that put the card here, on the card.
	if !strings.Contains(page, `>0.08<`) {
		t.Error("a skipped card should wear the worth it was given")
	}
}

// Best first is the other half of the sift: the low scorers are gone from the
// feed, and this puts what is left in the order the model would read it.
func TestBestFirstOrdersTheFeedByWorth(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	items := []core.Item{
		{App: "x", ID: "middling", At: old},
		{App: "x", ID: "best", At: time.Now()},
		{App: "x", ID: "unjudged", At: old},
		{App: "x", ID: "dull", At: old},
	}
	worth := map[string]float64{
		core.Key("x", "middling"): 0.55,
		core.Key("x", "best"):     0.94,
		core.Key("x", "dull"):     0.3,
	}
	sortFeed(items, orderBest, worth)
	var got []string
	for _, it := range items {
		got = append(got, it.ID)
	}
	// The unjudged one sits where "no answer yet" belongs: among the middling,
	// not under everything that has been judged.
	want := []string{"best", "middling", "unjudged", "dull"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("best-first order = %v, want %v", got, want)
		}
	}
	// The other two orders are still a clock, worth or no worth.
	sortFeed(items, orderDesc, worth)
	if items[0].ID != "best" || items[3].ID == "best" {
		t.Errorf("newest first should still be newest first: %+v", items)
	}

	// In that order the number is on the card, since it is the reason the card
	// is where it is.
	page := renderInput(t, pageInput{
		items: []core.Item{{App: "x", ID: "best", Title: "a"}}, total: 1,
		apps: []string{"x"}, now: time.Now(), order: orderBest, worth: worth,
	})
	if !strings.Contains(page, `>0.94<`) {
		t.Error("a card in best-first order should wear its worth")
	}
}

// The feed's own header: the pile is a link beside the other two lists, and the
// control that fills it sits with the rest of the header's buttons.
func TestFeedHeaderOffersTheSift(t *testing.T) {
	page := renderInput(t, pageInput{
		items: []core.Item{{App: "x", ID: "1", Title: "one"}}, total: 1,
		apps: []string{"x"}, now: time.Now(),
	})
	if !strings.Contains(page, `href="/?skipped=1"`) {
		t.Error("the header should link to the skipped pile")
	}
	if !strings.Contains(page, `>skipped</a>`) {
		t.Error("the header should name the skipped pile")
	}
	if !strings.Contains(page, `id="siftBtn"`) {
		t.Error("the feed should offer to sift")
	}
	if !strings.Contains(page, `fetch('/sift', {method:'POST'})`) {
		t.Error("the button should ask the server to start a run")
	}
}

// Marking all read in the skipped view clears that pile, not the feed behind it.
// The two are disjoint, and clearing the wrong one is the mistake worth being
// careful about here.
func TestMarkAllInTheSkippedViewClearsThePile(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "keep"), item("x", "2", "aside")}, now)
	c.judge(map[string]siftVerdict{
		core.Key("x", "1"): {Worth: 0.9, Rank: 3, Interest: -1},
		core.Key("x", "2"): {Worth: 0.02, Rank: 1, Interest: -1},
	}, now)

	req := httptest.NewRequest(http.MethodPost, "/mark-all", strings.NewReader("skipped=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleMarkAll(rec, req, c, newMarkFlusher(t.TempDir(), c), newSummarizer(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("mark-all = %d: %s", rec.Code, rec.Body)
	}
	if got := c.skippedCount(); got != 0 {
		t.Fatalf("skipped is %d after clearing the pile", got)
	}
	if got := c.unreadCount(); got != 1 {
		t.Fatalf("unread is %d: the feed behind the pile should be untouched", got)
	}
}

// The ladder as chips: what a sift made of the backlog, as three rungs you can
// stand in. Highest first, because "must read" is the one you came for, and
// picking one lands best first — a dozen items the sift thought highly of are
// not a sequence to work through in the order they happened.
func TestRankChipsStandForTheLadder(t *testing.T) {
	items := []core.Item{
		{App: "x", ID: "1", Title: "a", Rank: 3},
		{App: "x", ID: "2", Title: "b", Rank: 2},
		{App: "x", ID: "3", Title: "c", Rank: 2},
		{App: "x", ID: "4", Title: "d"}, // nothing has judged it
	}
	tally := tallyItems(items)
	page := renderInput(t, pageInput{
		items: items, total: len(items), apps: []string{"x"}, now: time.Now(),
		tally: &tally, query: url.Values{},
	})
	if !strings.Contains(page, `data-kind="rank" data-key="must"`) {
		t.Error("the ladder should be a chip group of its own")
	}
	if !strings.Contains(page, `<span>must read</span><span class="fn">1</span>`) {
		t.Errorf("a rung should count what stands on it: %s", page)
	}
	if !strings.Contains(page, `<span>worth a click</span><span class="fn">2</span>`) {
		t.Error("two items on the middle rung should say so")
	}
	// An unjudged item is on no rung, and a rung with nothing on it is not a
	// chip: three zeroes would be a row of nothing.
	if strings.Contains(page, `>skim</span>`) {
		t.Error("an empty rung should not be drawn")
	}
	if !strings.Contains(page, `href="/?order=best&amp;rank=must"`) {
		t.Errorf("picking a rung should land best first: %s", page)
	}

	// ...and picking one narrows to it.
	got := selectItems(items, feedSel{Kind: "rank", Key: "click"})
	if len(got) != 2 || got[0].ID != "2" || got[1].ID != "3" {
		t.Fatalf("the middle rung holds %+v, want items 2 and 3", got)
	}
	if n := selectItems(items, feedSel{Kind: "rank", Key: "skim"}); len(n) != 0 {
		t.Fatalf("nothing is on the bottom rung, got %+v", n)
	}

	// The ladder leads the order, and the yes/no sorts a rung's own items.
	feed := []core.Item{
		{App: "x", ID: "low-but-certain", Rank: 1},
		{App: "x", ID: "middle", Rank: 2},
		{App: "x", ID: "top", Rank: 3},
	}
	worth := map[string]float64{
		core.Key("x", "low-but-certain"): 0.99,
		core.Key("x", "middle"):          0.5,
		core.Key("x", "top"):             0.6,
	}
	sortFeed(feed, orderBest, worth)
	if feed[0].ID != "top" || feed[2].ID != "low-but-certain" {
		t.Fatalf("best first should lead with the ladder, not the probability: %+v", feed)
	}
}

// The ladder is only asked about once, but it is asked about everything: a row
// judged before the rungs existed carries a worth and no rung, which is half an
// answer, and the next run asks it again rather than filing it on rung zero.
func TestHalfJudgedItemsAreAskedAgain(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "one")}, now)
	e := c.byKey[core.Key("x", "1")]
	e.JudgedAt, e.Worth = now.UTC().Format(time.RFC3339), 0.8 // as an older run left it

	if got := c.unjudgedCount(); got != 1 {
		t.Fatalf("unjudged is %d: an item with no rung has not been judged", got)
	}
	s := testSifter(t, c, worthOf(map[string]float64{"1": 0.8}))
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	settledSift(t, s)
	if got := c.byKey[core.Key("x", "1")].Rank; got < 1 {
		t.Fatalf("rank is %d after a run, want a rung", got)
	}
}
