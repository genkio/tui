package douban

import (
	"context"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/douban/internal/readstore"
)

// Source adapts the douban client to core.Source for the merged view and the
// --count/--json/--mark-read commands. Douban exposes no read state for the
// timeline, so "unread" means "not in the local read store".
type Source struct {
	client *Client
	read   *readstore.Store
	max    int
}

func NewSource(client *Client, read *readstore.Store, max int) *Source {
	return &Source{client: client, read: read, max: max}
}

func (s *Source) Fetch(ctx context.Context) ([]core.Item, error) {
	statuses, err := s.client.Home(ctx, s.max)
	if err != nil {
		return nil, err
	}
	items := make([]core.Item, 0, len(statuses))
	for _, st := range statuses {
		if s.read.Has(st.ID) {
			continue
		}
		items = append(items, ToItem(st))
	}
	return items, nil
}

func (s *Source) Count(ctx context.Context) (int, bool, error) {
	statuses, err := s.client.Home(ctx, s.max)
	if err != nil {
		return 0, false, err
	}
	unread := 0
	for _, st := range statuses {
		if !s.read.Has(st.ID) {
			unread++
		}
	}
	// A full page of unread almost certainly has more beyond it; a read status
	// in the window marks where you left off, so a partial count is complete.
	capped := len(statuses) > 0 && unread >= len(statuses)
	return unread, capped, nil
}

func (s *Source) MarkRead(ctx context.Context, ids []string) error {
	for _, id := range ids {
		s.read.Mark(id)
	}
	return s.read.Save()
}

var _ core.Source = (*Source)(nil)

// ToItem normalizes a status into a core.Item for the feed widget. Like x,
// the full text rides in both Title and Body: the row clips it to one line and
// the expanded/web views show it whole. Read filtering stays with the caller.
func ToItem(st Status) core.Item {
	text := st.Text
	if text == "" {
		// e.g. a photo-upload status has no text at all; show the activity
		// ("上传照片到 <album>") so the row isn't blank
		text = st.Activity
	}
	it := core.Item{
		App:    "douban",
		ID:     st.ID,
		Title:  text,
		Body:   text,
		Source: st.Author,
		Author: st.Author,
		URL:    st.URL,
		Age:    st.Age,
	}
	if st.Activity != "" {
		it.Source = st.Author + " " + st.Activity
	}
	if !st.CreatedAt.IsZero() {
		it.At = st.CreatedAt.UTC()
	}
	return it
}
