package core

import "charm.land/lipgloss/v2"

// Theme is the feed's style set, rebuilt once the terminal reports its
// background so the palette matches. Body text keeps the terminal's own
// foreground; only accents and dimmed shades adapt.
type Theme struct {
	Header     lipgloss.Style
	Meta       lipgloss.Style
	StatusInfo lipgloss.Style
	StatusErr  lipgloss.Style
	Help       lipgloss.Style
	Empty      lipgloss.Style
	Spinner    lipgloss.Style
	SelGutter  lipgloss.Style
	Source     lipgloss.Style
	Title      lipgloss.Style
	TitleSel   lipgloss.Style
	Time       lipgloss.Style
	Text       lipgloss.Style
	Link       lipgloss.Style
	Read       lipgloss.Style
	chips      map[string]lipgloss.Style
}

func NewTheme(isDark bool) Theme {
	p := NewPalette(isDark)
	accent, green, red, subtle, faint := p.Accent, p.Green, p.Red, p.Subtle, p.Faint
	chip := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c))
	}
	return Theme{
		Header:     lipgloss.NewStyle().Bold(true).Foreground(accent),
		Meta:       lipgloss.NewStyle().Foreground(faint),
		StatusInfo: lipgloss.NewStyle().Foreground(green),
		StatusErr:  lipgloss.NewStyle().Foreground(red),
		Help:       lipgloss.NewStyle().Foreground(faint),
		Empty:      lipgloss.NewStyle().Foreground(subtle).Italic(true),
		Spinner:    lipgloss.NewStyle().Foreground(accent),
		SelGutter:  lipgloss.NewStyle().Foreground(accent),
		Source:     lipgloss.NewStyle().Foreground(subtle),
		Title:      lipgloss.NewStyle(),
		TitleSel:   lipgloss.NewStyle().Foreground(accent).Bold(true),
		Time:       lipgloss.NewStyle().Foreground(faint),
		Text:       lipgloss.NewStyle(),
		Link:       lipgloss.NewStyle().Foreground(accent).Underline(true),
		Read:       lipgloss.NewStyle().Foreground(faint),
		// app chips keep a fixed hue each so a service is recognizable at a glance
		chips: map[string]lipgloss.Style{
			"x":         chip("39"),  // blue
			"inoreader": chip("214"), // amber
			"folo":      chip("170"), // magenta
		},
	}
}

// Chip renders app's colored source tag (𝕏 / ino / folo) for the merged view.
func (t Theme) Chip(app string) string {
	label := map[string]string{"x": "𝕏", "inoreader": "ino", "folo": "folo"}[app]
	if label == "" {
		label = app
	}
	st, ok := t.chips[app]
	if !ok {
		st = t.Meta
	}
	return st.Render(label)
}

// PlainChip is the chip label without color, for a greyed (read) row.
func PlainChip(app string) string {
	if l := map[string]string{"x": "𝕏", "inoreader": "ino", "folo": "folo"}[app]; l != "" {
		return l
	}
	return app
}
