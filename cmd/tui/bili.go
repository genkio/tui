package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A bilibili card carries no stream a page can use: the mp4 hides behind a
// play-URL call, and the file itself is refused without a bilibili Referer —
// which a page cannot forge (and this one deliberately sends none at all). So the
// card's player points at /bili, and both the lookup and the fetch happen here.
// The route streams rather than redirects: a resolved URL expires within hours,
// so the stable src for a saved item is this address, not that one.
const (
	biliPath    = "/bili"
	biliViewAPI = "https://api.bilibili.com/x/web-interface/view"
	biliPlayAPI = "https://api.bilibili.com/x/player/playurl"
	biliReferer = "https://www.bilibili.com/"
	biliUA      = imageUA
	// A play URL states about two hours; re-resolve well inside that rather than
	// discover mid-seek that it went stale.
	biliPlayTTL = 30 * time.Minute
	// A video that would not resolve is retried later, in case bilibili was only
	// briefly unhappy (a risk-control window passes on its own).
	biliMissTTL = 5 * time.Minute
	biliLookups = 4
	// fnval=1 asks for the combined mp4, the one stream a plain <video> can play.
	// 80 is 1080p, which bilibili answers to a logged-in session; without one it
	// hands back whatever the try_look preview allows, normally 360p–720p.
	biliQuality = "80"
)

// The lookups get a deadline; the stream must not, since a client Timeout covers
// reading the body too and would cut a long video off mid-play. The request
// context bounds that one instead.
var (
	biliAPIHTTP    = &http.Client{Timeout: 20 * time.Second}
	biliStreamHTTP = &http.Client{}
)

// biliIDRe is the bvid shape a watch URL carries. Anything else is refused, so
// /bili can't be aimed elsewhere.
var biliIDRe = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)

// biliStream is one video's playable mirrors and when they were resolved.
type biliStream struct {
	urls []string // the mp4 and its mirrors, best first
	at   time.Time
}

// biliLens resolves bilibili videos to a stream and proxies the bytes.
type biliLens struct {
	mu      sync.Mutex
	cids    map[string]int64      // bvid -> cid; a video's page id never changes
	streams map[string]biliStream // bvid -> its mirrors, until they age out
	miss    map[string]time.Time  // bvid -> when it may be retried
	gate    chan struct{}

	now    func() time.Time
	cookie func() string
	cid    func(ctx context.Context, id, cookie string) (int64, error)
	durl   func(ctx context.Context, id string, cid int64, cookie string) ([]string, error)
	open   func(ctx context.Context, urls []string, rng string) (*http.Response, error)
}

func newBiliLens() *biliLens {
	return &biliLens{
		cids:    map[string]int64{},
		streams: map[string]biliStream{},
		miss:    map[string]time.Time{},
		gate:    make(chan struct{}, biliLookups),
		now:     time.Now,
		cookie:  biliCookie,
		cid:     fetchBiliCID,
		durl:    fetchBiliDurl,
		open:    openBiliStream,
	}
}

// biliCookie is the session the bilibili plugin captured, read fresh so a
// re-login while the server runs is picked up. It is what earns the good
// qualities; without it the anonymous preview stream is all bilibili offers.
func biliCookie() string { return os.Getenv("BLTUI_COOKIE") }

// handle streams one video to the page: the resolved mp4, proxied with the
// Referer bilibili's CDN insists on, and Range passed through so the player can
// seek. ?dl=1 sends the same bytes as a download instead.
func (l *biliLens) handle(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !biliIDRe.MatchString(id) {
		http.Error(w, "id must be a bilibili video id (BV…)", http.StatusBadRequest)
		return
	}

	rng := r.Header.Get("Range")
	resp, err := l.fetch(r.Context(), id, rng)
	if err != nil {
		http.Error(w, "bilibili video: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// The bytes under this address never change, but the address is only ever
	// reachable from this server, so keep it out of shared caches.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.URL.Query().Get("dl") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.mp4"`)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// fetch opens the stream, resolving it again once when every mirror refuses: a
// remembered URL that has expired looks exactly like this, and a second lookup
// is cheaper than a failed tap.
func (l *biliLens) fetch(ctx context.Context, id, rng string) (*http.Response, error) {
	urls, ok := l.stream(ctx, id, false)
	if !ok {
		return nil, errors.New("bilibili handed out no playable stream")
	}
	resp, err := l.open(ctx, urls, rng)
	if err == nil || ctx.Err() != nil {
		return resp, err
	}
	// Every mirror refused. A URL that quietly expired looks exactly like this,
	// so it is worth one fresh lookup before the tap is given up on.
	urls, ok = l.stream(ctx, id, true)
	if !ok {
		return nil, err
	}
	return l.open(ctx, urls, rng)
}

// stream returns the video's mirrors, resolving them when they are missing or
// aged out. fresh forces a lookup, ignoring what is remembered.
func (l *biliLens) stream(ctx context.Context, id string, fresh bool) ([]string, bool) {
	l.mu.Lock()
	got, hit := l.streams[id]
	until, missed := l.miss[id]
	l.mu.Unlock()
	switch {
	case fresh: // the caller just watched these mirrors fail
	case hit && l.now().Sub(got.at) < biliPlayTTL:
		return got.urls, true
	case missed && l.now().Before(until):
		return nil, false
	}

	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, false
	}
	urls, err := l.lookup(ctx, id)
	<-l.gate

	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil || len(urls) == 0 {
		l.miss[id] = l.now().Add(biliMissTTL)
		return nil, false
	}
	delete(l.miss, id)
	l.streams[id] = biliStream{urls: urls, at: l.now()}
	return urls, true
}

// lookup is the two calls a stream costs: the video's cid, then the play URLs
// for it. The cid is kept for the life of the process — it is part of the
// video's identity — while the URLs it yields are not.
func (l *biliLens) lookup(ctx context.Context, id string) ([]string, error) {
	cookie := l.cookie()
	l.mu.Lock()
	cid, known := l.cids[id]
	l.mu.Unlock()
	if !known {
		got, err := l.cid(ctx, id, cookie)
		if err != nil {
			return nil, err
		}
		cid = got
		l.mu.Lock()
		l.cids[id] = cid
		l.mu.Unlock()
	}
	return l.durl(ctx, id, cid, cookie)
}

// biliRequest builds a fetch that looks like the bilibili page it stands in for.
// The Referer is load-bearing twice over: the APIs risk-control a request without
// one, and the CDN answers 403 to a stream request that carries the wrong one.
func biliRequest(ctx context.Context, raw, cookie string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", biliUA)
	req.Header.Set("Referer", biliReferer)
	req.Header.Set("Origin", "https://www.bilibili.com")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	return req, nil
}

// biliAPI reads one JSON reply from bilibili's web API into out, turning a
// non-zero reply code into an error (bilibili answers HTTP 200 either way).
func biliAPI(ctx context.Context, raw, cookie string, out any) error {
	req, err := biliRequest(ctx, raw, cookie)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := biliAPIHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bilibili api: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var head struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return err
	}
	if head.Code != 0 {
		if head.Message == "" {
			head.Message = "no message"
		}
		return fmt.Errorf("bilibili api code %d: %s", head.Code, head.Message)
	}
	return json.Unmarshal(body, out)
}

// fetchBiliCID asks which page of the video to play. Every video has at least
// one; a multi-part upload plays its first, which is what its own page opens on.
func fetchBiliCID(ctx context.Context, id, cookie string) (int64, error) {
	var out struct {
		Data struct {
			CID   int64 `json:"cid"`
			Pages []struct {
				CID int64 `json:"cid"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := biliAPI(ctx, biliViewAPI+"?bvid="+url.QueryEscape(id), cookie, &out); err != nil {
		return 0, err
	}
	if cid := out.Data.CID; cid > 0 {
		return cid, nil
	}
	if len(out.Data.Pages) > 0 && out.Data.Pages[0].CID > 0 {
		return out.Data.Pages[0].CID, nil
	}
	return 0, errors.New("bilibili stated no playable page for that video")
}

// fetchBiliDurl asks for the combined mp4 and its mirrors. try_look is what lets
// a session-less server get a preview stream at all; with a session it is simply
// ignored.
func fetchBiliDurl(ctx context.Context, id string, cid int64, cookie string) ([]string, error) {
	q := url.Values{
		"bvid":     {id},
		"cid":      {strconv.FormatInt(cid, 10)},
		"qn":       {biliQuality},
		"fnval":    {"1"}, // combined mp4, not the split DASH tracks
		"fnver":    {"0"},
		"fourk":    {"0"},
		"try_look": {"1"},
	}
	var out struct {
		Data struct {
			Durl []struct {
				URL    string   `json:"url"`
				Backup []string `json:"backup_url"`
			} `json:"durl"`
		} `json:"data"`
	}
	if err := biliAPI(ctx, biliPlayAPI+"?"+q.Encode(), cookie, &out); err != nil {
		return nil, err
	}
	if len(out.Data.Durl) == 0 {
		return nil, errors.New("bilibili returned no mp4 stream (the video may need a session)")
	}
	first := out.Data.Durl[0]
	return biliMirrors(append([]string{first.URL}, first.Backup...)), nil
}

// biliMirrors keeps the addresses that are bilibili's own https media hosts, one
// per host, in the order given. A surprising reply therefore cannot make this
// server fetch anything else.
func biliMirrors(urls []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Scheme != "https" || !biliMediaHost(u.Host) || seen[u.Host] {
			continue
		}
		seen[u.Host] = true
		out = append(out, u.String())
	}
	return out
}

// biliMediaHost reports whether host is one bilibili serves video from: its own
// upos storage, its CDN domains, or the Akamai edge it rents.
func biliMediaHost(host string) bool {
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	for _, suffix := range []string{".bilivideo.com", ".bilivideo.cn", ".akamaized.net", ".hdslb.com"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// openBiliStream fetches the first mirror that answers, forwarding the player's
// Range so seeking works. A refusal is normal: an expired URL and a mirror that
// is simply down look the same, and the next one usually serves.
func openBiliStream(ctx context.Context, urls []string, rng string) (*http.Response, error) {
	var last error
	for _, u := range urls {
		req, err := biliRequest(ctx, u, "") // public files; no session goes to the CDN
		if err != nil {
			last = err
			continue
		}
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := biliStreamHTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
			return resp, nil
		}
		last = fmt.Errorf("mirror %s: %s", req.URL.Host, resp.Status)
		resp.Body.Close()
	}
	if last == nil {
		last = errors.New("no mirror to try")
	}
	return nil, last
}
