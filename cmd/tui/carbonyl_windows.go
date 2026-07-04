//go:build windows

package main

import (
	"errors"

	tea "charm.land/bubbletea/v2"
)

func openCarbonyl(string, bool) tea.Cmd {
	return func() tea.Msg {
		return errMsg{errors.New("carbonyl is not supported on windows")}
	}
}
