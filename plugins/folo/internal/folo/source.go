package folo

import (
	"context"

	"github.com/genkio/tui/core"
)

// Source adapts the Folo client to core.Source, so the standalone UI, the
// --json/--count/--mark-read commands, and the launcher's merged view all read
// pending articles the same way.
type Source struct {
	client     *Client
	unreadOnly bool
	max        int
}

var _ core.Source = (*Source)(nil)

func NewSource(client *Client, unreadOnly bool, max int) *Source {
	return &Source{client: client, unreadOnly: unreadOnly, max: max}
}

func (s *Source) Fetch(ctx context.Context) ([]core.Item, error) {
	arts, err := s.client.Unreads(ctx, s.unreadOnly, s.max)
	if err != nil {
		return nil, err
	}
	return ToItems(arts), nil
}

func (s *Source) Count(ctx context.Context) (int, bool, error) {
	arts, err := s.client.Unreads(ctx, true, s.max)
	if err != nil {
		return 0, false, err
	}
	return len(arts), len(arts) >= s.max, nil
}

func (s *Source) MarkRead(ctx context.Context, ids []string) error {
	var firstErr error
	for _, id := range ids {
		if err := s.client.MarkRead(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ToItems normalizes articles into core.Items. The list response carries only
// a short Summary; the full body is fetched lazily on expand, so Body starts as
// the summary. Published gives the merged view its exact sort time.
func ToItems(arts []Article) []core.Item {
	items := make([]core.Item, len(arts))
	for i, a := range arts {
		items[i] = core.Item{
			App:    "folo",
			ID:     a.ID,
			Title:  a.Title,
			Body:   a.Summary,
			Source: a.Feed,
			Author: a.Author,
			URL:    a.URL,
			Age:    a.Age,
			At:     a.Published.UTC(),
		}
	}
	return items
}
