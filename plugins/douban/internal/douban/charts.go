package douban

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/genkio/tui/core"
)

// chartItems is how many entries to ask a chart for. The weekly ones hold ten;
// asking for a few more keeps a longer list from being cut short.
const chartItems = 20

// chartTTL is how long a fetched chart stays good. They turn over weekly while
// the launcher polls --count every few minutes, so refetching per poll would
// spend hundreds of requests a day on a list that has not moved.
const chartTTL = 6 * time.Hour

// Charts returns the configured 榜单 as feed entries, newest chart first, one
// entry per ranked title. Reading them is best effort: a chart that fails to
// load falls back to its cached copy however old, and is dropped only when
// there is none. A garnish on the timeline is never a reason to fail the fetch.
func (c *Client) Charts(ctx context.Context, ids []string, now time.Time) []Status {
	if len(ids) == 0 {
		return nil
	}
	cache := loadChartCache()
	var out []Status
	seen := map[string]bool{}
	dirty := false
	for _, id := range ids {
		entry := cache.Charts[id]
		// an entry left by an older cache format carries no body, so it counts
		// as a miss rather than sidelining the chart until its TTL runs out
		if len(entry.Body) == 0 || now.Sub(entry.FetchedAt) > chartTTL {
			if body, err := c.chart(ctx, id); err == nil {
				entry = cachedChart{FetchedAt: now, Body: body}
				cache.Charts[id] = entry
				dirty = true
			}
		}
		var r chartResponse
		if len(entry.Body) == 0 || json.Unmarshal(entry.Body, &r) != nil {
			continue
		}
		for _, s := range r.statuses(now) {
			if seen[s.ID] { // a title can sit on two charts at once
				continue
			}
			seen[s.ID] = true
			out = append(out, s)
		}
	}
	if dirty {
		_ = cache.save()
	}
	return out
}

// chart reads one subject collection off the mobile API, returning the payload
// as it arrived: what gets cached is the fetch, never its reading, so a change
// to how a chart maps into the feed takes effect on the next render instead of
// waiting out the TTL. No session cookie rides along: the charts are public, so
// the login stays on www.douban.com.
func (c *Client) chart(ctx context.Context, id string) (json.RawMessage, error) {
	api := "https://m.douban.com/rexxar/api/v2/subject_collection/" + id +
		"/items?start=0&count=" + strconv.Itoa(chartItems)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", c.ua)
	req.Header.Set("accept", "application/json")
	// rexxar answers only for its own pages; the referer is the ticket in
	req.Header.Set("referer", "https://m.douban.com/subject_collection/"+id+"/")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("douban chart %s returned HTTP %d", id, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	// decode once here so a body that cannot be read is never cached
	var body chartResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("parsing douban chart %s: %w", id, err)
	}
	return raw, nil
}

// chartResponse is the slice of the collection payload the feed uses: the
// ranked entries, and the list's own name and update time.
type chartResponse struct {
	Items      []chartSubject `json:"subject_collection_items"`
	Collection struct {
		Name      string `json:"name"`
		Title     string `json:"title"`
		UpdatedAt string `json:"updated_at"` // "2006-01-02 15:04:05" Beijing wall clock
	} `json:"subject_collection"`
}

type chartSubject struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	CardSubtitle string   `json:"card_subtitle"` // year / country / genre / director / cast
	Description  string   `json:"description"`
	CoverURL     string   `json:"cover_url"`
	Photos       []string `json:"photos"`
	URL          string   `json:"url"`
	Rank         int      `json:"rank"`
	TrendUp      bool     `json:"trend_up"`
	TrendDown    bool     `json:"trend_down"`
	Rating       *struct {
		Value float64 `json:"value"`
		Count int     `json:"count"`
	} `json:"rating"` // null until a title has enough votes
}

// statuses turns a chart into feed entries. Every title on one list shares the
// list's update time, so a chart lands in the timeline where it was published
// rather than at whatever moment it was fetched.
func (r chartResponse) statuses(now time.Time) []Status {
	name := r.Collection.Name
	if name == "" {
		name = r.Collection.Title
	}
	var at time.Time
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", r.Collection.UpdatedAt, cst); err == nil {
		at = t.UTC()
	}
	out := make([]Status, 0, len(r.Items))
	for _, sub := range r.Items {
		if sub.ID == "" || sub.Title == "" {
			continue
		}
		s := Status{
			ID:     ChartID(sub.ID),
			Author: name,
			Title:  sub.headline(),
			Text:   strings.Join(sub.lines(), "\n\n"),
			URL:    sub.URL,
			Images: sub.images(),
		}
		if !at.IsZero() {
			s.CreatedAt = at
			s.Age = relAge(now.Sub(at))
		}
		out = append(out, s)
	}
	return out
}

// ChartID namespaces a subject so a chart entry can never collide with a status
// in the read store, where both live side by side.
func ChartID(subject string) string { return "chart:" + subject }

// headline is the row's one line: where the title ranks, which way it is
// moving, and what it is called.
func (s chartSubject) headline() string {
	if s.Rank <= 0 {
		return s.Title
	}
	trend := ""
	switch {
	case s.TrendUp:
		trend = "↑ "
	case s.TrendDown:
		trend = "↓ "
	}
	return fmt.Sprintf("#%d %s%s", s.Rank, trend, s.Title)
}

func (s chartSubject) lines() []string {
	var out []string
	if s.Rating != nil && s.Rating.Value > 0 {
		out = append(out, fmt.Sprintf("%.1f ★ %d人", s.Rating.Value, s.Rating.Count))
	}
	if s.CardSubtitle != "" {
		out = append(out, s.CardSubtitle)
	}
	if s.Description != "" {
		out = append(out, s.Description)
	}
	return out
}

// images leads with the poster, then whatever stills the chart carries.
func (s chartSubject) images() []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range append([]string{s.CoverURL}, s.Photos...) {
		if u := imageURL(raw); u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// sortByRecency orders the feed newest first, keeping entries that share a time
// (every title on one chart) in the order they arrived, i.e. by rank.
func sortByRecency(statuses []Status) {
	sort.SliceStable(statuses, func(i, j int) bool {
		a, b := statuses[i].CreatedAt, statuses[j].CreatedAt
		switch {
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		default:
			return a.After(b)
		}
	})
}

// chartCache is the fetched charts, kept between runs. It is a cache and
// nothing else: deleting the file costs one refetch.
type chartCache struct {
	path   string
	Charts map[string]cachedChart `json:"charts"`
}

type cachedChart struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Body      json.RawMessage `json:"body"`
}

// chartCachePath sits beside the read store: $TUI_STATE_DIR/state/douban-tui/charts.json.
func chartCachePath() string { return core.StatePath("douban-tui", "charts.json") }

func loadChartCache() *chartCache {
	c := &chartCache{path: chartCachePath(), Charts: map[string]cachedChart{}}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	var f chartCache
	if json.Unmarshal(data, &f) == nil && f.Charts != nil {
		c.Charts = f.Charts
	}
	return c
}

func (c *chartCache) save() error {
	if c.path == "" {
		return nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
