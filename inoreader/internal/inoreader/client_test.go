package inoreader

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const frag1001 = `<div class="article_title mb-2">` +
	`<a class="article_title_link" id="article_title_link_1001" href="https://ex.com/1" class="boldlink">First &amp; Title</a></div>` +
	`<a class="ajaxed" id="article_feed_info_link_1001" href="/feed/x"> Feed One </a>` +
	`<span class="article-author-1001">alice</span>` +
	`<span class="article_subtitle_date_wrapper"><span title="Date received">3h</span></span>` +
	`<div class="article_content" id="article_contents_inner_1001"><p>Hello <b>world</b></p><p>line two</p></div>`

const frag1002 = `<a class="article_title_link" id="article_title_link_1002" href="https://ex.com/2">Second</a>` +
	`<a id="article_feed_info_link_1002" href="/feed/y">Feed Two</a>` +
	`<span class="article-author-1002">bob</span>` +
	`<span class="article_subtitle_date_wrapper"><span>1d</span></span>` +
	`<div class="article_content" id="article_contents_inner_1002">Plain body</div>`

func printArticlesJSON() string {
	// set_seen_ids order is [1002, 1001]; Unreads must preserve it.
	esc := func(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }
	return `{"xjxobj":[
		{"cmd":"jc","func":"set_seen_ids","data":[[1002,1001]]},
		{"cmd":"jc","func":"articles_loaded","data":[{
			"1001":"` + esc(frag1001) + `",
			"1002":"` + esc(frag1002) + `"
		}]}
	]}`
}

func TestUnreadsScrapesAndOrders(t *testing.T) {
	var sawUnread bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("xjxfun") != "print_articles" {
			t.Errorf("unexpected xjxfun %q", r.URL.Query().Get("xjxfun"))
		}
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "%22view_unread%22%3A1") {
			sawUnread = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(printArticlesJSON()))
	}))
	defer srv.Close()

	c := New(srv.URL, "cookie=1", "test-agent")
	arts, err := c.Unreads(context.Background(), true, 50)
	if err != nil {
		t.Fatalf("Unreads: %v", err)
	}
	if !sawUnread {
		t.Error("view_unread:1 not sent for unread-only")
	}
	if len(arts) != 2 {
		t.Fatalf("want 2 articles, got %d", len(arts))
	}
	if arts[0].ID != "1002" || arts[1].ID != "1001" {
		t.Errorf("order not from set_seen_ids: %s,%s", arts[0].ID, arts[1].ID)
	}

	a := arts[1] // 1001
	if a.Title != "First & Title" {
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
	if a.Age != "3h" {
		t.Errorf("age: %q", a.Age)
	}
	if a.Content != "Hello world\nline two" {
		t.Errorf("content: %q", a.Content)
	}
}

func TestMarkReadPostsReadArticle(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("xjxfun") != "read_article" {
			t.Errorf("unexpected xjxfun %q", r.URL.Query().Get("xjxfun"))
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"xjxobj":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "cookie=1", "test-agent")
	if err := c.MarkRead(context.Background(), "1001"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if !strings.Contains(body, "read_article") {
		t.Errorf("body missing fn: %q", body)
	}
	// url-encoded {"1001":2}
	if !strings.Contains(body, "%221001%22%3A2") {
		t.Errorf("body missing id:2 payload: %q", body)
	}
}

func TestMarkUnreadPostsUnreadState(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("xjxfun") != "read_article" {
			t.Errorf("unexpected xjxfun %q", r.URL.Query().Get("xjxfun"))
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"xjxobj":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "cookie=1", "test-agent")
	if err := c.MarkUnread(context.Background(), "1001"); err != nil {
		t.Fatalf("MarkUnread: %v", err)
	}
	// url-encoded {"1001":1}, and state 1 as the trailing argument too
	if !strings.Contains(body, "%221001%22%3A1") {
		t.Errorf("body missing id:1 payload: %q", body)
	}
	if !strings.Contains(body, "Bfalse&xjxargs[]=N1") {
		t.Errorf("body missing trailing unread state: %q", body)
	}
}

func TestRejectedSession(t *testing.T) {
	// A logged-out session returns the HTML login page, not JSON.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<!doctype html><html>login</html>"))
	}))
	defer srv.Close()

	c := New(srv.URL, "bad", "test-agent")
	_, err := c.Unreads(context.Background(), true, 5)
	if err == nil || !strings.Contains(err.Error(), "rejected the session") {
		t.Fatalf("want rejected-session error, got %v", err)
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
