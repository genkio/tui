// Package x is a thin client for x.com's web GraphQL API, the same endpoints the
// website calls, authenticated with the browser-session auth_token cookie plus
// the ct0 CSRF token. It never logs or stores those secrets.
package x

import "time"

// Tweet is one post from a home timeline, flattened from the GraphQL response.
type Tweet struct {
	ID          string
	Handle      string // author screen_name, without the leading @
	Name        string // author display name
	Text        string // full body: the long-form note if present, else the post text
	CreatedAt   time.Time
	Age         string // relative time derived from CreatedAt, e.g. "2h"
	Replies     int
	Reposts     int
	Likes       int
	Quotes      int
	RepostBy    string       // display name of who reposted it onto your timeline; "" if original
	Quoted      *QuotedTweet // the quoted post, if any
	URL         string       // https://x.com/<handle>/status/<id>
	VideoURL    string       // best mp4 of the attached video or GIF; "" when none
	VideoPoster string       // still frame shown before the video plays
	VideoSecs   int          // clip length in seconds; 0 when x reported none
	Images      []string     // attached still photos, in x's order
	Article     *Article     // x's long-form post, when this is one
}

// Article is a long post. Its body lives outside the post text, which carries
// only a t.co link back to it, so a card that reads Text alone renders blank.
type Article struct {
	Title  string
	Text   string   // the whole body when the timeline returned it, else the preview
	Cover  string   // header image
	Images []string // stills laid out through the body
}

// QuotedTweet is the post a Tweet quotes, rendered as a card inside its parent.
type QuotedTweet struct {
	Handle      string
	Name        string
	Text        string
	URL         string // https://x.com/<handle>/status/<id>
	VideoURL    string // best mp4 of the quoted post's video or GIF; "" when none
	VideoPoster string
	VideoSecs   int
	Images      []string
}
