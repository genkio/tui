// Package ui implements the terminal interface: a single scrolling list of
// unread articles (oldest first) that expand inline, plus mark-as-read.
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

	"github.com/genkio/inoreader-tui/internal/config"
	"github.com/genkio/inoreader-tui/internal/inoreader"
)

// Run starts the TUI and blocks until the user quits. A positive refresh makes
// the unread list re-fetch itself on that interval.
func Run(ctx context.Context, client *inoreader.Client, cfg config.Config, refresh time.Duration) error {
	_, err := tea.NewProgram(newModel(ctx, client, cfg, refresh)).Run()
	return err
}

type Model struct {
	ctx    context.Context
	client *inoreader.Client
	cfg    config.Config

	feed    feedModel
	spinner spinner.Model
	help    help.Model
	keys    keyMap

	width, height   int
	loading         bool
	loadingNote     string
	status          string
	statusErr       bool
	lastRefresh     time.Time
	themeAuto       bool
	refreshInterval time.Duration
}

func newModel(ctx context.Context, client *inoreader.Client, cfg config.Config, refresh time.Duration) Model {
	themeAuto := applyConfiguredTheme(cfg.Theme)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	return Model{
		ctx:             ctx,
		client:          client,
		cfg:             cfg,
		feed:            newFeed(),
		spinner:         sp,
		help:            help.New(),
		keys:            defaultKeys(),
		loading:         true,
		loadingNote:     "Loading articles…",
		themeAuto:       themeAuto,
		refreshInterval: refresh,
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
	cmds := []tea.Cmd{m.spinner.Tick, fetchUnreads(m.ctx, m.client, m.cfg.UnreadOnly, m.cfg.MaxArticles, true)}
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

	case articlesMsg:
		m.loading = false
		m.lastRefresh = time.Now()
		m.feed.setArticles(msg.articles, msg.reset)
		return m, nil

	case autoRefreshMsg:
		var cmds []tea.Cmd
		if m.refreshInterval > 0 {
			cmds = append(cmds, scheduleRefresh(m.refreshInterval))
		}
		if !m.loading { // don't pile onto an in-flight manual refresh
			cmds = append(cmds, fetchUnreads(m.ctx, m.client, m.cfg.UnreadOnly, m.cfg.MaxArticles, false))
		}
		return m, tea.Batch(cmds...)

	case markedMsg:
		// Success is silent; the greyed row is the feedback. On failure, undo the
		// optimistic grey-out and surface the error.
		if msg.err != nil {
			m.feed.unmarkRead(msg.id)
			m.setStatus(friendlyError(msg.err), true)
		}
		return m, nil

	case unmarkedMsg:
		// The K un-grey was optimistic; if the server refused, grey the row back
		// and drop the pin so the UI doesn't promise an unread that won't survive
		// a refresh.
		if msg.err != nil {
			m.feed.revertKeep(msg.id)
			m.setStatus(friendlyError(msg.err), true)
		}
		return m, nil

	case openedMsg:
		m.setStatus("Opened in browser.", false)
		return m, nil

	case carbonylDoneMsg:
		m.clearStatus()
		return m, nil

	case copiedMsg:
		m.setStatus("Copied URL to clipboard.", false)
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
	// 'q' and esc back out of an expanded article first; 'q' on the bare list
	// quits. ctrl+c always quits (handled via the Quit binding below).
	switch msg.String() {
	case "q":
		if m.feed.collapseCursor() {
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		m.feed.collapseCursor()
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layout()
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		m.clearStatus()
		m.loading = true
		m.loadingNote = "Refreshing…"
		return m, tea.Batch(m.spinner.Tick, fetchUnreads(m.ctx, m.client, m.cfg.UnreadOnly, m.cfg.MaxArticles, true))

	case key.Matches(msg, m.keys.Up):
		if m.feed.scrollExpanded(-1) {
			return m, nil
		}
		return m, m.moveMarkingRead(-1)

	case key.Matches(msg, m.keys.Down):
		// Inside an expanded article that overflows the viewport, j scrolls the
		// body one line at a time; only once its tail is on screen does the
		// cursor move on (collapsing the article).
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
		a, ok := m.feed.selectedArticle()
		if !opened || !ok || m.feed.isRead(a.ID) || m.feed.isKept(a.ID) {
			return m, nil
		}
		m.feed.markReadLocal(a.ID)
		return m, markRead(m.ctx, m.client, a.ID)

	case key.Matches(msg, m.keys.ToggleFeed):
		m.feed.toggleFeed()
		return m, nil

	case key.Matches(msg, m.keys.OpenURL):
		a, ok := m.feed.selectedArticle()
		if !ok {
			return m, nil
		}
		if a.URL == "" {
			m.setStatus("No URL for this item.", true)
			return m, nil
		}
		return m, openURL(a.URL)

	case key.Matches(msg, m.keys.Carbonyl), key.Matches(msg, m.keys.CarbonylGfx):
		a, ok := m.feed.selectedArticle()
		if !ok {
			return m, nil
		}
		if a.URL == "" {
			m.setStatus("No URL for this item.", true)
			return m, nil
		}
		return m, openCarbonyl(a.URL, key.Matches(msg, m.keys.CarbonylGfx))

	case key.Matches(msg, m.keys.CopyURL):
		a, ok := m.feed.selectedArticle()
		if !ok {
			return m, nil
		}
		if a.URL == "" {
			m.setStatus("No URL for this item.", true)
			return m, nil
		}
		return m, copyToClipboard(a.URL)

	case key.Matches(msg, m.keys.Keep):
		// Kept articles render normally (never grey); the status line is the
		// only toggle feedback.
		a, sel := m.feed.selectedArticle()
		if !sel {
			return m, nil
		}
		wasRead := m.feed.isRead(a.ID)
		if kept, _ := m.feed.toggleKeep(); !kept {
			m.setStatus("Keep removed.", false)
			return m, nil
		}
		m.setStatus("Kept unread; j won't mark it read. K again to unlock.", false)
		if wasRead {
			// The grey-out already reached the server; undo it there too or the
			// next refresh would drop the article (the pin alone is local).
			return m, markUnread(m.ctx, m.client, a.ID)
		}
		return m, nil

	case key.Matches(msg, m.keys.Mark):
		a, ok := m.feed.selectedArticle()
		if !ok || m.feed.isRead(a.ID) {
			return m, nil
		}
		if m.feed.isKept(a.ID) {
			m.setStatus("Kept unread; press K to unlock first.", true)
			return m, nil
		}
		m.clearStatus()
		m.feed.markReadLocal(a.ID)
		return m, markRead(m.ctx, m.client, a.ID)
	}

	var cmd tea.Cmd
	m.feed.vp, cmd = m.feed.vp.Update(msg)
	return m, cmd
}

// moveMarkingRead moves the cursor and marks the article it left read (greyed,
// not removed), so you triage in either direction without pressing r. Kept
// articles stay unread.
func (m *Model) moveMarkingRead(delta int) tea.Cmd {
	before := m.feed.cursor
	leaving, ok := m.feed.selectedArticle()
	m.feed.moveCursor(delta)
	if !ok || m.feed.cursor == before || m.feed.isRead(leaving.ID) || m.feed.isKept(leaving.ID) {
		return nil
	}
	m.feed.markReadLocal(leaving.ID)
	return markRead(m.ctx, m.client, leaving.ID)
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
	left := headerStyle.Render("inoreader-tui")
	label := "Articles"
	if m.cfg.UnreadOnly {
		label = "Unread"
	}
	left += headerMeta.Render(fmt.Sprintf("  %s · %d", label, len(m.feed.articles)))

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
	case m.loading && len(m.feed.articles) == 0:
		return center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case len(m.feed.articles) == 0:
		return center(emptyStyle.Render("Inbox zero. Nothing unread."), m.width, h)
	default:
		return m.feed.View()
	}
}

func (m Model) statusView() string {
	switch {
	case m.loading && len(m.feed.articles) > 0:
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
		return "Inoreader rejected the session: the cookie may be invalid or expired. Re-copy INOREADER_COOKIE from your browser."
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
