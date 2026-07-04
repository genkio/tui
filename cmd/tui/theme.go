package main

import "charm.land/lipgloss/v2"

// theme holds the "all" feed's styles. It is a struct rather than the package
// globals main.go uses for the picker so the two screens can't clash, and so
// the palette can be rebuilt once the terminal reports its background.
type theme struct {
	header, meta               lipgloss.Style
	statusInfo, statusErr      lipgloss.Style
	help, empty, spinner       lipgloss.Style
	selGutter, source, title   lipgloss.Style
	titleSel, time, text, link lipgloss.Style
	read                       lipgloss.Style
	chips                      map[string]lipgloss.Style
}

// newTheme builds the palette for the detected background. Body text uses the
// terminal's own foreground; only accents and dimmed shades adapt. The app
// chips keep a fixed hue each so a service is recognizable at a glance.
func newTheme(isDark bool) theme {
	pick := lipgloss.LightDark(isDark)
	accent := pick(lipgloss.Color("26"), lipgloss.Color("39"))
	green := pick(lipgloss.Color("29"), lipgloss.Color("42"))
	red := pick(lipgloss.Color("124"), lipgloss.Color("203"))
	subtle := pick(lipgloss.Color("240"), lipgloss.Color("245"))
	faint := pick(lipgloss.Color("243"), lipgloss.Color("240"))

	chip := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c))
	}
	return theme{
		header:     lipgloss.NewStyle().Bold(true).Foreground(accent),
		meta:       lipgloss.NewStyle().Foreground(faint),
		statusInfo: lipgloss.NewStyle().Foreground(green),
		statusErr:  lipgloss.NewStyle().Foreground(red),
		help:       lipgloss.NewStyle().Foreground(faint),
		empty:      lipgloss.NewStyle().Foreground(subtle).Italic(true),
		spinner:    lipgloss.NewStyle().Foreground(accent),
		selGutter:  lipgloss.NewStyle().Foreground(accent),
		source:     lipgloss.NewStyle().Foreground(subtle),
		title:      lipgloss.NewStyle(),
		titleSel:   lipgloss.NewStyle().Foreground(accent).Bold(true),
		time:       lipgloss.NewStyle().Foreground(faint),
		text:       lipgloss.NewStyle(),
		link:       lipgloss.NewStyle().Foreground(accent).Underline(true),
		read:       lipgloss.NewStyle().Foreground(faint),
		chips: map[string]lipgloss.Style{
			"x":         chip("39"),  // blue
			"inoreader": chip("214"), // amber
			"folo":      chip("170"), // magenta
		},
	}
}

func (t theme) chip(app string) string {
	label := map[string]string{"x": "𝕏", "inoreader": "ino", "folo": "folo"}[app]
	if label == "" {
		label = app
	}
	st, ok := t.chips[app]
	if !ok {
		st = t.meta
	}
	return st.Render(label)
}
