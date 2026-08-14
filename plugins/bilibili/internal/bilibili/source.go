package bilibili

import (
	"context"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/bilibili/internal/readstore"
)

// Source adapts the bilibili client to core.Source for the merged view and the
// --count/--json/--mark-read commands. bilibili exposes no read state for the
// 动态 timeline, so "unread" means "not in the local read store".
type Source struct {
	client *Client
	read   *readstore.Store
	max    int
}

func NewSource(client *Client, read *readstore.Store, max int) *Source {
	return &Source{client: client, read: read, max: max}
}

func (s *Source) Fetch(ctx context.Context) ([]core.Item, error) {
	videos, err := s.client.Feed(ctx, s.max)
	if err != nil {
		return nil, err
	}
	items := make([]core.Item, 0, len(videos))
	for _, v := range videos {
		if s.read.Has(v.ID) {
			continue
		}
		items = append(items, ToItem(v))
	}
	return items, nil
}

// countWindow is how deep the badge looks. bilibili risk-controls bursts of
// requests and the launcher polls every few minutes, so a count reads one page
// rather than the whole fetch: a full page of unread already reports "N+", which
// is all the badge can say.
const countWindow = 12

func (s *Source) Count(ctx context.Context) (int, bool, error) {
	max := s.max
	if max > countWindow {
		max = countWindow
	}
	videos, err := s.client.Feed(ctx, max)
	if err != nil {
		return 0, false, err
	}
	unread := 0
	for _, v := range videos {
		if !s.read.Has(v.ID) {
			unread++
		}
	}
	// A full window of unread almost certainly has more beyond it; a read post in
	// the window marks where you left off, so a partial count is complete.
	capped := len(videos) > 0 && unread >= len(videos)
	return unread, capped, nil
}

func (s *Source) MarkRead(ctx context.Context, ids []string) error {
	for _, id := range ids {
		s.read.Mark(id)
	}
	return s.read.Save()
}

var _ core.Source = (*Source)(nil)
