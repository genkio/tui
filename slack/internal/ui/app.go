// Package ui implements the terminal interface: an unread list, a message and
// thread detail view, and mark-as-read, all driven by the MCP client.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genkio/slack-tui/internal/config"
	"github.com/genkio/slack-tui/internal/mcp"
	"github.com/genkio/slack-tui/internal/slack"
)

type screen int

const (
	screenList screen = iota
	screenDetail
)

// historyLimit is how many recent messages we fetch when opening a
// conversation. The server treats a bare number as a message count.
const historyLimit = "50"

const markDisabledMsg = "Mark-as-read is off. Set SLACK_MCP_MARK_TOOL=true and restart."

const reactDisabledMsg = "Reactions are off. Set SLACK_MCP_REACTION_TOOL=true and restart."

const noLinkMsg = "Set SLACK_TUI_SLACK_DOMAIN to your workspace (e.g. acme) to open messages in the browser."

// Run starts the TUI and blocks until the user quits. Full-screen (alt screen)
// mode is requested per-frame via the returned View. A positive refresh makes
// the unread list re-fetch itself on that interval.
func Run(ctx context.Context, client *mcp.Client, cfg config.Config, refresh time.Duration) error {
	_, err := tea.NewProgram(newModel(ctx, client, cfg, refresh)).Run()
	return err
}

type Model struct {
	ctx    context.Context
	client *mcp.Client
	cfg    config.Config

	screen  screen
	list    list.Model
	detail  detailModel
	picker  pickerModel
	spinner spinner.Model
	help    help.Model
	keys    keyMap

	width, height   int
	loading         bool
	loadingNote     string
	status          string
	statusErr       bool
	lastRefresh     time.Time
	markEnabled     bool
	reactEnabled    bool
	pickerActive    bool
	emojiLoaded     bool
	themeAuto       bool
	refreshInterval time.Duration
}

func newModel(ctx context.Context, client *mcp.Client, cfg config.Config, refresh time.Duration) Model {
	themeAuto := applyConfiguredTheme(cfg.Theme)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	l := list.New(nil, convDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(true)
	l.SetStatusBarItemName("conversation", "conversations")
	l.Styles.Title = listTitleStyle

	return Model{
		ctx:             ctx,
		client:          client,
		cfg:             cfg,
		screen:          screenList,
		list:            l,
		detail:          newDetail(),
		picker:          newPicker(),
		spinner:         sp,
		help:            help.New(),
		keys:            defaultKeys(),
		loading:         true,
		loadingNote:     "Loading unreads…",
		markEnabled:     client.HasTool(mcp.ToolMark) && config.MarkToolEnabled(),
		reactEnabled:    client.HasTool(mcp.ToolReactionAdd) && config.ReactionToolEnabled(),
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
	cmds := []tea.Cmd{m.spinner.Tick, fetchUnreads(m.ctx, m.client, m.cfg.Unreads)}
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
		if m.loading || m.detail.loading {
			return m, cmd
		}
		return m, nil

	case unreadsMsg:
		m.loading = false
		m.lastRefresh = time.Now()
		m.setConversations(msg.convs)
		return m, nil

	case autoRefreshMsg:
		var cmds []tea.Cmd
		if m.refreshInterval > 0 {
			cmds = append(cmds, scheduleRefresh(m.refreshInterval))
		}
		if !m.loading { // don't pile onto an in-flight manual refresh
			cmds = append(cmds, fetchUnreads(m.ctx, m.client, m.cfg.Unreads))
		}
		return m, tea.Batch(cmds...)

	case historyMsg:
		if msg.convID == m.detail.conv.ID {
			m.detail.setMessages(msg.msgs)
		}
		return m, nil

	case repliesMsg:
		m.detail.setReplies(msg.threadTS, msg.msgs)
		return m, nil

	case markedMsg:
		m.setStatus(fmt.Sprintf("Marked %q as read", msg.label), false)
		m.screen = screenList
		m.loading = true
		m.loadingNote = "Refreshing…"
		return m, tea.Batch(m.spinner.Tick, fetchUnreads(m.ctx, m.client, m.cfg.Unreads))

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

	case emojiListMsg:
		m.emojiLoaded = true
		m.picker.setNames(msg.names)
		return m, nil

	case emojiErrMsg:
		// Keep the picker open: the user can still type an exact name to react.
		m.picker.setErr(msg.err)
		return m, nil

	case reactedMsg:
		m.pickerActive = false
		m.picker.close()
		verb := "added"
		if msg.removed {
			verb = "removed"
		}
		m.setStatus(fmt.Sprintf("Reaction :%s: %s", msg.emoji, verb), false)
		m.detail.keepCursorTS = m.picker.ts // don't jump the cursor on the refresh
		return m, tea.Batch(m.spinner.Tick, fetchHistory(m.ctx, m.client, m.detail.conv.ID, historyLimit))

	case errMsg:
		m.loading = false
		m.detail.loading = false
		m.setStatus(friendlyError(msg.err), true)
		return m, nil
	}

	return m.forwardToActive(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// While the list is capturing filter text, let it consume every key.
	if m.screen == screenList && m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	// While the emoji picker is open it captures keys (typing must reach the
	// search field), so it is handled before the global bindings.
	if m.pickerActive {
		return m.handlePickerKey(msg)
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
		return m, tea.Batch(m.spinner.Tick, fetchUnreads(m.ctx, m.client, m.cfg.Unreads))
	}

	if m.screen == screenDetail {
		return m.handleDetailKey(msg)
	}
	return m.handleListKey(msg)
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Open):
		it, ok := m.list.SelectedItem().(convItem)
		if !ok {
			return m, nil
		}
		m.screen = screenDetail
		m.clearStatus()
		m.detail.open(it.conv)
		m.detail.setSize(m.bodyWidth(), m.bodyHeight())
		return m, tea.Batch(m.spinner.Tick, fetchHistory(m.ctx, m.client, it.conv.ID, historyLimit))

	case key.Matches(msg, m.keys.Mark):
		it, ok := m.list.SelectedItem().(convItem)
		if !ok {
			return m, nil
		}
		return m.mark(it.conv.ID, it.conv.Latest, it.conv.Name)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) handleDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = screenList
		m.clearStatus()
		return m, nil

	case key.Matches(msg, m.keys.Up):
		m.detail.moveCursor(-1)
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.detail.moveCursor(1)
		return m, nil

	case key.Matches(msg, m.keys.Top):
		m.detail.toTop()
		return m, nil

	case key.Matches(msg, m.keys.Bottom):
		m.detail.toBottom()
		return m, nil

	case key.Matches(msg, m.keys.ToggleBody):
		m.detail.toggleBody()
		return m, nil

	case key.Matches(msg, m.keys.Open):
		ts := m.detail.toggleThread()
		if ts == "" {
			return m, nil
		}
		return m, tea.Batch(m.spinner.Tick, fetchReplies(m.ctx, m.client, m.detail.conv.ID, ts))

	case key.Matches(msg, m.keys.OpenURL):
		url := m.detail.selectedMessageURL(m.cfg.SlackBaseURL())
		if url == "" {
			m.setStatus(noLinkMsg, true)
			return m, nil
		}
		return m, openURL(url)

	case key.Matches(msg, m.keys.Carbonyl), key.Matches(msg, m.keys.CarbonylGfx):
		url := m.detail.selectedMessageURL(m.cfg.SlackBaseURL())
		if url == "" {
			m.setStatus(noLinkMsg, true)
			return m, nil
		}
		return m, openCarbonyl(url, key.Matches(msg, m.keys.CarbonylGfx))

	case key.Matches(msg, m.keys.CopyURL):
		url := m.detail.selectedMessageURL(m.cfg.SlackBaseURL())
		if url == "" {
			m.setStatus(noLinkMsg, true)
			return m, nil
		}
		return m, copyToClipboard(url)

	case key.Matches(msg, m.keys.React):
		return m.openPicker()

	case key.Matches(msg, m.keys.Mark):
		c := m.detail.conv
		return m.mark(c.ID, m.detail.latestTS(), c.Name)
	}
	return m, nil
}

// openPicker starts the emoji picker for the cursored message, fetching the
// custom-emoji list on first use.
func (m Model) openPicker() (tea.Model, tea.Cmd) {
	if !m.reactEnabled {
		m.setStatus(reactDisabledMsg, true)
		return m, nil
	}
	msg, ok := m.detail.selectedMessage()
	if !ok {
		return m, nil
	}
	m.clearStatus()
	m.pickerActive = true
	cmds := []tea.Cmd{m.picker.open(m.detail.conv.ID, msg.ID)}
	if !m.emojiLoaded {
		m.picker.loading = true
		cmds = append(cmds, fetchEmojis(m.ctx))
	}
	return m, tea.Batch(cmds...)
}

// handlePickerKey routes keys while the emoji picker is open. Only ctrl+c, esc,
// enter, and the arrow/ctrl movement keys are intercepted; everything else
// (letters, backspace) edits the search field.
func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.pickerActive = false
		m.picker.close()
		m.clearStatus()
		return m, nil
	case "enter":
		name := m.picker.selected()
		if name == "" {
			return m, nil
		}
		return m, react(m.ctx, m.client, m.picker.channelID, m.picker.ts, name)
	case "up", "ctrl+p":
		m.picker.moveCursor(-1)
		return m, nil
	case "down", "ctrl+n":
		m.picker.moveCursor(1)
		return m, nil
	}
	return m, m.picker.update(msg)
}

// mark issues a mark-as-read, or explains why it can't.
func (m Model) mark(convID, ts, label string) (tea.Model, tea.Cmd) {
	if !m.markEnabled {
		m.setStatus(markDisabledMsg, true)
		return m, nil
	}
	return m, markRead(m.ctx, m.client, convID, ts, label)
}

func (m Model) forwardToActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.screen == screenList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.detail.vp, cmd = m.detail.vp.Update(msg)
	return m, cmd
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
	left := headerStyle.Render("slack-tui")

	var ctx string
	switch m.screen {
	case screenDetail:
		ctx = m.detail.conv.Type.Label() + " · " + m.detail.conv.Name
	default:
		ctx = fmt.Sprintf("Unread · %d", len(m.list.Items()))
	}
	left += headerMeta.Render("  " + ctx)

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
	if m.pickerActive {
		return m.picker.View(m.width, h)
	}
	if m.screen == screenDetail {
		if m.detail.loading {
			return center(m.spinner.View()+" Loading messages…", m.width, h)
		}
		if len(m.detail.messages) == 0 {
			return center(emptyStyle.Render("No messages to show."), m.width, h)
		}
		return m.detail.View()
	}

	switch {
	case m.loading && len(m.list.Items()) == 0:
		return center(m.spinner.View()+" "+m.loadingNote, m.width, h)
	case len(m.list.Items()) == 0:
		return center(emptyStyle.Render("Inbox zero. Nothing unread."), m.width, h)
	default:
		return m.list.View()
	}
}

func (m Model) statusView() string {
	switch {
	case m.loading && len(m.list.Items()) > 0:
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
	if m.screen == screenDetail {
		return m.help.ShortHelpView(m.keys.detailShortHelp())
	}
	return m.help.ShortHelpView(m.keys.listShortHelp())
}

func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	m.help.SetWidth(m.width)
	m.list.SetSize(m.width, m.bodyHeight())
	m.detail.setSize(m.bodyWidth(), m.bodyHeight())
}

func (m Model) bodyWidth() int { return m.width }

func (m Model) bodyHeight() int {
	help := 1
	if m.help.ShowAll {
		help = 4
	}
	// header(1) + blank(1) + status(1) + help
	h := m.height - 3 - help
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

// setConversations replaces the list, keeping the highlighted conversation
// selected across a refresh when it is still unread.
func (m *Model) setConversations(convs []slack.Conversation) {
	prev := ""
	if it, ok := m.list.SelectedItem().(convItem); ok {
		prev = it.conv.ID
	}
	m.list.SetItems(convItems(convs))
	if prev == "" {
		return
	}
	for i, item := range m.list.Items() {
		if ci, ok := item.(convItem); ok && ci.conv.ID == prev {
			m.list.Select(i)
			return
		}
	}
}

// friendlyError turns a raw tool error into a single readable line, with a
// special case for auth failures the user can act on.
func friendlyError(err error) string {
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "invalid_auth"),
		strings.Contains(low, "authentication failed"),
		strings.Contains(low, "not_authed"),
		strings.Contains(low, "token_expired"):
		return "Slack rejected the token: it may be invalid or expired. Check your SLACK_MCP_* env vars."
	case strings.Contains(low, "conversations_mark") && strings.Contains(low, "disabled"):
		return markDisabledMsg
	default:
		if i := strings.IndexByte(s, '\n'); i > 0 {
			s = s[:i]
		}
		return s
	}
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
