package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
	tea "github.com/charmbracelet/bubbletea"
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
		{ID: 3, Name: "dm-abc123-xyz456", Kind: "dm", Visibility: "private", MemberCount: 2},
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
	// DM should strip dm- prefix
	if strings.Contains(view, "dm-abc123-xyz456") {
		t.Error("sidebar should strip dm- prefix from DM names")
	}
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

func TestFeedScrollNavigation(t *testing.T) {
	fm := NewFeed()
	fm.SetSize(80, 20)

	now := tm(1000)
	var msgs []MsgView
	for i := 0; i < 10; i++ {
		msgs = append(msgs, MsgView{ID: int64(i), From: "agent1", FromName: "a", Body: "msg", Timestamp: now, ThreadID: int64(i), Delivered: true})
	}
	fm.SetMessages(msgs)

	// Move down
	fm.MoveDown()
	if fm.cursor != 1 {
		t.Fatalf("cursor should be 1 after MoveDown, got %d", fm.cursor)
	}

	// Move down several times
	for i := 0; i < 8; i++ {
		fm.MoveDown()
	}
	if fm.cursor != 9 {
		t.Fatalf("cursor should be 9, got %d", fm.cursor)
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
	if sel == nil || sel.ID != 42 {
		t.Fatalf("SelectedMessage should return first msg, got %v", sel)
	}

	fm.MoveDown()
	sel = fm.SelectedMessage()
	if sel == nil || sel.ID != 43 {
		t.Fatalf("SelectedMessage should return second msg, got %v", sel)
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

// ── helpers ──────────────────────────────────────────────────────────

func tm(unix int64) time.Time {
	return time.Unix(unix, 0)
}
