package ui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Top        key.Binding
	Bottom     key.Binding
	Expand     key.Binding
	OpenURL    key.Binding
	Carbonyl   key.Binding
	CopyURL    key.Binding
	Mark       key.Binding
	Keep       key.Binding
	ToggleFeed key.Binding
	Refresh    key.Binding
	Help       key.Binding
	Quit       key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up (marks read)")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down/scroll (marks read)")),
		Top:        key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:     key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Expand:     key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "expand/collapse (marks read)")),
		OpenURL:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser")),
		Carbonyl:   key.NewBinding(key.WithKeys("O"), key.WithHelp("O", "read in carbonyl")),
		CopyURL:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy URL")),
		Mark:       key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "mark read")),
		Keep:       key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "keep unread")),
		ToggleFeed: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "toggle feed column")),
		Refresh:    key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "close/quit")),
	}
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.OpenURL, k.Carbonyl, k.CopyURL, k.Mark, k.Keep, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.OpenURL, k.Carbonyl, k.CopyURL, k.Mark, k.Keep, k.ToggleFeed, k.Refresh},
		{k.Help, k.Quit},
	}
}
