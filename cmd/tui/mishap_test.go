package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// The latest reason is the one worth saying, and each one is a new name: a
// dismissal is remembered against the name, so the same trouble on the next
// fetch has to come back rather than stay put away.
func TestMishapKeepsTheLatestUnderANewName(t *testing.T) {
	var m *mishapLog
	if _, ok := m.latest(); ok {
		t.Error("a server with no mishap log has something to report")
	}

	m = &mishapLog{}
	if _, ok := m.latest(); ok {
		t.Error("something to report before anything failed")
	}
	m.note("sift", "")
	if _, ok := m.latest(); ok {
		t.Error("a failure with nothing to say about it made a banner")
	}

	m.note("sift", "typesafe said no")
	first, _ := m.latest()
	if first.ID == "" || !strings.Contains(first.says(), "the sift failed: typesafe said no") {
		t.Fatalf("mishap = %+v, says %q", first, first.says())
	}
	m.note("summary", "pi is not on PATH")
	second, ok := m.latest()
	if !ok || second.ID == first.ID {
		t.Fatalf("second mishap = %+v, want a name of its own", second)
	}
	if !strings.Contains(second.says(), "a summary could not be written: pi is not on PATH") {
		t.Errorf("says = %q", second.says())
	}
}

// The page hears about it on the status it already asks for.
func TestStatusCarriesTheLastMishap(t *testing.T) {
	cache := newTestCache(t)
	mishaps := &mishapLog{}
	read := func() (string, string) {
		rec := httptest.NewRecorder()
		showStatus(rec, &sweeper{}, cache, mishaps)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var got struct{ Mishap, Says string }
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got.Mishap, got.Says
	}
	if id, _ := read(); id != "" {
		t.Errorf("status names a mishap (%q) before anything failed", id)
	}
	mishaps.note("sift", "typesafe said no")
	id, says := read()
	if id == "" || !strings.Contains(says, "typesafe said no") {
		t.Errorf("status = %q %q, want the sift's failure", id, says)
	}
}

// Every briefing lands on the banner when it fails, whoever asked for it — the
// digests a fetch writes have nobody in front of them at all.
func TestAFailedBriefingReachesTheBanner(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "reddit", ID: "1", Title: "a post"}}, time.Now())
	mishaps := &mishapLog{}
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		return "", errors.New("pi failed: model refused")
	})
	sum.mishaps = mishaps

	if rec := post(t, sum, "app=reddit"); rec.Code != http.StatusAccepted {
		t.Fatalf("summarize = %d %s", rec.Code, rec.Body.String())
	}
	if j := settled(t, sum, "reddit"); j.State != "failed" {
		t.Fatalf("job = %+v, want a failed run", j)
	}
	m, ok := mishaps.latest()
	if !ok || !strings.Contains(m.Msg, "model refused") || m.Kind != "summary" {
		t.Errorf("mishap = %+v, want the failure the run reported", m)
	}
}

// A run of the same kind that goes through puts the banner away; one of the
// other kind leaves it, and the next failure still gets a name of its own.
func TestASuccessClearsItsOwnKindOfMishap(t *testing.T) {
	m := &mishapLog{}
	m.note("summary", "token expired")
	first, _ := m.latest()
	m.clear("sift")
	if _, ok := m.latest(); !ok {
		t.Fatal("a sift going through cleared a summary's failure")
	}
	m.clear("summary")
	if got, ok := m.latest(); ok {
		t.Fatalf("mishap = %+v after a summary went through", got)
	}
	m.note("summary", "token expired")
	if again, _ := m.latest(); again.ID == first.ID {
		t.Errorf("the failure after a clear reused the dismissed name %q", first.ID)
	}
}

// The re-login case: a briefing fails, the next one is written, and the banner
// goes without anyone dismissing it.
func TestAWrittenBriefingClearsTheBanner(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "reddit", ID: "1", Title: "a post"}}, time.Now())
	mishaps := &mishapLog{}
	expired := true
	sum := testSummarizer(t, cache, func(context.Context, string) (string, error) {
		if expired {
			return "", errors.New("pi failed: Your authentication token has expired.")
		}
		return "- a post", nil
	})
	sum.mishaps = mishaps

	post(t, sum, "app=reddit")
	settled(t, sum, "reddit")
	if _, ok := mishaps.latest(); !ok {
		t.Fatal("the failed briefing left no mishap")
	}
	expired = false
	if rec := post(t, sum, "app=reddit"); rec.Code != http.StatusAccepted {
		t.Fatalf("summarize = %d %s", rec.Code, rec.Body.String())
	}
	if j := settled(t, sum, "reddit"); j.State != "done" {
		t.Fatalf("job = %+v, want a written briefing", j)
	}
	if m, ok := mishaps.latest(); ok {
		t.Errorf("mishap = %+v still standing after a briefing was written", m)
	}
}
