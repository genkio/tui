package ui

import (
	"charm.land/bubbles/v2/key"

	"github.com/genkio/tui/core"
)

type keyMap struct {
	core.FeedKeys
	Quit key.Binding
}

func defaultKeys() keyMap {
	k := keyMap{
		FeedKeys: core.NewFeedKeys(),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "close/quit")),
	}
	k.ShowSource = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "show feed column"))
	return k
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Carbonyl, k.OpenURL, k.CopyURL, k.Mark, k.Keep, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Mark, k.Keep, k.ShowSource, k.Refresh},
		{k.Help, k.Quit},
	}
}
