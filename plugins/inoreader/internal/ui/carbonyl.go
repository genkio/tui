//go:build !windows

package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
)

// openCarbonyl renders the URL in carbonyl full screen, suspending the TUI
// until it exits.
func openCarbonyl(url string, graphics bool) tea.Cmd {
	c := core.Carbonyl(url, graphics)
	return tea.Exec(c, func(err error) tea.Msg {
		if err != nil {
			return errMsg{err}
		}
		if c.OpenBrowser() {
			return carbonylBrowseMsg{url: c.URL()}
		}
		return carbonylDoneMsg{}
	})
}
