package x

import (
	"context"
	"strings"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/x/internal/readstore"
)

// Source adapts the x client to core.Source for the merged view and the
// --count/--json/--mark-read commands. x has no server-side read state, so
// "unread" means "not in the local read store".
type Source struct {
	client *Client
	read   *readstore.Store
	tab    Tab
	max    int
}

func NewSource(client *Client, read *readstore.Store, tab Tab, max int) *Source {
	return &Source{client: client, read: read, tab: tab, max: max}
}

func (s *Source) Fetch(ctx context.Context) ([]core.Item, error) {
	tweets, err := s.client.Timeline(ctx, s.tab, s.max)
	if err != nil {
		return nil, err
	}
	items := make([]core.Item, 0, len(tweets))
	for _, t := range tweets {
		if s.read.Has(t.ID) {
			continue
		}
		items = append(items, ToItem(t))
	}
	return items, nil
}

func (s *Source) Count(ctx context.Context) (int, bool, error) {
	tweets, err := s.client.Timeline(ctx, s.tab, s.max)
	if err != nil {
		return 0, false, err
	}
	unread := 0
	for _, t := range tweets {
		if !s.read.Has(t.ID) {
			unread++
		}
	}
	// A full page of unread almost certainly has more beyond it; a read post in
	// the window marks where you left off, so a partial count is complete.
	capped := len(tweets) > 0 && unread >= len(tweets)
	return unread, capped, nil
}

func (s *Source) MarkRead(ctx context.Context, ids []string) error {
	for _, id := range ids {
		s.read.Mark(id)
	}
	return s.read.Save()
}

var _ core.Source = (*Source)(nil)

// ToItem normalizes a tweet into a core.Item for the feed widget. Read
// filtering (x tracks read locally, not on the server) stays with the caller.
func ToItem(t Tweet) core.Item {
	it := core.Item{
		App:    "x",
		ID:     t.ID,
		Title:  t.Text,
		Body:   tweetBody(t),
		Source: tweetSource(t),
		Author: t.Name,
		URL:    t.URL,
		Age:    t.Age,
	}
	if !t.CreatedAt.IsZero() {
		it.At = t.CreatedAt.UTC()
	}
	return it
}

// ToItems maps a whole timeline; read filtering stays with the caller (the UI
// greys or hides read posts by its own read store).
func ToItems(ts []Tweet) []core.Item {
	items := make([]core.Item, len(ts))
	for i, t := range ts {
		items[i] = ToItem(t)
	}
	return items
}

// tweetSource is the row's left label: the author handle, flagged when the post
// reached the timeline as someone else's repost.
func tweetSource(t Tweet) string {
	if t.RepostBy != "" {
		return "🔁 @" + t.Handle
	}
	return "@" + t.Handle
}

// tweetBody is the expanded text: the post plus its quoted post, if any, since
// the shared feed renders one body string rather than x's bespoke quote box.
func tweetBody(t Tweet) string {
	body := t.Text
	if t.Quoted != nil {
		body = strings.TrimSpace(body + "\n\nquoting @" + t.Quoted.Handle + ": " + t.Quoted.Text)
	}
	return body
}
