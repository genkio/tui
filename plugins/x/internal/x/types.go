// Package x is a thin client for x.com's web GraphQL API, the same endpoints the
// website calls, authenticated with the browser-session auth_token cookie plus
// the ct0 CSRF token. It never logs or stores those secrets.
package x

import "time"

// Tweet is one post from a home timeline, flattened from the GraphQL response.
type Tweet struct {
	ID        string
	Handle    string // author screen_name, without the leading @
	Name      string // author display name
	Text      string // full body: the long-form note if present, else the post text
	CreatedAt time.Time
	Age       string // relative time derived from CreatedAt, e.g. "2h"
	Replies   int
	Reposts   int
	Likes     int
	Quotes    int
	RepostBy  string       // display name of who reposted it onto your timeline; "" if original
	Quoted    *QuotedTweet // the quoted post, if any
	URL       string       // https://x.com/<handle>/status/<id>
}

// QuotedTweet is the post a Tweet quotes, shown inline when expanded.
type QuotedTweet struct {
	Handle string
	Name   string
	Text   string
}
