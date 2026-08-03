package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openTestStore creates a store for TUI tests with seeded data.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// registerTestAgent creates an agent in the store.
func registerTestAgent(t *testing.T, s *store.Store, id, harness, alias string) {
	t.Helper()
	err := s.Register(store.Agent{SessionID: id, Harness: harness})
	if err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
	if alias != "" {
		if err := s.SetAlias(id, alias); err != nil {
			t.Fatalf("alias %s: %v", alias, err)
		}
	}
}

// createTestChannel creates a channel with members.
func createTestChannel(t *testing.T, s *store.Store, name, kind, visibility, createdBy string, members ...string) store.Channel {
	t.Helper()
	ch, err := s.CreateChannel(name, kind, visibility, "", createdBy)
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	for _, m := range members {
		if err := s.Join(ch.ID, m); err != nil {
			t.Fatalf("join %s to %s: %v", m, name, err)
		}
	}
	return ch
}

func newTestModel(s *store.Store) *Model {
	m := New(s)
	return &m
}

// ── model update loop tests ───────────────────────────────────────────

func TestModelInit(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "agent1", "claude", "builder")
	createTestChannel(t, s, "general", "channel", "public", store.HumanID, "agent1")

	m := newTestModel(s)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}

	// Init should load channels and agents
	msg := cmd()
	switch msg.(type) {
	case channelsMsg, agentsMsg:
		// ok
	default:
		// the batch returns one at a time in test
	}
}

func TestModelHandlesChannelsMsg(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "agent1", "claude", "builder")
	ch := createTestChannel(t, s, "general", "channel", "public", store.HumanID, "agent1")

	m := newTestModel(s)
	cm := channelsMsg{channels: []store.Channel{ch}}
	newModel, _ := m.Update(cm)
	m2 := newModel.(*Model)

	if len(m2.channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(m2.channels))
	}
	if m2.currentChID != ch.ID {
		t.Fatalf("should auto-select first channel, got %d want %d", m2.currentChID, ch.ID)
	}
	if m2.currentCh != "general" {
		t.Fatalf("channel name should be general, got %s", m2.currentCh)
	}
}

func TestModelHandlesAgentsMsg(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "agent1", "claude", "builder")

	m := newTestModel(s)
	am := agentsMsg{agents: []store.Agent{
		{SessionID: store.HumanID, Harness: "human", TurnState: "idle"},
		{SessionID: "agent1", Harness: "claude", Alias: "builder", TurnState: "idle"},
	}}
	newModel, _ := m.Update(am)
	m2 := newModel.(*Model)

	if len(m2.agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(m2.agents))
	}
}

func TestModelHandlesHistoryMsg(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "agent1", "claude", "builder")
	ch := createTestChannel(t, s, "general", "channel", "public", store.HumanID, "agent1")

	// First set the channel
	m := newTestModel(s)
	m.channels = []store.Channel{ch}
	m.currentChID = ch.ID
	m.currentCh = "general"

	hm := historyMsg{
		channelID: ch.ID,
		messages: []store.Message{
			{ID: 1, ChannelID: ch.ID, From: "agent1", FromName: "builder", Body: "hello", ThreadID: 1, CreatedAt: 1000},
			{ID: 2, ChannelID: ch.ID, From: store.HumanID, FromName: "human", Body: "hi", ThreadID: 2, CreatedAt: 2000},
		},
	}
	newModel, _ := m.Update(hm)
	m2 := newModel.(*Model)

	msgs := m2.allMessages[ch.ID]
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	feedMsgs := m2.feed.messages
	if len(feedMsgs) != 2 {
		t.Fatalf("feed should have 2 messages, got %d", len(feedMsgs))
	}
	if !feedMsgs[0].IsSystem {
		// first msg from agent, human is not the sender
		if feedMsgs[0].IsOwn {
			t.Fatal("first message should not be marked as own")
		}
	}
	if !feedMsgs[1].IsOwn {
		t.Fatal("second message should be marked as own (from human)")
	}
}

func TestModelQuitsOnQ(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)

	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m2 := newModel.(*Model)

	if !m2.quitting {
		t.Fatal("model should be quitting")
	}
	if cmd == nil {
		t.Fatal("should return tea.Quit")
	}
}

func TestModelQuitsOnCtrlC(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)

	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m2 := newModel.(*Model)

	if !m2.quitting {
		t.Fatal("model should be quitting on ctrl+c")
	}
	if cmd == nil {
		t.Fatal("should return tea.Quit")
	}
}

func TestModelTabCyclesPanels(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)

	if m.focusedPanel != PanelFeed {
		t.Fatalf("default focus should be feed, got %d", m.focusedPanel)
	}

	// Tab to compose
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m2 := newModel.(*Model)
	if m2.focusedPanel != PanelCompose {
		t.Fatalf("tab should move to compose, got %d", m2.focusedPanel)
	}

	// Tab to sidebar
	newModel2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyTab})
	m3 := newModel2.(*Model)
	if m3.focusedPanel != PanelSidebar {
		t.Fatalf("tab should move to sidebar, got %d", m3.focusedPanel)
	}

	// Tab back to feed
	newModel3, _ := m3.Update(tea.KeyMsg{Type: tea.KeyTab})
	m4 := newModel3.(*Model)
	if m4.focusedPanel != PanelFeed {
		t.Fatalf("tab should wrap to feed, got %d", m4.focusedPanel)
	}
}

func TestModelShiftTabCyclesBackwards(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m2 := newModel.(*Model)
	if m2.focusedPanel != PanelSidebar {
		t.Fatalf("shift+tab should go to sidebar, got %d", m2.focusedPanel)
	}
}

func TestModelHelpToggle(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m2 := newModel.(*Model)
	if !m2.showHelp {
		t.Fatal("? should show help")
	}

	newModel2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m3 := newModel2.(*Model)
	if m3.showHelp {
		t.Fatal("? should toggle help off")
	}
}

func TestModelSidebarToggle(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)

	if !m.sidebar.showAgents {
		t.Fatal("agents should be shown by default")
	}

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m2 := newModel.(*Model)
	if m2.sidebar.showAgents {
		t.Fatal("s should hide agents")
	}
}

// ── sidebar rendering tests ──────────────────────────────────────────

func TestSidebarRendersChannels(t *testing.T) {
	sm := NewSidebar()
	sm.SetSize(22, 20)
	sm.focused = true

	sm.SetChannels([]ChannelView{
		{ID: 1, Name: "general", Kind: "channel", Visibility: "public", MemberCount: 3},
		{ID: 2, Name: "secrets", Kind: "channel", Visibility: "private", MemberCount: 2, Unread: 1},
		{ID: 3, Name: "dm:abc123:xyz456", Label: "reviewer", Kind: "dm", Visibility: "private", MemberCount: 2},
	})

	view := sm.View()
	// Should contain channel names
	if !strings.Contains(view, "general") {
		t.Error("sidebar should contain general")
	}
	if !strings.Contains(view, "secrets") {
		t.Error("sidebar should contain secrets")
	}
	if !strings.Contains(view, "CHANNELS") {
		t.Error("sidebar should have CHANNELS header")
	}
	if !strings.Contains(view, "[L]") {
		t.Error("sidebar should show private lock icon")
	}
	if !strings.Contains(view, "(1)") {
		t.Error("sidebar should show unread count")
	}
}

// A count in the channel row is the unread count and nothing else. It used to
// render the member count in the same parentheses, so "#general (4)" meant
// four unread, or four members, or one of each, with nothing to tell them apart.
func TestSidebarCountIsUnreadOnly(t *testing.T) {
	sm := NewSidebar()
	sm.SetSize(24, 20)
	sm.SetChannels([]ChannelView{
		{ID: 1, Name: "general", Kind: "channel", Visibility: "public", MemberCount: 4},
	})

	if view := sm.View(); strings.Contains(view, "(4)") {
		t.Errorf("a channel with 4 members and nothing unread must show no count:\n%s", view)
	}

	sm.SetChannels([]ChannelView{
		{ID: 1, Name: "general", Kind: "channel", Visibility: "public", MemberCount: 4, Unread: 2},
	})
	view := sm.View()
	if !strings.Contains(view, "(2)") {
		t.Errorf("the unread count should show:\n%s", view)
	}
	if strings.Contains(view, "(4)") {
		t.Errorf("the member count must not share the unread badge:\n%s", view)
	}
}

// A DM channel is named dm:<session>:<session>. Slicing three characters off
// that left "@aaaa1111:bbbb2222" in a 22-column sidebar, which is both unreadable
// and the wrong thing to read: the useful label is who you are talking to.
func TestSidebarNamesTheOtherPartyInADM(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	ch, err := s.DMChannel(store.HumanID, "aaaa1111")
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel(s)
	m.Update(agentsMsg{agents: mustRoster(t, s)})
	m.Update(channelsMsg{channels: []store.Channel{ch}})
	m.sidebar.SetSize(24, 20)

	view := m.sidebar.View()
	if !strings.Contains(view, "@builder") {
		t.Errorf("a DM should be labelled with the other party:\n%s", view)
	}
	if strings.Contains(view, "aaaa1111") {
		t.Errorf("a DM row should not show raw session ids:\n%s", view)
	}
}

func mustRoster(t *testing.T, s *store.Store) []store.Agent {
	t.Helper()
	agents, err := s.Roster()
	if err != nil {
		t.Fatal(err)
	}
	return agents
}

func TestSidebarRendersAgents(t *testing.T) {
	sm := NewSidebar()
	sm.SetSize(22, 20)
	sm.showAgents = true

	sm.SetAgents([]AgentView{
		{Name: "builder", TurnState: "working", Pending: 0},
		{Name: "reviewer", TurnState: "idle", Pending: 3},
		{Name: "auditor", TurnState: "idle", Pending: 0},
	})

	view := sm.View()
	// AGENTS, not MEMBERS: this list is everyone on the mesh. MEMBERS is the
	// heading on the list of who is in the open channel.
	if !strings.Contains(view, "AGENTS") {
		t.Error("sidebar should have AGENTS header")
	}
	if !strings.Contains(view, "builder") {
		t.Error("sidebar should contain builder")
	}
	if !strings.Contains(view, "reviewer") {
		t.Error("sidebar should contain reviewer")
	}
}

func TestSidebarLabelsBothLists(t *testing.T) {
	sm := NewSidebar()
	sm.SetSize(24, 24)
	sm.showAgents = true
	sm.SetChannels([]ChannelView{{ID: 1, Name: "general", Kind: "channel"}})
	sm.SetAgents([]AgentView{{Name: "builder", TurnState: "working"}})
	sm.SetChannelMembers([]store.MemberInfo{
		{Name: "human", Harness: "human", TurnState: "idle", Role: "owner"},
	})

	view := sm.View()
	// The channel members used to render unlabelled, directly above a roster
	// headed MEMBERS, so the two read as one list under the wrong heading.
	members := strings.Index(view, "MEMBERS")
	agents := strings.Index(view, "AGENTS")
	if members < 0 || agents < 0 {
		t.Fatalf("both lists need a heading, got:\n%s", view)
	}
	if members > agents {
		t.Error("the channel's members belong above the roster")
	}
}

func TestSidebarKeepsItsShapeWithALongMemberName(t *testing.T) {
	sm := NewSidebar()
	sm.SetSize(24, 20)
	sm.SetChannels([]ChannelView{{ID: 1, Name: "general", Kind: "channel"}})
	sm.SetChannelMembers([]store.MemberInfo{
		// Exactly the width renderMember truncates a name to, which is budgeted
		// against a one-column status glyph. The human's was an emoji, which is
		// two, so this row wrapped and the sidebar rendered a row past its height.
		{Name: strings.Repeat("n", 24-sidebarChrome-3), Harness: "human", TurnState: "idle"},
		{Name: "builder", Harness: "claude", TurnState: "working"},
	})

	lines := strings.Split(sm.View(), "\n")
	if len(lines) != 20 {
		t.Fatalf("sidebar is %d rows tall, want 20", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 24 {
			t.Fatalf("row %d is %d columns wide, want 24: %q", i, w, line)
		}
	}
}

func TestHintBarStaysOneRow(t *testing.T) {
	short := NewCompose()
	short.SetSize(60)
	short.SetHint("j/k: move")
	long := NewCompose()
	long.SetSize(60)
	long.SetHint(strings.Repeat("x: does a thing  ", 20))

	// A hint wider than the terminal used to wrap, making the whole layout one
	// row taller than the screen and scrolling the top row of every panel away.
	want := strings.Count(short.View(), "\n")
	if got := strings.Count(long.View(), "\n"); got != want {
		t.Fatalf("a long hint renders %d rows, a short one %d", got+1, want+1)
	}
}

func TestSidebarCursorNavigation(t *testing.T) {
	sm := NewSidebar()
	sm.SetChannels([]ChannelView{
		{ID: 1, Name: "general", Kind: "channel"},
		{ID: 2, Name: "backend", Kind: "channel"},
		{ID: 3, Name: "ops", Kind: "channel"},
	})

	if sm.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", sm.cursor)
	}

	sm.MoveDown()
	if sm.cursor != 1 {
		t.Fatalf("after MoveDown cursor should be 1, got %d", sm.cursor)
	}

	sm.MoveDown()
	if sm.cursor != 2 {
		t.Fatalf("after second MoveDown cursor should be 2, got %d", sm.cursor)
	}

	// Should not go beyond last item
	sm.MoveDown()
	if sm.cursor != 2 {
		t.Fatalf("cursor should stay at 2 (max), got %d", sm.cursor)
	}

	sm.MoveUp()
	if sm.cursor != 1 {
		t.Fatalf("after MoveUp cursor should be 1, got %d", sm.cursor)
	}

	sm.MoveUp()
	if sm.cursor != 0 {
		t.Fatalf("after second MoveUp cursor should be 0, got %d", sm.cursor)
	}

	// Should not go below 0
	sm.MoveUp()
	if sm.cursor != 0 {
		t.Fatalf("cursor should stay at 0 (min), got %d", sm.cursor)
	}
}

func TestSidebarCursorChannel(t *testing.T) {
	sm := NewSidebar()
	ch0 := ChannelView{ID: 1, Name: "general", Kind: "channel"}
	ch1 := ChannelView{ID: 2, Name: "backend", Kind: "channel"}
	sm.SetChannels([]ChannelView{ch0, ch1})

	sel := sm.CursorChannel()
	if sel == nil || sel.ID != 1 {
		t.Fatalf("CursorChannel should return first channel, got %v", sel)
	}

	sm.MoveDown()
	sel = sm.CursorChannel()
	if sel == nil || sel.ID != 2 {
		t.Fatalf("CursorChannel should return second channel, got %v", sel)
	}
}

// ── message feed tests ───────────────────────────────────────────────

func TestFeedRendersMessages(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(80, 20)
	fm.focused = true
	fm.channelName = "#general"

	now := tm(1000)
	fm.SetMessages([]MsgView{
		{ID: 1, From: "agent1", FromName: "builder", Body: "hello world", Timestamp: now, ThreadID: 1, Delivered: true},
		{ID: 2, From: store.HumanID, FromName: "human", Body: "hi there", Timestamp: now, ThreadID: 2, IsOwn: true, Delivered: true, Read: true},
	})

	view := fm.View()
	if !strings.Contains(view, "general") {
		t.Error("feed should show channel name")
	}
	if !strings.Contains(view, "builder") {
		t.Error("feed should show sender name")
	}
	if !strings.Contains(view, "hello world") {
		t.Error("feed should show message body")
	}
	if !strings.Contains(view, "you") {
		t.Error("feed should show 'you' for own messages")
	}
}

// The feed is the widest panel and it was rendering to the width of its longest
// message: a two-word message left a 90-column block in a 239-column pane, with
// the terminal's own background showing through the rest of the row.
func TestFeedFillsItsColumn(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(100, 10)
	fm.channelName = "#general"
	fm.SetMessages([]MsgView{
		{ID: 1, FromName: "builder", Body: "hi", Timestamp: tm(1000), ThreadID: 1, Delivered: true},
	})

	for i, line := range strings.Split(fm.View(), "\n") {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("line %d is %d columns wide, want 100: %q", i, w, line)
		}
	}
}

// A chat reads from the bottom. Rendering top-down left the newest message
// stranded at the top of a mostly empty panel, which is the "dead space below
// the messages" the feed looked like at every size.
func TestFeedAnchorsToTheBottom(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(60, 12)
	fm.SetMessages([]MsgView{
		{ID: 1, FromName: "builder", Body: "newest", Timestamp: tm(1000), ThreadID: 1, Delivered: true},
	})

	fm.channelName = "#general"
	lines := strings.Split(fm.View(), "\n")
	last := -1
	for i, line := range lines {
		if strings.Contains(line, "newest") {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("the message is not rendered at all")
	}
	// The header names the channel and belongs at the top whatever the
	// messages do. Only the messages sink.
	if !strings.Contains(lines[0], "#general") {
		t.Fatalf("the channel header should be the first row, got %q", lines[0])
	}
	// The body is the last thing a message renders, so it must sit within a
	// row or two of the floor rather than at the ceiling.
	if last < len(lines)-3 {
		t.Fatalf("message body on row %d of %d: the feed is still top-anchored", last, len(lines))
	}
}

// The cursor counts messages and the viewport counts lines. They were set from
// each other, so in a feed of three-line messages the view scrolled at a third
// of the speed of the selection and the selected message left the screen.
func TestFeedKeepsSelectionVisible(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(60, 12)
	now := tm(1000)
	var msgs []MsgView
	for i := 0; i < 30; i++ {
		msgs = append(msgs, MsgView{
			ID: int64(i + 1), FromName: "a", Body: fmt.Sprintf("body-%d", i),
			Timestamp: now, ThreadID: int64(i + 1), Delivered: true,
		})
	}
	fm.SetMessages(msgs)
	fm.focused = true

	// Walk the whole feed. Whichever message is selected must be on screen.
	for i := len(msgs) - 1; i >= 0; i-- {
		view := fm.View()
		want := fmt.Sprintf("body-%d", i)
		if !strings.Contains(view, want) {
			t.Fatalf("selected message %d (%s) is off screen:\n%s", i, want, view)
		}
		fm.MoveUp()
	}
	for i := 0; i < len(msgs); i++ {
		view := fm.View()
		want := fmt.Sprintf("body-%d", i)
		if !strings.Contains(view, want) {
			t.Fatalf("selected message %d (%s) is off screen on the way down:\n%s", i, want, view)
		}
		fm.MoveDown()
	}
}

func TestFeedScrollNavigation(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(80, 20)

	now := tm(1000)
	var msgs []MsgView
	for i := 0; i < 10; i++ {
		msgs = append(msgs, MsgView{ID: int64(i), From: "agent1", FromName: "a", Body: "msg", Timestamp: now, ThreadID: int64(i), Delivered: true})
	}
	fm.SetMessages(msgs)

	// A feed opens on the newest message, the way a chat does.
	if fm.cursor != 9 {
		t.Fatalf("cursor should start on the last message, got %d", fm.cursor)
	}

	// Should not go past last
	fm.MoveDown()
	if fm.cursor != 9 {
		t.Fatalf("cursor should stay at 9, got %d", fm.cursor)
	}

	// Move up
	fm.MoveUp()
	if fm.cursor != 8 {
		t.Fatalf("cursor should be 8 after MoveUp, got %d", fm.cursor)
	}

	fm.MoveDown()
	if fm.cursor != 9 {
		t.Fatalf("cursor should be 9 after MoveDown, got %d", fm.cursor)
	}

	// Scroll to top
	fm.ScrollToTop()
	if fm.cursor != 0 {
		t.Fatalf("cursor should be 0 after ScrollToTop, got %d", fm.cursor)
	}

	// Scroll to bottom
	fm.ScrollToBottom()
	if fm.cursor != 9 {
		t.Fatalf("cursor should be 9 after ScrollToBottom, got %d", fm.cursor)
	}
}

func TestFeedSelectedMessage(t *testing.T) {
	fm := NewFeed()
	now := tm(1000)
	fm.SetMessages([]MsgView{
		{ID: 42, From: "agent1", FromName: "builder", Body: "msg1", Timestamp: now, ThreadID: 1, Delivered: true},
		{ID: 43, From: "agent2", FromName: "reviewer", Body: "msg2", Timestamp: now, ThreadID: 2, Delivered: true},
	})

	sel := fm.SelectedMessage()
	if sel == nil || sel.ID != 43 {
		t.Fatalf("SelectedMessage should return the newest msg, got %v", sel)
	}

	fm.MoveUp()
	sel = fm.SelectedMessage()
	if sel == nil || sel.ID != 42 {
		t.Fatalf("SelectedMessage should return the older msg, got %v", sel)
	}
}

// New traffic must not move a reader who has scrolled back.
func TestFeedKeepsThePositionOfAReaderWhoScrolledUp(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(60, 10)
	now := tm(1000)
	var msgs []MsgView
	for i := 0; i < 5; i++ {
		msgs = append(msgs, MsgView{ID: int64(i + 1), FromName: "a", Body: "msg", Timestamp: now, ThreadID: int64(i + 1)})
	}
	fm.SetMessages(msgs)
	fm.MoveUp()
	fm.MoveUp()
	held := fm.SelectedMessage().ID

	fm.SetMessages(append(msgs, MsgView{ID: 99, FromName: "b", Body: "new", Timestamp: now, ThreadID: 99}))
	if got := fm.SelectedMessage().ID; got != held {
		t.Fatalf("a new message moved the cursor from %d to %d", held, got)
	}

	// And a reader sitting at the bottom follows the new message down.
	fm.ScrollToBottom()
	fm.SetMessages(append(msgs,
		MsgView{ID: 99, FromName: "b", Body: "new", Timestamp: now, ThreadID: 99},
		MsgView{ID: 100, FromName: "b", Body: "newer", Timestamp: now, ThreadID: 100}))
	if got := fm.SelectedMessage().ID; got != 100 {
		t.Fatalf("a reader at the bottom should follow, got %d", got)
	}
}

func TestFeedSystemMessagesDimmed(t *testing.T) {
	fm := NewFeed()
	now := tm(1000)
	fm.SetMessages([]MsgView{
		{ID: 1, From: "agent1", FromName: "builder", Body: "--- builder joined #general", Timestamp: now, ThreadID: 1, IsSystem: true, Delivered: true},
	})

	view := fm.View()
	if !strings.Contains(view, "joined") {
		t.Error("feed should show system message body")
	}
}

func TestFeedReceiptIcons(t *testing.T) {
	fm := NewFeed()
	now := tm(1000)
	fm.SetMessages([]MsgView{
		{ID: 1, From: "agent1", FromName: "builder", Body: "msg", Timestamp: now, ThreadID: 1, Delivered: true, Pending: false},
		{ID: 2, From: "agent1", FromName: "builder", Body: "msg", Timestamp: now, ThreadID: 2, Delivered: true, Read: true, Pending: false},
		{ID: 3, From: "agent1", FromName: "builder", Body: "msg", Timestamp: now, ThreadID: 3, Delivered: false, Pending: true},
	})

	view := fm.View()
	if !strings.Contains(view, "✓") {
		t.Error("feed should show sent icon")
	}
	if !strings.Contains(view, "✓✓") {
		t.Error("feed should show read icon")
	}
	if !strings.Contains(view, "◔") {
		t.Error("feed should show pending icon")
	}
}

// ── compose submit tests ─────────────────────────────────────────────

func TestComposeEnterClearsInput(t *testing.T) {
	cm := NewCompose()
	cm.Focus()
	cm.input.SetValue("hello world")

	// Simulate enter via model
	s := openTestStore(t)
	registerTestAgent(t, s, "agent1", "claude", "builder")
	ch := createTestChannel(t, s, "general", "channel", "public", store.HumanID, "agent1")

	m := newTestModel(s)
	m.channels = []store.Channel{ch}
	m.currentChID = ch.ID
	m.currentCh = "general"
	m.focusedPanel = PanelCompose
	m.compose = cm
	m.updateFocus()

	// Send enter
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := newModel.(*Model)

	if m2.compose.Value() != "" {
		t.Error("compose should be cleared after enter")
	}
	if cmd == nil {
		t.Error("enter should trigger a send command")
	}
}

func TestComposeDoesNotSendEmpty(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)
	m.focusedPanel = PanelCompose
	m.updateFocus()

	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := newModel.(*Model)

	if m2.compose.Value() != "" {
		t.Error("compose should remain empty")
	}
	if cmd != nil {
		t.Error("empty compose should not trigger send")
	}
}

func TestComposeAcceptsTypingWhenFocused(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)
	m.focusedPanel = PanelCompose
	m.updateFocus()

	// Letters that double as global bindings must type, not trigger.
	for _, r := range []rune("qi?/s") {
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = newModel.(*Model)
	}

	if got := m.compose.Value(); got != "qi?/s" {
		t.Errorf("typing should reach compose input, got %q", got)
	}
	if m.quitting {
		t.Error("typing 'q' while composing should not quit")
	}
	if m.showHelp {
		t.Error("typing '?' while composing should not open help")
	}
	if !m.sidebar.showAgents {
		t.Error("typing 's' while composing should not toggle agents")
	}
}

func TestComposeTypingDoesNotLeakWhenUnfocused(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)
	m.focusedPanel = PanelFeed
	m.updateFocus()

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m2 := newModel.(*Model)

	if m2.compose.Value() != "" {
		t.Error("typing while feed focused should not reach compose input")
	}
}

func TestComposeReplyMode(t *testing.T) {
	cm := NewCompose()
	if cm.mode != ComposeNormal {
		t.Fatal("default mode should be ComposeNormal")
	}

	cm.SetMode(ComposeReply)
	if cm.mode != ComposeReply {
		t.Fatal("mode should be ComposeReply")
	}

	view := cm.View()
	if !strings.Contains(view, "r>") {
		t.Error("reply mode should show r> prompt")
	}

	cm.Reset()
	if cm.mode != ComposeNormal {
		t.Fatal("reset should return to ComposeNormal")
	}
	if cm.replyToID != 0 {
		t.Fatal("reset should clear replyToID")
	}
}

func TestComposeEditMode(t *testing.T) {
	cm := NewCompose()
	cm.SetMode(ComposeEdit)
	cm.SetValue("original text")

	view := cm.View()
	if !strings.Contains(view, "e>") {
		t.Error("edit mode should show e> prompt")
	}
	if !strings.Contains(view, "original text") {
		t.Error("edit mode should show original text")
	}
}

func TestComposeSearchMode(t *testing.T) {
	cm := NewCompose()
	cm.SetMode(ComposeSearch)

	view := cm.View()
	if !strings.Contains(view, "/") {
		t.Error("search mode should show / prompt")
	}
}

// ── model view tests ─────────────────────────────────────────────────

func TestModelViewShowsGoodbyeWhenQuitting(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)
	m.quitting = true

	view := m.View()
	if !strings.Contains(view, "Goodbye") {
		t.Error("quitting model should show goodbye")
	}
}

func TestModelViewShowsError(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)
	m.err = store.ErrNotFound

	view := m.View()
	if !strings.Contains(view, "Error") {
		t.Error("model with error should show error")
	}
}

func TestModelViewShowsHelp(t *testing.T) {
	s := openTestStore(t)
	m := newTestModel(s)
	m.width = 80
	m.height = 24
	m.showHelp = true

	view := m.View()
	if !strings.Contains(view, "Keybindings") {
		t.Error("help view should show keybindings")
	}
	if !strings.Contains(view, "Navigation") {
		t.Error("help view should show navigation section")
	}
}

// ── search tests ─────────────────────────────────────────────────────

func TestSearchActivateDeactivate(t *testing.T) {
	sm := NewSearch()
	if sm.active {
		t.Fatal("search should not be active by default")
	}

	sm.Activate()
	if !sm.active {
		t.Fatal("search should be active after Activate")
	}

	sm.Deactivate()
	if sm.active {
		t.Fatal("search should not be active after Deactivate")
	}
}

func TestSearchResultsNavigation(t *testing.T) {
	sm := NewSearch()
	sm.Activate()
	sm.SetResults([]SearchResult{
		{ID: 1, ChannelName: "general", FromName: "builder", Body: "hello"},
		{ID: 2, ChannelName: "backend", FromName: "reviewer", Body: "world"},
		{ID: 3, ChannelName: "ops", FromName: "auditor", Body: "test"},
	})

	if sm.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", sm.cursor)
	}

	sm.MoveDown()
	if sm.cursor != 1 {
		t.Fatalf("cursor should be 1, got %d", sm.cursor)
	}

	sm.MoveDown()
	if sm.cursor != 2 {
		t.Fatalf("cursor should be 2, got %d", sm.cursor)
	}

	sm.MoveDown()
	if sm.cursor != 2 {
		t.Fatalf("cursor should stay at 2, got %d", sm.cursor)
	}

	sel := sm.SelectedResult()
	if sel == nil || sel.ID != 3 {
		t.Fatalf("selected should be third result, got %v", sel)
	}
}

func TestSearchRendersResults(t *testing.T) {
	sm := NewSearch()
	sm.SetSize(80, 24)
	sm.Activate()
	sm.SetResults([]SearchResult{
		{ID: 1, ChannelName: "general", FromName: "builder", Body: "found this message"},
	})

	view := sm.View()
	if !strings.Contains(view, "general") {
		t.Error("search results should show channel name")
	}
	if !strings.Contains(view, "builder") {
		t.Error("search results should show sender")
	}
	if !strings.Contains(view, "found this") {
		t.Error("search results should show snippet")
	}
}

// ── picker tests ─────────────────────────────────────────────────────

func TestPickerFilterFuzzy(t *testing.T) {
	pm := NewPicker()
	pm.Activate(PickerAgent, []PickerItem{
		{Name: "builder", Detail: "claude"},
		{Name: "reviewer", Detail: "pi"},
		{Name: "auditor", Detail: "codex"},
		{Name: "build-bot", Detail: "gemini"},
	})

	// Initially all items visible
	if len(pm.filtered) != 4 {
		t.Fatalf("all 4 items should be visible, got %d", len(pm.filtered))
	}

	// Type to filter
	pm.input.SetValue("build")
	pm.filter()
	if len(pm.filtered) != 2 {
		t.Fatalf("should have 2 matches for 'build', got %d: %v", len(pm.filtered), pm.filtered)
	}

	pm.input.SetValue("xyzzy")
	pm.filter()
	if len(pm.filtered) != 0 {
		t.Fatalf("should have 0 matches for 'xyzzy', got %d", len(pm.filtered))
	}
}

// ── detail tests ─────────────────────────────────────────────────────

func TestDetailToggle(t *testing.T) {
	dm := NewDetail()
	if dm.visible {
		t.Fatal("detail should not be visible by default")
	}

	dm.Toggle()
	if !dm.visible {
		t.Fatal("detail should be visible after toggle")
	}

	dm.Toggle()
	if dm.visible {
		t.Fatal("detail should not be visible after second toggle")
	}
}

func TestDetailShowsReceipts(t *testing.T) {
	dm := NewDetail()
	dm.SetSize(30, 20)

	now := tm(1000)
	dm.Show(&MsgView{ID: 42, FromName: "builder", ThreadID: 1, Timestamp: now}, []store.Receipt{
		{Reader: "reviewer", Name: "reviewer", Harness: "pi", At: 1000, Source: "drain"},
		{Reader: store.HumanID, Name: "human", Harness: "human", At: 1100, Source: "client"},
	})

	view := dm.View()
	if !strings.Contains(view, "Details") {
		t.Error("detail should show Details header")
	}
	if !strings.Contains(view, "Read Receipts") {
		t.Error("detail should show Read Receipts section")
	}
	if !strings.Contains(view, "reviewer") {
		t.Error("detail should show receipt reader names")
	}
}

// ── thread tests ─────────────────────────────────────────────────────

func TestThreadOpenClose(t *testing.T) {
	th := NewThread()
	if th.active {
		t.Fatal("thread should not be active by default")
	}

	now := tm(1000)
	root := MsgView{ID: 1, FromName: "builder", Body: "root message", Timestamp: now, ThreadID: 1}
	replies := []MsgView{
		{ID: 2, FromName: "reviewer", Body: "reply one", Timestamp: now, ThreadID: 1},
	}

	th.Open(root, replies)
	if !th.active {
		t.Fatal("thread should be active after open")
	}
	if th.root == nil {
		t.Fatal("thread should have a root")
	}

	th.Close()
	if th.active {
		t.Fatal("thread should not be active after close")
	}
}

// ── styles / helpers tests ───────────────────────────────────────────

func TestAgentDots(t *testing.T) {
	tests := []struct {
		agent AgentView
		want  string
	}{
		{AgentView{TurnState: "working", Pending: 0}, "●"},
		{AgentView{TurnState: "idle", Pending: 0}, "○"},
		{AgentView{TurnState: "idle", Pending: 3}, "◐"},
		{AgentView{TurnState: "working", Pending: 1}, "◐"},
		{AgentView{Dead: true}, "✕"},
	}

	for _, tt := range tests {
		got := AgentDot(tt.agent)
		if got != tt.want {
			t.Errorf("AgentDot(%+v) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestReceiptIcons(t *testing.T) {
	read := ReceiptIcon(true, true, false)
	if read != "✓✓" {
		t.Errorf("read receipt icon should be ✓✓, got %q", read)
	}

	sent := ReceiptIcon(true, false, false)
	if sent != "✓" {
		t.Errorf("sent receipt icon should be ✓, got %q", sent)
	}

	pending := ReceiptIcon(false, false, true)
	if pending != "◔" {
		t.Errorf("pending receipt icon should be ◔, got %q", pending)
	}

	none := ReceiptIcon(false, false, false)
	if none != "" {
		t.Errorf("no receipt should be empty, got %q", none)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Errorf("short string should be unchanged, got %q", got)
	}

	got := Truncate("hello world this is long", 10)
	if len([]rune(got)) != 10 {
		t.Errorf("truncated string should be 10 runes, got %d: %q", len([]rune(got)), got)
	}
}

// ── channel administration from the keyboard ─────────────────────────
//
// Everything a human can do to a channel goes through these key paths, and none
// of them had a test: the invite path resolved an agent by its display name, so
// two agents sharing an alias invited whichever came first, and the rest did not
// exist at all.

// adminModel is a loaded TUI with the sidebar focused: one channel, one other
// agent, and the store already read.
func adminModel(t *testing.T, s *store.Store) (*Model, store.Channel) {
	t.Helper()
	ch := createTestChannel(t, s, "general", "channel", "public", store.HumanID)
	m := newTestModel(s)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain(t, m, m.Init())
	m.focusedPanel = PanelSidebar
	m.updateFocus()
	return m, ch
}

// drain runs a command and feeds every message it produces back into the model,
// the way the Bubble Tea runtime does, so a test sees the state the user would.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch typed := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, c := range typed {
			drain(t, m, c)
		}
	case errMsg:
		t.Fatalf("command failed: %v", typed.error)
	default:
		_, next := m.Update(msg)
		drain(t, m, next)
	}
}

// press sends keys one at a time, running whatever each one produces. Multi-rune
// strings are typed as text unless they name a key ("enter", "ctrl+r").
func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "ctrl+r":
			msg = tea.KeyMsg{Type: tea.KeyCtrlR}
		case "ctrl+n":
			msg = tea.KeyMsg{Type: tea.KeyCtrlN}
		default:
			for _, r := range k {
				_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				drain(t, m, cmd)
			}
			continue
		}
		_, cmd := m.Update(msg)
		drain(t, m, cmd)
	}
}

func isMember(t *testing.T, s *store.Store, channelID int64, sessionID string) bool {
	t.Helper()
	ok, err := s.IsMember(channelID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// unreadOf reads the badge the sidebar would draw for one channel.
func unreadOf(t *testing.T, m *Model, channelID int64) int {
	t.Helper()
	for _, ch := range m.sidebar.channels {
		if ch.ID == channelID {
			return ch.Unread
		}
	}
	t.Fatalf("channel %d is not in the sidebar", channelID)
	return 0
}

func TestUnreadBadgeShowsOnAChannelNeverOpened(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	first := createTestChannel(t, s, "aaa-first", "channel", "public", store.HumanID, "aaaa1111")
	never := createTestChannel(t, s, "zzz-never-opened", "channel", "public", store.HumanID, "aaaa1111")
	for _, body := range []string{"one", "two", "three"} {
		if _, err := s.Send(store.Post{ChannelID: never.ID, From: "aaaa1111", Body: body}, store.DefaultCaps()); err != nil {
			t.Fatal(err)
		}
	}

	m := newTestModel(s)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain(t, m, m.Init())

	if m.currentChID == never.ID {
		t.Fatalf("this test needs the other channel open, got %d", m.currentChID)
	}
	if _, loaded := m.allMessages[never.ID]; loaded {
		t.Fatal("the unopened channel should have no history loaded")
	}
	// The badge used to be counted from the loaded history, so a channel you had
	// never opened, the only kind whose badge you need, always read zero.
	if got := unreadOf(t, m, never.ID); got != 3 {
		t.Fatalf("unopened channel should show 3 unread, got %d", got)
	}
	if got := unreadOf(t, m, first.ID); got != 0 {
		t.Fatalf("an empty channel should show nothing, got %d", got)
	}
}

func TestOpeningAChannelClearsItsUnreadBadge(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	createTestChannel(t, s, "aaa-first", "channel", "public", store.HumanID, "aaaa1111")
	unread := createTestChannel(t, s, "zzz-unread", "channel", "public", store.HumanID, "aaaa1111")
	if _, err := s.Send(store.Post{ChannelID: unread.ID, From: "aaaa1111", Body: "one"}, store.DefaultCaps()); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(s)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain(t, m, m.Init())
	m.focusedPanel = PanelSidebar
	m.updateFocus()

	press(t, m, "j", "enter")
	if m.currentChID != unread.ID {
		t.Fatalf("should have opened the second channel, got %d", m.currentChID)
	}
	if got := unreadOf(t, m, unread.ID); got != 0 {
		t.Fatalf("reading a channel clears its badge, got %d", got)
	}
}

func TestCancellingTheInvitePickerDisarmsIt(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	m, ch := adminModel(t, s)

	// Open the invite picker, walk away from it, then open a DM. The DM picker
	// is the same picker, so an armed invite ran instead of opening the DM.
	press(t, m, "i", "esc")
	press(t, m, "ctrl+n")
	press(t, m, "enter")

	if isMember(t, s, ch.ID, "aaaa1111") {
		t.Error("a cancelled invite must not go through")
	}
	dm, err := s.ChannelByName(store.DMChannelName(ViewerSession, "aaaa1111"))
	if err != nil {
		t.Fatalf("ctrl+n should have opened a DM: %v", err)
	}
	if m.currentChID != dm.ID {
		t.Errorf("the DM should be the open channel, got %d want %d", m.currentChID, dm.ID)
	}
}

func TestInviteAddsTheAgentThePickerHadSelected(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	registerTestAgent(t, s, "bbbb2222", "codex", "reviewer")
	m, ch := adminModel(t, s)

	press(t, m, "i")
	if !m.showPicker {
		t.Fatal("i should open the agent picker")
	}
	press(t, m, "j")
	picked := m.picker.Selected()
	if picked == nil {
		t.Fatal("nothing is selected after moving down")
	}
	press(t, m, "enter")

	if !isMember(t, s, ch.ID, picked.Key) {
		t.Fatalf("%s was picked but is not a member", picked.Name)
	}
	for _, id := range []string{"aaaa1111", "bbbb2222"} {
		if id != picked.Key && isMember(t, s, ch.ID, id) {
			t.Fatalf("%s was invited and was not the one picked", id)
		}
	}
}

// The viewer is already in the channel and is not a candidate to invite.
func TestInviteDoesNotOfferTheViewer(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	m, _ := adminModel(t, s)

	press(t, m, "i")
	for _, item := range m.picker.items {
		if item.Key == store.HumanID {
			t.Fatal("the viewer is in the invite list")
		}
	}
}

func TestRemoveMemberAsksBeforeItRemoves(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	m, ch := adminModel(t, s)
	if err := s.Join(ch.ID, "aaaa1111"); err != nil {
		t.Fatal(err)
	}

	press(t, m, "x", "enter")
	if !m.showPrompt {
		t.Fatal("picking a member should ask for confirmation")
	}
	if !isMember(t, s, ch.ID, "aaaa1111") {
		t.Fatal("the member was removed before the confirmation was answered")
	}

	// Answering no leaves them in.
	press(t, m, "n", "enter")
	if !isMember(t, s, ch.ID, "aaaa1111") {
		t.Fatal("answering no still removed the member")
	}

	press(t, m, "x", "enter", "y", "enter")
	if isMember(t, s, ch.ID, "aaaa1111") {
		t.Fatal("the member should be gone")
	}
}

func TestRemoveMemberDoesNotOfferTheViewer(t *testing.T) {
	s := openTestStore(t)
	registerTestAgent(t, s, "aaaa1111", "claude", "builder")
	m, ch := adminModel(t, s)
	s.Join(ch.ID, "aaaa1111")

	press(t, m, "x")
	for _, item := range m.picker.items {
		if item.Key == store.HumanID {
			t.Fatal("x is for removing other people; leaving is L")
		}
	}
}

func TestLeaveChannelRemovesTheViewer(t *testing.T) {
	s := openTestStore(t)
	m, ch := adminModel(t, s)
	if !isMember(t, s, ch.ID, store.HumanID) {
		t.Fatal("the creator should start as a member")
	}

	press(t, m, "L", "y", "enter")
	if isMember(t, s, ch.ID, store.HumanID) {
		t.Fatal("L should have left the channel")
	}
}

func TestArchiveHidesTheChannelOnlyAfterConfirmation(t *testing.T) {
	s := openTestStore(t)
	m, ch := adminModel(t, s)

	press(t, m, "A", "enter") // confirm defaults to no
	if _, err := s.ChannelByName("general"); err != nil {
		t.Fatalf("an unconfirmed archive removed the channel: %v", err)
	}

	press(t, m, "A", "y", "enter")
	if _, err := s.ChannelByName("general"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the channel should be archived, got %v", err)
	}
	for _, c := range m.channels {
		if c.ID == ch.ID {
			t.Fatal("the archived channel is still in the sidebar")
		}
	}
}

func TestSetTopicShowsInTheFeedHeader(t *testing.T) {
	s := openTestStore(t)
	m, ch := adminModel(t, s)

	press(t, m, "T")
	press(t, m, "ship the release")
	press(t, m, "enter")

	got, err := s.ChannelByName("general")
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != "ship the release" {
		t.Fatalf("topic is %q", got.Topic)
	}
	if m.currentChID != ch.ID {
		t.Fatalf("the test is looking at the wrong channel")
	}
	if view := m.feed.View(); !strings.Contains(view, "ship the release") {
		t.Fatalf("the topic should show in the feed header:\n%s", view)
	}
}

// The prompt is a text field. Every letter that is also a command has to reach
// it, or the channels a human can name are the ones that avoid those letters.
func TestATopicCanContainTheKeysThatAreAlsoCommands(t *testing.T) {
	s := openTestStore(t)
	m, _ := adminModel(t, s)

	press(t, m, "T")
	press(t, m, "quiet? ask first")
	if !m.showPrompt {
		t.Fatal("typing q or ? closed the prompt")
	}
	press(t, m, "enter")

	got, _ := s.ChannelByName("general")
	if got.Topic != "quiet? ask first" {
		t.Fatalf("topic is %q", got.Topic)
	}
}

func TestAccessRulesAreAddedAndRemovedFromOneList(t *testing.T) {
	s := openTestStore(t)
	m, ch := adminModel(t, s)

	press(t, m, "ctrl+r")
	if !m.showPicker {
		t.Fatal("ctrl+r should open the rules list")
	}
	press(t, m, "enter") // the "+ add rule" entry
	press(t, m, "harness:claude")
	press(t, m, "enter")

	rules, err := s.Rules(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Subject != "harness" || rules[0].Value != "claude" {
		t.Fatalf("got rules %v", rules)
	}

	// The same list is where a rule is removed.
	press(t, m, "ctrl+r", "j", "enter", "y", "enter")
	if rules, _ := s.Rules(ch.ID); len(rules) != 0 {
		t.Fatalf("the rule should be gone, got %v", rules)
	}
}

func TestARuleThatIsNotSubjectColonValueIsRefused(t *testing.T) {
	s := openTestStore(t)
	m, ch := adminModel(t, s)

	press(t, m, "ctrl+r", "enter")
	press(t, m, "claude")
	// Not drained through the helper: this one is expected to raise an error.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("a malformed rule should report an error")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Fatal("a malformed rule should report an error")
	}
	if rules, _ := s.Rules(ch.ID); len(rules) != 0 {
		t.Fatalf("nothing should have been written, got %v", rules)
	}
}

// The admin keys act on the sidebar cursor when the sidebar has focus, and on
// the open channel otherwise. Without that, half of them are unreachable from
// where a human actually sits.
func TestAdminKeysActOnTheOpenChannelFromTheFeed(t *testing.T) {
	s := openTestStore(t)
	m, ch := adminModel(t, s)
	second := createTestChannel(t, s, "backend", "channel", "public", store.HumanID)
	drain(t, m, m.reloadChannels())

	m.currentChID = second.ID
	m.currentCh = second.Name
	m.focusedPanel = PanelFeed
	m.updateFocus()

	press(t, m, "T")
	press(t, m, "from the feed")
	press(t, m, "enter")

	got, _ := s.ChannelByName("backend")
	if got.Topic != "from the feed" {
		t.Fatalf("the topic landed on the wrong channel: backend has %q", got.Topic)
	}
	if first, _ := s.ChannelByName("general"); first.Topic != "" {
		t.Fatalf("the sidebar cursor channel was written instead: %q", first.Topic)
	}
	_ = ch
}

// ── helpers ──────────────────────────────────────────────────────────

func tm(unix int64) time.Time {
	return time.Unix(unix, 0)
}
