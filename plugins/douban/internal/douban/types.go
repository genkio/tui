// Package douban is a thin client for Douban's following timeline, scraped
// from the server-rendered desktop homepage (www.douban.com) the way a browser
// reads it, authenticated with the browser-session cookie set. It never logs
// or stores those secrets.
package douban

import "time"

// Status is one entry from the following timeline (友邻广播): something a
// followed user said, reshared, or marked (看过/在读/...), flattened from the
// rexxar home_timeline response. Reshared originals and link cards are folded
// into Text so one string carries the whole story.
type Status struct {
	ID        string
	Author    string // display name of the followed user
	Activity  string // e.g. "说", "转发", "看过"; may be empty
	Text      string // status text, with any reshared original / card appended
	URL       string // canonical web URL for the status
	CreatedAt time.Time
	Age       string // relative time derived from CreatedAt, e.g. "2h"
}
