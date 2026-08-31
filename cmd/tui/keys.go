package main

import (
	"charm.land/bubbles/v2/key"

	"github.com/genkio/tui/core"
)

// allKeyMap is the server-backed feed's terminal key set.
type allKeyMap struct {
	core.FeedKeys
	Back key.Binding
	Save key.Binding
}

func defaultAllKeys() allKeyMap {
	feed := core.NewFeedKeys()
	feed.Up.SetHelp("↑/k", "up")
	feed.Down.SetHelp("↓/j", "down/scroll")
	feed.Expand.SetHelp("space", "expand/collapse")
	return allKeyMap{
		FeedKeys: feed,
		Back:     key.NewBinding(key.WithKeys("q", "esc"), key.WithHelp("q", "quit")),
		Save:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
	}
}

func (k allKeyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Save, k.Carbonyl, k.OpenURL, k.CopyURL, k.Refresh, k.Help, k.Back}
}

func (k allKeyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.Save, k.ShowSource, k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Refresh},
		{k.Help, k.Back},
	}
}
