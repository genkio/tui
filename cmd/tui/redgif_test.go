package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRedgifClipFrom(t *testing.T) {
	raw := `{"gif":{"duration":12.7,"hasAudio":true,"id":"elementaryhoarseflea","urls":{
	  "sd":"https://media.redgifs.com/ElementaryHoarseFlea-mobile.mp4",
	  "hd":"https://media.redgifs.com/ElementaryHoarseFlea.mp4",
	  "poster":"https://media.redgifs.com/ElementaryHoarseFlea-poster.jpg",
	  "html":"https://www.redgifs.com/ifr/elementaryhoarseflea"}}}`
	clip, err := redgifClipFrom([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if clip.Video != "https://media.redgifs.com/ElementaryHoarseFlea.mp4" {
		t.Errorf("video = %q, want the HD cut", clip.Video)
	}
	if clip.Poster != "https://media.redgifs.com/ElementaryHoarseFlea-poster.jpg" {
		t.Errorf("poster = %q", clip.Poster)
	}
	if clip.Secs != 13 {
		t.Errorf("secs = %d, want 13 (12.7 rounded)", clip.Secs)
	}

	// No HD: the mobile cut still plays.
	clip, err = redgifClipFrom([]byte(`{"gif":{"urls":{"sd":"https://media.redgifs.com/A-mobile.mp4"}}}`))
	if err != nil || clip.Video != "https://media.redgifs.com/A-mobile.mp4" {
		t.Errorf("clip = %+v, err = %v; want the SD fallback", clip, err)
	}

	// Anything not on redgifs' own media host never reaches the page's <video>.
	for _, bad := range []string{
		`{"gif":{"urls":{"hd":"http://media.redgifs.com/A.mp4"}}}`,
		`{"gif":{"urls":{"hd":"https://evil.example.com/A.mp4"}}}`,
		`{"gif":{"urls":{"hd":"javascript:alert(1)"}}}`,
	} {
		clip, err := redgifClipFrom([]byte(bad))
		if err != nil || clip.Video != "" {
			t.Errorf("%s gave %q, want nothing playable", bad, clip.Video)
		}
	}

	if _, err := redgifClipFrom([]byte("not json")); err == nil {
		t.Error("a non-JSON reply should error, not report a clip")
	}
}

func TestRedgifLensCachesAndRetriesMisses(t *testing.T) {
	calls := 0
	clock := time.Now()
	l := newRedgifLens()
	l.now = func() time.Time { return clock }
	l.mint = func(context.Context) (string, error) { return "tok", nil }
	l.gif = func(context.Context, string, string) (redgifClip, error) {
		calls++
		return redgifClip{Video: "https://media.redgifs.com/A.mp4", Secs: 9}, nil
	}
	for i := 0; i < 3; i++ {
		if clip, ok := l.get(context.Background(), "a"); !ok || clip.Secs != 9 {
			t.Fatalf("get = %+v %v, want the clip", clip, ok)
		}
	}
	if calls != 1 {
		t.Errorf("asked redgifs %d times, want 1 (the URL is the id in another casing)", calls)
	}

	calls = 0
	l.gif = func(context.Context, string, string) (redgifClip, error) {
		calls++
		return redgifClip{}, errors.New("nope")
	}
	l.get(context.Background(), "gone")
	l.get(context.Background(), "gone")
	if calls != 1 {
		t.Errorf("a fresh miss was retried at once (%d calls); it should wait", calls)
	}
	clock = clock.Add(redgifMissTTL + time.Second)
	l.get(context.Background(), "gone")
	if calls != 2 {
		t.Errorf("calls = %d, want the miss retried once its window passed", calls)
	}
}

func TestRedgifLensReusesTheTokenUntilItExpires(t *testing.T) {
	minted := 0
	clock := time.Now()
	l := newRedgifLens()
	l.now = func() time.Time { return clock }
	l.mint = func(context.Context) (string, error) { minted++; return "tok", nil }

	for i := 0; i < 3; i++ {
		if _, err := l.authToken(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	if minted != 1 {
		t.Errorf("minted %d tokens, want 1 while the first is fresh", minted)
	}
	clock = clock.Add(redgifTokenTTL + time.Minute)
	if _, err := l.authToken(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if minted != 2 {
		t.Errorf("minted %d tokens, want an aged-out one replaced", minted)
	}
}

// A token can go stale before its stated life: one refusal costs a fresh token
// and a second attempt, not a dead player.
func TestRedgifLookupRetriesOnceOnARefusedToken(t *testing.T) {
	l := newRedgifLens()
	minted := 0
	l.mint = func(context.Context) (string, error) { minted++; return "tok" + strconv.Itoa(minted), nil }

	var seen []string
	l.gif = func(_ context.Context, id, token string) (redgifClip, error) {
		seen = append(seen, token)
		if token == "tok1" {
			return redgifClip{}, errRedgifAuth
		}
		return redgifClip{Video: "https://media.redgifs.com/A.mp4"}, nil
	}

	clip, err := l.lookup(context.Background(), "a")
	if err != nil || clip.Video == "" {
		t.Fatalf("lookup = %+v, %v; want the retry to succeed", clip, err)
	}
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Errorf("tokens tried: %v; want the refused one replaced before retrying", seen)
	}

	// A refusal that survives a fresh token is a failure, not an endless retry.
	l.gif = func(context.Context, string, string) (redgifClip, error) { return redgifClip{}, errRedgifAuth }
	if _, err := l.lookup(context.Background(), "a"); !errors.Is(err, errRedgifAuth) {
		t.Errorf("err = %v, want the refusal reported", err)
	}
}

func TestRedgifHandler(t *testing.T) {
	l := newRedgifLens()
	l.mint = func(context.Context) (string, error) { return "tok", nil }
	l.gif = func(_ context.Context, id, _ string) (redgifClip, error) {
		if id != "elementaryhoarseflea" {
			return redgifClip{}, errors.New("no such gif")
		}
		return redgifClip{
			Video:  "https://media.redgifs.com/ElementaryHoarseFlea.mp4",
			Poster: "https://media.redgifs.com/ElementaryHoarseFlea-poster.jpg",
			Secs:   12,
		}, nil
	}

	rec := httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, "/redgif?id=ElementaryHoarseFlea", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"video":"https://media.redgifs.com/ElementaryHoarseFlea.mp4"`, `"len":"0:12"`, `"poster":`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer %s, want %s", body, want)
		}
	}
	if rec.Header().Get("Cache-Control") == "" {
		t.Error("a resolved clip should be cacheable by the browser")
	}

	// Nothing to play: the page keeps its button and can try again.
	rec = httptest.NewRecorder()
	l.handle(rec, httptest.NewRequest(http.MethodGet, "/redgif?id=missing", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("an unresolved clip gave %d, want 502", rec.Code)
	}

	// Anything that isn't a clip id is refused, so /redgif can't be aimed
	// elsewhere.
	for _, bad := range []string{"", "ab", "..%2F..%2Fetc%2Fpasswd", "some-clip", "a/b", strings.Repeat("a", 65)} {
		rec = httptest.NewRecorder()
		l.handle(rec, httptest.NewRequest(http.MethodGet, "/redgif?id="+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id=%q gave %d, want 400", bad, rec.Code)
		}
	}
}
