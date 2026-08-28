package main

import tea "charm.land/bubbletea/v2"

type allClientModel struct {
	all   allModel
	start tea.Cmd
}

func newAllClientModel(server string) allClientModel {
	all, start := newAllModel(server).enter(0, 0)
	return allClientModel{all: all, start: start}
}

func (m allClientModel) Init() tea.Cmd { return m.start }

func (m allClientModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.all.width, m.all.height = size.Width, size.Height
		m.all.layout()
		return m, nil
	}
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "ctrl+c":
			m.all.flushNow()
			return m, tea.Quit
		case "q", "esc":
			if m.all.feed.CollapseCursor() {
				return m, nil
			}
			m.all.flushNow()
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.all, cmd = m.all.Update(msg)
	return m, cmd
}

func (m allClientModel) View() tea.View {
	view := tea.NewView(m.all.View())
	view.AltScreen = true
	return view
}
