package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// The list is what the reader typed, tidied: one subject per line, blanks and
// repeats dropped, and bounded — the whole of it rides in every sift request.
func TestInterestsParseWhatWasTyped(t *testing.T) {
	got := parseInterests("  the Fed's rate path \n\nanything about Zig\n\nThe FED'S RATE PATH\n")
	if len(got) != 2 || got[0] != "the Fed's rate path" || got[1] != "anything about Zig" {
		t.Fatalf("parsed %q", got)
	}
	long := strings.Repeat("x", maxInterestLen+50)
	if n := len([]rune(parseInterests(long)[0])); n != maxInterestLen {
		t.Errorf("a line of %d runes, want it clipped to %d", n, maxInterestLen)
	}
	many := strings.Repeat("a\nb\nc\nd\ne\nf\ng\nh\n", 20) // more distinct lines than the bound
	if n := len(parseInterests(strings.ReplaceAll(many, "a", "z"))); n > maxInterests {
		t.Errorf("kept %d lines, want at most %d", n, maxInterests)
	}
}

// The list is one question with an escape hatch, not a yes/no per subject: a
// probability only means something against the alternatives, and "none of them"
// is the alternative that is true nearly every time.
func TestSiftAsksTheListAsOneChoice(t *testing.T) {
	it := core.Item{App: "hn", ID: "1", Title: "Zig 0.16 released", Body: "the release notes"}
	// No list, no question: its only possible answer would be "none".
	if _, asked := siftRequest(it, nil).Questions[siftInterestID]; asked {
		t.Error("no list, no question")
	}
	list := []string{"anything about Zig", "the Fed's rate path"}
	body := siftRequest(it, list)
	if len(body.Questions) != 3 {
		t.Fatalf("%d questions, want the cut, the ladder and the subject", len(body.Questions))
	}
	q, ok := body.Questions[siftInterestID]
	if !ok || q.Type != "choice" {
		t.Fatalf("interest question = %+v, want a choice", q)
	}
	// One item alone is the whole state, so there is no index to point at.
	one, ok := body.State.(siftState)
	if !ok || one.Item.Title != "Zig 0.16 released" || one.Item.Text != "the release notes" {
		t.Fatalf("state = %+v, want the item on its own", body.State)
	}
	if strings.Contains(q.Instructions, "items[") {
		t.Errorf("nothing to index into when the item is alone: %q", q.Instructions)
	}
	options, ok := q.Criteria.(map[string]string)
	if !ok || len(options) != len(list)+1 {
		t.Fatalf("options = %+v, want one per subject and the escape hatch", q.Criteria)
	}
	for j, subject := range list {
		if options[siftSubjectID(j)] != subject {
			t.Errorf("option %s = %q, want the subject as written", siftSubjectID(j), options[siftSubjectID(j)])
		}
	}
	if options[siftNoSubject] == "" {
		t.Error("there should be a way to answer none of them")
	}
}

// The subject question is asked of one item at a time, and the answers land on
// the batch's verdicts. A rare-event judgment is not safe in company: the same
// item comes back differently depending on what it was batched with.
func TestSubjectIsAskedOfOneItemAtATime(t *testing.T) {
	c := newTestCache(t)
	c.upsert([]core.Item{item("x", "1", "one"), item("x", "2", "two")}, time.Now())
	var alone int
	s := testSifter(t, c, func(_ context.Context, it core.Item, interests []string) (siftVerdict, error) {
		alone++
		v := siftVerdict{Worth: 0.9, Rank: 2}
		if it.ID == "1" {
			v.Interest, v.InterestFor = 0.97, interests[0]
		}
		return v, nil
	})
	if err := s.interests.set("anything about Zig"); err != nil {
		t.Fatal(err)
	}
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	settledSift(t, s)
	if alone != 2 {
		t.Fatalf("asked %d times, want one per item", alone)
	}
	if c.matchedCount() != 1 {
		t.Fatalf("matched %d, want the one the subject caught", c.matchedCount())
	}
	got := c.unread(time.Now(), "")
	for _, it := range got {
		if it.ID == "1" && it.MatchedFor != "anything about Zig" {
			t.Errorf("item 1 = %+v, want the subject on it", it)
		}
	}
}

// Reading the answer back: a subject picked is that subject with the weight it
// was picked at, and anything else is no match at all.
func TestSiftMatchReadsTheChoice(t *testing.T) {
	list := []string{"anything about Zig", "the Fed's rate path"}
	p, subject := siftMatch("s1", map[string]float64{"s1": 0.97, "none": 0.02}, list)
	if p != 0.97 || subject != "the Fed's rate path" {
		t.Errorf("picked (%v, %q), want the second subject", p, subject)
	}
	if p, subject := siftMatch(siftNoSubject, map[string]float64{"none": 1}, list); p != 0 || subject != "" {
		t.Errorf("none of them = (%v, %q), want no match", p, subject)
	}
	// A subject that has since left the list is not a match against the one
	// that took its place.
	if p, subject := siftMatch("s9", map[string]float64{"s9": 1}, list); p != 0 || subject != "" {
		t.Errorf("unknown option = (%v, %q), want no match", p, subject)
	}
}

// Which subject caught an item is kept with it, so a match can be argued with
// rather than just believed.
func TestTheMatchedSubjectRidesWithTheItem(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "Zig 0.16 released")}, now)
	c.judge(map[string]siftVerdict{
		core.Key("x", "1"): {Worth: 0.9, Rank: 2, Interest: 0.95, InterestFor: "anything about Zig"},
	}, now)
	got := c.unread(now, "")
	if len(got) != 1 || got[0].MatchedFor != "anything about Zig" {
		t.Fatalf("item = %+v, want the subject that caught it", got)
	}
	page := renderInput(t, pageInput{
		items: got, total: 1, apps: []string{"x"}, now: now,
		sel: feedSel{Kind: "mine", Key: "mine"}, query: url.Values{"mine": {"1"}},
	})
	if !strings.Contains(page, `title="the subject on your list this answers">anything about Zig<`) {
		t.Errorf("the card should wear the subject: %s", page)
	}
}

// What the reader asked for by name outranks what the sift made of it: a match
// is never skipped, and it gets a chip of its own.
func TestMatchedItemsSurviveTheCutAndGetAChip(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a zig post nobody rates"), item("x", "2", "plain filler")}, now)
	c.judge(map[string]siftVerdict{
		core.Key("x", "1"): {Worth: 0.05, Rank: 1, Interest: 0.95, InterestFor: "zig"},
		core.Key("x", "2"): {Worth: 0.05, Rank: 1, Interest: 0.01},
	}, now)

	if got := c.matchedCount(); got != 1 {
		t.Fatalf("matched is %d, want 1", got)
	}
	if got := c.skippedCount(); got != 1 {
		t.Fatalf("skipped is %d: a match must survive the cut", got)
	}
	var kept []string
	for _, it := range c.unread(now, "") {
		kept = append(kept, it.ID)
		if it.ID == "1" && !it.Matched {
			t.Error("the item should come out of the cache carrying its match")
		}
	}
	if len(kept) != 1 || kept[0] != "1" {
		t.Fatalf("the feed holds %v, want the matched item alone", kept)
	}

	tally := tallyItems(c.unread(now, ""))
	page := renderInput(t, pageInput{
		items: c.unread(now, ""), total: 1, apps: []string{"x"}, now: now,
		tally: &tally, query: url.Values{},
	})
	if !strings.Contains(page, `<span>for me</span><span class="fn">1</span>`) {
		t.Errorf("the chip should count what the list caught: %s", page)
	}
	if !strings.Contains(page, `href="/?mine=1" data-kind="mine"`) {
		t.Error("the chip should link to a page of them")
	}
}

// Editing the list drops every judgment behind it: they were answers about the
// old list, and the next sift asks again from the top.
func TestSavingInterestsForgetsEveryJudgment(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "one"), item("x", "2", "two")}, now)
	c.judge(map[string]siftVerdict{
		core.Key("x", "1"): {Worth: 0.9, Rank: 2, Interest: 0.9, InterestFor: "zig"},
		core.Key("x", "2"): {Worth: 0.02, Rank: 1, Interest: -1},
	}, now)
	if c.unjudgedCount() != 0 {
		t.Fatal("both were judged")
	}

	store := &interestStore{}
	req := httptest.NewRequest(http.MethodPost, "/interests", strings.NewReader("interests=the+Fed%27s+rate+path"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleInterests(rec, req, store, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body)
	}
	if got := store.list(); len(got) != 1 || got[0] != "the Fed's rate path" {
		t.Fatalf("stored %q", got)
	}
	if got := c.unjudgedCount(); got != 2 {
		t.Fatalf("unjudged is %d after the list changed, want both back", got)
	}
	if c.skippedCount() != 0 || c.matchedCount() != 0 {
		t.Error("nothing should be skipped or matched on a list nothing has been judged against")
	}
}

// The list reaches the model as it stands when the run starts, not per batch:
// one edited halfway through would leave a backlog judged against two lists.
func TestSiftSendsTheListItStartedWith(t *testing.T) {
	c := newTestCache(t)
	c.upsert([]core.Item{item("x", "1", "one")}, time.Now())
	store := &interestStore{}
	if err := store.set("anything about Zig"); err != nil {
		t.Fatal(err)
	}
	var asked []string
	s := testSifter(t, c, func(_ context.Context, _ core.Item, interests []string) (siftVerdict, error) {
		asked = interests
		return siftVerdict{Worth: 0.9, Rank: 2, Interest: 0.9, InterestFor: interests[0]}, nil
	})
	s.interests = store
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	settledSift(t, s)
	if len(asked) != 1 || asked[0] != "anything about Zig" {
		t.Fatalf("the run asked about %q", asked)
	}
	if c.matchedCount() != 1 {
		t.Error("the match should have landed")
	}
}
