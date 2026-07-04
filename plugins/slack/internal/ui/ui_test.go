package ui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/plugins/slack/internal/slack"
)

func testModel() Model {
	l := list.New(nil, convDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	return Model{
		screen:  screenList,
		list:    l,
		detail:  newDetail(),
		picker:  newPicker(),
		spinner: spinner.New(),
		help:    help.New(),
		keys:    defaultKeys(),
	}
}

func TestListRenderSmoke(t *testing.T) {
	var m tea.Model = testModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(unreadsMsg{convs: []slack.Conversation{
		{ID: "D1", Name: "alice", Type: slack.TypeDM, UnreadCount: 3},
		{ID: "C1", Name: "general", Type: slack.TypeInternal, UnreadCount: 12},
	}})

	view := m.View().Content
	for _, want := range []string{"alice", "general", "3", "12", "DM", "Channel"} {
		if !strings.Contains(view, want) {
			t.Errorf("list view missing %q\n%s", want, view)
		}
	}
}

func TestEmptyStateRenders(t *testing.T) {
	var m tea.Model = testModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(unreadsMsg{convs: nil})
	if !strings.Contains(m.View().Content, "Inbox zero") {
		t.Errorf("expected inbox-zero empty state:\n%s", m.View().Content)
	}
}

func TestDetailRenderSmoke(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 24
	m.layout()
	m.screen = screenDetail
	m.detail.open(slack.Conversation{ID: "C1", Name: "general", Type: slack.TypeInternal, LastRead: "1700000000.000000"})
	m.detail.setSize(m.bodyWidth(), m.bodyHeight())

	var tm tea.Model = m
	tm, _ = tm.Update(historyMsg{convID: "C1", msgs: []slack.Message{
		{ID: "1700000100.000100", RealName: "Alice", Text: "first unread message", ThreadTS: "1700000100.000100"},
		{ID: "1700000200.000200", RealName: "Bob", Text: "a standalone reply"},
	}})

	view := tm.View().Content
	for _, want := range []string{"Alice", "first unread message", "Bob", "[+ thread]", "new"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q\n%s", want, view)
		}
	}
}

func TestBodyClipping(t *testing.T) {
	d := newDetail()
	d.setSize(60, 40)
	d.setMessages([]slack.Message{{ID: "1700000000.000100", RealName: "Bot", Text: "l1\nl2\nl3\nl4\nl5\nl6"}})

	if view := d.View(); !strings.Contains(view, "more (space)") || strings.Contains(view, "l6") {
		t.Errorf("clipped body should hide overflow behind an indicator:\n%s", view)
	}

	d.toggleBody()
	if view := d.View(); !strings.Contains(view, "l6") {
		t.Errorf("expanded body should show every line:\n%s", view)
	}
}

func TestWrapText(t *testing.T) {
	got := wrapText("the quick brown fox", 9)
	want := []string{"the quick", "brown fox"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapText = %v, want %v", got, want)
	}

	// A word longer than the width is hard-broken.
	long := wrapText("https://example.com/really/long/path", 10)
	for _, line := range long {
		if len(line) > 10 {
			t.Errorf("line exceeds width: %q", line)
		}
	}

	// Newlines are preserved as paragraph breaks.
	if got := wrapText("a\nb", 80); len(got) != 2 {
		t.Errorf("expected 2 lines across newline, got %v", got)
	}
}

func TestFriendlyError(t *testing.T) {
	cases := map[string]string{
		"calling conversations_unreads: invalid_auth":                "Slack rejected the token",
		"conversations_mark failed: the tool is disabled by default": markDisabledMsg,
		"some boring error\nwith a second line we should drop":       "some boring error",
	}
	for in, wantPrefix := range cases {
		got := friendlyError(errors.New(in))
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("friendlyError(%q) = %q, want prefix %q", in, got, wantPrefix)
		}
	}
}

func TestMessageURL(t *testing.T) {
	base := "https://acme.slack.com"
	const want = "https://acme.slack.com/archives/C1/p1700000000000100"

	if got := messageURL(base, "C1", "1700000000.000100", ""); got != want {
		t.Errorf("standalone: got %q, want %q", got, want)
	}
	// A thread root (thread_ts == ts) needs no anchor.
	if got := messageURL(base, "C1", "1700000000.000100", "1700000000.000100"); got != want {
		t.Errorf("root: got %q, want %q", got, want)
	}
	// A reply carries the thread anchor so the link opens in-thread.
	if got := messageURL(base, "C1", "1700000050.000200", "1700000000.000100"); !strings.Contains(got, "?thread_ts=1700000000.000100&cid=C1") {
		t.Errorf("reply missing thread anchor: %q", got)
	}
}

func TestRefreshPreservesSelection(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 24
	m.layout()
	m.setConversations([]slack.Conversation{
		{ID: "A", Name: "a", Type: slack.TypeInternal, UnreadCount: 1},
		{ID: "B", Name: "b", Type: slack.TypeInternal, UnreadCount: 1},
		{ID: "C", Name: "c", Type: slack.TypeInternal, UnreadCount: 1},
	})
	m.list.Select(1)

	// Refresh: A is now read (gone), B/C remain, D is new.
	m.setConversations([]slack.Conversation{
		{ID: "B", Name: "b", Type: slack.TypeInternal, UnreadCount: 2},
		{ID: "C", Name: "c", Type: slack.TypeInternal, UnreadCount: 1},
		{ID: "D", Name: "d", Type: slack.TypeInternal, UnreadCount: 1},
	})
	if it, ok := m.list.SelectedItem().(convItem); !ok || it.conv.ID != "B" {
		t.Errorf("expected B still selected after refresh, got ok=%v id=%q", ok, it.conv.ID)
	}
}

func TestPickerFuzzyFilter(t *testing.T) {
	p := newPicker()
	p.setNames([]string{"cat", "catjam", "party_cat", "dog", "thumbsup"})
	if len(p.filtered) != 5 {
		t.Fatalf("empty query should show all names, got %d", len(p.filtered))
	}

	p.input.SetValue("cat")
	p.filter()
	got := strings.Join(p.filtered, ",")
	for _, want := range []string{"cat", "catjam", "party_cat"} {
		if !strings.Contains(got, want) {
			t.Errorf("query cat missing %q, got %v", want, p.filtered)
		}
	}
	for _, absent := range []string{"dog", "thumbsup"} {
		for _, m := range p.filtered {
			if m == absent {
				t.Errorf("query cat should not match %q", absent)
			}
		}
	}
}

func TestPickerSelectedFallback(t *testing.T) {
	p := newPicker()
	p.setNames([]string{"partyparrot"})
	p.input.SetValue("no_such_emoji")
	p.filter()
	// No fuzzy match -> Enter still reacts with whatever was typed (covers
	// standard emoji, which aren't in the custom list).
	if got := p.selected(); got != "no_such_emoji" {
		t.Errorf("selected with no match = %q, want the typed text", got)
	}
}

func TestReactKeyOpensPicker(t *testing.T) {
	m := testModel()
	m.reactEnabled = true
	m.emojiLoaded = true // skip the network fetch
	m.width, m.height = 80, 24
	m.layout()
	m.screen = screenDetail
	m.detail.open(slack.Conversation{ID: "C1", Name: "general", Type: slack.TypeInternal})
	m.detail.setSize(m.bodyWidth(), m.bodyHeight())
	m.detail.setMessages([]slack.Message{{ID: "1700000100.000100", RealName: "Alice", Text: "hi"}})

	var tm tea.Model = m
	tm, _ = tm.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	mm, ok := tm.(Model)
	if !ok || !mm.pickerActive {
		t.Fatalf("expected picker active after 'e', pickerActive=%v", ok && mm.pickerActive)
	}
	if !strings.Contains(mm.View().Content, "Add reaction") {
		t.Errorf("picker view should be shown:\n%s", mm.View().Content)
	}
}

func TestReactDisabledShowsHint(t *testing.T) {
	m := testModel()
	m.reactEnabled = false
	m.screen = screenDetail
	m.detail.setMessages([]slack.Message{{ID: "1700000100.000100", RealName: "Alice", Text: "hi"}})

	tm, _ := m.openPicker()
	mm := tm.(Model)
	if mm.pickerActive {
		t.Error("picker must not open when reactions are disabled")
	}
	if !strings.Contains(mm.status, "SLACK_MCP_REACTION_TOOL") {
		t.Errorf("expected an enable hint, got %q", mm.status)
	}
}

func TestForceHeight(t *testing.T) {
	if got := forceHeight("a\nb", 4); strings.Count(got, "\n") != 3 {
		t.Errorf("forceHeight pad: got %q", got)
	}
	if got := forceHeight("a\nb\nc\nd\ne", 2); strings.Count(got, "\n") != 1 {
		t.Errorf("forceHeight trim: got %q", got)
	}
}
