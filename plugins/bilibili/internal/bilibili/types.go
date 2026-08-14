// Package bilibili is a thin client for the 动态 timeline: what the uploaders
// you follow have posted, read from the same web-dynamic JSON API t.bilibili.com
// itself calls, authenticated with the browser-session cookie set. It never logs
// or stores those secrets.
package bilibili

import (
	"time"

	"github.com/genkio/tui/core"
)

// Video is one video post on the following 动态: an uploader's own submission,
// or an episode of a series they published. The dynamic id is the identity —
// two dynamics can point at the same video (a re-post), and read state is about
// the post you saw, not the file behind it.
type Video struct {
	ID     string // dynamic id_str, what the read store keys on
	BVID   string // "BV1xx411c7mD"; empty for a series episode, which has no bvid
	Title  string
	Note   string // what the uploader wrote when posting it, when they wrote anything
	Desc   string // the video's own description
	Author string
	URL    string // canonical watch page
	Cover  string
	Secs   int    // running time, parsed from the "12:34" the feed states
	Views  string // play count as the feed words it ("1.2万"), not a number
	Badge  string // "番剧", "合作" and friends; empty for a plain upload
	PubAt  time.Time
	Age    string // relative time derived from PubAt, e.g. "2h"
}

// ToItem normalizes a video into a core.Item for the feed widget. The web card
// grows its own player from the watch URL (bilibili hands out no stream a page
// can use), so only the still and the running time travel with the item.
func ToItem(v Video) core.Item {
	it := core.Item{
		App:     "bilibili",
		ID:      v.ID,
		Title:   v.title(),
		Body:    v.body(),
		Source:  v.source(), // carries the author, so Author would only repeat it
		URL:     v.URL,
		Age:     v.Age,
		Poster:  v.Cover,
		VidSecs: v.Secs,
	}
	if !v.PubAt.IsZero() {
		it.At = v.PubAt.UTC()
	}
	return it
}

// source is the row's byline: who posted it and how many have watched, which is
// most of what decides whether a video is worth the tap.
func (v Video) source() string {
	if v.Views == "" {
		return v.Author
	}
	return v.Author + " · " + v.Views + "播放"
}

// title leads with the badge a series episode carries, so a 番剧 row is not
// mistaken for something a person you follow made.
func (v Video) title() string {
	if v.Badge != "" {
		return "[" + v.Badge + "] " + v.Title
	}
	return v.Title
}

// body is what the uploader said about the video: their own note when posting
// it, else the video's description. Both is noise — the note is usually the
// description's first line.
func (v Video) body() string {
	if v.Note != "" {
		return v.Note
	}
	return v.Desc
}
