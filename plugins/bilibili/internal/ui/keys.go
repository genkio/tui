package ui

import (
	"charm.land/bubbles/v2/key"

	"github.com/genkio/tui/core"
)

type keyMap struct {
	core.FeedKeys
	UnreadOnly key.Binding
	Quit       key.Binding
}

func defaultKeys() keyMap {
	k := keyMap{
		FeedKeys:   core.NewFeedKeys(),
		UnreadOnly: key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "toggle unread-only")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "close/quit")),
	}
	k.ShowSource = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "show uploader names"))
	return k
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Mark, k.Keep, k.Carbonyl, k.OpenURL, k.CopyURL, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.Mark, k.Keep, k.UnreadOnly, k.ShowSource},
		{k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Refresh},
		{k.Help, k.Quit},
	}
}
