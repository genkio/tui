package ui

import (
	"charm.land/bubbles/v2/key"

	"github.com/genkio/tui/core"
)

type keyMap struct {
	core.FeedKeys
	UnreadOnly   key.Binding
	SwitchTab    key.Binding
	ToggleHandle key.Binding
	Quit         key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		FeedKeys:     core.NewFeedKeys(),
		UnreadOnly:   key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "toggle unread-only")),
		SwitchTab:    key.NewBinding(key.WithKeys("left", "right", "h", "l"), key.WithHelp("←/→", "For You / Following")),
		ToggleHandle: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "toggle @handle column")),
		Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "close/quit")),
	}
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.SwitchTab, k.Expand, k.Mark, k.Keep, k.Carbonyl, k.OpenURL, k.CopyURL, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.SwitchTab, k.Expand, k.Mark, k.Keep, k.UnreadOnly, k.ToggleHandle},
		{k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Refresh},
		{k.Help, k.Quit},
	}
}
