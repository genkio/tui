package main

import "charm.land/bubbles/v2/key"

// allKeyMap is the "all" timeline's keys. It mirrors the individual apps (j/k
// triage, o/O carbonyl, b browser, c copy, r mark, R refresh) so muscle memory
// carries over; q backs out to the picker rather than quitting the launcher.
type allKeyMap struct {
	Up          key.Binding
	Down        key.Binding
	Top         key.Binding
	Bottom      key.Binding
	Expand      key.Binding
	OpenURL     key.Binding
	Carbonyl    key.Binding
	CarbonylGfx key.Binding
	CopyURL     key.Binding
	Mark        key.Binding
	Keep        key.Binding
	Refresh     key.Binding
	Help        key.Binding
	Back        key.Binding
}

func defaultAllKeys() allKeyMap {
	return allKeyMap{
		Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up (marks read)")),
		Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down/scroll (marks read)")),
		Top:         key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:      key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Expand:      key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "expand/collapse (marks read)")),
		OpenURL:     key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "open in browser")),
		Carbonyl:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "read in carbonyl")),
		CarbonylGfx: key.NewBinding(key.WithKeys("O"), key.WithHelp("O", "carbonyl w/ graphics")),
		CopyURL:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy URL")),
		Mark:        key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "mark read")),
		Keep:        key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "keep unread")),
		Refresh:     key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
		Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Back:        key.NewBinding(key.WithKeys("q", "esc"), key.WithHelp("q", "back to picker")),
	}
}

func (k allKeyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Expand, k.Mark, k.Keep, k.Carbonyl, k.OpenURL, k.CopyURL, k.Refresh, k.Help, k.Back}
}

func (k allKeyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Expand, k.Mark, k.Keep, k.Carbonyl, k.CarbonylGfx, k.OpenURL, k.CopyURL, k.Refresh},
		{k.Help, k.Back},
	}
}
