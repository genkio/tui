package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestYTSecsFromPlayer(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		// The reply calls the video unplayable — this is not a browser — but the
		// length is stated anyway, which is the only field the badge needs.
		{"unplayable but stated", `{"playabilityStatus":{"status":"UNPLAYABLE"},"videoDetails":{"videoId":"x","lengthSeconds":"213"}}`, 213},
		{"livestream nonsense", `{"videoDetails":{"lengthSeconds":"75037431"}}`, 0},
		{"no details", `{"playabilityStatus":{"status":"ERROR"}}`, 0},
		{"zero", `{"videoDetails":{"lengthSeconds":"0"}}`, 0},
	}
	for _, c := range cases {
		got, err := ytSecsFromPlayer([]byte(c.raw))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: secs = %d, want %d", c.name, got, c.want)
		}
	}
	if _, err := ytSecsFromPlayer([]byte("not json")); err == nil {
		t.Error("a non-JSON reply should error, not report a length")
	}
}

func TestYTLensCaches(t *testing.T) {
	calls := 0
	clock := time.Now()
	y := newYTLens()
	y.now = func() time.Time { return clock }
	y.fetch = func(context.Context, string) (int, error) { calls++; return 95, nil }

	for i := 0; i < 3; i++ {
		if got := y.get(context.Background(), "aqz-KE-bpKQ"); got != 95 {
			t.Fatalf("get = %d, want 95", got)
		}
	}
	if calls != 1 {
		t.Errorf("asked YouTube %d times, want 1 (a length never changes)", calls)
	}
}

func TestYTLensRetriesMissesLater(t *testing.T) {
	calls := 0
	clock := time.Now()
	y := newYTLens()
	y.now = func() time.Time { return clock }
	y.fetch = func(context.Context, string) (int, error) { calls++; return 0, errors.New("nope") }

	y.get(context.Background(), "aqz-KE-bpKQ")
	y.get(context.Background(), "aqz-KE-bpKQ")
	if calls != 1 {
		t.Errorf("a fresh miss was retried at once (%d calls); it should wait", calls)
	}
	clock = clock.Add(ytMissTTL + time.Second)
	y.get(context.Background(), "aqz-KE-bpKQ")
	if calls != 2 {
		t.Errorf("calls = %d, want the miss retried once its window passed", calls)
	}
}

func TestYTLenHandler(t *testing.T) {
	y := newYTLens()
	y.fetch = func(_ context.Context, id string) (int, error) {
		if id == "aqz-KE-bpKQ" {
			return 635, nil
		}
		return 0, nil
	}

	rec := httptest.NewRecorder()
	y.handle(rec, httptest.NewRequest(http.MethodGet, "/ytlen?v=aqz-KE-bpKQ", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"len":"10:35"`) {
		t.Fatalf("got %d %s, want the formatted length", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") == "" {
		t.Error("a resolved length should be cacheable by the browser")
	}

	// Unknown: a blank label, and nothing for the browser to remember.
	rec = httptest.NewRecorder()
	y.handle(rec, httptest.NewRequest(http.MethodGet, "/ytlen?v=00000000000", nil))
	if !strings.Contains(rec.Body.String(), `"len":""`) {
		t.Errorf("unknown length should answer blank: %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Error("a miss must not be cached by the browser: it may resolve later")
	}

	// Anything that isn't a video id is refused, so /ytlen can't be aimed
	// elsewhere.
	for _, bad := range []string{"", "short", "..%2F..%2Fetc%2Fpasswd", "aqz-KE-bpKQxx", "aqz%2FKE-bpKQ"} {
		rec = httptest.NewRecorder()
		y.handle(rec, httptest.NewRequest(http.MethodGet, "/ytlen?v="+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("v=%q gave %d, want 400", bad, rec.Code)
		}
	}
}
