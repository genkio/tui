package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// item is one unread entry in the "all" timeline, normalized from any app's
// --json output. key uniquely identifies it across apps (ids collide between
// services, e.g. a numeric inoreader id and an x tweet id).
type item struct {
	App    string
	ID     string
	Title  string
	Body   string
	Source string
	Author string
	URL    string
	Age    string
	sortAt time.Time // publish time for the merged sort; zero sinks to the bottom
}

func (it item) key() string { return it.App + "\x00" + it.ID }

// wire mirrors the dumpItem shape every app prints; kept private so the app
// modules and the launcher can evolve their own copies independently.
type wire struct {
	App    string `json:"app"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Source string `json:"source"`
	Author string `json:"author"`
	URL    string `json:"url"`
	Age    string `json:"age"`
	TS     string `json:"ts"`
}

// parseItems reads an app's --json output into items, deriving each one's sort
// time from its absolute ts when present, else from its relative age. now is
// threaded in (not time.Now) so a whole fetch shares one clock and tests are
// deterministic. Any noise around the JSON array (build chatter) is tolerated.
func parseItems(out []byte, now time.Time) ([]item, error) {
	raw := extractJSONArray(out)
	if raw == nil {
		return nil, nil
	}
	var ws []wire
	if err := json.Unmarshal(raw, &ws); err != nil {
		return nil, err
	}
	items := make([]item, 0, len(ws))
	for _, w := range ws {
		items = append(items, item{
			App:    w.App,
			ID:     w.ID,
			Title:  w.Title,
			Body:   w.Body,
			Source: w.Source,
			Author: w.Author,
			URL:    w.URL,
			Age:    w.Age,
			sortAt: sortTime(w.TS, w.Age, now),
		})
	}
	return items, nil
}

// extractJSONArray returns the outermost [...] span of b, or nil when there is
// none, so a subprocess that printed only an error yields no items.
func extractJSONArray(b []byte) []byte {
	i := indexByte(b, '[')
	j := lastIndexByte(b, ']')
	if i < 0 || j < i {
		return nil
	}
	return b[i : j+1]
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// sortTime resolves an item's position on the merged timeline: the exact ts if
// the app gave one, else now minus its parsed relative age (Inoreader only
// exposes "2h"-style ages), else the zero time so it sorts last.
func sortTime(ts, age string, now time.Time) time.Time {
	if ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return t.UTC()
		}
	}
	if d, ok := ageToDuration(age); ok {
		return now.Add(-d).UTC()
	}
	return time.Time{}
}

var reAge = regexp.MustCompile(`^(\d+)\s*([a-z]+)`)

// ageToDuration parses a compact relative age like "5m", "2h", "3d", "1w",
// "1mo" into a duration back from now. "now"/"just now" is zero. Anything else
// (an absolute date on an old item) reports false so the item sinks.
func ageToDuration(age string) (time.Duration, bool) {
	s := strings.ToLower(strings.TrimSpace(age))
	if s == "" {
		return 0, false
	}
	if s == "now" || s == "just now" {
		return 0, true
	}
	m := reAge.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	unit := time.Duration(n)
	switch {
	case strings.HasPrefix(m[2], "mo"): // month before the bare "m" (minute) case
		return unit * 30 * 24 * time.Hour, true
	case strings.HasPrefix(m[2], "s"):
		return unit * time.Second, true
	case strings.HasPrefix(m[2], "m"):
		return unit * time.Minute, true
	case strings.HasPrefix(m[2], "h"):
		return unit * time.Hour, true
	case strings.HasPrefix(m[2], "d"):
		return unit * 24 * time.Hour, true
	case strings.HasPrefix(m[2], "w"):
		return unit * 7 * 24 * time.Hour, true
	case strings.HasPrefix(m[2], "y"):
		return unit * 365 * 24 * time.Hour, true
	}
	return 0, false
}

// mergeSort orders the combined feed newest first. Items without a resolvable
// time keep to the bottom in their original per-app order (a stable sort).
func mergeSort(items []item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].sortAt, items[j].sortAt
		switch {
		case a.IsZero() && b.IsZero():
			return false
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		default:
			return a.After(b)
		}
	})
}
