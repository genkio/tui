// Package reddit is a thin client for Reddit's classic JSON API
// (old.reddit.com/*.json), the same endpoints the legacy web client uses,
// authenticated with the browser-session cookie set. It never logs or stores
// those secrets.
package reddit

import "time"

// Post is one entry from the authenticated home timeline, flattened from the
// JSON Listing response.
type Post struct {
	ID        string // base36 post id (matches the permalink)
	Subreddit string // bare subreddit name, e.g. "golang"
	Title     string
	SelfText  string // self-post body; "" for link posts
	IsSelf    bool   // true when this is a text post
	Author    string
	Permalink string // "/r/<sub>/comments/<id>/<slug>/"
	URL       string // external link for link posts; a reddit URL otherwise
	CreatedAt time.Time
	Age       string   // relative time derived from CreatedAt, e.g. "2h"
	Images    []string // gallery, direct image link, or reddit's link preview
}
