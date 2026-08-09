package x

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(base string) *Client {
	c := New("token", "csrf", "", "")
	c.base = base
	return c
}

// The rich body of a long post only comes back when the request asks for it.
func TestTimelineAsksForArticleContent(t *testing.T) {
	var toggles string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toggles = r.URL.Query().Get("fieldToggles")
		w.Write(buildSample())
	}))
	defer srv.Close()

	if _, err := testClient(srv.URL).Timeline(context.Background(), Following, 20); err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if !strings.Contains(toggles, `"withArticleRichContentState":true`) {
		t.Errorf("fieldToggles = %q, want the rich article content asked for", toggles)
	}
}

// An endpoint that doesn't know the toggle answers 400. The toggle only enriches
// long posts, so the timeline must survive without it rather than go dark.
func TestTimelineFallsBackWhenTogglesRejected(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("fieldToggles") != "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"errors":[{"message":"unknown fieldToggle"}]}`))
			return
		}
		w.Write(buildSample())
	}))
	defer srv.Close()

	tweets, err := testClient(srv.URL).Timeline(context.Background(), Following, 20)
	if err != nil {
		t.Fatalf("Timeline should have retried without the toggles: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want the toggled one plus one retry", calls)
	}
	if len(tweets) != 3 {
		t.Errorf("got %d tweets from the retry, want 3", len(tweets))
	}
}

// A failure that isn't about the toggles must not be retried or reworded away.
func TestTimelineDoesNotRetryOtherFailures(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).Timeline(context.Background(), Following, 20)
	if err == nil || !strings.Contains(err.Error(), "rejected the session") {
		t.Fatalf("err = %v, want the stale-session message", err)
	}
	if calls != 1 {
		t.Errorf("made %d requests, want no retry", calls)
	}
}
