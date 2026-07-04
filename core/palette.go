package core

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is the color set every screen themes from. Body text keeps the
// terminal's own foreground; only accents and dimmed shades adapt to light vs
// dark, so the same palette reads well on either background.
type Palette struct {
	Accent color.Color
	Green  color.Color
	Red    color.Color
	Subtle color.Color
	Faint  color.Color
}

func NewPalette(isDark bool) Palette {
	pick := lipgloss.LightDark(isDark)
	return Palette{
		Accent: pick(lipgloss.Color("26"), lipgloss.Color("39")),
		Green:  pick(lipgloss.Color("29"), lipgloss.Color("42")),
		Red:    pick(lipgloss.Color("124"), lipgloss.Color("203")),
		Subtle: pick(lipgloss.Color("240"), lipgloss.Color("245")),
		Faint:  pick(lipgloss.Color("243"), lipgloss.Color("240")),
	}
}
