// Command tui is a launcher for the terminal apps in this repo (x, inoreader,
// slack). It lists them, runs the selected one as a subprocess, and kicks off
// that project's `make auth` first when it isn't logged in yet. Because each TUI
// runs as a child process, quitting it (q) drops back here; q again exits.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type app struct {
	name string
	desc string
	dir  string
	// authGroups lists sets of env vars; the app counts as logged in when any
	// one group is fully present (slack accepts xoxp OR xoxc+xoxd).
	authGroups [][]string
}

func appsIn(root string) []app {
	return []app{
		{"x", "x.com home timelines (For You / Following)", filepath.Join(root, "x"),
			[][]string{{"XTUI_AUTH_TOKEN", "XTUI_CT0"}}},
		{"inoreader", "Inoreader unread article triage", filepath.Join(root, "inoreader"),
			[][]string{{"INOREADER_COOKIE"}}},
		{"slack", "Slack unread messages and threads", filepath.Join(root, "slack"),
			[][]string{{"SLACK_MCP_XOXP_TOKEN"}, {"SLACK_MCP_XOXC_TOKEN", "SLACK_MCP_XOXD_TOKEN"}}},
	}
}

// authed reports whether the app has the env it needs, sourcing the project's
// .env on top of the current environment the way `make run` does.
func (a app) authed() bool {
	var cands []string
	seen := map[string]bool{}
	for _, g := range a.authGroups {
		for _, v := range g {
			if !seen[v] {
				seen[v] = true
				cands = append(cands, v)
			}
		}
	}
	present := sourcedVars(a.dir, cands)
	for _, g := range a.authGroups {
		ok := true
		for _, v := range g {
			if !present[v] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func sourcedVars(dir string, names []string) map[string]bool {
	out := map[string]bool{}
	if len(names) == 0 {
		return out
	}
	var b strings.Builder
	b.WriteString("set -a; [ -f .env ] && . ./.env; set +a; ")
	for _, n := range names {
		fmt.Fprintf(&b, "[ -n \"${%s}\" ] && echo %s; ", n, n)
	}
	b.WriteString("true")
	cmd := exec.Command("bash", "-c", b.String())
	cmd.Dir = dir
	o, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, name := range strings.Fields(string(o)) {
		out[name] = true
	}
	return out
}

// missingAuthTools lists the tools `make auth` needs (browser capture) that are
// not on PATH, so the picker can warn before a login attempt fails mid-flow.
func missingAuthTools() []string {
	var missing []string
	for _, t := range []string{"playwright-cli", "jq", "node"} {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	return missing
}

type execDoneMsg struct {
	name  string
	phase string // "auth" or "run"
	err   error
}

func runApp(a app) tea.Cmd {
	cmd := exec.Command("make", "-C", a.dir, "run")
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return execDoneMsg{a.name, "run", err} })
}

func authApp(a app) tea.Cmd {
	cmd := exec.Command("make", "-C", a.dir, "auth")
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return execDoneMsg{a.name, "auth", err} })
}

type model struct {
	apps          []app
	authed        []bool
	cursor        int
	width, height int
	status        string
	statusErr     bool
	pendingRun    string   // app to run once its auth flow finishes
	missing       []string // login tools not on PATH (warned before an auth attempt)
}

func newModel(root string) model {
	m := model{apps: appsIn(root)}
	m.authed = make([]bool, len(m.apps))
	m.refreshAuth()
	m.missing = missingAuthTools()
	return m
}

func (m *model) refreshAuth() {
	for i, a := range m.apps {
		m.authed[i] = a.authed()
	}
}

func (m model) appByName(name string) (int, app, bool) {
	for i, a := range m.apps {
		if a.name == name {
			return i, a, true
		}
	}
	return 0, app{}, false
}

func (m model) anyNeedsLogin() bool {
	for _, ok := range m.authed {
		if !ok {
			return true
		}
	}
	return false
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.apps)-1 {
				m.cursor++
			}
			return m, nil
		case "enter", " ":
			a := m.apps[m.cursor]
			m.setStatus("", false)
			if m.authed[m.cursor] {
				return m, runApp(a)
			}
			// Login drives a browser via playwright; bail with a clear message
			// rather than launching an auth flow that would fail partway.
			if m.missing = missingAuthTools(); len(m.missing) > 0 {
				m.setStatus("Can't log in to "+a.name+": install "+strings.Join(m.missing, ", "), true)
				return m, nil
			}
			// Not logged in: run the project's auth flow, then open it on success.
			m.pendingRun = a.name
			return m, authApp(a)
		}
		return m, nil

	case execDoneMsg:
		m.refreshAuth()
		if msg.err != nil {
			m.setStatus("✗ "+msg.name+": "+msg.err.Error(), true)
			m.pendingRun = ""
			return m, nil
		}
		if msg.phase == "auth" && m.pendingRun == msg.name {
			m.pendingRun = ""
			if i, a, ok := m.appByName(msg.name); ok && m.authed[i] {
				return m, runApp(a)
			}
			m.setStatus("Login didn't complete for "+msg.name+". Try again.", true)
		}
		return m, nil
	}
	return m, nil
}

func (m *model) setStatus(s string, isErr bool) { m.status = s; m.statusErr = isErr }

func (m model) View() tea.View {
	var b strings.Builder
	b.WriteString(titleStyle.Render("tui") + "  " + dimStyle.Render("pick an app · enter to open") + "\n\n")

	if len(m.missing) > 0 && m.anyNeedsLogin() {
		b.WriteString(warnStyle.Render("⚠ login needs "+strings.Join(m.missing, ", ")+" on PATH") + "\n\n")
	}

	for i, a := range m.apps {
		cursor, nameSt := "  ", itemStyle
		if i == m.cursor {
			cursor, nameSt = accentStyle.Render("▌ "), selStyle
		}
		badge := dimStyle.Render("○ needs login")
		if m.authed[i] {
			badge = okStyle.Render("● ready")
		}
		b.WriteString(cursor + nameSt.Render(fmt.Sprintf("%-11s", a.name)) + dimStyle.Render(a.desc) + "  " + badge + "\n")
	}
	if m.status != "" {
		st := statusInfoStyle
		if m.statusErr {
			st = statusErrStyle
		}
		b.WriteString("\n" + st.Render(m.status))
	}
	enterHelp := "enter open"
	if !m.authed[m.cursor] {
		enterHelp = "enter log in & open"
	}
	b.WriteString("\n\n" + dimStyle.Render("↑/↓ or j/k move · "+enterHelp+" · q quit"))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

var (
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	accentStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	selStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	itemStyle       = lipgloss.NewStyle()
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	okStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	statusInfoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	statusErrStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui: "+err.Error())
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err != nil {
		fmt.Fprintln(os.Stderr, "tui: run from the repo root (no ./x here): "+err.Error())
		os.Exit(1)
	}
	if _, err := tea.NewProgram(newModel(root)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui: "+err.Error())
		os.Exit(1)
	}
}
