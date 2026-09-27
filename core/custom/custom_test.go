package custom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
)

func parser(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "p.js")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A forum thread page, served as Shift_JIS.
func sjisServer(t *testing.T, declare bool) *httptest.Server {
	t.Helper()
	page := `<html><head><title>掲示板 - forum.example.com</title></head><body>
<article class="post" data-id="1"><span class="author"><b>匿名</b></span><time datetime="2025-05-15T09:03:21+09:00">5月15日</time>
<div class="content"> 一行目 <br> 二行目 <img src="https://i.example/a.jpg"> </div></article>
<article class="post" data-id="2"><span class="author"><b>管理人</b></span>
<div class="content">スレッドは閉じられました</div></article>
</body></html>`
	body, err := japanese.ShiftJIS.NewEncoder().String(page)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/thread" {
			http.NotFound(w, r)
			return
		}
		if declare {
			w.Header().Set("Content-Type", "text/html; charset=Shift_JIS")
		} else {
			w.Header().Set("Content-Type", "text/html")
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const threadParser = `
module.exports = {
  label: "frm",
  fetch() {
    const $ = load(get(BASE + "/thread", OPTS));
    const title = $("title").text().replace(" - forum.example.com", "");
    return $("article.post").map((i, el) => {
      const post = $(el);
      const ts = post.find("time").attr("datetime");
      if (!ts) return null;
      return {
        id: Number(post.attr("data-id")),
        html: post.find(".content").html(),
        author: post.find(".author b").text(),
        source: title,
        ts,
      };
    });
  },
};`

func threadSrc(base, opts string) string {
	return "const BASE = " + `"` + base + `"` + "; const OPTS = " + opts + ";\n" + threadParser
}

// The whole reason for the engine: an RSSHub-style parser runs as written,
// against a page in the encoding the site actually serves.
func TestFetchParsesAShiftJISPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		declare bool
		opts    string
	}{
		{"encoding from the response", true, "undefined"},
		{"encoding named by the parser", false, `{ encoding: "shift_jis" }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := sjisServer(t, tc.declare)
			ws, err := Fetch(context.Background(), parser(t, threadSrc(srv.URL, tc.opts)))
			if err != nil {
				t.Fatal(err)
			}
			if len(ws) != 1 {
				t.Fatalf("got %d items, want 1 (the undated notice is left out by the parser)", len(ws))
			}
			w := ws[0]
			if w.ID != "1" {
				t.Errorf("id = %q, want a number stringified", w.ID)
			}
			if w.Body != "一行目\n二行目" {
				t.Errorf("body = %q, want the html flattened with its line break", w.Body)
			}
			if len(w.Images) != 1 || w.Images[0] != "https://i.example/a.jpg" {
				t.Errorf("images = %v, want the inline image lifted onto the card", w.Images)
			}
			if w.Source != "掲示板" || w.Author != "匿名" {
				t.Errorf("source, author = %q, %q", w.Source, w.Author)
			}
			if w.TS != "2025-05-15T00:03:21Z" {
				t.Errorf("ts = %q, want Japan time in UTC", w.TS)
			}
		})
	}
}

func TestLoadReadsTheChip(t *testing.T) {
	m, err := Load(parser(t, `module.exports = { label: "frm", color: "#2a9d8f", description: "d", fetch() { return [] } }`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Label != "frm" || m.Color != "#2a9d8f" || m.Description != "d" {
		t.Errorf("meta = %+v", m)
	}
}

// Load fetches nothing, but it is where a file that could never fetch is
// caught, so the chip can say so before the first sweep does.
func TestLoadRefusesWhatCannotFetch(t *testing.T) {
	for name, src := range map[string]string{
		"no fetch":     `module.exports = { label: "x" }`,
		"syntax error": `module.exports = { fetch() { return [ }`,
		"throws":       `throw new Error("top level")`,
	} {
		if _, err := Load(parser(t, src)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestFetchShapes(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"async fetch is waited on", `module.exports = { async fetch() { return [{ id: "a", ts: new Date(0) }] } }`, "a"},
		{"exports assigned field by field", `exports.fetch = () => [{ id: "b", ts: 0 }]`, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := Fetch(context.Background(), parser(t, tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(ws) != 1 || ws[0].ID != tc.want || ws[0].TS != "1970-01-01T00:00:00Z" {
				t.Fatalf("got %+v", ws)
			}
		})
	}
}

// id is what read state hangs on, so an item without one is a broken parser,
// not an item to file under a blank key.
func TestFetchRefusesAnItemWithoutAnID(t *testing.T) {
	_, err := Fetch(context.Background(), parser(t, `module.exports = { fetch() { return [{ id: "a" }, { title: "t" }] } }`))
	if err == nil || !strings.Contains(err.Error(), "item 1") {
		t.Fatalf("err = %v, want it to name item 1", err)
	}
}

// A failed request is an exception the parser can catch, and one it does not
// catch fails the fetch with the status in it.
func TestGetThrowsOnAnErrorStatus(t *testing.T) {
	srv := sjisServer(t, true)
	caught := `module.exports = { fetch() { try { get("` + srv.URL + `/gone") } catch (e) { return [{ id: e.message }] } } }`
	ws, err := Fetch(context.Background(), parser(t, caught))
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || !strings.Contains(ws[0].ID, "404") {
		t.Fatalf("got %+v, want the 404 caught", ws)
	}
	_, err = Fetch(context.Background(), parser(t, `module.exports = { fetch() { return get("`+srv.URL+`/gone") } }`))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the 404", err)
	}
}

// A parser stuck in a loop is stopped by the fetch's deadline, not left to
// hold the sweep.
func TestFetchStopsARunawayParser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Fetch(ctx, parser(t, `module.exports = { fetch() { for (;;) {} } }`))
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s to stop", d)
	}
}

func TestSelection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<ul><li class="a" data-n="1">one</li><li data-n="2">two</li><li class="a">three</li></ul>`))
	}))
	defer srv.Close()
	src := `module.exports = { fetch() {
  const $ = load(get("` + srv.URL + `"));
  const li = $("li");
  const out = [];
  li.each(function (i, el) { out.push(i + ":" + $(el).text()); if (i === 1) return false; });
  return [
    { id: "each", title: out.join(",") },
    { id: "array", title: li.toArray().map(el => el.attr("data-n") ?? "none").join(",") },
    { id: "filter", title: String(li.filter(".a").length) + " " + li.last().hasClass("a") + " " + li.first().is(".a") },
    { id: "walk", title: li.eq(1).parent().children().length + " " + li.eq(1).next().text() + " " + li.eq(1).prev().text() },
    { id: "missing", title: String($("p").html()) + " " + $("p").text() + "|" + String(li.attr("nope")) },
  ];
} }`
	ws, err := Fetch(context.Background(), parser(t, src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"each":    "0:one,1:two",
		"array":   "1,2,none",
		"filter":  "2 true true",
		"walk":    "3 three one",
		"missing": "null |undefined",
	}
	for _, w := range ws {
		if w.Title != want[w.ID] {
			t.Errorf("%s = %q, want %q", w.ID, w.Title, want[w.ID])
		}
	}
}
