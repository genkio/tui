package reddit

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/genkio/tui/core"
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
			Images:    d.photos(),
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

	// Image sources, richest first. The request sets raw_json=1, so these come
	// back unescaped and are usable as-is.
	GalleryData *struct {
		Items []struct {
			MediaID string `json:"media_id"`
		} `json:"items"`
	} `json:"gallery_data"`
	MediaMetadata map[string]struct {
		S struct {
			U string `json:"u"`
		} `json:"s"`
	} `json:"media_metadata"`
	Preview struct {
		Images []struct {
			Source struct {
				URL string `json:"url"`
			} `json:"source"`
		} `json:"images"`
	} `json:"preview"`
}

// photos lists a post's images: a gallery in the order reddit lays it out, else
// a direct image link, else the preview reddit renders for a link post.
func (d postRaw) photos() []string {
	if d.GalleryData != nil {
		var out []string
		for _, g := range d.GalleryData.Items {
			// media_metadata is a map, so gallery_data carries the only ordering
			if u := core.ImageURL(d.MediaMetadata[g.MediaID].S.U); u != "" {
				out = append(out, u)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if isImageURL(d.URL) {
		return []string{d.URL}
	}
	var out []string
	for _, p := range d.Preview.Images {
		if u := core.ImageURL(p.Source.URL); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// isImageURL reports whether a link post points straight at an image, which
// i.redd.it and image hosts do.
func isImageURL(u string) bool {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.ToLower(u)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}
