package ui

import "charm.land/lipgloss/v2"

// Styles are package globals rebuilt by setTheme once the terminal reports its
// background color, so the same palette reads well on light and dark terminals.
var (
	headerStyle, headerMeta                                              lipgloss.Style
	statusInfoStyle, statusErrStyle, helpStyle, emptyStyle, spinnerStyle lipgloss.Style
	selGutterStyle, handleStyle, titleStyle, titleSelStyle               lipgloss.Style
	timeStyle, textStyle, linkStyle, quoteStyle                          lipgloss.Style
)

// Default to a dark palette until the terminal answers the background query;
// most terminals are dark, and the answer (if any) re-themes before first paint.
func init() { setTheme(true) }

// setTheme rebuilds styles for the detected background. Body text uses the
// terminal's own foreground; only accents and dimmed shades adapt.
func setTheme(isDark bool) {
	pick := lipgloss.LightDark(isDark)
	accent := pick(lipgloss.Color("26"), lipgloss.Color("39"))
	green := pick(lipgloss.Color("29"), lipgloss.Color("42"))
	red := pick(lipgloss.Color("124"), lipgloss.Color("203"))
	subtle := pick(lipgloss.Color("240"), lipgloss.Color("245"))
	faint := pick(lipgloss.Color("243"), lipgloss.Color("240"))

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	headerMeta = lipgloss.NewStyle().Foreground(faint)

	statusInfoStyle = lipgloss.NewStyle().Foreground(green)
	statusErrStyle = lipgloss.NewStyle().Foreground(red)
	helpStyle = lipgloss.NewStyle().Foreground(faint)
	emptyStyle = lipgloss.NewStyle().Foreground(subtle).Italic(true)
	spinnerStyle = lipgloss.NewStyle().Foreground(accent)

	selGutterStyle = lipgloss.NewStyle().Foreground(accent)
	handleStyle = lipgloss.NewStyle().Foreground(subtle)
	titleStyle = lipgloss.NewStyle()
	titleSelStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	timeStyle = lipgloss.NewStyle().Foreground(faint)
	textStyle = lipgloss.NewStyle()
	linkStyle = lipgloss.NewStyle().Foreground(accent).Underline(true)
	quoteStyle = lipgloss.NewStyle().Foreground(subtle)
}
