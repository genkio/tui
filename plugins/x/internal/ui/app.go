// Package ui implements the terminal interface: a scrolling list of posts from
// one home timeline (For You or Following) that expand inline, with tab to
// switch feeds and o to open a post in the browser.
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

	"github.com/genkio/tui/plugins/x/internal/config"
	"github.com/genkio/tui/plugins/x/internal/readstore"
	"github.com/genkio/tui/plugins/x/internal/x"
)

// saveDebounce coalesces a burst of read marks (e.g. holding j) into one disk
// write instead of one per keystroke.
const saveDebounce = 1500 * time.Millisecond

// Run starts the TUI and blocks until the user quits. A positive refresh makes
// the current timeline re-fetch itself on that interval.
func Run(ctx context.Context, client *x.Client, cfg config.Config, refresh time.Duration) error {
	_, err := tea.NewProgram(newModel(ctx, client, cfg, refresh)).Run()
	return err
}

type Model struct {
	ctx    context.Context
	client *x.Client
	cfg    config.Config

	tab   x.Tab
	cache map[x.Tab][]x.Tweet // last fetched posts per tab, so switching back is instant

	read *readstore.Store // persistent set of read tweet ids; shared with feed

	feed    feedModel
	spinner spinner.Model
	help    help.Model
	keys    keyMap

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

func newModel(ctx context.Context, client *x.Client, cfg config.Config, refresh time.Duration) Model {
	themeAuto := applyConfiguredTheme(cfg.Theme)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	tab := tabFromString(cfg.DefaultTab)
	read := readstore.Load("")
	return Model{
		ctx:             ctx,
		client:          client,
		cfg:             cfg,
		tab:             tab,
		cache:           map[x.Tab][]x.Tweet{},
		read:            read,
		feed:            newFeed(read, cfg.UnreadOnly),
		spinner:         sp,
		help:            help.New(),
		keys:            defaultKeys(),
		loading:         true,
		loadingNote:     "Loading " + tab.String() + "…",
		themeAuto:       themeAuto,
		refreshInterval: refresh,
	}
}

func tabFromString(s string) x.Tab {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "foryou", "for you", "for-you":
		return x.ForYou
	default:
		return x.Following
	}
}

// applyConfiguredTheme honors an explicit "light"/"dark" choice and reports
// whether to instead auto-detect from the terminal background.
func applyConfiguredTheme(theme string) bool {
	switch theme {
	case "light":
		setTheme(false)
	case "dark":
		setTheme(true)
	default:
		return true
	}
	return false
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, fetchTimeline(m.ctx, m.client, m.tab, m.cfg.MaxTweets, true)}
	if m.themeAuto {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	if m.refreshInterval > 0 {
		cmds = append(cmds, scheduleRefresh(m.refreshInterval))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.BackgroundColorMsg:
		if m.themeAuto {
			setTheme(msg.IsDark())
			m.spinner.Style = spinnerStyle
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

	case timelineMsg:
		m.loading = false
		m.lastRefresh = time.Now()
		m.cache[msg.tab] = msg.tweets
		if msg.tab == m.tab { // a stale fetch for the other tab just updates its cache
			m.feed.setTweets(msg.tweets, msg.reset)
		}
		return m, nil

	case autoRefreshMsg:
		var cmds []tea.Cmd
		if m.refreshInterval > 0 {
			cmds = append(cmds, scheduleRefresh(m.refreshInterval))
		}
		if !m.loading { // don't pile onto an in-flight manual refresh
			cmds = append(cmds, fetchTimeline(m.ctx, m.client, m.tab, m.cfg.MaxTweets, false))
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

	var cmd tea.Cmd
	m.feed.vp, cmd = m.feed.vp.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// 'q' and esc back out of an expanded post first; 'q' on the bare list quits.
	switch msg.String() {
	case "q":
		if m.feed.collapseCursor() {
			return m, nil
		}
		return m.quit()
	case "esc":
		m.feed.collapseCursor()
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
		delete(m.cache, m.tab)
		m.loading = true
		m.loadingNote = "Refreshing…"
		return m, tea.Batch(m.spinner.Tick, fetchTimeline(m.ctx, m.client, m.tab, m.cfg.MaxTweets, true))

	case key.Matches(msg, m.keys.SwitchTab):
		// Arrows map to the tab's screen position: left is For You, right is Following.
		switch msg.String() {
		case "left", "h":
			return m.switchTab(x.ForYou)
		default:
			return m.switchTab(x.Following)
		}

	case key.Matches(msg, m.keys.ToggleHandle):
		m.feed.toggleHandle()
		return m, nil

	case key.Matches(msg, m.keys.Up):
		if m.feed.scrollExpanded(-1) {
			return m, nil
		}
		return m, m.moveMarkingRead(-1)

	case key.Matches(msg, m.keys.Down):
		// Inside an expanded post that overflows the viewport, j scrolls the body
		// one line at a time; only once its tail is on screen does the cursor move
		// on (collapsing the post).
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
		t, ok := m.feed.selectedTweet()
		if !opened || !ok || m.feed.isRead(t.ID) || m.feed.isKept(t.ID) {
			return m, nil
		}
		m.feed.markReadLocal(t.ID)
		return m, m.scheduleSave()

	case key.Matches(msg, m.keys.OpenURL):
		t, ok := m.feed.selectedTweet()
		if !ok {
			return m, nil
		}
		if t.URL == "" {
			m.setStatus("No URL for this post.", true)
			return m, nil
		}
		return m, openURL(t.URL)

	case key.Matches(msg, m.keys.Carbonyl), key.Matches(msg, m.keys.CarbonylGfx):
		t, ok := m.feed.selectedTweet()
		if !ok {
			return m, nil
		}
		if t.URL == "" {
			m.setStatus("No URL for this post.", true)
			return m, nil
		}
		return m, openCarbonyl(t.URL, key.Matches(msg, m.keys.CarbonylGfx))

	case key.Matches(msg, m.keys.CopyURL):
		t, ok := m.feed.selectedTweet()
		if !ok {
			return m, nil
		}
		if t.URL == "" {
			m.setStatus("No URL for this post.", true)
			return m, nil
		}
		return m, copyToClipboard(t.URL)

	case key.Matches(msg, m.keys.Mark):
		t, ok := m.feed.selectedTweet()
		if !ok || m.feed.isRead(t.ID) {
			return m, nil
		}
		if m.feed.isKept(t.ID) {
			m.setStatus("Kept unread; press K to unlock first.", true)
			return m, nil
		}
		m.clearStatus()
		m.feed.markReadLocal(t.ID)
		return m, m.scheduleSave()

	case key.Matches(msg, m.keys.Keep):
		// Kept posts render normally (never grey); the status line is the only
		// toggle feedback. Un-keeping leaves the post unread, not read.
		if _, sel := m.feed.selectedTweet(); !sel {
			return m, nil
		}
		if kept, _ := m.feed.toggleKeep(); kept {
			m.setStatus("Kept unread; scrolling won't mark it read. K again to unlock.", false)
		} else {
			m.setStatus("Keep removed.", false)
		}
		return m, m.scheduleSave() // keep un-marks read; persist that

	case key.Matches(msg, m.keys.UnreadOnly):
		if m.feed.toggleUnreadOnly() {
			m.setStatus("Showing unread only.", false)
		} else {
			m.setStatus("Showing all posts (read ones greyed).", false)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.feed.vp, cmd = m.feed.vp.Update(msg)
	return m, cmd
}

// quit persists any pending read marks before exiting so marks made inside the
// debounce window aren't lost. The save is synchronous and cheap.
func (m Model) quit() (tea.Model, tea.Cmd) {
	_ = m.read.Save()
	return m, tea.Quit
}

// moveMarkingRead moves the cursor and marks the post it left read (greyed, not
// removed mid-scroll), so you triage in either direction. Kept and already-read
// posts are left alone. Returns the save-scheduling command, if any.
func (m *Model) moveMarkingRead(delta int) tea.Cmd {
	before := m.feed.cursor
	leaving, ok := m.feed.selectedTweet()
	m.feed.moveCursor(delta)
	if !ok || m.feed.cursor == before || m.feed.isRead(leaving.ID) || m.feed.isKept(leaving.ID) {
		return nil
	}
	m.feed.markReadLocal(leaving.ID)
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

// switchTab moves to the given tab, showing its cached posts instantly when we
// have them, else fetching. A no-op when already there.
func (m Model) switchTab(to x.Tab) (tea.Model, tea.Cmd) {
	if to == m.tab {
		return m, nil
	}
	m.tab = to
	m.clearStatus()
	if cached, ok := m.cache[to]; ok {
		m.feed.setTweets(cached, true)
		return m, nil
	}
	m.loading = true
	m.loadingNote = "Loading " + to.String() + "…"
	return m, tea.Batch(m.spinner.Tick, fetchTimeline(m.ctx, m.client, to, m.cfg.MaxTweets, true))
}

func (m Model) View() tea.View {
	content := "starting…"
	if m.width > 0 {
		body := forceHeight(m.bodyView(), m.bodyHeight())
		parts := []string{m.headerView(), "", body, m.statusView(), m.helpView()}
		content = strings.Join(parts, "\n")
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) headerView() string {
	left := headerStyle.Render("x-tui")

	foryou, following := "For You", "Following"
	if m.tab == x.ForYou {
		foryou, following = titleSelStyle.Render(foryou), headerMeta.Render(following)
	} else {
		foryou, following = headerMeta.Render(foryou), titleSelStyle.Render(following)
	}
	left += "  " + foryou + headerMeta.Render(" · ") + following
	mode := "all"
	if m.feed.unreadOnly {
		mode = "unread"
	}
	left += headerMeta.Render(fmt.Sprintf("  %s · %d", mode, len(m.feed.tweets)))

	var meta []string
	if m.refreshInterval > 0 {
		meta = append(meta, "auto "+m.refreshInterval.String())
	}
	if !m.lastRefresh.IsZero() {
		meta = append(meta, "updated "+m.lastRefresh.Format("15:04:05"))
	}
	right := ""
	if len(meta) > 0 {
		right = headerMeta.Render(strings.Join(meta, " · "))
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
	case m.loading && len(m.feed.tweets) == 0:
		return center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case len(m.feed.tweets) == 0:
		return center(emptyStyle.Render("Timeline empty."), m.width, h)
	default:
		return m.feed.View()
	}
}

func (m Model) statusView() string {
	switch {
	case m.loading && len(m.feed.tweets) > 0:
		return m.spinner.View() + " " + helpStyle.Render(m.loadingNote)
	case m.statusErr && m.status != "":
		return statusErrStyle.Render(m.status)
	case m.status != "":
		return statusInfoStyle.Render(m.status)
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
	m.feed.setSize(m.width, m.bodyHeight())
}

func (m Model) bodyHeight() int {
	helpH := 1
	if m.help.ShowAll {
		helpH = 4
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
	if strings.Contains(low, "rejected the session") || strings.Contains(low, "401") || strings.Contains(low, "403") {
		return "x.com rejected the session: the cookie may be expired. Re-run make auth."
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

func center(s string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

// forceHeight pads or trims s to exactly h lines so the footer stays pinned.
func forceHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
