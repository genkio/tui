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

// A subject only reaches the model when there is one: an empty list would be a
// question with no possible yes, asked of every item in the backlog.
func TestSiftAsksAboutInterestsOnlyWhenThereAreSome(t *testing.T) {
	items := []core.Item{{App: "hn", ID: "1", Title: "Zig 0.16 released"}}
	if _, asked := siftRequest(items, nil).Questions[siftInterestID(0)]; asked {
		t.Error("no list, no question")
	}
	body := siftRequest(items, []string{"anything about Zig"})
	q, ok := body.Questions[siftInterestID(0)]
	if !ok || q.Type != "noul" {
		t.Fatalf("interest question = %+v, want a noul", q)
	}
	if !strings.Contains(q.Instructions, "`items[0]`") || !strings.Contains(q.Instructions, "`interests`") {
		t.Errorf("the question should name the item and the list: %q", q.Instructions)
	}
	if len(body.State.Interests) != 1 || body.State.Interests[0] != "anything about Zig" {
		t.Errorf("the list should go in the state as written: %+v", body.State.Interests)
	}
}

// What the reader asked for by name outranks what the sift made of it: a match
// is never skipped, and it gets a chip of its own.
func TestMatchedItemsSurviveTheCutAndGetAChip(t *testing.T) {
	c := newTestCache(t)
	now := time.Now()
	c.upsert([]core.Item{item("x", "1", "a zig post nobody rates"), item("x", "2", "plain filler")}, now)
	c.judge(map[string]siftVerdict{
		core.Key("x", "1"): {Worth: 0.05, Rank: 1, Interest: 0.95},
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
		core.Key("x", "1"): {Worth: 0.9, Rank: 2, Interest: 0.9},
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
	s := testSifter(t, c, func(_ context.Context, items []core.Item, interests []string) (map[string]siftVerdict, error) {
		asked = interests
		out := map[string]siftVerdict{}
		for _, it := range items {
			out[core.Key(it.App, it.ID)] = siftVerdict{Worth: 0.9, Rank: 2, Interest: 0.8}
		}
		return out, nil
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
