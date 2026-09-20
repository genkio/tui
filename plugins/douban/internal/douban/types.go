// Package douban is a thin client for Douban's following timeline, scraped
// from the server-rendered desktop homepage (www.douban.com) the way a browser
// reads it, authenticated with the browser-session cookie set. It never logs
// or stores those secrets.
package douban

import (
	"time"

	"github.com/genkio/tui/core"
)

// Status is one entry in the feed: something a followed user said, reshared or
// marked (看过/在读/...) on the following timeline (友邻广播), or a title off one
// of the 榜单 charts. A subject card the status is about (a book, a movie) is
// folded into Text, since it is the status's own content; what it passed along
// from someone else rides in Embed.
type Status struct {
	ID        string
	Author    string // display name of the followed user, or the chart's name
	Activity  string // e.g. "说", "转发", "看过"; may be empty
	Title     string // headline when it differs from Text: a chart entry's rank and name
	Text      string // status text, with any subject/topic card appended
	URL       string // canonical web URL for the status
	CreatedAt time.Time
	Age       string      // relative time derived from CreatedAt, e.g. "2h"
	Images    []string    // attached pictures; a reshared post keeps its own
	Embed     *core.Quote // what a 转发 passed along: the original status, or the discussion it points at
	Clips     []Clip      // sayings the stream cut short, and where the rest lives
}

// Clip is a saying douban truncated in the timeline. A long 动态 reaches the
// homepage cut to a few lines and ending in a （全文） link; the rest exists
// only on the page that link points at, which is login-walled, so the whole
// text has to be fetched with the same session cookie.
type Clip struct {
	Text string // the clipped saying, verbatim as it appears in Text
	URL  string // the page carrying it whole
}
