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
)

// allModel is the "all" timeline screen: a merged, time-sorted feed of every
// authed feed app's unread items, with the same triage behavior as the
// standalone apps. It is driven by the launcher's top-level model, which owns
// the home/all screen switch and decides which apps qualify (see app.feed).
type allModel struct {
	root string   // repo root; apps run via `make -C plugins/<app>`
	apps []string // authed feed apps this screen fetches, set on enter

	feed    feed
	spinner spinner.Model
	help    help.Model
	keys    allKeyMap
	th      theme

	pending    map[string][]string // app -> ids marked read, awaiting a flush
	flushArmed bool

	width, height int
	loading       bool
	loadingNote   string
	status        string
	statusErr     bool
	themeAuto     bool
	lastRefresh   time.Time
}

func newAllModel(root string) allModel {
	th := newTheme(true) // dark until the terminal answers the background query
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = th.spinner
	return allModel{
		root:    root,
		th:      th,
		feed:    newFeed(th),
		spinner: sp,
		help:    help.New(),
		keys:    defaultAllKeys(),
		pending: map[string][]string{},
	}
}

// enter (re)opens the screen for the given authed apps at the current size and
// kicks off the concurrent fetch. themeAuto asks the terminal for its
// background so the palette matches, like the standalone apps.
func (m allModel) enter(apps []string, w, h int) (allModel, tea.Cmd) {
	m.apps = apps
	m.width, m.height = w, h
	m.loading = true
	m.loadingNote = "Loading all timelines…"
	m.status = ""
	m.statusErr = false
	m.themeAuto = true
	m.layout()
	return m, tea.Batch(m.spinner.Tick, fetchAll(m.root, apps), tea.RequestBackgroundColor)
}

func (m allModel) Update(msg tea.Msg) (allModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if m.themeAuto {
			m.th = newTheme(msg.IsDark())
			m.spinner.Style = m.th.spinner
			m.feed.setTheme(m.th)
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
		m.lastRefresh = time.Now()
		m.feed.setItems(msg.items)
		if msg.note != "" {
			m.setStatus(msg.note, true)
		}
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

	var cmd tea.Cmd
	m.feed.vp, cmd = m.feed.vp.Update(msg)
	return m, cmd
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
		m.loadingNote = "Refreshing…"
		return m, tea.Batch(m.spinner.Tick, fetchAll(m.root, m.apps))

	case key.Matches(msg, m.keys.Up):
		if m.feed.scrollExpanded(-1) {
			return m, nil
		}
		return m, m.moveMarkingRead(-1)

	case key.Matches(msg, m.keys.Down):
		if m.feed.scrollExpanded(1) {
			return m, nil
		}
		return m, m.moveMarkingRead(1)

	case key.Matches(msg, m.keys.Top):
		m.feed.toTop()
		return m, nil

	case key.Matches(msg, m.keys.Bottom):
		m.feed.toBottom()
		return m, nil

	case key.Matches(msg, m.keys.Expand):
		opened := m.feed.toggleCursor()
		it, ok := m.feed.selected()
		if !opened || !ok || m.feed.isRead(it.key()) || m.feed.isKept(it.key()) {
			return m, nil
		}
		return m, m.markItem(it)

	case key.Matches(msg, m.keys.Mark):
		it, ok := m.feed.selected()
		if !ok || m.feed.isRead(it.key()) {
			return m, nil
		}
		if m.feed.isKept(it.key()) {
			m.setStatus("Kept unread; press K to unlock first.", true)
			return m, nil
		}
		m.clearStatus()
		return m, m.markItem(it)

	case key.Matches(msg, m.keys.Keep):
		it, ok := m.feed.selected()
		if !ok {
			return m, nil
		}
		if kept, _ := m.feed.toggleKeep(); kept {
			m.unqueue(it)
			m.setStatus("Kept unread; scrolling won't mark it read. K again to unlock.", false)
		} else {
			m.setStatus("Keep removed.", false)
		}
		return m, nil

	case key.Matches(msg, m.keys.OpenURL):
		if it, ok := m.feed.selected(); ok {
			return m, m.withURL(it, openURL)
		}
		return m, nil

	case key.Matches(msg, m.keys.Carbonyl):
		if it, ok := m.feed.selected(); ok {
			return m, m.withURL(it, func(u string) tea.Cmd { return openCarbonyl(u, false) })
		}
		return m, nil

	case key.Matches(msg, m.keys.CarbonylGfx):
		if it, ok := m.feed.selected(); ok {
			return m, m.withURL(it, func(u string) tea.Cmd { return openCarbonyl(u, true) })
		}
		return m, nil

	case key.Matches(msg, m.keys.CopyURL):
		if it, ok := m.feed.selected(); ok {
			return m, m.withURL(it, copyToClipboard)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.feed.vp, cmd = m.feed.vp.Update(msg)
	return m, cmd
}

func (m *allModel) withURL(it item, act func(string) tea.Cmd) tea.Cmd {
	if it.URL == "" {
		m.setStatus("No URL for this item.", true)
		return nil
	}
	return act(it.URL)
}

// moveMarkingRead moves the cursor and marks the row it left read, so triage
// happens by scrolling in either direction.
func (m *allModel) moveMarkingRead(delta int) tea.Cmd {
	before := m.feed.cursor
	leaving, ok := m.feed.selected()
	m.feed.moveCursor(delta)
	if !ok || m.feed.cursor == before || m.feed.isRead(leaving.key()) || m.feed.isKept(leaving.key()) {
		return nil
	}
	return m.markItem(leaving)
}

// markItem greys the row and queues its id for a debounced flush to that app's
// own read state (x's local store, or Inoreader/Folo's server).
func (m *allModel) markItem(it item) tea.Cmd {
	m.feed.markRead(it.key())
	m.pending[it.App] = append(m.pending[it.App], it.ID)
	if m.flushArmed {
		return nil
	}
	m.flushArmed = true
	return scheduleFlush()
}

// unqueue drops a not-yet-flushed mark so keeping an item unread cancels it. A
// mark already flushed to the app's store can't be undone (no mark-unread).
func (m *allModel) unqueue(it item) {
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
			cmds = append(cmds, flushMarks(m.root, app, ids))
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
		_ = runMarkRead(m.root, app, ids, 30*time.Second)
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
	body := forceHeight(m.bodyView(), m.bodyHeight())
	return strings.Join([]string{m.headerView(), "", body, m.statusView(), m.helpView()}, "\n")
}

func (m allModel) headerView() string {
	th := m.th
	left := th.header.Render("all")
	left += th.meta.Render(fmt.Sprintf("  %d unread · %s", len(m.feed.items), strings.Join(m.apps, " · ")))

	var meta []string
	if !m.lastRefresh.IsZero() {
		meta = append(meta, "updated "+m.lastRefresh.Format("15:04:05"))
	}
	right := ""
	if len(meta) > 0 {
		right = th.meta.Render(strings.Join(meta, " · "))
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
	case m.loading && len(m.feed.items) == 0:
		return center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case len(m.feed.items) == 0:
		return center(m.th.empty.Render("Inbox zero across every timeline."), m.width, h)
	default:
		return m.feed.View()
	}
}

func (m allModel) statusView() string {
	switch {
	case m.loading && len(m.feed.items) > 0:
		return m.spinner.View() + " " + m.th.help.Render(m.loadingNote)
	case m.statusErr && m.status != "":
		return m.th.statusErr.Render(m.status)
	case m.status != "":
		return m.th.statusInfo.Render(m.status)
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
	m.feed.setSize(m.width, m.bodyHeight())
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

// friendlyAllError trims a raw error to one readable line.
func friendlyAllError(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}
