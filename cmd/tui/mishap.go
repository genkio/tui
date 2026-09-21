package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// A mishap is the last thing that failed with nobody in front of it. The sift
// and the summaries both run on the server's own clock — a fetch asks for them
// every quarter of an hour — so a model that has started refusing, a key that
// has expired or a CLI that has stopped answering shows up as a feature that
// quietly does nothing, and the reason reaches the log and nowhere else. The
// page's own toasts only ever reached whoever happened to be watching the
// button at the time.
//
// One at a time rather than a list: what is worth saying is that the reading is
// failing and why, and the latest reason is the truest one. Every note takes a
// name, which is what a dismissal is remembered against — the same trouble on
// the next fetch is a new name, so the banner comes back rather than staying
// dismissed for a thing that is still happening.
//
// The name carries the server's start time as well as the count, so a browser
// that dismissed the third mishap of yesterday's server is not still dismissing
// the third of this one. In memory only either way: a restart is a fresh start,
// and a reason from before it is about a server that is no longer running.
type mishap struct {
	ID   string
	Kind string // "sift" or "summary": which half of the reading it was
	Msg  string
	At   time.Time
}

type mishapLog struct {
	mu   sync.Mutex
	last mishap
	boot int64
	n    int64
}

// note files one. Nil-safe and empty-safe: the callers are failure paths in
// tests as well as in the server, and a failure with nothing to say about it is
// not worth a banner.
func (m *mishapLog) note(kind, msg string) {
	if m == nil || msg == "" {
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.boot == 0 {
		m.boot = now.Unix()
	}
	m.n++
	m.last = mishap{
		ID:   strconv.FormatInt(m.boot, 10) + "-" + strconv.FormatInt(m.n, 10),
		Kind: kind, Msg: msg, At: now,
	}
}

func (m *mishapLog) latest() (mishap, bool) {
	if m == nil {
		return mishap{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last, m.last.ID != ""
}

// showStatus (GET /status) is what a page asks while a fetch it started is
// going, and every minute besides, for the banner.
func showStatus(w http.ResponseWriter, sweep *sweeper, cache *feedCache, mishaps *mishapLog) {
	out := struct {
		Fetching bool   `json:"fetching"`
		Unread   int    `json:"unread"`
		Mishap   string `json:"mishap,omitempty"` // empty when nothing has gone wrong
		Says     string `json:"says,omitempty"`
		When     string `json:"when,omitempty"`
	}{Fetching: sweep.sweeping(), Unread: cache.unreadCount()}
	if m, ok := mishaps.latest(); ok {
		out.Mishap, out.Says, out.When = m.ID, m.says(), m.At.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// says is the banner's own line: what stopped working, then the reason in the
// words whatever refused used. The kind is worth naming because the two fail
// for different reasons and are fixed in different places — a sift is the
// TypeSafe key, a summary is the pi CLI.
func (m mishap) says() string {
	what := "a summary could not be written"
	if m.Kind == "sift" {
		what = "the sift failed"
	}
	return what + ": " + m.Msg
}
