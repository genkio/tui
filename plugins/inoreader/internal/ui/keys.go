package ui

import (
	"charm.land/bubbles/v2/key"

	"github.com/genkio/tui/core"
)

type keyMap struct {
	core.FeedKeys
	ToggleFeed key.Binding
	Quit       key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		FeedKeys:   core.NewFeedKeys(),
		ToggleFeed: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "toggle feed column")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "close/quit")),
	}
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Carbonyl, k.OpenURL, k.CopyURL, k.Mark, k.Keep, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Mark, k.Keep, k.ToggleFeed, k.Refresh},
		{k.Help, k.Quit},
	}
}
