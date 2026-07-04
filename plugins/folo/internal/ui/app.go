// Package ui implements the terminal interface: a single scrolling list of
// pending (unread) Folo articles that expand inline, plus mark-as-read. The
// list itself is core.Feed, shared with the other apps and the merged view.
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
	"github.com/genkio/tui/plugins/folo/internal/config"
	"github.com/genkio/tui/plugins/folo/internal/folo"
)

// Run starts the TUI and blocks until the user quits. A positive refresh makes
// the list re-fetch itself on that interval.
func Run(ctx context.Context, client *folo.Client, cfg config.Config, refresh time.Duration) error {
	_, err := tea.NewProgram(newModel(ctx, client, cfg, refresh)).Run()
	return err
}

type Model struct {
	ctx    context.Context
	client *folo.Client
	cfg    config.Config

	feed    core.Feed
	th      core.Theme
	spinner spinner.Model
	help    help.Model
	keys    keyMap

	// Full bodies are fetched lazily on first expand; these track which ids are
	// done or in flight so a re-expand doesn't refetch (a failure clears both,
	// so it retries).
	loaded  map[string]bool
	loading map[string]bool

	width, height   int
	loadingNote     string
	fetching        bool
	status          string
	statusErr       bool
	lastRefresh     time.Time
	themeAuto       bool
	refreshInterval time.Duration
}

func newModel(ctx context.Context, client *folo.Client, cfg config.Config, refresh time.Duration) Model {
	th, themeAuto := initialTheme(cfg.Theme)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = th.Spinner

	return Model{
		ctx:             ctx,
		client:          client,
		cfg:             cfg,
		feed:            core.NewFeed(th, false), // single source: no per-app chip
		th:              th,
		spinner:         sp,
		help:            help.New(),
		keys:            defaultKeys(),
		loaded:          map[string]bool{},
		loading:         map[string]bool{},
		fetching:        true,
		loadingNote:     "Loading articles…",
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
		if m.fetching {
			return m, cmd
		}
		return m, nil

	case articlesMsg:
		m.fetching = false
		m.lastRefresh = time.Now()
		// A fresh fetch is the unread baseline, so the read overlay is cleared.
		m.feed.SetItems(msg.items, msg.reset)
		m.feed.ClearRead()
		if msg.reset { // manual refresh re-reads bodies from the server
			m.loaded = map[string]bool{}
			m.loading = map[string]bool{}
		}
		return m, nil

	case contentMsg:
		delete(m.loading, msg.id)
		if msg.err != nil {
			m.setStatus(friendlyError(msg.err), true) // summary stays; re-expand retries
		} else {
			m.loaded[msg.id] = true
			m.feed.SetBody(core.Key("folo", msg.id), msg.text)
		}
		return m, nil

	case autoRefreshMsg:
		var cmds []tea.Cmd
		if m.refreshInterval > 0 {
			cmds = append(cmds, scheduleRefresh(m.refreshInterval))
		}
		if !m.fetching { // don't pile onto an in-flight manual refresh
			cmds = append(cmds, fetchUnreads(m.ctx, m.client, m.cfg.UnreadOnly, m.cfg.MaxArticles, false))
		}
		return m, tea.Batch(cmds...)

	case markedMsg:
		// Success is silent; the greyed row is the feedback. On failure, undo the
		// optimistic grey-out and surface the error.
		if msg.err != nil {
			m.feed.Unmark(core.Key("folo", msg.id))
			m.setStatus(friendlyError(msg.err), true)
		}
		return m, nil

	case unmarkedMsg:
		// The K un-grey was optimistic; if the server refused, grey the row back
		// and drop the pin so the UI doesn't promise an unread that won't survive
		// a refresh.
		if msg.err != nil {
			m.feed.RevertKeep(core.Key("folo", msg.id))
			m.setStatus(friendlyError(msg.err), true)
		}
		return m, nil

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

	case errMsg:
		m.fetching = false
		m.setStatus(friendlyError(msg.err), true)
		return m, nil
	}

	return m, m.feed.Update(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// 'q' and esc back out of an expanded article first; 'q' on the bare list
	// quits. ctrl+c always quits (handled via the Quit binding below).
	switch msg.String() {
	case "q":
		if m.feed.CollapseCursor() {
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		m.feed.CollapseCursor()
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
		m.fetching = true
		m.loadingNote = "Refreshing…"
		return m, tea.Batch(m.spinner.Tick, fetchUnreads(m.ctx, m.client, m.cfg.UnreadOnly, m.cfg.MaxArticles, true))

	case key.Matches(msg, m.keys.Up):
		if m.feed.ScrollExpanded(-1) {
			return m, nil
		}
		return m, m.moveMarkingRead(-1)

	case key.Matches(msg, m.keys.Down):
		// Inside an expanded article that overflows the viewport, j scrolls the
		// body one line at a time; only once its tail is on screen does the
		// cursor move on (collapsing the article).
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
		if !opened || !ok {
			return m, nil
		}
		var cmds []tea.Cmd
		// The list response omits the body; fetch it the first time it's shown.
		if !m.loaded[it.ID] && !m.loading[it.ID] {
			m.loading[it.ID] = true
			cmds = append(cmds, fetchContent(m.ctx, m.client, it.ID))
		}
		// Expanding also marks read, unless the article is pinned (K).
		if !m.feed.IsRead(it.Key()) && !m.feed.IsKept(it.Key()) {
			m.feed.MarkRead(it.Key())
			cmds = append(cmds, markRead(m.ctx, m.client, it.ID))
		}
		return m, tea.Batch(cmds...)

	case key.Matches(msg, m.keys.ToggleFeed):
		m.feed.ToggleSource()
		return m, nil

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

	case key.Matches(msg, m.keys.Keep):
		// Kept articles render normally (never grey); the status line is the
		// only toggle feedback.
		it, sel := m.feed.Selected()
		if !sel {
			return m, nil
		}
		wasRead := m.feed.IsRead(it.Key())
		if kept, _ := m.feed.ToggleKeep(); !kept {
			m.setStatus("Keep removed.", false)
			return m, nil
		}
		m.setStatus("Kept unread; j won't mark it read. K again to unlock.", false)
		if wasRead {
			// The grey-out already reached the server; undo it there too or the
			// next refresh would drop the article (the pin alone is local).
			return m, markUnread(m.ctx, m.client, it.ID)
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
		m.feed.MarkRead(it.Key())
		return m, markRead(m.ctx, m.client, it.ID)
	}

	return m, m.feed.Update(msg)
}

// selectedURL is the cursored item's URL, with a status set when it has none.
func (m *Model) selectedURL() (string, bool) {
	it, ok := m.feed.Selected()
	if !ok {
		return "", false
	}
	if it.URL == "" {
		m.setStatus("No URL for this item.", true)
		return "", false
	}
	return it.URL, true
}

// moveMarkingRead moves the cursor and marks the article it left read (greyed,
// not removed), so you triage in either direction without pressing r. Kept
// articles stay unread.
func (m *Model) moveMarkingRead(delta int) tea.Cmd {
	before := m.feed.Cursor()
	leaving, ok := m.feed.Selected()
	m.feed.MoveCursor(delta)
	if !ok || m.feed.Cursor() == before || m.feed.IsRead(leaving.Key()) || m.feed.IsKept(leaving.Key()) {
		return nil
	}
	m.feed.MarkRead(leaving.Key())
	return markRead(m.ctx, m.client, leaving.ID)
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
	left := m.th.Header.Render("folo-tui")
	label := "Articles"
	if m.cfg.UnreadOnly {
		label = "Pending"
	}
	left += m.th.Meta.Render(fmt.Sprintf("  %s · %d", label, m.feed.Len()))

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
	case m.fetching && m.feed.Len() == 0:
		return core.Center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case m.feed.Len() == 0:
		return core.Center(m.th.Empty.Render("All caught up. Nothing pending."), m.width, h)
	default:
		return m.feed.View()
	}
}

func (m Model) statusView() string {
	switch {
	case m.fetching && m.feed.Len() > 0:
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
		return "Folo rejected the session: the cookie may be invalid or expired. Run make auth (or re-copy FOLO_COOKIE)."
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}
