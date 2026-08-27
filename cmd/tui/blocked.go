package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// blocker is the block list: the keywords you have asked to keep out of the
// feed, and the posts they caught.
//
// A sweep screens what it fetched before the cache ever sees it, so a blocked
// post never becomes backlog you have to triage away. It is filed rather than
// dropped: the blocked-items table is a record you can read back and clear when
// you have seen enough of it, not a black hole.
type blocker struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	wordPath string
	itemPath string
	db       *feedDB
	words    []string
	items    []blockedItem // newest block first
}

func loadBlockerDB(db *feedDB) (*blocker, error) {
	words, items, err := db.loadBlocker()
	if err != nil {
		return nil, err
	}
	return &blocker{db: db, wordPath: db.path, itemPath: db.path, words: words, items: items}, nil
}

// blockedItem is one post the list caught, kept whole the way a saved one is:
// the feed it came from is transient, and the blocked view has to render it
// long after that feed has moved on.
type blockedItem struct {
	core.Wire
	BlockedAt string `json:"blocked_at"`
	Keyword   string `json:"keyword,omitempty"` // which word caught it
}

type keywordFile struct {
	Keywords []string `json:"keywords"`
}

type blockedFile struct {
	Items []blockedItem `json:"items"`
}

const (
	// maxKeywords and maxKeywordLen bound what the modal can post. The list is
	// walked once per fetched item, so a pasted novel should be a clear refusal
	// rather than a sweep that quietly slows to a crawl.
	maxKeywords   = 500
	maxKeywordLen = 200
)

// loadBlocker reads the legacy JSON stores used by tests and migration.
func loadBlocker(wordPath, itemPath string) *blocker {
	if wordPath == "" {
		wordPath = core.StatePath("tui", "keywords.json")
	}
	if itemPath == "" {
		itemPath = core.StatePath("tui", "blocked.json")
	}
	b := &blocker{wordPath: wordPath, itemPath: itemPath}
	if data, err := os.ReadFile(wordPath); err == nil {
		var f keywordFile
		if json.Unmarshal(data, &f) == nil {
			b.words = parseKeywords(strings.Join(f.Keywords, "\n"))
		}
	}
	if data, err := os.ReadFile(itemPath); err == nil {
		var f blockedFile
		if json.Unmarshal(data, &f) == nil {
			b.items = f.Items
		}
	}
	return b
}

// parseKeywords reads the modal's textarea: one keyword per line, blanks
// dropped and repeats folded (case-insensitively), so the number the header
// states is the number that does the work.
func parseKeywords(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		w := strings.TrimSpace(line)
		if w == "" {
			continue
		}
		k := strings.ToLower(w)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, w)
	}
	return out
}

// validKeywords is what the /keywords handler answers 400 with, so a refusal
// says which of the two caps was hit rather than silently keeping the old list.
func validKeywords(words []string) error {
	if len(words) > maxKeywords {
		return fmt.Errorf("%d keywords is past the %d cap", len(words), maxKeywords)
	}
	for _, w := range words {
		if len([]rune(w)) > maxKeywordLen {
			return fmt.Errorf("a keyword is longer than %d characters", maxKeywordLen)
		}
	}
	return nil
}

func (b *blocker) keywordCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.words)
}

// keywordText is the list as the textarea shows it: one per line.
func (b *blocker) keywordText() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.words, "\n")
}

func (b *blocker) setKeywords(words []string) error {
	b.mu.Lock()
	b.words = words
	b.mu.Unlock()
	return b.saveWords()
}

func (b *blocker) count() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

// match reports which keyword the title carries, blank when none does. Plain
// case-insensitive substring: the list is hand-written and short, and a
// substring is what "keep posts about X out" means for a Chinese title as much
// as an English one, where there are no word boundaries to match on.
func (b *blocker) match(title string) string {
	if b == nil || title == "" {
		return ""
	}
	t := strings.ToLower(title)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, w := range b.words {
		if strings.Contains(t, strings.ToLower(w)) {
			return w
		}
	}
	return ""
}

// split sorts a fetch into what the feed keeps and what a keyword caught. Only
// the title is read: a post that carries its whole text as its title (an x
// post) has no title to speak of and is left alone, and matching a keyword
// anywhere in a body would block a great deal more than was asked for.
func (b *blocker) split(items []core.Item, now time.Time) ([]core.Item, []blockedItem) {
	if b == nil || b.keywordCount() == 0 {
		return items, nil
	}
	stamp := now.UTC().Format(time.RFC3339)
	keep := make([]core.Item, 0, len(items))
	var caught []blockedItem
	for _, it := range items {
		w := b.match(itemTitle(it))
		if w == "" {
			keep = append(keep, it)
			continue
		}
		caught = append(caught, blockedItem{Wire: it.Wire(), BlockedAt: stamp, Keyword: w})
	}
	return keep, caught
}

// file stores what a screen caught, skipping anything already there, and
// reports how many were new. Nothing upstream knows we don't want these, so a
// blocked post is fetched again on every sweep — the dedupe is what keeps the
// file from growing a copy of it per sweep.
func (b *blocker) file(caught []blockedItem) (int, error) {
	b.mu.Lock()
	add := make([]blockedItem, 0, len(caught))
	for _, e := range caught {
		if blockedIndex(b.items, e.App, e.ID) >= 0 || blockedIndex(add, e.App, e.ID) >= 0 {
			continue
		}
		add = append(add, e)
	}
	if len(add) == 0 {
		b.mu.Unlock()
		return 0, nil
	}
	b.items = append(add, b.items...)
	b.mu.Unlock()
	return len(add), b.saveItems()
}

// purge re-screens the backlog that is already cached, so a keyword added now
// also clears the posts sitting in the feed rather than only the ones that
// arrive next. Without it, a word you added because of what you were looking at
// would leave exactly that on screen.
//
// Filed before dropped, the way the sweeper persists before it says anything
// upstream: a crash in between leaves a duplicate, which the next screen folds
// away, rather than an item that exists nowhere.
func (b *blocker) purge(cache *feedCache, now time.Time) (int, error) {
	_, caught := b.split(cache.unread(now, ""), now)
	if len(caught) == 0 {
		return 0, nil
	}
	fresh, err := b.file(caught)
	if err != nil {
		return fresh, err
	}
	keys := make([]string, 0, len(caught))
	for _, e := range caught {
		keys = append(keys, core.Key(e.App, e.ID))
	}
	cache.drop(keys)
	return fresh, cache.save()
}

// list returns the blocked posts as feed items, most recently blocked first —
// what the last sweep kept out is what you came to check. Ages are recomputed
// from the publish time, the way the saved list does it.
func (b *blocker) list(now time.Time) []core.Item {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	byRecent := make([]blockedItem, len(b.items))
	copy(byRecent, b.items)
	// RFC3339 in UTC, so lexical order is chronological; a stable sort leaves
	// one screen's worth in the order the fetch handed it over.
	sort.SliceStable(byRecent, func(i, j int) bool { return byRecent[i].BlockedAt > byRecent[j].BlockedAt })
	out := make([]core.Item, 0, len(byRecent))
	for _, e := range byRecent {
		it := e.Wire.Item(now)
		if !it.At.IsZero() {
			it.Age = humanAgo(it.At)
		}
		out = append(out, it)
	}
	return out
}

// caughtBy names the keyword that blocked one post, which the compact row shows
// so the list answers "why is this here" without being opened.
func (b *blocker) caughtBy(app, id string) string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if i := blockedIndex(b.items, app, id); i >= 0 {
		return b.items[i].Keyword
	}
	return ""
}

func blockedIndex(items []blockedItem, app, id string) int {
	for i, e := range items {
		if e.App == app && e.ID == id {
			return i
		}
	}
	return -1
}

func (b *blocker) saveWords() error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.mu.Lock()
	words := append([]string(nil), b.words...)
	items := append([]blockedItem(nil), b.items...)
	db := b.db
	if words == nil {
		words = []string{}
	}
	b.mu.Unlock()
	if db != nil {
		return db.replaceBlocker(words, items)
	}
	data, err := json.MarshalIndent(keywordFile{Keywords: words}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(b.wordPath, data)
}

func (b *blocker) saveItems() error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.mu.Lock()
	words := append([]string(nil), b.words...)
	items := append([]blockedItem(nil), b.items...)
	db := b.db
	b.mu.Unlock()
	if db != nil {
		return db.replaceBlocker(words, items)
	}
	data, err := json.MarshalIndent(blockedFile{Items: items}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(b.itemPath, data)
}

// writeAtomic writes data via a temp file and a rename, so a reader — or a file
// syncing client — never sees half of it. A store with no resolvable path is a
// silent no-op, the way the saved list treats one.
func writeAtomic(path string, data []byte) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
