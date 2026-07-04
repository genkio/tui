package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/genkio/tui/core"
)

// Styles are package globals rebuilt by setTheme once the terminal reports its
// background color, so the same palette reads well on light and dark terminals.
var (
	headerStyle, headerMeta                                              lipgloss.Style
	statusInfoStyle, statusErrStyle, helpStyle, emptyStyle, spinnerStyle lipgloss.Style
	listTitleStyle, cursorStyle, nameStyle, nameSelStyle, countStyle     lipgloss.Style
	badgeDMStyle, badgeChanStyle                                         lipgloss.Style
	authorStyle, timeStyle, textStyle, dimStyle                          lipgloss.Style
	threadHintStyle, dividerStyle, selGutterStyle                        lipgloss.Style
)

// Default to a dark palette until the terminal answers the background query;
// most terminals are dark, and the answer (if any) re-themes before first paint.
func init() { setTheme(true) }

// setTheme rebuilds styles for the detected background. Body text uses the
// terminal's own foreground, which is legible on the user's background by
// definition; only accents and dimmed shades adapt to light vs dark.
func setTheme(isDark bool) {
	p := core.NewPalette(isDark)
	accent, green, red, subtle, faint := p.Accent, p.Green, p.Red, p.Subtle, p.Faint
	pick := lipgloss.LightDark(isDark)
	magenta := pick(lipgloss.Color("125"), lipgloss.Color("212"))
	yellow := pick(lipgloss.Color("130"), lipgloss.Color("221"))

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	headerMeta = lipgloss.NewStyle().Foreground(faint)

	statusInfoStyle = lipgloss.NewStyle().Foreground(green)
	statusErrStyle = lipgloss.NewStyle().Foreground(red)
	helpStyle = lipgloss.NewStyle().Foreground(faint)
	emptyStyle = lipgloss.NewStyle().Foreground(subtle).Italic(true)
	spinnerStyle = lipgloss.NewStyle().Foreground(accent)

	listTitleStyle = lipgloss.NewStyle().Bold(true)
	cursorStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	nameStyle = lipgloss.NewStyle()
	nameSelStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	countStyle = lipgloss.NewStyle().Foreground(yellow)
	badgeDMStyle = lipgloss.NewStyle().Foreground(magenta)
	badgeChanStyle = lipgloss.NewStyle().Foreground(subtle)

	authorStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	timeStyle = lipgloss.NewStyle().Foreground(faint)
	textStyle = lipgloss.NewStyle()
	dimStyle = lipgloss.NewStyle().Foreground(faint)
	threadHintStyle = lipgloss.NewStyle().Foreground(subtle)
	dividerStyle = lipgloss.NewStyle().Foreground(red).Bold(true)
	selGutterStyle = lipgloss.NewStyle().Foreground(accent)
}
