package reddit

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

// parseHome reads the Listing returned by old.reddit.com/.json (the logged-in
// home feed), keeping
// only the t3 post children (the listing can also carry pinned/mod badge
// trailers and other non-post kinds we don't show).
func parseHome(body []byte) ([]Post, error) {
	var l listing
	if err := json.Unmarshal(body, &l); err != nil {
		return nil, fmt.Errorf("decoding reddit home feed (is the session valid?): %w", err)
	}
	posts := make([]Post, 0, len(l.Data.Children))
	for _, ch := range l.Data.Children {
		if ch.Kind != "t3" {
			continue
		}
		d := ch.Data
		p := Post{
			ID:        d.ID,
			Subreddit: d.Subreddit,
			Title:     cleanTitle(d.Title),
			SelfText:  cleanTitle(d.SelfText),
			IsSelf:    d.IsSelf,
			Author:    d.Author,
			Permalink: d.Permalink,
			URL:       d.URL,
		}
		if d.CreatedUTC > 0 {
			p.CreatedAt = time.Unix(int64(d.CreatedUTC), 0).UTC()
			p.Age = relAge(time.Since(p.CreatedAt))
		}
		posts = append(posts, p)
	}
	return posts, nil
}

// cleanTitle unescapes HTML entities the API may leave in a title or body.
func cleanTitle(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

func relAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
}

type listing struct {
	Kind string      `json:"kind"`
	Data listingData `json:"data"`
}

type listingData struct {
	Children []child `json:"children"`
}

type child struct {
	Kind string  `json:"kind"`
	Data postRaw `json:"data"`
}

type postRaw struct {
	ID         string  `json:"id"`
	Subreddit  string  `json:"subreddit"`
	Title      string  `json:"title"`
	SelfText   string  `json:"selftext"`
	IsSelf     bool    `json:"is_self"`
	Author     string  `json:"author"`
	Permalink  string  `json:"permalink"`
	URL        string  `json:"url"`
	CreatedUTC float64 `json:"created_utc"` // Reddit sends this as a float (e.g. 1786017121.0)
}
