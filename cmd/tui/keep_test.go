package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

func TestKeepSourceNamesWhatYtDlpCanRead(t *testing.T) {
	for _, c := range []struct {
		it                  core.Item
		video, yt, rg, want string
	}{
		{core.Item{App: "x"}, "https://video.twimg.com/a.mp4", "", "", "https://video.twimg.com/a.mp4"},
		{core.Item{App: "bilibili", URL: "https://www.bilibili.com/video/BV1xx411c7mD"}, biliPath + "?id=BV1xx411c7mD", "", "", "https://www.bilibili.com/video/BV1xx411c7mD"},
		{core.Item{App: "inoreader"}, "", "dQw4w9WgXcQ", "", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		{core.Item{App: "reddit"}, "", "", "tealbluefinwhale", "https://www.redgifs.com/watch/tealbluefinwhale"},
		{core.Item{App: "reddit"}, "", "", "", ""},
	} {
		if got := keepSource(c.it, c.video, c.yt, c.rg); got != c.want {
			t.Errorf("keepSource(%+v) = %q, want %q", c, got, c.want)
		}
	}
}

// fakeSaver answers /api/saves the way the download server does, writing the file into
// the requested dir when the url does not ask to fail.
func fakeSaver(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var posts atomic.Int32
	jobs := map[string]string{}
	paths := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/saves":
			posts.Add(1)
			var req struct{ URL, Dir, Name, Title string }
			json.NewDecoder(r.Body).Decode(&req)
			id := req.Name
			id = req.Title
			if strings.Contains(req.URL, "fail") {
				jobs[id] = "error"
			} else {
				os.MkdirAll(req.Dir, 0o755)
				path := filepath.Join(req.Dir, strings.Replace(req.Name, "{title}", req.Title, 1)+".mp4")
				os.WriteFile(path, []byte("mp4"), 0o644)
				jobs[id] = "done"
				paths[id] = path
			}
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "queued"})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/saves/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/saves/")
			st, ok := jobs[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			out := map[string]string{"id": id, "status": st, "path": paths[id]}
			if st == "error" {
				out["error"] = "yt-dlp failed: no video"
			}
			json.NewEncoder(w).Encode(out)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func keepCall(t *testing.T, k *keeper, method, name, src string) keepStatus {
	t.Helper()
	var r *http.Request
	if method == "POST" {
		r = httptest.NewRequest("POST", "/keep", strings.NewReader(url.Values{"n": {name}, "u": {src}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest("GET", "/keep?n="+url.QueryEscape(name), nil)
	}
	w := httptest.NewRecorder()
	k.handle(w, r)
	var st keepStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("%s %s: %d %s", method, name, w.Code, w.Body)
	}
	return st
}

func keepSettle(t *testing.T, k *keeper, name string) keepStatus {
	t.Helper()
	for range 200 {
		if st := keepCall(t, k, "GET", name, ""); st.State != keepPending {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("keep never settled")
	return keepStatus{}
}

func TestKeepFollowsTheDownloadToAFile(t *testing.T) {
	saver, posts := fakeSaver(t)
	k := newKeeper(saver.URL, filepath.Join(t.TempDir(), "videos"))
	k.poll = time.Millisecond

	if st := keepCall(t, k, "POST", "x-1", "https://video.twimg.com/a.mp4"); st.State != keepPending {
		t.Fatalf("started as %+v", st)
	}
	if st := keepSettle(t, k, "x-1"); st.State != keepDone {
		t.Fatalf("settled as %+v", st)
	}
	if got := k.states()["x-1"].State; got != keepDone {
		t.Fatalf("page sees %q", got)
	}
	// Kept is the file: a second tap asks the download server for nothing.
	keepCall(t, k, "POST", "x-1", "https://video.twimg.com/a.mp4")
	if n := posts.Load(); n != 1 {
		t.Fatalf("%d posts", n)
	}
	// Deleting the file un-keeps it.
	os.Remove(filepath.Join(k.dir, "x-1.mp4"))
	if st := keepCall(t, k, "GET", "x-1", ""); st.State != keepNone {
		t.Fatalf("after delete: %+v", st)
	}
}

func TestKeepCarriesTheErrorAndTakesARetry(t *testing.T) {
	saver, posts := fakeSaver(t)
	k := newKeeper(saver.URL, t.TempDir())
	k.poll = time.Millisecond

	keepCall(t, k, "POST", "x-2", "https://example.com/fail")
	st := keepSettle(t, k, "x-2")
	if st.State != keepError || !strings.Contains(st.Error, "no video") {
		t.Fatalf("settled as %+v", st)
	}
	keepCall(t, k, "POST", "x-2", "https://example.com/fail")
	if n := posts.Load(); n != 2 {
		t.Fatalf("retry posted %d times in all", n)
	}
}

func TestKeepSaysSoWhenTheSaverIsDown(t *testing.T) {
	k := newKeeper("http://127.0.0.1:1", t.TempDir())
	st := keepCall(t, k, "POST", "x-3", "https://video.twimg.com/a.mp4")
	if st.State != keepError || !strings.Contains(st.Error, "not answering") {
		t.Fatalf("got %+v", st)
	}
}

func TestKeepButtonReplacesTheDownloadLink(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a post.mp4"), nil, 0o644)
	keeps = newKeeper("http://127.0.0.1:1", dir)
	keeps.record("x-50", "a post.mp4")
	t.Cleanup(func() { keeps = nil })

	out := renderPage(t, []core.Item{{App: "x", ID: "50", Title: "t", URL: "https://x.com/a/status/50", Video: "https://video.twimg.com/a.mp4"}}, []string{"x"}, nil, "", "")
	if strings.Contains(out, "/dl?") {
		t.Fatal("still links /dl")
	}
	if !strings.Contains(out, `class="keep" type="button" data-n="x-50"`) || !strings.Contains(out, `data-state="kept"`) {
		t.Fatalf("no kept button in %s", out)
	}
}

func TestKeepTitleIsTheFirstLineWithoutLinks(t *testing.T) {
	if got := keepTitle("", "https://t.co/x\n  a  post https://a.b about it \nmore"); got != "a post about it" {
		t.Fatalf("got %q", got)
	}
	if got := []rune(keepTitle(strings.Repeat("語", 100))); len(got) != keepTitleRunes {
		t.Fatalf("cut to %d", len(got))
	}
}
