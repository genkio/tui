package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A redgifs post on reddit is a link post: the card carries the watch page, and
// nothing in the feed JSON is playable. The mp4 itself is public and needs no
// headers, but it lives under the id in its original casing — the lowercase id
// the link carries answers 403 — and only redgifs' API knows that casing. So
// the footer's video button asks over /redgif and the lookup happens here.
const (
	redgifAuthAPI = "https://api.redgifs.com/v2/auth/temporary"
	redgifGifAPI  = "https://api.redgifs.com/v2/gifs/"
	// redgifs binds the temporary token to the address *and* user agent that
	// asked for it, so the token call and every lookup after it wear the same
	// browser user agent.
	redgifUA = imageUA
	// The token states a day; refresh well inside that rather than discover it
	// expired mid-tap.
	redgifTokenTTL = 12 * time.Hour
	// A resolved clip is kept for the life of the process (the mp4 URL is the id
	// in another casing, which never changes); a miss is retried later, in case
	// redgifs was only briefly unhappy.
	redgifMissTTL = 10 * time.Minute
	redgifLookups = 4
)

var redgifHTTP = &http.Client{Timeout: 15 * time.Second}

// errRedgifAuth is the one failure worth a second attempt: the token went stale
// earlier than its stated life, so a fresh one may answer.
var errRedgifAuth = errors.New("redgifs rejected the token")

type redgifClip struct {
	Video  string
	Poster string
	Secs   int
}

// redgifLens resolves and remembers redgifs clips.
type redgifLens struct {
	mu    sync.Mutex
	clips map[string]redgifClip
	miss  map[string]time.Time
	token string
	tokAt time.Time
	gate  chan struct{}

	now  func() time.Time
	mint func(context.Context) (string, error)                           // swapped in tests
	gif  func(ctx context.Context, id, token string) (redgifClip, error) // swapped in tests
}

func newRedgifLens() *redgifLens {
	return &redgifLens{
		clips: map[string]redgifClip{},
		miss:  map[string]time.Time{},
		gate:  make(chan struct{}, redgifLookups),
		now:   time.Now,
		mint:  fetchRedgifToken,
		gif:   fetchRedgif,
	}
}

// get returns the clip for an id, or false when redgifs didn't say.
func (l *redgifLens) get(ctx context.Context, id string) (redgifClip, bool) {
	l.mu.Lock()
	clip, hit := l.clips[id]
	until, missed := l.miss[id]
	l.mu.Unlock()
	switch {
	case hit:
		return clip, true
	case missed && l.now().Before(until):
		return redgifClip{}, false
	}

	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return redgifClip{}, false
	}
	clip, err := l.lookup(ctx, id)
	<-l.gate

	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil || clip.Video == "" {
		l.miss[id] = l.now().Add(redgifMissTTL)
		return redgifClip{}, false
	}
	l.clips[id] = clip
	return clip, true
}

func (l *redgifLens) lookup(ctx context.Context, id string) (redgifClip, error) {
	tok, err := l.authToken(ctx, false)
	if err != nil {
		return redgifClip{}, err
	}
	clip, err := l.gif(ctx, id, tok)
	if errors.Is(err, errRedgifAuth) {
		if tok, err = l.authToken(ctx, true); err != nil {
			return redgifClip{}, err
		}
		clip, err = l.gif(ctx, id, tok)
	}
	return clip, err
}

// authToken hands out the cached anonymous token, minting a new one when it is
// missing, aged out, or force says the old one was refused.
func (l *redgifLens) authToken(ctx context.Context, force bool) (string, error) {
	l.mu.Lock()
	tok, at := l.token, l.tokAt
	l.mu.Unlock()
	if !force && tok != "" && l.now().Sub(at) < redgifTokenTTL {
		return tok, nil
	}
	tok, err := l.mint(ctx)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	l.token, l.tokAt = tok, l.now()
	l.mu.Unlock()
	return tok, nil
}

// redgifIDRe is the id shape the watch URL carries: a run of lowercase words
// with no separators. Anything else is refused, so /redgif can't be aimed
// elsewhere.
var redgifIDRe = regexp.MustCompile(`^[a-z0-9]{3,64}$`)

// handle answers the page's "where is this clip?" with a direct mp4, its poster
// and its length. The browser may keep an answer: the URL is the id in another
// casing, so it is as fixed as the id is.
func (l *redgifLens) handle(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.URL.Query().Get("id"))
	if !redgifIDRe.MatchString(id) {
		http.Error(w, "id must be a redgifs clip id", http.StatusBadRequest)
		return
	}
	clip, ok := l.get(r.Context(), id)
	if !ok {
		http.Error(w, "redgifs did not answer for that clip", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"video":  clip.Video,
		"poster": clip.Poster,
		"len":    vidLen(clip.Secs),
	})
}

// fetchRedgifToken asks for the anonymous token redgifs' own site uses: no
// account, no key, good for a day.
func fetchRedgifToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, redgifAuthAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", redgifUA)
	resp, err := redgifHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("redgifs auth: %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("redgifs auth returned no token")
	}
	return out.Token, nil
}

func fetchRedgif(ctx context.Context, id, token string) (redgifClip, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, redgifGifAPI+id, nil)
	if err != nil {
		return redgifClip{}, err
	}
	req.Header.Set("User-Agent", redgifUA)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := redgifHTTP.Do(req)
	if err != nil {
		return redgifClip{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return redgifClip{}, errRedgifAuth
	case resp.StatusCode != http.StatusOK:
		return redgifClip{}, fmt.Errorf("redgifs gif: %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return redgifClip{}, err
	}
	return redgifClipFrom(raw)
}

// redgifClipFrom reads the playable stream out of a gif reply: the HD mp4 when
// there is one, else the mobile cut. Only https media URLs are taken, so a
// surprising reply can't put anything else in the page's <video src>.
func redgifClipFrom(raw []byte) (redgifClip, error) {
	var out struct {
		Gif struct {
			Duration float64 `json:"duration"`
			URLs     struct {
				HD        string `json:"hd"`
				SD        string `json:"sd"`
				Poster    string `json:"poster"`
				Thumbnail string `json:"thumbnail"`
			} `json:"urls"`
		} `json:"gif"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return redgifClip{}, err
	}
	u := out.Gif.URLs
	clip := redgifClip{
		Video:  firstMedia(u.HD, u.SD),
		Poster: firstMedia(u.Poster, u.Thumbnail),
	}
	if d := out.Gif.Duration; d > 0 && d < 24*3600 {
		clip.Secs = int(math.Round(d))
	}
	return clip, nil
}

// firstMedia returns the first URL that is redgifs' own https media host.
func firstMedia(urls ...string) string {
	for _, u := range urls {
		if strings.HasPrefix(u, "https://media.redgifs.com/") {
			return u
		}
	}
	return ""
}
