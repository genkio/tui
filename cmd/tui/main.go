// Command tui is a launcher for the terminal apps in this repo (x, inoreader,
// slack, folo). It lists them, runs the selected one as a subprocess, and kicks off
// that project's `make auth` first when it isn't logged in yet. Because each TUI
// runs as a child process, quitting it (q) drops back here; q again exits.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// feed marks an app the "all" timeline aggregates: one that implements the
	// `make json` / `make mark-read` contract. Flip this on for a new reader app
	// and it joins the merged feed automatically once logged in. Slack is off:
	// it's a chat model, not an article stream.
	feed bool
}

func appsIn(root string) []app {
	return []app{
		{"x", "x.com home timelines (For You / Following)", filepath.Join(root, "plugins", "x"),
			[][]string{{"XTUI_AUTH_TOKEN", "XTUI_CT0"}}, true},
		{"inoreader", "Inoreader unread article triage", filepath.Join(root, "plugins", "inoreader"),
			[][]string{{"INOREADER_COOKIE"}}, true},
		{"slack", "Slack unread messages and threads", filepath.Join(root, "plugins", "slack"),
			[][]string{{"SLACK_MCP_XOXP_TOKEN"}, {"SLACK_MCP_XOXC_TOKEN", "SLACK_MCP_XOXD_TOKEN"}}, false},
		{"folo", "Folo pending articles (Follow reader)", filepath.Join(root, "plugins", "folo"),
			[][]string{{"FOLO_COOKIE"}}, true},
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

type (
	pollTickMsg  struct{}
	clockTickMsg struct{}
	countMsg     struct {
		name  string
		token string // "12", "75+", "0"; empty when the count failed
		err   bool
	}
)

func schedulePoll(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return pollTickMsg{} })
}

// clockInterval re-renders often enough to keep the "updated Nm ago" label from
// lagging its minute granularity, without a busy loop.
const clockInterval = 30 * time.Second

func scheduleClock() tea.Cmd {
	return tea.Tick(clockInterval, func(time.Time) tea.Msg { return clockTickMsg{} })
}

// fetchCount runs the app's `make count` (which sources its .env and prints one
// unread-count token) and reports the parsed token. It shells out per poll
// rather than importing each app, keeping the launcher decoupled from their
// clients and honoring whatever session each app already has.
func fetchCount(a app) tea.Cmd {
	name, dir := a.name, a.dir
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "make", "-C", dir, "count").Output()
		if err != nil {
			return countMsg{name: name, err: true}
		}
		tok := parseCountToken(string(out))
		if tok == "" {
			return countMsg{name: name, err: true}
		}
		return countMsg{name: name, token: tok}
	}
}

var reCountToken = regexp.MustCompile(`^\d+\+?$`)

// parseCountToken pulls the last count-shaped word out of the subprocess output,
// tolerating any stray build chatter the toolchain might emit.
func parseCountToken(s string) string {
	tok := ""
	for _, f := range strings.Fields(s) {
		if reCountToken.MatchString(f) {
			tok = f
		}
	}
	return tok
}

// screen is the launcher's active view: the app picker, or the merged "all"
// timeline that reads across every logged-in app.
type screen int

const (
	screenHome screen = iota
	screenAll
)

type model struct {
	apps          []app
	authed        []bool
	cursor        int
	width, height int
	status        string
	statusErr     bool
	pendingRun    string   // app to run once its auth flow finishes
	missing       []string // login tools not on PATH (warned before an auth attempt)

	screen screen
	all    allModel // the "all" timeline screen; active when screen == screenAll

	pollEvery time.Duration     // unread-count poll interval; 0 disables polling
	counts    map[string]string // app name -> last count token
	countErr  map[string]bool   // app name -> last poll failed
	running   bool              // a child TUI/auth flow is active; pause polling
	lastFetch time.Time         // when the most recent count landed, for the freshness label
}

func newModel(root string, pollEvery time.Duration) model {
	m := model{
		apps:      appsIn(root),
		pollEvery: pollEvery,
		counts:    map[string]string{},
		countErr:  map[string]bool{},
		all:       newAllModel(root),
	}
	m.authed = make([]bool, len(m.apps))
	m.refreshAuth()
	m.missing = missingAuthTools()
	return m
}

// The picker shows the "all" timeline as a synthetic first row above the real
// apps, so the cursor spans one extra position.
func (m model) numRows() int         { return len(m.apps) + 1 }
func (m model) isAllRow(i int) bool  { return i == 0 }
func (m model) appIndex(row int) int { return row - 1 } // row>0 maps to m.apps

// authedFeedApps is the logged-in subset of the apps the "all" timeline merges,
// in registry order. It's derived from the registry's feed flag, so a newly
// registered reader app is picked up automatically once it's authed.
func (m model) authedFeedApps() []string {
	var out []string
	for i, a := range m.apps {
		if a.feed && m.authed[i] {
			out = append(out, a.name)
		}
	}
	return out
}

// saturated reports whether an app's last count was capped ("N+"). Re-polling a
// saturated service can't move the badge off the ceiling, so the periodic poll
// skips it until a manual refresh or a return from the app re-checks it.
func (m model) saturated(name string) bool {
	return strings.HasSuffix(m.counts[name], "+")
}

// countAuthed fetches counts for logged-in apps at once, or nil when none apply.
// With skipSaturated it leaves capped services alone (used by the periodic poll);
// manual refresh and post-run refresh pass false to re-check everything, so a
// service you've read down loses its "N+" and resumes normal polling.
func (m model) countAuthed(skipSaturated bool) tea.Cmd {
	var cmds []tea.Cmd
	for i, a := range m.apps {
		if !m.authed[i] {
			continue
		}
		if skipSaturated && m.saturated(a.name) {
			continue
		}
		cmds = append(cmds, fetchCount(a))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// pollAuthed is the periodic poll: gated on polling being enabled, and skips
// saturated services.
func (m model) pollAuthed() tea.Cmd {
	if m.pollEvery <= 0 {
		return nil
	}
	return m.countAuthed(true)
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

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{scheduleClock()} // keep the freshness label ticking even with polling off
	if m.pollEvery > 0 {
		cmds = append(cmds, m.pollAuthed(), schedulePoll(m.pollEvery))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Window size feeds both screens; track it even while the all timeline owns
	// the view so returning to the picker is laid out correctly.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		if m.screen == screenAll {
			m.all.width, m.all.height = m.width, m.height
			m.all.layout()
		}
		return m, nil
	}
	if m.screen == screenAll {
		return m.updateAll(msg)
	}

	switch msg := msg.(type) {
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
			if m.cursor < m.numRows()-1 {
				m.cursor++
			}
			return m, nil
		case "r":
			// Manual count refresh, available even when periodic polling is off.
			if m.running {
				return m, nil
			}
			m.setStatus("Checking unread counts…", false)
			return m, m.countAuthed(false)
		case "enter", " ":
			m.setStatus("", false)
			if m.isAllRow(m.cursor) {
				apps := m.authedFeedApps()
				if len(apps) == 0 {
					m.setStatus("Log into x, inoreader, or folo to open the all timeline.", true)
					return m, nil
				}
				m.screen = screenAll
				var cmd tea.Cmd
				m.all, cmd = m.all.enter(apps, m.width, m.height)
				return m, cmd
			}
			a := m.apps[m.appIndex(m.cursor)]
			if m.authed[m.appIndex(m.cursor)] {
				m.running = true // pause polling while the child owns the terminal
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
			m.running = true
			return m, authApp(a)
		}
		return m, nil

	case execDoneMsg:
		m.running = false
		m.refreshAuth()
		if msg.err != nil {
			m.setStatus("✗ "+msg.name+": "+msg.err.Error(), true)
			m.pendingRun = ""
			return m, nil
		}
		if msg.phase == "auth" && m.pendingRun == msg.name {
			m.pendingRun = ""
			if i, a, ok := m.appByName(msg.name); ok && m.authed[i] {
				m.running = true
				return m, runApp(a)
			}
			m.setStatus("Login didn't complete for "+msg.name+". Try again.", true)
			return m, nil
		}
		// Back from a run: counts likely changed while you triaged, so re-check
		// everything (even saturated services, which you may have read down).
		if m.pollEvery > 0 {
			return m, m.countAuthed(false)
		}
		return m, nil

	case pollTickMsg:
		cmds := []tea.Cmd{schedulePoll(m.pollEvery)}
		// Don't poll a service a child TUI or the all timeline is already hitting.
		if !m.running && m.screen == screenHome {
			cmds = append(cmds, m.pollAuthed())
		}
		return m, tea.Batch(cmds...)

	case clockTickMsg:
		return m, scheduleClock() // re-render so the "updated Nm ago" label stays current

	case countMsg:
		if m.status == "Checking unread counts…" {
			m.setStatus("", false)
		}
		if msg.err {
			m.countErr[msg.name] = true
		} else {
			m.counts[msg.name] = msg.token
			m.countErr[msg.name] = false
			m.lastFetch = time.Now()
		}
		return m, nil
	}
	return m, nil
}

// updateAll drives the all-timeline screen, intercepting only the keys that
// change screen (ctrl+c quits the launcher; q/esc collapse an expanded row or
// else back out to the picker) and delegating everything else to allModel.
func (m model) updateAll(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "ctrl+c":
			m.all.flushNow()
			return m, tea.Quit
		case "q", "esc":
			if m.all.feed.collapseCursor() {
				return m, nil
			}
			return m.leaveAll()
		}
	}
	var cmd tea.Cmd
	m.all, cmd = m.all.Update(msg)
	return m, cmd
}

// leaveAll returns to the picker, flushing any pending read marks first so
// nothing triaged is lost, then re-checking counts since triage likely changed
// them (mirroring the recount on returning from a child app).
func (m model) leaveAll() (tea.Model, tea.Cmd) {
	if m.all.hasPending() {
		m.setStatus("Saving reads…", false)
	}
	m.all.flushNow()
	m.screen = screenHome
	return m, m.countAuthed(false)
}

func (m *model) setStatus(s string, isErr bool) { m.status = s; m.statusErr = isErr }

func (m model) View() tea.View {
	if m.screen == screenAll {
		v := tea.NewView(m.all.View())
		v.AltScreen = true
		return v
	}

	var b strings.Builder
	left := titleStyle.Render("tui") + "  " + dimStyle.Render("pick an app · enter to open")

	var meta []string
	if m.pollEvery > 0 {
		meta = append(meta, "every "+m.pollEvery.String())
	}
	if !m.lastFetch.IsZero() {
		meta = append(meta, "updated "+humanAgo(m.lastFetch))
	}
	right := ""
	if len(meta) > 0 {
		right = dimStyle.Render(strings.Join(meta, " · "))
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(left + strings.Repeat(" ", gap) + right + "\n\n")

	if len(m.missing) > 0 && m.anyNeedsLogin() {
		b.WriteString(warnStyle.Render("⚠ login needs "+strings.Join(m.missing, ", ")+" on PATH") + "\n\n")
	}

	renderRow := func(rowIdx int, name, desc, badge string) {
		cursor, nameSt := "  ", itemStyle
		if rowIdx == m.cursor {
			cursor, nameSt = accentStyle.Render("▌ "), selStyle
		}
		b.WriteString(cursor + nameSt.Render(fmt.Sprintf("%-11s", name)) + dimStyle.Render(desc) + "  " + badge + "\n")
	}

	// The all timeline leads the list as a synthetic first row, above the apps
	// it aggregates.
	renderRow(0, "all", "Merged unread across x · inoreader · folo", m.allBadge())
	for i, a := range m.apps {
		renderRow(i+1, a.name, a.desc, m.badge(i))
	}

	if m.status != "" {
		st := statusInfoStyle
		if m.statusErr {
			st = statusErrStyle
		}
		b.WriteString("\n" + st.Render(m.status))
	}
	enterHelp := "enter open"
	if !m.isAllRow(m.cursor) && !m.authed[m.appIndex(m.cursor)] {
		enterHelp = "enter log in & open"
	}
	help := "↑/↓ or j/k move · " + enterHelp + " · r refresh counts · q quit"
	b.WriteString("\n\n" + dimStyle.Render(help))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// badge renders the right-hand status for app i: login state, or its unread
// count once a poll has landed.
func (m model) badge(i int) string {
	if !m.authed[i] {
		return dimStyle.Render("○ needs login")
	}
	name := m.apps[i].name
	tok, ok := m.counts[name]
	switch {
	case ok && tok == "0":
		return dimStyle.Render("● all read")
	case ok:
		return okStyle.Render("● ") + countStyle.Render(tok) + dimStyle.Render(" unread")
	case m.pollEvery > 0 && !m.countErr[name]:
		return dimStyle.Render("● checking…")
	default:
		return okStyle.Render("● ready")
	}
}

// allBadge sums the unread counts of the logged-in feed apps for the all row.
// A saturated ("N+") component makes the whole sum saturated, since the true
// total is at least that much.
func (m model) allBadge() string {
	apps := m.authedFeedApps()
	if len(apps) == 0 {
		return dimStyle.Render("○ needs a login")
	}
	total, saturated, haveAny := 0, false, false
	for _, name := range apps {
		tok, ok := m.counts[name]
		if !ok {
			continue
		}
		haveAny = true
		if strings.HasSuffix(tok, "+") {
			saturated = true
		}
		n, _ := strconv.Atoi(strings.TrimSuffix(tok, "+"))
		total += n
	}
	switch {
	case !haveAny:
		return dimStyle.Render("● checking…")
	case total == 0 && !saturated:
		return dimStyle.Render("● all read")
	default:
		label := strconv.Itoa(total)
		if saturated {
			label += "+"
		}
		return okStyle.Render("● ") + countStyle.Render(label) + dimStyle.Render(" unread")
	}
}

// humanAgo renders how long ago t was at minute granularity, for the freshness
// label.
func humanAgo(t time.Time) string {
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

var (
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	accentStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	selStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	itemStyle       = lipgloss.NewStyle()
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	okStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	countStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	statusInfoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	statusErrStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

func main() {
	// Default 5m, overridable by TUI_POLL (env) then --poll (flag). 0 disables.
	interval := 5 * time.Minute
	if v := os.Getenv("TUI_POLL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}
	poll := flag.Duration("poll", interval, "unread-count poll interval (e.g. 5m; 0 disables)")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui: "+err.Error())
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "x")); err != nil {
		fmt.Fprintln(os.Stderr, "tui: run from the repo root (no ./plugins/x here): "+err.Error())
		os.Exit(1)
	}
	if _, err := tea.NewProgram(newModel(root, *poll)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui: "+err.Error())
		os.Exit(1)
	}
}
