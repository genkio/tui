package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/genkio/tui/core"
)

// Reddit's half of the gist. A post's card is its title and, at best, the first
// screen of its text; the thread under it is where the subreddit actually says
// what it thinks, and none of that travels in the feed.
//
// The comments come from reddit's own JSON API, the same one the reddit app
// reads its timeline with: /comments/<id>.json answers with the post and the
// whole visible tree in one call, and the id alone is enough — no subreddit, no
// slug. The session cookie rides along when there is one, so a private or
// quarantined sub answers the way it does in the browser.
var redditItemAPIs = []string{
	"https://old.reddit.com/comments/",
	"https://www.reddit.com/comments/",
}

// How many comments to ask for. Reddit answers a deep thread with the top of
// the tree and "more" stubs in place of the rest; this is the most it will hand
// over in one call, and a summary of the 500 best-scored comments is not a
// worse summary than one of the 5000.
const redditCommentLimit = 500

// A post id is base36 and nothing else. Checked rather than escaped because it
// goes in a URL path: an id this does not recognize is not an id, and saying so
// beats asking reddit about it.
var redditIDRe = regexp.MustCompile(`^[a-z0-9]+$`)

// redditRefOf reports whether an item is a reddit post, which is the whole test
// here: unlike Hacker News, whose stories arrive through someone else's feed,
// reddit's come from the reddit app itself and carry the post id as their own.
func redditRefOf(it core.Item) (gistRef, bool) {
	if strings.TrimSpace(it.App) != "reddit" {
		return gistRef{}, false
	}
	id := strings.TrimPrefix(strings.TrimSpace(it.ID), "t3_")
	if !redditIDRe.MatchString(id) {
		return gistRef{}, false
	}
	return gistRef{Service: gistReddit, ID: id, Kind: "post"}, true
}

// redditHosted reports whether a URL points at reddit's own domains rather than
// an external article: a self post, a gallery and a v.redd.it clip all link
// back into the site, and none of them is a page worth fetching for a prompt.
func redditHosted(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "reddit.com") || strings.Contains(l, "redd.it")
}

func redditCookie() string { return os.Getenv("RDTUI_COOKIE") }

func fetchRedditThread(ctx context.Context, ref gistRef) (discussion, error) {
	ctx, cancel := context.WithTimeout(ctx, threadTimeout)
	defer cancel()
	query := ".json?raw_json=1&sort=top&limit=" + strconv.Itoa(redditCommentLimit)
	var lastErr error
	// Reddit rotates which host serves the legacy JSON API, so a path that 404s
	// on one may answer on the other; the reddit app's own client does the same.
	for _, base := range redditItemAPIs {
		th, err := readRedditThread(ctx, base+ref.ID+query, ref)
		if err == nil {
			return th, nil
		}
		lastErr = err
	}
	return discussion{}, lastErr
}

func readRedditThread(ctx context.Context, endpoint string, ref gistRef) (discussion, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return discussion{}, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	if c := redditCookie(); c != "" {
		req.Header.Set("Cookie", c)
	}
	resp, err := threadHTTP.Do(req)
	if err != nil {
		return discussion{}, fmt.Errorf("could not reach reddit: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return discussion{}, errors.New("reddit rejected the session — re-run `tui reddit --auth`")
	case resp.StatusCode == http.StatusTooManyRequests:
		return discussion{}, errors.New("reddit is rate-limiting this machine — wait a bit and ask again")
	case resp.StatusCode != http.StatusOK:
		return discussion{}, fmt.Errorf("reddit said %s about that post", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, threadMaxBody))
	if err != nil {
		return discussion{}, err
	}
	return redditThreadOf(ref, raw)
}

// redditThreadOf reads the two listings a comments page answers with: the post
// itself, then the tree under it.
func redditThreadOf(ref gistRef, raw []byte) (discussion, error) {
	var page []redditListing
	if err := json.Unmarshal(raw, &page); err != nil {
		return discussion{}, fmt.Errorf("reddit answered with something unreadable: %w", err)
	}
	if len(page) < 2 {
		return discussion{}, errors.New("reddit answered with no discussion at all")
	}
	th := discussion{Ref: ref, Replies: redditKids(page[1].Data.Children)}
	th.Count = discCount(th.Replies)
	for _, ch := range page[0].Data.Children {
		if ch.Kind != "t3" {
			continue
		}
		p := ch.Data
		th.Title = redditText(p.Title)
		th.Author = strings.TrimSpace(p.Author)
		th.Text = redditText(p.SelfText)
		th.Points = p.Score
		// Only a link out is an article. A self post, a gallery or a hosted clip
		// links back into the site, and what it says is already here.
		if !p.IsSelf && !redditHosted(p.URL) {
			th.URL = strings.TrimSpace(p.URL)
		}
		break
	}
	return th, nil
}

// redditKids keeps the shape of the tree while dropping what is not there to
// read: a deleted comment, and the "more" stubs reddit leaves where it stopped
// unfolding. What was said under a deleted comment is still an answer to
// something, so it moves up rather than going with it.
func redditKids(in []redditChild) []discNode {
	out := make([]discNode, 0, len(in))
	for _, ch := range in {
		if ch.Kind != "t1" {
			continue
		}
		kids := redditKids(ch.Data.Replies.listing.Data.Children)
		text := redditText(ch.Data.Body)
		if text == "" {
			out = append(out, kids...)
			continue
		}
		node := discNode{Author: strings.TrimSpace(ch.Data.Author), Text: text, Kids: kids}
		if ch.Data.CreatedUTC > 0 {
			node.At = time.Unix(int64(ch.Data.CreatedUTC), 0).UTC()
		}
		out = append(out, node)
	}
	return out
}

// redditText is a comment's body as it reads. Reddit hands over Markdown, which
// a summary can read as-is; what it leaves behind is the tombstone of a comment
// that is no longer there, which is not text and not worth a line in the
// prompt.
func redditText(s string) string {
	out := strings.TrimSpace(html.UnescapeString(s))
	if out == "[deleted]" || out == "[removed]" {
		return ""
	}
	return strings.TrimSpace(gapRe.ReplaceAllString(out, "\n\n"))
}

type redditListing struct {
	Data struct {
		Children []redditChild `json:"children"`
	} `json:"data"`
}

type redditChild struct {
	Kind string    `json:"kind"` // "t3" a post, "t1" a comment, "more" a stub
	Data redditRaw `json:"data"`
}

// redditRaw is both kinds at once: a post carries the title fields and a
// comment the body ones, and reddit names them the same way in both listings.
type redditRaw struct {
	Title      string        `json:"title"`
	SelfText   string        `json:"selftext"`
	URL        string        `json:"url"`
	Author     string        `json:"author"`
	Score      int           `json:"score"`
	IsSelf     bool          `json:"is_self"`
	Body       string        `json:"body"`
	CreatedUTC float64       `json:"created_utc"`
	Replies    redditReplies `json:"replies"`
}

// redditReplies is the one field that cannot be a plain struct: reddit writes
// an empty string where a comment has no replies and a whole Listing where it
// has, in the same place.
type redditReplies struct {
	listing redditListing
}

func (r *redditReplies) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	return json.Unmarshal(b, &r.listing)
}
