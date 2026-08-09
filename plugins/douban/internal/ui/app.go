// Package ui implements the terminal interface for douban-tui: a scrolling
// list of statuses from the following timeline (友邻广播) that expand inline,
// with o to read one in carbonyl. The list itself is core.Feed, shared with the
// other apps and the merged view; douban's local read store and unread-only
// filter live here in the model.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/douban/internal/config"
	"github.com/genkio/tui/plugins/douban/internal/douban"
	"github.com/genkio/tui/plugins/douban/internal/readstore"
)

// saveDebounce coalesces a burst of read marks (e.g. holding j) into one disk
// write instead of one per keystroke.
const saveDebounce = 1500 * time.Millisecond

// Run starts the TUI and blocks until the user quits. A positive refresh makes
// the timeline re-fetch itself on that interval.
func Run(ctx context.Context, client *douban.Client, cfg config.Config, refresh time.Duration) error {
	_, err := tea.NewProgram(newModel(ctx, client, cfg, refresh)).Run()
	return err
}

type Model struct {
	ctx    context.Context
	client *douban.Client
	cfg    config.Config

	cache []douban.Status // last fetched statuses, so a refresh can keep the read overlay
	read  *readstore.Store

	feed       core.Feed
	th         core.Theme
	unreadOnly bool
	spinner    spinner.Model
	help       help.Model
	keys       keyMap

	width, height   int
	loading         bool
	loadingNote     string
	status          string
	statusErr       bool
	savePending     bool
	lastRefresh     time.Time
	themeAuto       bool
	refreshInterval time.Duration
}

func newModel(ctx context.Context, client *douban.Client, cfg config.Config, refresh time.Duration) Model {
	th, themeAuto := initialTheme(cfg.Theme)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = th.Spinner

	return Model{
		ctx:             ctx,
		client:          client,
		cfg:             cfg,
		read:            readstore.Load(""),
		feed:            core.NewFeed(th, false), // single source: no per-app chip
		th:              th,
		unreadOnly:      cfg.UnreadOnly,
		spinner:         sp,
		help:            help.New(),
		keys:            defaultKeys(),
		loading:         true,
		loadingNote:     "Loading following timeline…",
		themeAuto:       themeAuto,
		refreshInterval: refresh,
	}
}

// initialTheme honors an explicit "light"/"dark" choice and reports whether to
// instead auto-detect from the terminal background.
func initialTheme(pref string) (core.Theme, bool) {
	switch pref {
	case "light":
		return core.NewTheme(false), false
	case "dark":
		return core.NewTheme(true), false
	default:
		return core.NewTheme(true), true // dark until the terminal answers
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, fetchHome(m.ctx, m.client, m.cfg, true)}
	if m.themeAuto {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	if m.refreshInterval > 0 {
		cmds = append(cmds, scheduleRefresh(m.refreshInterval))
	}
	return tea.Batch(cmds...)
}

// showStatuses maps raw statuses into the feed, applying the unread-only filter
// and reflecting the read store in the grey overlay. Marking read mid-session
// only greys in place (see markLocal); the filter runs here, on the next fetch
// or a view toggle, so the list never shifts under the cursor as you scroll.
func (m *Model) showStatuses(raw []douban.Status, resetCursor bool) {
	items := make([]core.Item, 0, len(raw))
	for _, st := range raw {
		if m.unreadOnly && m.read.Has(st.ID) && !m.feed.IsKept(core.Key("douban", st.ID)) {
			continue
		}
		items = append(items, douban.ToItem(st))
	}
	m.feed.SetItems(items, resetCursor)
	m.feed.ClearRead()
	for _, it := range items {
		if m.read.Has(it.ID) && !m.feed.IsKept(it.Key()) {
			m.feed.MarkRead(it.Key())
		}
	}
}

func (m *Model) markLocal(id string) {
	m.read.Mark(id)
	m.feed.MarkRead(core.Key("douban", id))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.BackgroundColorMsg:
		if m.themeAuto {
			m.th = core.NewTheme(msg.IsDark())
			m.spinner.Style = m.th.Spinner
			m.feed.SetTheme(m.th)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.loading {
			return m, cmd
		}
		return m, nil

	case homeMsg:
		m.loading = false
		m.lastRefresh = time.Now()
		m.cache = msg.statuses
		m.showStatuses(msg.statuses, msg.reset)
		return m, nil

	case autoRefreshMsg:
		var cmds []tea.Cmd
		if m.refreshInterval > 0 {
			cmds = append(cmds, scheduleRefresh(m.refreshInterval))
		}
		if !m.loading { // don't pile onto an in-flight manual refresh
			cmds = append(cmds, fetchHome(m.ctx, m.client, m.cfg, false))
		}
		return m, tea.Batch(cmds...)

	case openedMsg:
		m.setStatus("Opened in browser.", false)
		return m, nil

	case carbonylDoneMsg:
		m.clearStatus()
		return m, nil

	case carbonylBrowseMsg:
		m.clearStatus()
		return m, openURL(msg.url)

	case copiedMsg:
		m.setStatus("Copied URL to clipboard.", false)
		return m, nil

	case flushReadMsg:
		// The debounce window elapsed; persist the accumulated marks in the
		// background and let the next mark schedule a fresh window.
		m.savePending = false
		return m, saveRead(m.read)

	case readSavedMsg:
		if msg.err != nil {
			m.setStatus(friendlyError(msg.err), true)
		}
		return m, nil

	case errMsg:
		m.loading = false
		m.setStatus(friendlyError(msg.err), true)
		return m, nil
	}

	return m, m.feed.Update(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// 'q' and esc back out of an expanded status first; 'q' on the bare list quits.
	switch msg.String() {
	case "q":
		if m.feed.CollapseCursor() {
			return m, nil
		}
		return m.quit()
	case "esc":
		m.feed.CollapseCursor()
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m.quit()

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layout()
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		m.clearStatus()
		m.cache = nil
		m.loading = true
		m.loadingNote = "Refreshing…"
		return m, tea.Batch(m.spinner.Tick, fetchHome(m.ctx, m.client, m.cfg, true))

	case key.Matches(msg, m.keys.ShowSource):
		m.feed.ToggleSource()
		return m, nil

	case key.Matches(msg, m.keys.Up):
		if m.feed.ScrollExpanded(-1) {
			return m, nil
		}
		return m, m.moveMarkingRead(-1)

	case key.Matches(msg, m.keys.Down):
		// Inside an expanded status that overflows the viewport, j scrolls the
		// body one line at a time; only once its tail is on screen does the
		// cursor move on (collapsing the status).
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
		m.markLocal(it.ID)
		return m, m.scheduleSave()

	case key.Matches(msg, m.keys.OpenURL):
		if u, ok := m.selectedURL(); ok {
			return m, openURL(u)
		}
		return m, nil

	case key.Matches(msg, m.keys.Carbonyl), key.Matches(msg, m.keys.CarbonylGfx):
		if u, ok := m.selectedURL(); ok {
			return m, openCarbonyl(u, key.Matches(msg, m.keys.CarbonylGfx))
		}
		return m, nil

	case key.Matches(msg, m.keys.CopyURL):
		if u, ok := m.selectedURL(); ok {
			return m, copyToClipboard(u)
		}
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
		m.markLocal(it.ID)
		return m, m.scheduleSave()

	case key.Matches(msg, m.keys.Keep):
		// Kept statuses render normally (never grey); the status line is the
		// only toggle feedback. Un-keeping leaves the status unread, not read.
		it, sel := m.feed.Selected()
		if !sel {
			return m, nil
		}
		if kept, _ := m.feed.ToggleKeep(); kept {
			m.read.Unmark(it.ID) // pinning restores unread in the store too
			m.setStatus("Kept unread; scrolling won't mark it read. K again to unlock.", false)
		} else {
			m.setStatus("Keep removed.", false)
		}
		return m, m.scheduleSave() // keep un-marks read; persist that

	case key.Matches(msg, m.keys.UnreadOnly):
		m.unreadOnly = !m.unreadOnly
		m.showStatuses(m.cache, false)
		if m.unreadOnly {
			m.setStatus("Showing unread only.", false)
		} else {
			m.setStatus("Showing all statuses (read ones greyed).", false)
		}
		return m, nil
	}

	return m, m.feed.Update(msg)
}

// selectedURL is the cursored status's URL, with a status line set when it has
// none.
func (m *Model) selectedURL() (string, bool) {
	it, ok := m.feed.Selected()
	if !ok {
		return "", false
	}
	if it.URL == "" {
		m.setStatus("No URL for this status.", true)
		return "", false
	}
	return it.URL, true
}

// quit persists any pending read marks before exiting so marks made inside the
// debounce window aren't lost. The save is synchronous and cheap.
func (m Model) quit() (tea.Model, tea.Cmd) {
	_ = m.read.Save()
	return m, tea.Quit
}

// moveMarkingRead moves the cursor and marks the status it left read (greyed,
// not removed mid-scroll), so you triage in either direction. Kept and
// already-read statuses are left alone. Returns the save-scheduling command,
// if any.
func (m *Model) moveMarkingRead(delta int) tea.Cmd {
	before := m.feed.Cursor()
	leaving, ok := m.feed.Selected()
	m.feed.MoveCursor(delta)
	if !ok || m.feed.Cursor() == before || m.feed.IsRead(leaving.Key()) || m.feed.IsKept(leaving.Key()) {
		return nil
	}
	m.markLocal(leaving.ID)
	return m.scheduleSave()
}

// scheduleSave arms a single debounced flush of the read store. Rapid marks
// (holding j) collapse into one write; the timer re-arms only after it fires.
func (m *Model) scheduleSave() tea.Cmd {
	if m.savePending {
		return nil
	}
	m.savePending = true
	return tea.Tick(saveDebounce, func(time.Time) tea.Msg { return flushReadMsg{} })
}

func (m Model) View() tea.View {
	content := "starting…"
	if m.width > 0 {
		body := core.ForceHeight(m.bodyView(), m.bodyHeight())
		parts := []string{m.headerView(), "", body, m.statusView(), m.helpView()}
		content = strings.Join(parts, "\n")
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) headerView() string {
	left := m.th.Header.Render("douban-tui")
	mode := "all"
	if m.unreadOnly {
		mode = "unread"
	}
	left += m.th.Meta.Render(fmt.Sprintf("  %s · %d", mode, m.feed.Len()))

	var meta []string
	if m.refreshInterval > 0 {
		meta = append(meta, "auto "+m.refreshInterval.String())
	}
	if !m.lastRefresh.IsZero() {
		meta = append(meta, "updated "+m.lastRefresh.Format("15:04:05"))
	}
	right := ""
	if len(meta) > 0 {
		right = m.th.Meta.Render(strings.Join(meta, " · "))
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) bodyView() string {
	h := m.bodyHeight()
	switch {
	case m.loading && m.feed.Len() == 0:
		return core.Center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case m.feed.Len() == 0:
		return core.Center(m.th.Empty.Render("Following timeline empty."), m.width, h)
	default:
		return m.feed.View()
	}
}

func (m Model) statusView() string {
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

func (m Model) helpView() string {
	if m.help.ShowAll {
		return m.help.FullHelpView(m.keys.fullHelp())
	}
	return m.help.ShortHelpView(m.keys.shortHelp())
}

func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	m.help.SetWidth(m.width)
	m.feed.SetSize(m.width, m.bodyHeight())
}

func (m Model) bodyHeight() int {
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

func (m *Model) setStatus(s string, isErr bool) {
	m.status = s
	m.statusErr = isErr
}

func (m *Model) clearStatus() {
	m.status = ""
	m.statusErr = false
}

// friendlyError turns a raw error into a single readable line, with a special
// case for auth failures the user can act on.
func friendlyError(err error) string {
	s := err.Error()
	low := strings.ToLower(s)
	if strings.Contains(low, "rejected the session") || strings.Contains(low, "session is stale") {
		return "douban rejected the session: the cookie may be expired. Re-run `tui douban --auth`."
	}
	if strings.Contains(low, "decoding douban timeline") {
		return s // the parse error already says the session may be invalid
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}
