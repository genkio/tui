package main

import (
	"charm.land/bubbles/v2/key"

	"github.com/genkio/tui/core"
)

// allKeyMap is the server-backed feed's terminal key set.
type allKeyMap struct {
	core.FeedKeys
	Back      key.Binding
	ContinueX key.Binding
}

func defaultAllKeys() allKeyMap {
	return allKeyMap{
		FeedKeys:  core.NewFeedKeys(),
		Back:      key.NewBinding(key.WithKeys("q", "esc"), key.WithHelp("q", "quit")),
		ContinueX: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "continue on x For You")),
	}
}

func (k allKeyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Mark, k.Keep, k.Carbonyl, k.OpenURL, k.CopyURL, k.Refresh, k.Help, k.Back}
}

func (k allKeyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.Mark, k.Keep, k.ShowSource, k.ContinueX, k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Refresh},
		{k.Help, k.Back},
	}
}
