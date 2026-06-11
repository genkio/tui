package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sahilm/fuzzy"
)

// pickerModel is the emoji reaction picker: a text field that fuzzy-filters the
// workspace's custom emoji names. Enter reacts with the highlighted name, or
// with the raw typed text when nothing matches (so standard emoji still work by
// exact name without bundling the full unicode set).
type pickerModel struct {
	input    textinput.Model
	names    []string // full custom-emoji set, cached for the session
	filtered []string
	cursor   int
	loading  bool
	err      string

	channelID string // target message for the reaction
	ts        string
}

func newPicker() pickerModel {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.Placeholder = "search emoji"
	ti.CharLimit = 64
	return pickerModel{input: ti}
}

// open targets a message and resets the field. Returns the focus command.
func (p *pickerModel) open(channelID, ts string) tea.Cmd {
	p.channelID = channelID
	p.ts = ts
	p.err = ""
	p.cursor = 0
	p.input.Reset()
	p.filter()
	return p.input.Focus()
}

func (p *pickerModel) close() {
	p.input.Blur()
}

func (p *pickerModel) setNames(names []string) {
	p.names = names
	p.loading = false
	p.err = ""
	p.filter()
}

func (p *pickerModel) setErr(err error) {
	p.loading = false
	p.err = err.Error()
}

// update forwards a key to the text field and re-filters, resetting the cursor
// to the top match. The caller handles movement/submit/cancel keys first.
func (p *pickerModel) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.cursor = 0
	p.filter()
	return cmd
}

func (p *pickerModel) filter() {
	q := strings.TrimSpace(p.input.Value())
	if q == "" {
		p.filtered = p.names
	} else {
		matches := fuzzy.Find(q, p.names)
		p.filtered = make([]string, len(matches))
		for i, m := range matches {
			p.filtered[i] = m.Str
		}
	}
	p.clampCursor()
}

func (p *pickerModel) moveCursor(delta int) {
	p.cursor += delta
	p.clampCursor()
}

func (p *pickerModel) clampCursor() {
	if p.cursor >= len(p.filtered) {
		p.cursor = len(p.filtered) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// selected is the emoji name Enter will react with: the highlighted match, or
// the raw typed text when there is no match.
func (p pickerModel) selected() string {
	if p.cursor >= 0 && p.cursor < len(p.filtered) {
		return p.filtered[p.cursor]
	}
	return strings.TrimSpace(p.input.Value())
}

func (p pickerModel) View(width, height int) string {
	boxWidth := min(48, width-4)
	if boxWidth < 16 {
		boxWidth = max(width-4, 16)
	}
	listRows := height - 8 // title, input, status, footer, borders, padding
	if listRows < 3 {
		listRows = 3
	}

	var b strings.Builder
	b.WriteString(authorStyle.Render("Add reaction"))
	b.WriteByte('\n')
	b.WriteString(p.input.View())
	b.WriteString("\n\n")
	b.WriteString(p.listView(listRows))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("up/down move · enter react · esc cancel"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(boxWidth).
		Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (p pickerModel) listView(rows int) string {
	switch {
	case p.loading:
		return helpStyle.Render("loading emoji…")
	case p.err != "":
		return statusErrStyle.Render(truncate(p.err, 44)) + "\n" +
			helpStyle.Render("enter reacts with the typed name")
	case len(p.filtered) == 0:
		return emptyStyle.Render("no match, enter reacts with the typed name")
	}

	start, end := window(len(p.filtered), p.cursor, rows)
	var lines []string
	for i := start; i < end; i++ {
		name := ":" + p.filtered[i] + ":"
		if i == p.cursor {
			lines = append(lines, cursorStyle.Render("› ")+nameSelStyle.Render(name))
		} else {
			lines = append(lines, "  "+nameStyle.Render(name))
		}
	}
	if more := len(p.filtered) - end; more > 0 {
		lines = append(lines, helpStyle.Render(fmt.Sprintf("  +%d more", more)))
	}
	return strings.Join(lines, "\n")
}

// window returns a [start,end) slice of n items that keeps cursor visible,
// centering it once the list overflows the available rows.
func window(n, cursor, rows int) (int, int) {
	if n <= rows {
		return 0, n
	}
	start := cursor - rows/2
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > n {
		end = n
		start = end - rows
	}
	return start, end
}
