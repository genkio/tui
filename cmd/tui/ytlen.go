package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// A YouTube card is built in the browser from a link in the body, and nothing
// it can reach carries the clip's length: oEmbed omits it, the watch page buries
// it a megabyte in, and asking the player means loading the player — the very
// download the badge exists to let you refuse. The endpoint YouTube's own site
// calls answers with ~16KB of JSON that carries it, and takes no API key, so the
// lookup happens here and the page asks over /ytlen.
const (
	ytPlayerAPI = "https://www.youtube.com/youtubei/v1/player"
	ytClientVer = "2.20240726.00.00"
	// A 24/7 livestream reports a nonsense length (years); treat anything past a
	// day as no answer rather than print it.
	ytMaxSecs = 24 * 3600
	// Lengths never change, so a hit is kept for the life of the process. A miss
	// is retried later, in case YouTube was only briefly unhappy.
	ytMissTTL = 10 * time.Minute
	// YouTube gets a handful of lookups at a time: a feed of thirty video items
	// otherwise opens thirty connections the moment the page loads.
	ytLookups = 4
)

var ytHTTP = &http.Client{Timeout: 10 * time.Second}

// ytLens resolves and remembers YouTube clip lengths.
type ytLens struct {
	mu   sync.Mutex
	secs map[string]int       // id -> length, resolved
	miss map[string]time.Time // id -> when it may be retried
	gate chan struct{}

	now   func() time.Time
	fetch func(context.Context, string) (int, error) // swapped in tests
}

func newYTLens() *ytLens {
	return &ytLens{
		secs:  map[string]int{},
		miss:  map[string]time.Time{},
		gate:  make(chan struct{}, ytLookups),
		now:   time.Now,
		fetch: fetchYTLen,
	}
}

// get returns the clip's length in seconds, or 0 when YouTube didn't say.
func (y *ytLens) get(ctx context.Context, id string) int {
	y.mu.Lock()
	secs, hit := y.secs[id]
	until, missed := y.miss[id]
	y.mu.Unlock()
	switch {
	case hit:
		return secs
	case missed && y.now().Before(until):
		return 0
	}

	select {
	case y.gate <- struct{}{}:
	case <-ctx.Done():
		return 0
	}
	secs, err := y.fetch(ctx, id)
	<-y.gate

	y.mu.Lock()
	defer y.mu.Unlock()
	if err != nil || secs <= 0 {
		y.miss[id] = y.now().Add(ytMissTTL)
		return 0
	}
	y.secs[id] = secs
	return secs
}

var ytIDRe = regexp.MustCompile(`^[\w-]{11}$`) // same shape the page's ytId() matches

// handle answers the page's "how long is this one?" with the formatted badge
// text, blank when unknown. The browser may keep an answer: a length is fixed,
// and a reload on mobile data should not ask again.
func (y *ytLens) handle(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("v")
	if !ytIDRe.MatchString(id) {
		http.Error(w, "v must be a YouTube video id", http.StatusBadRequest)
		return
	}
	secs := y.get(r.Context(), id)
	w.Header().Set("Content-Type", "application/json")
	if secs > 0 {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"len": vidLen(secs)})
}

// fetchYTLen asks YouTube's player endpoint for one video's details. The reply
// says the video is unplayable (this is not a browser and carries no session),
// but it still states the length, which is all the badge needs.
func fetchYTLen(ctx context.Context, id string) (int, error) {
	body, err := json.Marshal(map[string]any{
		"videoId": id,
		"context": map[string]any{
			"client": map[string]any{"clientName": "WEB", "clientVersion": ytClientVer, "hl": "en"},
		},
	})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ytPlayerAPI, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ytHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("youtube player: %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	return ytSecsFromPlayer(raw)
}

// ytSecsFromPlayer reads the length out of a player response, reporting 0 for
// anything it can't trust.
func ytSecsFromPlayer(raw []byte) (int, error) {
	var out struct {
		VideoDetails struct {
			LengthSeconds string `json:"lengthSeconds"`
		} `json:"videoDetails"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	secs, err := strconv.Atoi(out.VideoDetails.LengthSeconds)
	if err != nil || secs <= 0 || secs > ytMaxSecs {
		return 0, nil
	}
	return secs, nil
}
