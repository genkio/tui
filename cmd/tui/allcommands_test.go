package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

func TestTerminalFeedUsesServerState(t *testing.T) {
	cache := newTestCache(t)
	now := time.Now()
	cache.upsert([]core.Item{{App: "bilibili", ID: "7", Title: "clip", URL: "https://www.bilibili.com/video/BV1GJ411x7h7"}}, now)
	flusher := &markFlusher{cache: cache, wake: make(chan struct{}, 1)}
	saved := loadSaved(filepath.Join(t.TempDir(), "saved.json"))
	rendered := newRenderedItems()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("json") != "1" || r.URL.Query().Get("order") != "desc" {
			t.Errorf("feed query = %q, want json and newest-first order", r.URL.RawQuery)
		}
		writeJSONItems(w, cache.unread(now, ""), nil, feedAPIResponse{Apps: []string{"bilibili"}})
	})
	mux.HandleFunc("/mark", func(w http.ResponseWriter, r *http.Request) {
		handleMark(w, r, cache, flusher)
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		handleSave(w, r, saved, cache, rendered)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	feed, err := fetchServerFeed(server.URL, "following")
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 || feed.Items[0].App != "bilibili" || feed.Items[0].ID != "7" {
		t.Fatalf("initial terminal feed = %+v", feed.Items)
	}
	if feed.Items[0].Type != "video" {
		t.Fatalf("terminal API type = %q, want video", feed.Items[0].Type)
	}
	if err := saveServerItem(server.URL, feed.Items[0].Item(now)); err != nil {
		t.Fatal(err)
	}
	if !saved.has("bilibili", "7") {
		t.Fatal("terminal save did not reach the server's saved store")
	}
	if cache.unreadCount() != 1 {
		t.Fatal("saving should not change unread state")
	}
	if err := markServerRead(server.URL, "bilibili", []string{"7"}); err != nil {
		t.Fatal(err)
	}
	feed, err = fetchServerFeed(server.URL, "following")
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 0 {
		t.Fatalf("terminal read did not leave the shared backlog: %+v", feed.Items)
	}
	select {
	case <-flusher.wake:
	default:
		t.Fatal("terminal read should wake the server's upstream flusher")
	}
}

func TestTerminalForYouUsesLiveServerQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("x"); got != "foryou" {
			t.Fatalf("x query = %q, want foryou", got)
		}
		writeJSONItems(w, nil, nil, feedAPIResponse{Apps: []string{"x"}})
	}))
	defer server.Close()
	if _, err := fetchServerFeed(server.URL, "foryou"); err != nil {
		t.Fatal(err)
	}
}

func TestFeedServerErrorsAreActionable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := fetchServerFeed(server.URL, "following")
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("server error = %v", err)
	}
}

func TestTerminalFeedCarriesServerWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONItems(w, nil, []string{"x"}, feedAPIResponse{Warn: "x session is stale; run `tui x --auth`."})
	}))
	defer server.Close()
	msg := fetchAll(server.URL, "following")().(allItemsMsg)
	for _, want := range []string{"couldn't load: x", "tui x --auth"} {
		if !strings.Contains(msg.note, want) {
			t.Fatalf("terminal warning %q does not contain %q", msg.note, want)
		}
	}
}

func TestDefaultFeedServer(t *testing.T) {
	t.Setenv("TUI_SERVER_URL", "")
	if got := defaultFeedServer(); got != localFeedServer {
		t.Fatalf("default server = %q", got)
	}
	t.Setenv("TUI_SERVER_URL", " http://100.64.0.1:9000 ")
	if got := defaultFeedServer(); got != "http://100.64.0.1:9000" {
		t.Fatalf("environment server = %q", got)
	}
	if got, err := normalizeServerURL("http://localhost:8080/"); err != nil || got != "http://localhost:8080" {
		t.Fatalf("normalized server = %q, %v", got, err)
	}
	if _, err := normalizeServerURL("file:///tmp/feed"); err == nil {
		t.Fatal("non-HTTP server URL should fail")
	}
}
