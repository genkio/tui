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
