package ui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Open       key.Binding
	Back       key.Binding
	Mark       key.Binding
	React      key.Binding
	OpenURL    key.Binding
	ToggleBody key.Binding
	Refresh    key.Binding
	Top        key.Binding
	Bottom     key.Binding
	Help       key.Binding
	Quit       key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Open:       key.NewBinding(key.WithKeys("enter", "l", "right"), key.WithHelp("enter", "open")),
		Back:       key.NewBinding(key.WithKeys("esc", "h", "left", "backspace"), key.WithHelp("esc", "back")),
		Mark:       key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "mark read")),
		React:      key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "react")),
		OpenURL:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser")),
		ToggleBody: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "expand text")),
		Refresh:    key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
		Top:        key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:     key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) listShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Open, k.Mark, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) detailShortHelp() []key.Binding {
	expand := key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "expand thread"))
	return []key.Binding{k.Up, k.Down, expand, k.ToggleBody, k.React, k.OpenURL, k.Back, k.Mark, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Open, k.ToggleBody, k.React, k.OpenURL, k.Back, k.Mark, k.Refresh},
		{k.Help, k.Quit},
	}
}
