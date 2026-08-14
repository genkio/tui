package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubBiliLens is a lens that answers from memory: the bvid resolves to one
// mirror per lookup round, and a "stream" is that mirror's name as its body.
func stubBiliLens(now *time.Time) (*biliLens, *int) {
	lookups := 0
	l := newBiliLens()
	l.now = func() time.Time { return *now }
	l.cookie = func() string { return "SESSDATA=test" }
	l.cid = func(context.Context, string, string) (int64, error) { return 42, nil }
	l.durl = func(_ context.Context, id string, cid int64, cookie string) ([]string, error) {
		lookups++
		if cid != 42 || cookie != "SESSDATA=test" {
			return nil, fmt.Errorf("unexpected lookup args: cid=%d cookie=%q", cid, cookie)
		}
		return []string{fmt.Sprintf("https://upos-x.bilivideo.com/%s/round%d.mp4", id, lookups)}, nil
	}
	l.open = func(_ context.Context, urls []string, _ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"video/mp4"}, "Accept-Ranges": {"bytes"}},
			Body:       io.NopCloser(strings.NewReader(urls[0])),
		}, nil
	}
	return l, &lookups
}

func TestBiliHandleStreams(t *testing.T) {
	now := time.Now()
	l, lookups := stubBiliLens(&now)

	// The player's Range has to reach the mirror or seeking cannot work.
	ranges := []string{}
	inner := l.open
	l.open = func(ctx context.Context, urls []string, rng string) (*http.Response, error) {
		ranges = append(ranges, rng)
		return inner(ctx, urls, rng)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil)
	req.Header.Set("Range", "bytes=100-")
	l.handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != "https://upos-x.bilivideo.com/BV1GJ411x7h7/round1.mp4" {
		t.Errorf("body = %q, want the resolved mirror", got)
	}
	if rec.Header().Get("Content-Type") != "video/mp4" || rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("upstream media headers should carry through: %v", rec.Header())
	}
	if len(ranges) != 1 || ranges[0] != "bytes=100-" {
		t.Errorf("ranges forwarded = %v, want the player's own", ranges)
	}
	if rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("a plain play request is not a download: %q", rec.Header().Get("Content-Disposition"))
	}

	// A second play reuses the resolved mirror rather than asking again.
	rec = httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil))
	if *lookups != 1 {
		t.Errorf("lookups = %d, want the first answer reused", *lookups)
	}

	// Once the URL has aged past its life, the next play resolves it again.
	now = now.Add(biliPlayTTL + time.Minute)
	rec = httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil))
	if *lookups != 2 {
		t.Errorf("lookups = %d, want a fresh URL after the TTL", *lookups)
	}
	if got := rec.Body.String(); !strings.HasSuffix(got, "round2.mp4") {
		t.Errorf("body = %q, want the re-resolved mirror", got)
	}
}

func TestBiliHandleDownloadNamesTheFile(t *testing.T) {
	now := time.Now()
	l, _ := stubBiliLens(&now)
	rec := httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7&dl=1", nil))
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="BV1GJ411x7h7.mp4"` {
		t.Errorf("Content-Disposition = %q", got)
	}
}

func TestBiliHandleRejectsAnythingButABvid(t *testing.T) {
	now := time.Now()
	l, lookups := stubBiliLens(&now)
	for _, id := range []string{"", "BV1", "https://evil.example.com/x.mp4", "av12345", "BV1GJ411x7h7x"} {
		rec := httptest.NewRecorder()
		l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id="+id, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: status = %d, want 400", id, rec.Code)
		}
	}
	if *lookups != 0 {
		t.Errorf("a refused id should cost no lookup, got %d", *lookups)
	}
}

// An expired URL and a mirror that is simply down look the same from here, so a
// refusal is worth one fresh lookup before the tap is given up on.
func TestBiliHandleRetriesWithAFreshURL(t *testing.T) {
	now := time.Now()
	l, lookups := stubBiliLens(&now)
	l.open = func(_ context.Context, urls []string, _ string) (*http.Response, error) {
		if strings.HasSuffix(urls[0], "round1.mp4") {
			return nil, errors.New("mirror upos-x.bilivideo.com: 403 Forbidden")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(urls[0])),
			Header:     http.Header{},
		}, nil
	}

	rec := httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if *lookups != 2 {
		t.Errorf("lookups = %d, want the refusal to buy one retry", *lookups)
	}
}

func TestBiliHandleGivesUpOnALookupFailure(t *testing.T) {
	now := time.Now()
	l, _ := stubBiliLens(&now)
	calls := 0
	l.durl = func(context.Context, string, int64, string) ([]string, error) {
		calls++
		return nil, errors.New("bilibili api code -352: risk control")
	}

	rec := httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}

	if calls != 1 {
		t.Errorf("lookup calls = %d, want one: a lookup that failed is not worth repeating now", calls)
	}

	// The failure is remembered briefly, so a feed full of taps can't hammer a
	// risk-controlled API.
	rec = httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil))
	if calls != 1 {
		t.Errorf("lookup calls = %d, want the miss remembered between taps", calls)
	}
	now = now.Add(biliMissTTL + time.Minute)
	rec = httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, biliPath+"?id=BV1GJ411x7h7", nil))
	if calls != 2 {
		t.Errorf("lookup calls = %d, want the video tried again once the miss ages out", calls)
	}
}

func TestBiliMirrors(t *testing.T) {
	got := biliMirrors([]string{
		"https://upos-sz-mirror08c.bilivideo.com/a.mp4",
		"https://upos-sz-mirror08c.bilivideo.com/b.mp4", // same host, no second try
		"http://upos-sz-mirrorcos.bilivideo.com/c.mp4",  // plain http, refused
		"https://cn-hk-eq-01-03.bilivideo.cn/d.mp4",
		"https://xy1.akamaized.net/e.mp4",
		"https://evil.example.com/f.mp4", // not bilibili's, refused
		"not a url at all",
	})
	want := []string{
		"https://upos-sz-mirror08c.bilivideo.com/a.mp4",
		"https://cn-hk-eq-01-03.bilivideo.cn/d.mp4",
		"https://xy1.akamaized.net/e.mp4",
	}
	if len(got) != len(want) {
		t.Fatalf("mirrors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mirror %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBiliVideoIDAndKeepURL(t *testing.T) {
	for _, tc := range []struct {
		app, url string
		want     string
	}{
		{"bilibili", "https://www.bilibili.com/video/BV1GJ411x7h7", "BV1GJ411x7h7"},
		{"bilibili", "https://m.bilibili.com/video/BV1GJ411x7h7?spm_id=333", "BV1GJ411x7h7"},
		{"bilibili", "https://www.bilibili.com/bangumi/play/ep123456", ""}, // a series episode has no bvid
		{"reddit", "https://www.bilibili.com/video/BV1GJ411x7h7", ""},      // only bilibili's own items
	} {
		if got := biliVideoID(tc.app, tc.url); got != tc.want {
			t.Errorf("biliVideoID(%q, %q) = %q, want %q", tc.app, tc.url, got, tc.want)
		}
	}

	if got := keepURL("bilibili", "9", biliPath+"?id=BV1GJ411x7h7"); got != biliPath+"?id=BV1GJ411x7h7&dl=1" {
		t.Errorf("a bilibili clip is already coming through this server: %q", got)
	}
	if got := keepURL("x", "50", "https://video.twimg.com/a.mp4"); got != "/dl?n=x-50.mp4&u=https%3A%2F%2Fvideo.twimg.com%2Fa.mp4" {
		t.Errorf("a direct mp4 goes through /dl: %q", got)
	}
	if got := keepURL("douban", "1", ""); got != "" {
		t.Errorf("nothing to keep should offer no link: %q", got)
	}
}
