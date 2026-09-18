package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// interestStore is the reader's own list: the subjects they are following at
// the moment, typed into settings one per line, and the third thing a sift asks
// about every item.
//
// It is the counterpart of the block list, and deliberately its opposite in
// every way. A keyword is a string matched against a title, which is the right
// tool for "never show me this word" and useless for "I am reading about the
// Fed this month" — the posts that answer that never say it. So this list is
// not matched against anything; it is given to the model as what the reader is
// after, and the model says whether an item is about it. An item that is gets a
// chip of its own, and is never skipped however little the cut made of it: the
// reader named the subject themselves.
//
// It lives on the server rather than in the browser, unlike the settings beside
// it, because the judging happens on the server and the answer belongs to the
// backlog rather than to the tab that asked.
type interestStore struct {
	mu    sync.Mutex
	db    *feedDB
	text  string
	lines []string
}

const (
	// What the textarea will take: a handful of subjects, not an essay. Every
	// item is asked about every subject, one question each, so the list is what
	// decides how many items fit in a request (siftBatchFor) — and a list
	// longer than this has stopped being a list of interests anyway.
	maxInterests   = 12
	maxInterestLen = 120
)

func loadInterestsDB(db *feedDB) (*interestStore, error) {
	text, err := db.loadInterests()
	if err != nil {
		return nil, err
	}
	return &interestStore{db: db, text: text, lines: parseInterests(text)}, nil
}

// list is what a sift asks about, and empty when nothing has been typed — in
// which case the question is not asked at all.
func (s *interestStore) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

// text is the list as the textarea shows it, one per line.
func (s *interestStore) words() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

func (s *interestStore) set(text string) error {
	lines := parseInterests(text)
	s.mu.Lock()
	s.text, s.lines = text, lines
	s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.saveInterests(strings.Join(lines, "\n"))
}

// parseInterests takes the textarea apart: one subject per line, blanks and
// repeats dropped, each clipped to something a line can hold.
func parseInterests(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len([]rune(line)) > maxInterestLen {
			line = strings.TrimSpace(string([]rune(line)[:maxInterestLen]))
		}
		if key := strings.ToLower(line); !seen[key] {
			seen[key] = true
			out = append(out, line)
		}
		if len(out) == maxInterests {
			break
		}
	}
	return out
}

// handleInterests (POST /interests) replaces the list. Every judgment in the
// backlog goes with it: an answer about a list is worth nothing once the list
// has changed, so the next sift asks again from the top rather than leaving a
// chip full of what the old list caught.
func handleInterests(w http.ResponseWriter, r *http.Request, interests *interestStore, cache *feedCache) {
	text := r.FormValue("interests")
	if len([]rune(text)) > maxInterests*maxInterestLen {
		http.Error(w, "that is longer than a list of interests", http.StatusBadRequest)
		return
	}
	if err := interests.set(text); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	forgotten := cache.forget()
	if err := cache.save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"interests":%d,"forgotten":%d}`, len(interests.list()), forgotten)
}
