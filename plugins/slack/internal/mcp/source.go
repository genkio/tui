package mcp

import (
	"context"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/slack/internal/config"
)

// Source adapts the Slack MCP client to core.Source for the unread count. Slack
// is a chat model, not an article stream, so it stays out of the merged "all"
// view: Fetch yields nothing and MarkRead is a no-op (marking is per-message in
// the UI). Only Count is meaningful, for the launcher's badge.
type Source struct {
	client  *Client
	unreads config.UnreadsConfig
}

func NewSource(client *Client, unreads config.UnreadsConfig) *Source {
	return &Source{client: client, unreads: unreads}
}

func (s *Source) Fetch(context.Context) ([]core.Item, error) { return nil, nil }

func (s *Source) Count(ctx context.Context) (int, bool, error) {
	convs, err := s.client.Unreads(ctx, s.unreads)
	if err != nil {
		return 0, false, err
	}
	total := 0
	for _, c := range convs {
		total += c.UnreadCount
	}
	capped := s.unreads.MaxChannels > 0 && len(convs) >= s.unreads.MaxChannels
	return total, capped, nil
}

func (s *Source) MarkRead(context.Context, []string) error { return nil }

var _ core.Source = (*Source)(nil)
