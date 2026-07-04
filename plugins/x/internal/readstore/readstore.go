// Package readstore persists which x.com posts the user has marked read. x.com
// exposes no server-side read state, so x-tui tracks it locally: a set of tweet
// ids saved as JSON under the user's state dir and re-applied on every fetch, so
// a post you already saw stays greyed (or hidden) across refreshes and restarts.
package readstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// maxEntries caps how many read markers we retain so the file can't grow without
// bound. A home timeline only surfaces recent posts, so once an id ages out of
// the newest maxEntries it will never resurface and is safe to forget.
const maxEntries = 20000

// Store is a persistent set of read tweet ids. It is safe for concurrent use:
// the UI marks and queries from the update loop while Save runs on a background
// goroutine.
type Store struct {
	path string
	mu   sync.Mutex
	ids  map[string]int64 // tweet id -> unix seconds when marked read
}

type fileFormat struct {
	Read map[string]int64 `json:"read"`
}

// Load reads the store at path, or the default location when path is empty. A
// missing or unreadable file yields an empty store rather than an error: read
// tracking is a convenience, never a reason to fail startup.
func Load(path string) *Store {
	if path == "" {
		path = DefaultPath()
	}
	s := &Store{path: path, ids: map[string]int64{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f fileFormat
	if json.Unmarshal(data, &f) == nil && f.Read != nil {
		s.ids = f.Read
	}
	return s
}

// DefaultPath is $XDG_STATE_HOME/x-tui/read.json, falling back to
// ~/.local/state/x-tui/read.json. Empty when the home dir can't be resolved.
func DefaultPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "x-tui", "read.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "x-tui", "read.json")
}

// Path reports where the store persists.
func (s *Store) Path() string { return s.path }

// Has reports whether id has been marked read.
func (s *Store) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

// Mark records id as read, refreshing its recency so pruning keeps it longest.
func (s *Store) Mark(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	s.ids[id] = time.Now().Unix()
	s.mu.Unlock()
}

// Unmark restores id to unread.
func (s *Store) Unmark(id string) {
	s.mu.Lock()
	delete(s.ids, id)
	s.mu.Unlock()
}

// Save writes the store to disk atomically (temp file + rename) after pruning to
// the newest maxEntries markers. A store with no resolvable path is a silent
// no-op.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	s.prune()
	snapshot := make(map[string]int64, len(s.ids))
	for k, v := range s.ids {
		snapshot[k] = v
	}
	s.mu.Unlock()

	data, err := json.Marshal(fileFormat{Read: snapshot})
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

// prune drops the oldest markers once the set exceeds maxEntries. Caller holds
// the lock.
func (s *Store) prune() {
	if len(s.ids) <= maxEntries {
		return
	}
	type entry struct {
		id string
		ts int64
	}
	entries := make([]entry, 0, len(s.ids))
	for id, ts := range s.ids {
		entries = append(entries, entry{id, ts})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ts > entries[j].ts })
	kept := make(map[string]int64, maxEntries)
	for _, e := range entries[:maxEntries] {
		kept[e.id] = e.ts
	}
	s.ids = kept
}
