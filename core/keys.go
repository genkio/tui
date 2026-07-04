package core

import "charm.land/bubbles/v2/key"

// FeedKeys are the bindings every scrolling feed shares, so j/k triage, o/O
// carbonyl, b browser, c copy, r/K mark all mean the same thing in every app
// and in the merged view. Each screen embeds this and adds its own (tabs,
// toggles, quit vs back-to-picker).
type FeedKeys struct {
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
}

func NewFeedKeys() FeedKeys {
	return FeedKeys{
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
	}
}
