package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// savedStore persists the items starred from the web page. It keeps the whole
// item, not just an id: a saved item has to outlive the feed it came from, and
// feeds are transient (an unread list you triage away, an x timeline window
// that scrolls past). It lives under the shared state dir, so --state-dir puts
// it alongside credentials and read state for syncing between devices.
type savedStore struct {
	mu    sync.Mutex
	path  string
	items []savedItem // newest save first
}

type savedItem struct {
	core.Wire
	SavedAt string `json:"saved_at"`
}

type savedFile struct {
	Items []savedItem `json:"items"`
}

// loadSaved reads the store, or the default location when path is empty. A
// missing or corrupt file yields an empty store rather than an error: saving is
// a convenience, never a reason to refuse to serve the page.
func loadSaved(path string) *savedStore {
	if path == "" {
		path = core.StatePath("tui", "saved.json")
	}
	s := &savedStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f savedFile
	if json.Unmarshal(data, &f) == nil {
		s.items = f.Items
	}
	return s
}

func (s *savedStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *savedStore) has(app, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index(app, id) >= 0
}

// index reports where app+id sits in items, or -1. Callers hold the lock.
func (s *savedStore) index(app, id string) int {
	for i, it := range s.items {
		if it.App == app && it.ID == id {
			return i
		}
	}
	return -1
}

// add stores it, refreshing an existing entry rather than duplicating it.
func (s *savedStore) add(it core.Item, now time.Time) error {
	s.mu.Lock()
	entry := savedItem{Wire: it.Wire(), SavedAt: now.UTC().Format(time.RFC3339)}
	if i := s.index(it.App, it.ID); i >= 0 {
		s.items[i] = entry
	} else {
		s.items = append([]savedItem{entry}, s.items...)
	}
	s.mu.Unlock()
	return s.save()
}

func (s *savedStore) remove(app, id string) error {
	s.mu.Lock()
	if i := s.index(app, id); i >= 0 {
		s.items = append(s.items[:i], s.items[i+1:]...)
	}
	s.mu.Unlock()
	return s.save()
}

// list returns the saved items as feed items, recomputing each one's relative
// age from its publish time so a post saved last week doesn't still read "2h".
func (s *savedStore) list(now time.Time) []core.Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]core.Item, 0, len(s.items))
	for _, e := range s.items {
		it := e.Wire.Item(now)
		if !it.At.IsZero() {
			it.Age = humanAgo(it.At)
		}
		items = append(items, it)
	}
	return items
}

// save writes the store atomically (temp file + rename). A store with no
// resolvable path is a silent no-op.
func (s *savedStore) save() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	data, err := json.MarshalIndent(savedFile{Items: s.items}, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// renderedItems remembers the items most recently sent to a browser, so /save
// can persist a whole item from just the app+id the button posts back. The page
// the user is looking at was rendered by this process, so a save right after a
// render always resolves; a restart in between is the one gap, and the page
// reports it rather than saving a stub.
type renderedItems struct {
	mu    sync.Mutex
	byKey map[string]core.Item
	order []string
}

// maxRendered caps the cache at several feed-loads' worth of items, so a
// long-running server can't grow it without bound.
const maxRendered = 2000

func newRenderedItems() *renderedItems {
	return &renderedItems{byKey: map[string]core.Item{}}
}

func (r *renderedItems) put(items []core.Item) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, it := range items {
		k := it.Key()
		if _, ok := r.byKey[k]; !ok {
			r.order = append(r.order, k)
		}
		r.byKey[k] = it
	}
	for len(r.order) > maxRendered {
		delete(r.byKey, r.order[0])
		r.order = r.order[1:]
	}
}

func (r *renderedItems) get(app, id string) (core.Item, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it, ok := r.byKey[core.Key(app, id)]
	return it, ok
}
