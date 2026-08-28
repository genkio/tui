package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genkio/tui/core"
)

// allModel is the terminal client for the server's merged unread feed.
type allModel struct {
	server string
	apps   []string

	feed    core.Feed
	spinner spinner.Model
	help    help.Model
	keys    allKeyMap
	th      core.Theme

	pending    map[string][]string // app -> ids marked read, awaiting a flush
	flushArmed bool

	width, height int
	loading       bool
	loadingNote   string
	status        string
	statusErr     bool
	themeAuto     bool
	lastRefresh   time.Time
	fetching      bool
	capped        bool

	xTab     string // which x timeline to show: following | foryou
	xOffered bool   // whether we've already suggested switching to For You
}

func newAllModel(server string) allModel {
	th := core.NewTheme(true) // dark until the terminal answers the background query
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = th.Spinner
	return allModel{
		server:  server,
		th:      th,
		feed:    core.NewFeed(th, true), // merged view: show the per-source chip
		spinner: sp,
		help:    help.New(),
		keys:    defaultAllKeys(),
		pending: map[string][]string{},
		xTab:    "following",
	}
}

// enter opens the server-backed feed and asks the terminal for its background
// so the palette matches the standalone apps.
func (m allModel) enter(w, h int) (allModel, tea.Cmd) {
	m.width, m.height = w, h
	m.loading = true
	m.loadingNote = "Loading feed…"
	m.status = ""
	m.statusErr = false
	m.themeAuto = true
	m.xTab = "following" // every entry starts on x Following
	m.xOffered = false
	m.layout()
	return m, tea.Batch(m.spinner.Tick, fetchAll(m.server, "following"), tea.RequestBackgroundColor)
}

func (m allModel) Update(msg tea.Msg) (allModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if m.themeAuto {
			m.th = core.NewTheme(msg.IsDark())
			m.spinner.Style = m.th.Spinner
			m.feed.SetTheme(m.th)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.loading {
			return m, cmd
		}
		return m, nil

	case allItemsMsg:
		m.loading = false
		m.lastRefresh = msg.updated
		m.fetching = msg.fetching
		m.capped = msg.capped
		m.apps = msg.apps
		m.feed.ClearRead()
		m.feed.SetItems(msg.items, true)
		m.clearStatus()
		if msg.note != "" {
			m.setStatus(msg.note, true)
		}
		m.maybeOfferX()
		return m, nil

	case flushTickMsg:
		m.flushArmed = false
		return m, m.drainPending()

	case markFlushedMsg:
		if msg.err != nil {
			// Re-queue so the next flush (or leaving the screen) retries; marking
			// is idempotent, so a duplicate id is harmless.
			m.pending[msg.app] = append(append([]string{}, msg.ids...), m.pending[msg.app]...)
			m.setStatus("mark-read failed for "+msg.app+"; will retry", true)
		}
		return m, nil

	case unmarkFlushedMsg:
		if msg.err != nil {
			m.feed.RevertKeep(msg.item.Key())
			m.setStatus("could not keep item unread: "+friendlyAllError(msg.err), true)
		}
		return m, nil

	case openedMsg:
		m.setStatus("Opened in browser.", false)
		return m, nil

	case copiedMsg:
		m.setStatus("Copied URL to clipboard.", false)
		return m, nil

	case carbonylDoneMsg:
		m.clearStatus()
		return m, nil

	case carbonylBrowseMsg:
		m.clearStatus()
		return m, openURL(msg.url)

	case errMsg:
		m.loading = false
		m.setStatus(friendlyAllError(msg.err), true)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, m.feed.Update(msg)
}

func (m allModel) handleKey(msg tea.KeyPressMsg) (allModel, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layout()
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		m.clearStatus()
		m.flushNow() // land pending marks first so they don't reappear unread
		m.loading = true
		m.loadingNote = "Reloading…"
		return m, tea.Batch(m.spinner.Tick, fetchAll(m.server, m.xTab))

	case key.Matches(msg, m.keys.ContinueX):
		// 'f' switches x to For You once Following is exhausted.
		if m.offerableX() {
			m.clearStatus()
			m.xTab = "foryou"
			m.xOffered = false
			m.loading = true
			m.loadingNote = "Loading x For You…"
			return m, tea.Batch(m.spinner.Tick, fetchAll(m.server, "foryou"))
		}
		return m, nil

	case key.Matches(msg, m.keys.Up):
		if m.feed.ScrollExpanded(-1) {
			return m, nil
		}
		return m, m.moveMarkingRead(-1)

	case key.Matches(msg, m.keys.Down):
		if m.feed.ScrollExpanded(1) {
			return m, nil
		}
		return m, m.moveMarkingRead(1)

	case key.Matches(msg, m.keys.Top):
		m.feed.ToTop()
		return m, nil

	case key.Matches(msg, m.keys.Bottom):
		m.feed.ToBottom()
		return m, nil

	case key.Matches(msg, m.keys.Expand):
		opened := m.feed.ToggleCursor()
		it, ok := m.feed.Selected()
		if !opened || !ok || m.feed.IsRead(it.Key()) || m.feed.IsKept(it.Key()) {
			return m, nil
		}
		return m, m.markItem(it)

	case key.Matches(msg, m.keys.ShowSource):
		m.feed.ToggleSource()
		return m, nil

	case key.Matches(msg, m.keys.Mark):
		it, ok := m.feed.Selected()
		if !ok || m.feed.IsRead(it.Key()) {
			return m, nil
		}
		if m.feed.IsKept(it.Key()) {
			m.setStatus("Kept unread; press K to unlock first.", true)
			return m, nil
		}
		m.clearStatus()
		return m, m.markItem(it)

	case key.Matches(msg, m.keys.Keep):
		it, ok := m.feed.Selected()
		if !ok {
			return m, nil
		}
		if kept, _ := m.feed.ToggleKeep(); kept {
			m.unqueue(it)
			m.setStatus("Kept unread; scrolling won't mark it read. K again to unlock.", false)
			return m, unmarkServer(m.server, it)
		} else {
			m.setStatus("Keep removed.", false)
		}
		return m, nil

	case key.Matches(msg, m.keys.OpenURL):
		if it, ok := m.feed.Selected(); ok {
			return m, m.withURL(it, openURL)
		}
		return m, nil

	case key.Matches(msg, m.keys.Carbonyl):
		if it, ok := m.feed.Selected(); ok {
			return m, m.withURL(it, func(u string) tea.Cmd { return openCarbonyl(u, false) })
		}
		return m, nil

	case key.Matches(msg, m.keys.CarbonylGfx):
		if it, ok := m.feed.Selected(); ok {
			return m, m.withURL(it, func(u string) tea.Cmd { return openCarbonyl(u, true) })
		}
		return m, nil

	case key.Matches(msg, m.keys.CopyURL):
		if it, ok := m.feed.Selected(); ok {
			return m, m.withURL(it, copyToClipboard)
		}
		return m, nil
	}

	return m, m.feed.Update(msg)
}

func (m *allModel) withURL(it core.Item, act func(string) tea.Cmd) tea.Cmd {
	if it.URL == "" {
		m.setStatus("No URL for this item.", true)
		return nil
	}
	return act(it.URL)
}

// moveMarkingRead moves the cursor and marks the row it left read, so triage
// happens by scrolling in either direction.
func (m *allModel) moveMarkingRead(delta int) tea.Cmd {
	before := m.feed.Cursor()
	leaving, ok := m.feed.Selected()
	m.feed.MoveCursor(delta)
	if !ok || m.feed.Cursor() == before || m.feed.IsRead(leaving.Key()) || m.feed.IsKept(leaving.Key()) {
		return nil
	}
	return m.markItem(leaving)
}

// markItem greys the row and queues its id for a debounced flush to that app's
// own read state (x's local store, or Inoreader/Folo's server).
func (m *allModel) markItem(it core.Item) tea.Cmd {
	m.feed.MarkRead(it.Key())
	m.pending[it.App] = append(m.pending[it.App], it.ID)
	m.maybeOfferX()
	if m.flushArmed {
		return nil
	}
	m.flushArmed = true
	return scheduleFlush()
}

// unqueue drops a not-yet-flushed mark so keeping an item unread cancels it. A
// mark already flushed to the app's store can't be undone (no mark-unread).
func (m *allModel) unqueue(it core.Item) {
	ids := m.pending[it.App]
	for i, id := range ids {
		if id == it.ID {
			m.pending[it.App] = append(ids[:i], ids[i+1:]...)
			return
		}
	}
}

// drainPending fires one flush per app for everything queued, clearing the
// queue. In-flight ids are captured by value in the command.
func (m *allModel) drainPending() tea.Cmd {
	var cmds []tea.Cmd
	for app, ids := range m.pending {
		if len(ids) > 0 {
			cmds = append(cmds, flushMarks(m.server, app, ids))
		}
	}
	m.pending = map[string][]string{}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// flushNow lands every queued mark synchronously. Called when leaving the
// screen or before a refresh, mirroring the standalone apps' save-on-quit, so
// nothing triaged here is lost.
func (m *allModel) flushNow() {
	for app, ids := range m.pending {
		_ = markServerRead(m.server, app, ids)
	}
	m.pending = map[string][]string{}
	m.flushArmed = false
}

// hasPending reports whether any marks await a flush, so the launcher can show
// a brief "saving" note when leaving.
func (m allModel) hasPending() bool {
	for _, ids := range m.pending {
		if len(ids) > 0 {
			return true
		}
	}
	return false
}

func (m allModel) View() string {
	if m.width == 0 {
		return "starting…"
	}
	body := core.ForceHeight(m.bodyView(), m.bodyHeight())
	return strings.Join([]string{m.headerView(), "", body, m.statusView(), m.helpView()}, "\n")
}

func (m allModel) headerView() string {
	th := m.th
	left := th.Header.Render("all")
	count := fmt.Sprint(m.feed.Len())
	if m.capped {
		count += "+"
	}
	left += th.Meta.Render(fmt.Sprintf("  %s unread · %s", count, strings.Join(m.apps, " · ")))

	var meta []string
	if m.fetching {
		meta = append(meta, "fetching")
	} else if !m.lastRefresh.IsZero() {
		meta = append(meta, "updated "+m.lastRefresh.Format("15:04:05"))
	}
	right := ""
	if len(meta) > 0 {
		right = th.Meta.Render(strings.Join(meta, " · "))
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m allModel) bodyView() string {
	h := m.bodyHeight()
	switch {
	case m.loading && m.feed.Len() == 0:
		return core.Center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case m.feed.Len() == 0:
		return core.Center(m.th.Empty.Render("Inbox zero across every timeline."), m.width, h)
	default:
		return m.feed.View()
	}
}

func (m allModel) statusView() string {
	switch {
	case m.loading && m.feed.Len() > 0:
		return m.spinner.View() + " " + m.th.Help.Render(m.loadingNote)
	case m.statusErr && m.status != "":
		return m.th.StatusErr.Render(m.status)
	case m.status != "":
		return m.th.StatusInfo.Render(m.status)
	default:
		return ""
	}
}

func (m allModel) helpView() string {
	if m.help.ShowAll {
		return m.help.FullHelpView(m.keys.fullHelp())
	}
	return m.help.ShortHelpView(m.keys.shortHelp())
}

func (m *allModel) layout() {
	if m.width == 0 {
		return
	}
	m.help.SetWidth(m.width)
	m.feed.SetSize(m.width, m.bodyHeight())
}

func (m allModel) bodyHeight() int {
	helpH := 1
	if m.help.ShowAll {
		helpH = 3
	}
	// header(1) + blank(1) + status(1) + help
	h := m.height - 3 - helpH
	if h < 3 {
		h = 3
	}
	return h
}

func (m *allModel) setStatus(s string, isErr bool) { m.status = s; m.statusErr = isErr }
func (m *allModel) clearStatus()                   { m.status = ""; m.statusErr = false }

// hasX reports whether x is among the authed feed apps this screen merged.
func (m *allModel) hasX() bool {
	for _, a := range m.apps {
		if a == "x" {
			return true
		}
	}
	return false
}

// offerableX reports whether we should suggest continuing on x For You: x is
// authed, we're on Following, and there's nothing unread left in the feed.
func (m *allModel) offerableX() bool {
	if m.xTab != "following" || !m.hasX() {
		return false
	}
	if m.feed.Len() == 0 {
		return true
	}
	for _, it := range m.feed.Items() {
		if !m.feed.IsRead(it.Key()) {
			return false
		}
	}
	return true
}

// maybeOfferX shows the "continue on x For You" hint once, when Following is
// exhausted (so the user knows 'f' will switch), and only if not already on
// For You.
func (m *allModel) maybeOfferX() {
	if m.xOffered || !m.offerableX() {
		return
	}
	m.xOffered = true
	m.setStatus("All read — press f to continue on x For You.", false)
}

// friendlyAllError trims a raw error to one readable line.
func friendlyAllError(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}
