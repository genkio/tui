package folo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const entriesJSON = `{"code":0,"data":[
	{"read":false,"feeds":{"title":"Feed One","siteUrl":"https://one.example.com"},
	 "entries":{"id":"e1","title":"First &amp; Title","url":"https://ex.com/1","author":"alice",
	            "description":"<p>summary text</p>","publishedAt":"2026-06-25T00:00:00.000Z"}},
	{"read":false,"feeds":{"title":"","siteUrl":"https://www.two.example.com/path"},
	 "entries":{"id":"e2","title":"Second","url":"https://ex.com/2","publishedAt":"2026-06-24T00:00:00Z"}}
]}`

func TestUnreadsParsesEntries(t *testing.T) {
	var req entriesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/entries" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(entriesJSON))
	}))
	defer srv.Close()

	c := New(srv.URL, "https://app.folo.is", "cookie=1", "test-agent")
	arts, err := c.Unreads(context.Background(), true, 50)
	if err != nil {
		t.Fatalf("Unreads: %v", err)
	}

	// The Articles view, pending-only, fetched in one page.
	if req.View != 0 {
		t.Errorf("view = %d, want 0", req.View)
	}
	if req.Read == nil || *req.Read != false {
		t.Errorf("read = %v, want pointer to false", req.Read)
	}
	if req.Limit != 50 {
		t.Errorf("limit = %d, want 50", req.Limit)
	}

	if len(arts) != 2 {
		t.Fatalf("want 2 articles, got %d", len(arts))
	}
	a := arts[0]
	if a.ID != "e1" {
		t.Errorf("id: %q", a.ID)
	}
	if a.Title != "First & Title" { // HTML entity decoded
		t.Errorf("title: %q", a.Title)
	}
	if a.URL != "https://ex.com/1" {
		t.Errorf("url: %q", a.URL)
	}
	if a.Feed != "Feed One" {
		t.Errorf("feed: %q", a.Feed)
	}
	if a.Author != "alice" {
		t.Errorf("author: %q", a.Author)
	}
	if a.Summary != "summary text" {
		t.Errorf("summary: %q", a.Summary)
	}
	if a.Published.IsZero() || a.Age == "" {
		t.Errorf("published/age not parsed: %v %q", a.Published, a.Age)
	}
	// No feed title -> falls back to the site host, sans www.
	if arts[1].Feed != "two.example.com" {
		t.Errorf("feed host fallback: %q", arts[1].Feed)
	}
}

func TestContentFlattensBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/entries" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("id"); got != "e1" {
			t.Errorf("id query = %q, want e1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"entries":{"content":"<p>Body <b>x</b></p><p>two</p>"}}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "cookie=1", "test-agent")
	body, err := c.Content(context.Background(), "e1")
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if body != "Body x\ntwo" {
		t.Errorf("content: %q", body)
	}
}

func TestMarkReadPostsEntryIDs(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(`{"code":0,"data":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "cookie=1", "test-agent")
	if err := c.MarkRead(context.Background(), "e1"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if method != http.MethodPost || path != "/reads" {
		t.Errorf("want POST /reads, got %s %s", method, path)
	}
	if !strings.Contains(body, `"entryIds":["e1"]`) {
		t.Errorf("body: %q", body)
	}
}

func TestMarkUnreadDeletesEntryID(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(`{"code":0,"data":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "cookie=1", "test-agent")
	if err := c.MarkUnread(context.Background(), "e1"); err != nil {
		t.Fatalf("MarkUnread: %v", err)
	}
	if method != http.MethodDelete || path != "/reads" {
		t.Errorf("want DELETE /reads, got %s %s", method, path)
	}
	if !strings.Contains(body, `"entryId":"e1"`) {
		t.Errorf("body: %q", body)
	}
}

func TestRejectedSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":1,"message":"unauthorized"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "bad", "test-agent")
	_, err := c.Unreads(context.Background(), true, 5)
	if err == nil || !strings.Contains(err.Error(), "rejected the session") {
		t.Fatalf("want rejected-session error, got %v", err)
	}
}

func TestEntriesRequestReadOmitempty(t *testing.T) {
	all, _ := json.Marshal(entriesRequest{View: 0, Limit: 20})
	if strings.Contains(string(all), "read") {
		t.Errorf("read should be omitted for the all view: %s", all)
	}
	no := false
	pending, _ := json.Marshal(entriesRequest{View: 0, Limit: 20, Read: &no})
	if !strings.Contains(string(pending), `"read":false`) {
		t.Errorf("read:false should be sent for pending: %s", pending)
	}
}

func TestSanitizeCookie(t *testing.T) {
	cases := map[string]string{
		"  abc=1; def=2 ":     "abc=1; def=2",
		"Cookie: abc=1":       "abc=1",
		"cookie:abc=1; def=2": "abc=1; def=2",
		"abc=1":               "abc=1",
	}
	for in, want := range cases {
		if got := sanitizeCookie(in); got != want {
			t.Errorf("sanitizeCookie(%q) = %q, want %q", in, got, want)
		}
	}
}
