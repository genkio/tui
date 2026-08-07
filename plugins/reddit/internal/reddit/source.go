package reddit

import (
	"context"
	"strings"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/reddit/internal/readstore"
)

// Source adapts the reddit client to core.Source for the merged view and the
// --count/--json/--mark-read commands. Reddit exposes no read state for this
// JSON API, so "unread" means "not in the local read store".
type Source struct {
	client *Client
	read   *readstore.Store
	max    int
}

func NewSource(client *Client, read *readstore.Store, max int) *Source {
	return &Source{client: client, read: read, max: max}
}

func (s *Source) Fetch(ctx context.Context) ([]core.Item, error) {
	posts, err := s.client.Home(ctx, s.max)
	if err != nil {
		return nil, err
	}
	items := make([]core.Item, 0, len(posts))
	for _, p := range posts {
		if s.read.Has(p.ID) {
			continue
		}
		items = append(items, ToItem(p))
	}
	return items, nil
}

func (s *Source) Count(ctx context.Context) (int, bool, error) {
	posts, err := s.client.Home(ctx, s.max)
	if err != nil {
		return 0, false, err
	}
	unread := 0
	for _, p := range posts {
		if !s.read.Has(p.ID) {
			unread++
		}
	}
	// A full page of unread almost certainly has more beyond it; a read post in
	// the window marks where you left off, so a partial count is complete.
	capped := len(posts) > 0 && unread >= len(posts)
	return unread, capped, nil
}

func (s *Source) MarkRead(ctx context.Context, ids []string) error {
	for _, id := range ids {
		s.read.Mark(id)
	}
	return s.read.Save()
}

var _ core.Source = (*Source)(nil)

// ToItem normalizes a post into a core.Item for the feed widget. Read filtering
// (tracked locally) stays with the caller.
func ToItem(p Post) core.Item {
	it := core.Item{
		App:    "reddit",
		ID:     p.ID,
		Title:  p.Title,
		Body:   p.SelfText,
		Source: "r/" + p.Subreddit,
		Author: p.Author,
		URL:    itemURL(p),
		Age:    p.Age,
	}
	if !p.CreatedAt.IsZero() {
		it.At = p.CreatedAt.UTC()
	}
	return it
}

// itemURL is where the row opens: the external article for a link post, else
// the Reddit thread. A post whose URL is a reddit-hosted url (i.redd.it,
// v.redd.it, a gallery, or the comments page itself) opens the thread so
// carbonyl shows the whole thing.
func itemURL(p Post) string {
	switch {
	case p.IsSelf, p.URL == "", isRedditHosted(p.URL):
		return threadURL(p.Permalink)
	default:
		return p.URL
	}
}

// isRedditHosted reports whether u points at reddit's own domains rather than an
// external article.
func isRedditHosted(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "reddit.com") || strings.Contains(l, "redd.it")
}

// threadURL completes the relative permalink to an absolute reddit URL. The
// legacy host renders light in carbonyl and honors the session cookie.
func threadURL(permalink string) string {
	if strings.HasPrefix(permalink, "http") {
		return permalink
	}
	return "https://old.reddit.com" + permalink
}
