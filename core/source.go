package core

import "context"

// Source is a plugin's data contract: fetch its unread items (normalized to
// Item), count them, and mark ids read in its own store or server. A plugin
// implements it once; its --json/--count/--mark-read commands and the merged
// "all" view all read through it.
type Source interface {
	Fetch(ctx context.Context) ([]Item, error)
	Count(ctx context.Context) (n int, capped bool, err error)
	MarkRead(ctx context.Context, ids []string) error
}
