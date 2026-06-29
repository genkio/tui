package ui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up           key.Binding
	Down         key.Binding
	Top          key.Binding
	Bottom       key.Binding
	Expand       key.Binding
	OpenURL      key.Binding
	CopyURL      key.Binding
	SwitchTab    key.Binding
	ToggleHandle key.Binding
	Refresh      key.Binding
	Help         key.Binding
	Quit         key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:         key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down/scroll")),
		Top:          key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:       key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Expand:       key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "expand/collapse")),
		OpenURL:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser")),
		CopyURL:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy URL")),
		SwitchTab:    key.NewBinding(key.WithKeys("left", "right", "h", "l"), key.WithHelp("←/→", "For You / Following")),
		ToggleHandle: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "toggle @handle column")),
		Refresh:      key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "close/quit")),
	}
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.SwitchTab, k.Expand, k.OpenURL, k.CopyURL, k.ToggleHandle, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.SwitchTab, k.Expand, k.OpenURL, k.CopyURL, k.ToggleHandle, k.Refresh},
		{k.Help, k.Quit},
	}
}
