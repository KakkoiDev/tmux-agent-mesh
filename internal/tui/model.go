package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SessionID is the TUI viewer.
const ViewerSession = store.HumanID

// Panel indices for tab cycling.
const (
	PanelSidebar = iota
	PanelFeed
	PanelCompose
)

// PromptAction tracks what to do when a prompt is confirmed.
type PromptAction int

const (
	ActionNone PromptAction = iota
	ActionCreateChannel
	ActionRenameChannel
	ActionDeleteChannel
	ActionToggleVisibility
	ActionInviteMember
	ActionRenameAgent
)

// Model is the top-level Bubble Tea model for the mesh TUI.
type Model struct {
	store *store.Store

	// Sub-models
	sidebar SidebarModel
	feed    FeedModel
	compose ComposeModel
	detail  DetailModel
	thread  ThreadModel
	search  SearchModel
	picker  PickerModel
	prompt  PromptModel

	// Data
	channels    []store.Channel
	channelIdx  map[int64]store.Channel
	agents      []store.Agent
	currentChID int64
	currentCh   string
	allMessages map[int64][]store.Message // channel_id -> messages

	// Focus
	focusedPanel int

	// View state
	showHelp          bool
	showThread        bool
	showSearch        bool
	showPicker        bool
	showPrompt        bool
	promptAction      PromptAction
	promptCtxID       int64  // channel ID or agent index
	promptChannelName string // temp storage for channel name during creation
	waitingForGG      bool   // second 'g' for gg binding

	// Dimensions
	width  int
	height int

	// Quit
	quitting bool
	err      error
}

// New creates a new TUI model with the given store.
func New(s *store.Store) Model {
	return Model{
		store:        s,
		sidebar:      NewSidebar(),
		feed:         NewFeed(),
		compose:      NewCompose(),
		detail:       NewDetail(),
		thread:       NewThread(),
		search:       NewSearch(),
		picker:       NewPicker(),
		prompt:       NewPrompt(),
		channelIdx:   make(map[int64]store.Channel),
		allMessages:  make(map[int64][]store.Message),
		focusedPanel: PanelFeed,
	}
}

// Init loads initial data.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadChannels, m.loadAgents)
}

func (m Model) loadChannels() tea.Msg {
	channels, err := m.store.Channels()
	if err != nil {
		return errMsg{err}
	}
	return channelsMsg{channels}
}

func (m Model) loadAgents() tea.Msg {
	agents, err := m.store.Roster()
	if err != nil {
		return errMsg{err}
	}
	return agentsMsg{agents}
}

type channelsMsg struct{ channels []store.Channel }
type agentsMsg struct{ agents []store.Agent }
type historyMsg struct {
	channelID int64
	messages  []store.Message
}
type receiptsMsg struct {
	messageID int64
	receipts  []store.Receipt
}
type errMsg struct{ error }
type sentMsg struct{ message store.Message }
type searchResultsMsg struct{ results []SearchResult }
type threadRepliesMsg struct {
	threadID int64
	replies  []store.Message
}

func (e errMsg) Error() string { return e.error.Error() }

// Update handles all messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil

	case tea.KeyMsg:
		// Reset gg on any non-g key
		if msg.String() != "g" {
			m.waitingForGG = false
		}
		return m.handleKey(msg)

	case channelsMsg:
		m.channels = msg.channels
		m.channelIdx = make(map[int64]store.Channel, len(msg.channels))
		for _, ch := range msg.channels {
			m.channelIdx[ch.ID] = ch
		}
		m.updateSidebar()
		if m.currentChID == 0 && len(msg.channels) > 0 {
			m.currentChID = msg.channels[0].ID
			m.currentCh = msg.channels[0].Name
			return m, m.loadHistory(m.currentChID)
		}
		return m, nil

	case agentsMsg:
		m.agents = msg.agents
		m.updateSidebar()
		return m, nil

	case historyMsg:
		m.allMessages[msg.channelID] = msg.messages
		if msg.channelID == m.currentChID {
			m.updateFeed()
		}
		return m, nil

	case sentMsg:
		return m, m.loadHistory(m.currentChID)

	case receiptsMsg:
		if m.detail.visible {
			m.detail.receipts = msg.receipts
		}
		return m, nil

	case searchResultsMsg:
		m.search.SetResults(msg.results)
		return m, nil

	case threadRepliesMsg:
		m.handleThreadReplies(msg)
		return m, nil

	case errMsg:
		m.err = msg.error
		return m, nil
	}

	// Delegate to sub-models
	var cmd tea.Cmd
	switch m.focusedPanel {
	case PanelSidebar:
		m.sidebar, cmd = m.sidebar.Update(msg)
	case PanelFeed:
		m.feed, cmd = m.feed.Update(msg)
	case PanelCompose:
		m.compose, cmd = m.compose.Update(msg)
	}
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit. ctrl+c always quits; 'q' quits everywhere except while
	// composing, where it is a letter to be typed.
	if key == "ctrl+c" || (key == "q" && m.focusedPanel != PanelCompose) {
		if m.showPrompt {
			m.prompt.Deactivate()
			m.showPrompt = false
			m.promptAction = ActionNone
			m.promptChannelName = ""
			return m, nil
		}
		if m.showPicker {
			m.picker.Deactivate()
			m.showPicker = false
			return m, nil
		}
		if m.showSearch {
			m.search.Deactivate()
			m.showSearch = false
			return m, nil
		}
		if m.showThread {
			m.thread.Close()
			m.showThread = false
			return m, nil
		}
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	}

	// Global escape
	if key == "esc" {
		if m.showPrompt {
			m.prompt.Deactivate()
			m.showPrompt = false
			m.promptAction = ActionNone
			return m, nil
		}
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		if m.showThread {
			m.thread.Close()
			m.showThread = false
			return m, nil
		}
		if m.showSearch {
			m.search.Deactivate()
			m.showSearch = false
			m.compose.Focus()
			m.focusedPanel = PanelCompose
			m.updateFocus()
			return m, nil
		}
		if m.showPicker {
			m.picker.Deactivate()
			m.showPicker = false
			return m, nil
		}
		if m.compose.mode != ComposeNormal {
			m.compose.Reset()
			return m, nil
		}
		return m, nil
	}

	// Global toggles
	if key == "?" && m.focusedPanel != PanelCompose {
		m.showHelp = !m.showHelp
		return m, nil
	}

	if key == "tab" {
		m.focusedPanel = (m.focusedPanel + 1) % 3
		m.updateFocus()
		return m, nil
	}

	if key == "shift+tab" {
		m.focusedPanel = (m.focusedPanel + 2) % 3
		m.updateFocus()
		return m, nil
	}

	// Handle overlays first
	if m.showHelp {
		return m, nil
	}
	if m.showPrompt {
		return m.handlePromptKey(msg)
	}
	if m.showPicker {
		return m.handlePickerKey(msg)
	}
	if m.showSearch {
		return m.handleSearchKey(msg)
	}
	if m.showThread {
		return m.handleThreadKey(msg)
	}

	// Toggle sidebar agents — not while composing (letters type).
	if key == "s" && m.focusedPanel != PanelCompose {
		m.sidebar.showAgents = !m.sidebar.showAgents
		return m, nil
	}

	// Toggle detail (not while composing or in sidebar)
	if key == "i" && m.focusedPanel != PanelCompose && m.focusedPanel != PanelSidebar {
		if m.detail.visible {
			m.detail.Hide()
		} else if sel := m.feed.SelectedMessage(); sel != nil {
			receipts, err := m.store.Receipts(sel.ID, ViewerSession)
			if err == nil {
				m.detail.Show(sel, receipts)
			}
		}
		m.resize()
		return m, nil
	}

	// Normal mode keys. When the compose panel has focus, character input
	// goes to the compose input (global actions are handled above);
	// otherwise navigation/action keys are handled in normal mode.
	if m.focusedPanel == PanelCompose {
		return m.handleComposeKey(msg)
	}
	return m.handleNormalKey(msg)
}

// handleComposeKey routes keys to the compose text input. Global keys
// (quit, esc, tab, ?, s, i, ctrl+n, ctrl+k) are handled before this runs.
func (m *Model) handleComposeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.handleEnter()
	case "ctrl+n", "ctrl+k":
		// Global shortcuts stay available while typing.
		return m.handleNormalKey(msg)
	default:
		var cmd tea.Cmd
		m.compose, cmd = m.compose.Update(msg)
		return m, cmd
	}
}

func (m *Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "enter":
		return m.handleEnter()

	case "j", "down":
		return m.handleNav(1)

	case "k", "up":
		return m.handleNav(-1)

	case "g":
		if m.waitingForGG {
			m.waitingForGG = false
			if m.focusedPanel == PanelFeed {
				m.feed.ScrollToTop()
			} else if m.focusedPanel == PanelSidebar {
				m.sidebar.ScrollToTop()
			}
			return m, nil
		}
		m.waitingForGG = true
		return m, nil

	case "G":
		m.waitingForGG = false
		if m.focusedPanel == PanelFeed {
			m.feed.ScrollToBottom()
		} else if m.focusedPanel == PanelSidebar {
			m.sidebar.ScrollToBottom()
		}
		return m, nil

	case "ctrl+d":
		m.waitingForGG = false
		if m.focusedPanel == PanelFeed {
			m.feed.PageDown()
		}
		return m, nil

	case "ctrl+u":
		m.waitingForGG = false
		if m.focusedPanel == PanelFeed {
			m.feed.PageUp()
		}
		return m, nil

	case "r":
		if m.focusedPanel == PanelSidebar {
			// Rename channel
			if ch := m.sidebar.CursorChannel(); ch != nil {
				m.promptAction = ActionRenameChannel
				m.promptCtxID = ch.ID
				m.prompt.ActivateText("Rename channel #"+ch.Name, "New name...")
				m.showPrompt = true
			}
			return m, nil
		}
		if m.focusedPanel == PanelFeed {
			if sel := m.feed.SelectedMessage(); sel != nil {
				m.compose.SetMode(ComposeReply)
				m.compose.replyToID = sel.ID
				m.compose.Focus()
				m.focusedPanel = PanelCompose
				m.updateFocus()
			}
		}
		return m, nil

	case "t":
		if m.focusedPanel == PanelFeed {
			if sel := m.feed.SelectedMessage(); sel != nil {
				return m, m.openThread(sel.ThreadID)
			}
		}
		return m, nil

	case "h":
		// Close thread view
		if m.showThread {
			m.thread.Close()
			m.showThread = false
			return m, nil
		}
		return m, nil

	case "e":
		if m.focusedPanel == PanelFeed {
			if sel := m.feed.SelectedMessage(); sel != nil && sel.IsOwn {
				m.compose.SetMode(ComposeEdit)
				m.compose.editID = sel.ID
				m.compose.SetValue(sel.Body)
				m.compose.Focus()
				m.focusedPanel = PanelCompose
				m.updateFocus()
			}
		}
		return m, nil

	case "d":
		if m.focusedPanel == PanelSidebar {
			// Delete channel
			if ch := m.sidebar.CursorChannel(); ch != nil {
				m.promptAction = ActionDeleteChannel
				m.promptCtxID = ch.ID
				m.prompt.ActivateConfirm("Delete channel #" + ch.Name + "?")
				m.showPrompt = true
			}
			return m, nil
		}
		if m.focusedPanel == PanelFeed {
			if sel := m.feed.SelectedMessage(); sel != nil && sel.IsOwn {
				// Soft-delete would go here when store supports it.
				_ = sel
			}
		}
		return m, nil

	case "c":
		if m.focusedPanel == PanelSidebar {
			m.promptAction = ActionCreateChannel
			m.prompt.ActivateText("Create channel", "Channel name...")
			m.showPrompt = true
		}
		return m, nil

	case "R":
		if m.focusedPanel == PanelSidebar {
			// Rename agent: show agent picker
			m.promptAction = ActionRenameAgent
			m.showPicker = true
			var items []PickerItem
			for _, a := range m.agents {
				if a.SessionID != ViewerSession {
					items = append(items, PickerItem{
						Name:   a.Name(),
						Detail: a.Harness,
					})
				}
			}
			m.picker.Activate(PickerAgent, items)
		}
		return m, nil

	case "p":
		if m.focusedPanel == PanelSidebar {
			if ch := m.sidebar.CursorChannel(); ch != nil {
				m.promptAction = ActionToggleVisibility
				m.promptCtxID = ch.ID
				newVis := "public"
				if ch.Visibility == "public" {
					newVis = "private"
				}
				m.prompt.ActivateConfirm("Make #" + ch.Name + " " + newVis + "?")
				m.showPrompt = true
			}
		}
		return m, nil

	case "i":
		if m.focusedPanel == PanelSidebar {
			// Invite opens the agent picker
			m.promptAction = ActionInviteMember
			m.showPicker = true
			var items []PickerItem
			for _, a := range m.agents {
				if a.SessionID != ViewerSession {
					items = append(items, PickerItem{
						Name:   a.Name(),
						Detail: a.Harness,
					})
				}
			}
			m.picker.Activate(PickerAgent, items)
		}
		return m, nil

	case "ctrl+t":
		if m.focusedPanel == PanelFeed {
			if sel := m.feed.SelectedMessage(); sel != nil {
				return m, m.openThread(sel.ThreadID)
			}
		}
		return m, nil

	case "ctrl+n":
		m.showPicker = true
		var items []PickerItem
		for _, a := range m.agents {
			if a.SessionID != ViewerSession {
				items = append(items, PickerItem{
					Name:   a.Name(),
					Detail: a.Harness,
				})
			}
		}
		m.picker.Activate(PickerAgent, items)
		return m, nil

	case "ctrl+k":
		m.showPicker = true
		var items []PickerItem
		for _, ch := range m.channels {
			if ch.Kind == "channel" {
				items = append(items, PickerItem{
					ID:     ch.ID,
					Name:   ch.Name,
					Detail: fmt.Sprintf("%d members", len(ch.Members)),
				})
			}
		}
		m.picker.Activate(PickerChannel, items)
		return m, nil

	case "/":
		m.showSearch = true
		m.search.Activate()
		m.focusedPanel = PanelCompose
		return m, nil

	default:
		// Reset gg on any other key
		m.waitingForGG = false
	}

	return m, nil
}

func (m *Model) handleEnter() (tea.Model, tea.Cmd) {
	if m.focusedPanel == PanelCompose {
		body := strings.TrimSpace(m.compose.Value())
		if body == "" {
			return m, nil
		}

		switch m.compose.mode {
		case ComposeReply:
			// Send as reply
			m.compose.Reset()
			return m, m.sendMessage(body)
		case ComposeEdit:
			// Edit existing message
			m.compose.Reset()
			// TODO: store.Edit() when implemented
			return m, nil
		default:
			m.compose.Reset()
			return m, m.sendMessage(body)
		}
	}

	if m.focusedPanel == PanelFeed {
		if sel := m.feed.SelectedMessage(); sel != nil {
			return m, m.openThread(sel.ThreadID)
		}
		return m, nil
	}

	if m.focusedPanel == PanelSidebar {
		if ch := m.sidebar.CursorChannel(); ch != nil {
			m.currentChID = ch.ID
			m.currentCh = ch.Name
			m.updateSidebar()
			return m, m.loadHistory(ch.ID)
		}
	}

	return m, nil
}

func (m *Model) handleNav(dir int) (tea.Model, tea.Cmd) {
	switch m.focusedPanel {
	case PanelSidebar:
		if dir > 0 {
			m.sidebar.MoveDown()
		} else {
			m.sidebar.MoveUp()
		}
	case PanelFeed:
		if dir > 0 {
			m.feed.MoveDown()
		} else {
			m.feed.MoveUp()
		}
		if m.detail.visible {
			if sel := m.feed.SelectedMessage(); sel != nil {
				return m, m.loadReceipts(sel.ID)
			}
		}
	}
	m.waitingForGG = false
	return m, nil
}

func (m *Model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc", "ctrl+c":
		m.picker.Deactivate()
		m.showPicker = false
		return m, nil

	case "up", "k":
		m.picker.MoveUp()
		return m, nil

	case "down", "j":
		m.picker.MoveDown()
		return m, nil

	case "enter":
		sel := m.picker.Selected()
		m.picker.Deactivate()
		m.showPicker = false

		if sel == nil {
			return m, nil
		}

		switch m.picker.mode {
		case PickerAgent:
			if m.promptAction == ActionRenameAgent {
				// Find agent by name and prompt for rename
				for _, a := range m.agents {
					if a.Name() == sel.Name && a.SessionID != ViewerSession {
						m.promptCtxID = 0
						// Store session ID in a way we can retrieve — use agents index
						for idx, ag := range m.agents {
							if ag.SessionID == a.SessionID {
								m.promptCtxID = int64(idx)
								break
							}
						}
						m.prompt.ActivateText("Rename agent "+a.Name(), "New name...")
						m.showPrompt = true
						return m, nil
					}
				}
				m.promptAction = ActionNone
				return m, nil
			}
			if m.promptAction == ActionInviteMember {
				// Invite to current channel
				if ch := m.sidebar.CursorChannel(); ch != nil {
					for _, a := range m.agents {
						if a.Name() == sel.Name && a.SessionID != ViewerSession {
							err := m.store.Join(ch.ID, a.SessionID)
							if err == nil {
								// Reload channels
								return m, tea.Batch(
									func() tea.Msg {
										channels, _ := m.store.Channels()
										return channelsMsg{channels}
									},
								)
							}
						}
					}
				}
				m.promptAction = ActionNone
				return m, nil
			}
			// Default: DM
			for _, a := range m.agents {
				if a.Name() == sel.Name && a.SessionID != ViewerSession {
					ch, err := m.store.DMChannel(ViewerSession, a.SessionID)
					if err == nil {
						m.currentChID = ch.ID
						m.currentCh = ch.Name
						// Reload channels and switch
						return m, tea.Batch(
							func() tea.Msg {
								channels, _ := m.store.Channels()
								return channelsMsg{channels}
							},
							m.loadHistory(ch.ID),
						)
					}
				}
			}
		case PickerChannel:
			if sel.ID != 0 {
				m.currentChID = sel.ID
				m.currentCh = sel.Name
				m.updateSidebar()
				return m, m.loadHistory(sel.ID)
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	return m, cmd
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.search.Deactivate()
		m.showSearch = false
		m.compose.Focus()
		m.focusedPanel = PanelCompose
		m.updateFocus()
		return m, nil

	case "up", "k":
		m.search.MoveUp()
		return m, nil

	case "down", "j":
		m.search.MoveDown()
		return m, nil

	case "enter":
		if sel := m.search.SelectedResult(); sel != nil {
			for _, ch := range m.channels {
				if ch.Name == sel.ChannelName {
					m.currentChID = ch.ID
					m.currentCh = ch.Name
					m.updateSidebar()
					m.search.Deactivate()
					m.showSearch = false
					m.compose.Focus()
					m.focusedPanel = PanelCompose
					m.updateFocus()
					return m, m.loadHistory(ch.ID)
				}
			}
		}
		query := m.search.input.Value()
		if query != "" {
			return m, m.doSearch(query)
		}
		return m, nil

	default:
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		query := m.search.input.Value()
		if len(query) >= 2 {
			return m, m.doSearch(query)
		}
		return m, cmd
	}
}

func (m *Model) handleThreadKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc", "h":
		m.thread.Close()
		m.showThread = false
		return m, nil

	case "up", "k":
		m.thread.MoveUp()
		return m, nil

	case "down", "j":
		m.thread.MoveDown()
		return m, nil

	case "r":
		// Reply in thread
		if m.thread.root != nil {
			m.compose.SetMode(ComposeReply)
			m.compose.replyToID = m.thread.root.ID
			m.compose.Focus()
			m.focusedPanel = PanelCompose
			m.updateFocus()
			// Keep thread visible so user sees context while composing
		}
		return m, nil

	case "enter":
		// If compose is focused and thread is showing, Enter sends the reply
		if m.focusedPanel == PanelCompose {
			body := strings.TrimSpace(m.compose.Value())
			if body == "" {
				return m, nil
			}
			m.compose.Reset()
			return m, m.sendMessage(body)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.thread, cmd = m.thread.Update(msg)
	return m, cmd
}

func (m *Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc", "ctrl+c":
		m.prompt.Deactivate()
		m.showPrompt = false
		m.promptAction = ActionNone
		m.promptChannelName = ""
		return m, nil

	case "enter":
		m.prompt.Deactivate()
		m.showPrompt = false
		action := m.promptAction
		m.promptAction = ActionNone
		return m, m.executePromptAction(action, m.prompt.input.Value(), m.prompt.confirmVal)

	case "y":
		if m.prompt.mode == PromptConfirm {
			m.prompt.confirmVal = true
		}
		return m, nil

	case "n":
		if m.prompt.mode == PromptConfirm {
			m.prompt.confirmVal = false
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

func (m *Model) handleThreadReplies(msg threadRepliesMsg) {
	var root MsgView
	var views []MsgView
	for _, msgs := range m.allMessages {
		for _, storeMsg := range msgs {
			if storeMsg.ThreadID == msg.threadID {
				if storeMsg.ID == msg.threadID {
					root = m.msgToView(storeMsg)
				} else {
					views = append(views, m.msgToView(storeMsg))
				}
			}
		}
	}
	m.thread.Open(root, views)
	m.showThread = true
}

// executePromptAction runs the action after a prompt is confirmed.
func (m *Model) executePromptAction(action PromptAction, text string, confirmed bool) tea.Cmd {
	switch action {
	case ActionCreateChannel:
		if m.promptChannelName == "" {
			// First step: got channel name, now ask about visibility
			if text == "" {
				return nil
			}
			m.promptChannelName = text
			m.promptAction = ActionCreateChannel
			m.prompt.ActivateConfirm("Make #" + text + " private?")
			m.showPrompt = true
			return nil
		}
		// Second step: confirmed visibility choice
		name := m.promptChannelName
		m.promptChannelName = ""
		visibility := "public"
		if confirmed {
			visibility = "private"
		}
		return m.createChannel(name, visibility)

	case ActionRenameChannel:
		if text == "" {
			return nil
		}
		return m.renameChannel(m.promptCtxID, text)

	case ActionDeleteChannel:
		if !confirmed {
			return nil
		}
		return m.deleteChannel(m.promptCtxID)

	case ActionToggleVisibility:
		if !confirmed {
			return nil
		}
		return m.toggleVisibility(m.promptCtxID)

	case ActionInviteMember:
		// handled via picker, not prompt
		return nil

	case ActionRenameAgent:
		if text == "" {
			return nil
		}
		agentIdx := int(m.promptCtxID)
		if agentIdx >= 0 && agentIdx < len(m.agents) {
			return m.renameAgent(m.agents[agentIdx].SessionID, text)
		}
		return nil
	}
	return nil
}

func (m Model) createChannel(name, visibility string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.store.CreateChannel(name, "channel", visibility, "", ViewerSession)
		if err != nil {
			return errMsg{err}
		}
		channels, _ := m.store.Channels()
		return channelsMsg{channels}
	}
}

func (m Model) renameChannel(channelID int64, newName string) tea.Cmd {
	return func() tea.Msg {
		err := m.store.RenameChannel(channelID, newName)
		if err != nil {
			return errMsg{err}
		}
		channels, _ := m.store.Channels()
		return channelsMsg{channels}
	}
}

func (m Model) deleteChannel(channelID int64) tea.Cmd {
	return func() tea.Msg {
		err := m.store.ArchiveChannel(channelID)
		if err != nil {
			return errMsg{err}
		}
		channels, _ := m.store.Channels()
		return channelsMsg{channels}
	}
}

func (m Model) toggleVisibility(channelID int64) tea.Cmd {
	return func() tea.Msg {
		ch, ok := m.channelIdx[channelID]
		if !ok {
			return errMsg{fmt.Errorf("channel not found")}
		}
		newVis := "public"
		if ch.Visibility == "public" {
			newVis = "private"
		}
		err := m.store.SetChannelVisibility(channelID, newVis)
		if err != nil {
			return errMsg{err}
		}
		channels, _ := m.store.Channels()
		return channelsMsg{channels}
	}
}

func (m Model) renameAgent(sessionID, newName string) tea.Cmd {
	return func() tea.Msg {
		err := m.store.SetAlias(sessionID, newName)
		if err != nil {
			return errMsg{err}
		}
		agents, _ := m.store.Roster()
		return agentsMsg{agents}
	}
}

func (m *Model) resize() {
	sidebarW := m.width * 25 / 100; if sidebarW < 18 { sidebarW = 18 }; if sidebarW > 30 { sidebarW = 30 }
	detailW := 0
	if m.detail.visible {
		detailW = 30
	}
	feedW := m.width - sidebarW - detailW
	if feedW < 20 {
		feedW = 20
	}

	composeH := 3
	feedH := m.height - composeH

	m.sidebar.SetSize(sidebarW, feedH)
	m.feed.SetSize(feedW, feedH)
	// The compose bar spans the full terminal width (sidebar + feed + detail).
	m.compose.SetSize(m.width)
	m.detail.SetSize(detailW, feedH)
	m.thread.SetSize(m.width, m.height)
	m.search.SetSize(m.width, m.height)
	m.picker.SetSize(m.width, m.height)
	m.prompt.SetSize(m.width, m.height)
}

func (m *Model) updateFocus() {
	m.sidebar.focused = m.focusedPanel == PanelSidebar
	m.feed.focused = m.focusedPanel == PanelFeed
	if m.focusedPanel == PanelCompose {
		m.compose.Focus()
		m.compose.SetHint("enter: send  shift+enter: newline  esc: feed  tab: sidebar")
	} else if m.focusedPanel == PanelSidebar {
		m.compose.Blur()
		m.compose.SetHint("j/k: navigate  space: mark read  d: delete channel  c: create  r: rename  R: rename agent  p: toggle private  i: invite  tab: feed")
	} else if m.focusedPanel == PanelFeed {
		m.compose.Blur()
		m.compose.SetHint("j/k: scroll  enter/t: thread  r: reply  i: detail  /: search  tab: sidebar")
	}
}

func (m *Model) updateSidebar() {
	var chViews []ChannelView
	for _, ch := range m.channels {
		unread := 0
		if msgs, ok := m.allMessages[ch.ID]; ok {
			for _, msg := range msgs {
				receipts, _ := m.store.Receipts(msg.ID, ViewerSession)
				if len(receipts) == 0 {
					unread++
				}
			}
		}
		chViews = append(chViews, ChannelView{
			ID:          ch.ID,
			Name:        ch.Name,
			Kind:        ch.Kind,
			Visibility:  ch.Visibility,
			Unread:      unread,
			MemberCount: len(ch.Members),
			Selected:    ch.ID == m.currentChID,
		})
	}
	m.sidebar.SetChannels(chViews)

	var agentViews []AgentView
	for _, a := range m.agents {
		agentViews = append(agentViews, AgentView{
			SessionID: a.SessionID,
			Name:      a.Name(),
			TurnState: a.TurnState,
			Pending:   a.Pending,
			Dead:      false,
		})
	}
	m.sidebar.SetAgents(agentViews)
}

func (m *Model) updateFeed() {
	msgs, ok := m.allMessages[m.currentChID]
	if !ok {
		m.feed.SetMessages(nil)
		return
	}

	var views []MsgView
	for _, msg := range msgs {
		views = append(views, m.msgToView(msg))
	}
	m.feed.SetMessages(views)
	m.feed.channelName = m.currentCh
}

func (m *Model) msgToView(msg store.Message) MsgView {
	return MsgView{
		ID:          msg.ID,
		From:        msg.From,
		FromName:    msg.FromName,
		Body:        msg.Body,
		Timestamp:   time.Unix(msg.CreatedAt, 0),
		Hops:        msg.Hops,
		ExpectReply: msg.ExpectReply,
		ReplyToID:   msg.ReplyToID,
		ThreadID:    msg.ThreadID,
		IsOwn:       msg.From == ViewerSession,
		IsSystem:    strings.HasPrefix(msg.Body, "---"),
		Delivered:   true,
		Pending:     false,
	}
}

func (m Model) loadHistory(channelID int64) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.store.History(channelID, ViewerSession, 200)
		if err != nil {
			return errMsg{err}
		}
		return historyMsg{channelID: channelID, messages: msgs}
	}
}

func (m Model) loadReceipts(messageID int64) tea.Cmd {
	return func() tea.Msg {
		receipts, err := m.store.Receipts(messageID, ViewerSession)
		if err != nil {
			return errMsg{err}
		}
		return receiptsMsg{messageID: messageID, receipts: receipts}
	}
}

func (m Model) sendMessage(body string) tea.Cmd {
	replyToID := m.compose.replyToID
	isReply := m.compose.mode == ComposeReply
	return func() tea.Msg {
		post := store.Post{
			ChannelID: m.currentChID,
			From:      ViewerSession,
			Body:      body,
		}
		// If replying, set thread linkage
		if isReply && replyToID != 0 {
			for _, msgs := range m.allMessages {
				for _, storeMsg := range msgs {
					if storeMsg.ID == replyToID {
						post.ThreadID = storeMsg.ThreadID
						post.ReplyToID = replyToID
						break
					}
				}
			}
		}
		msg, err := m.store.Send(post, store.DefaultCaps())
		if err != nil {
			return errMsg{err}
		}
		// Mark own message as read
		_ = m.store.MarkRead(msg.ID, ViewerSession)
		return sentMsg{msg}
	}
}

func (m Model) doSearch(query string) tea.Cmd {
	return func() tea.Msg {
		var results []SearchResult
		for _, msgs := range m.allMessages {
			for _, msg := range msgs {
				if strings.Contains(strings.ToLower(msg.Body), strings.ToLower(query)) {
					ch, ok := m.channelIdx[msg.ChannelID]
					chName := ""
					if ok {
						chName = ch.Name
					}
					results = append(results, SearchResult{
						ID:          msg.ID,
						ChannelName: chName,
						FromName:    msg.FromName,
						Body:        msg.Body,
						CreatedAt:   msg.CreatedAt,
					})
				}
			}
		}
		return searchResultsMsg{results}
	}
}

func (m Model) openThread(threadID int64) tea.Cmd {
	return func() tea.Msg {
		var replies []store.Message
		for _, msgs := range m.allMessages {
			for _, msg := range msgs {
				if msg.ThreadID == threadID && msg.ID != threadID {
					replies = append(replies, msg)
				}
			}
		}
		return threadRepliesMsg{threadID: threadID, replies: replies}
	}
}

// View renders everything.
func (m *Model) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}

	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	if m.showHelp {
		return HelpView(m.width, m.height)
	}

	// Full-screen overlays
	if m.showPrompt && m.prompt.active {
		return m.prompt.View()
	}
	if m.showThread && m.thread.active {
		return m.thread.View()
	}
	if m.showSearch && m.search.active {
		return m.search.View()
	}
	if m.showPicker && m.picker.active {
		return m.picker.View()
	}

	// Main layout
	composeH := 3
	feedH := m.height - composeH
	if feedH < 0 {
		feedH = 0
	}

	sidebarW := m.width * 25 / 100; if sidebarW < 18 { sidebarW = 18 }; if sidebarW > 30 { sidebarW = 30 }
	detailW := 0
	if m.detail.visible {
		detailW = 30
	}
	feedW := m.width - sidebarW - detailW
	if feedW < 20 {
		feedW = 20
	}

	sidebar := m.sidebar.View()
	feed := m.feed.View()
	compose := m.compose.View()
	detail := m.detail.View()

	// Arrange panels: sidebar + feed side by side, then the detail panel
	// joined to the feed's right (a plain concatenation would append detail
	// below the grid and get truncated by heightLimit).
	topContent := joinHorizontal(sidebarW, sidebar, feedW, feed)
	if detailW > 0 && detail != "" {
		topContent = joinHorizontal(sidebarW+feedW, topContent, detailW, detail)
	}
	topContent = heightLimit(feedH, topContent)

	return topContent + "\n" + compose
}

// joinHorizontal joins two strings side by side, padding the first to w1.
func joinHorizontal(w1 int, s1 string, w2 int, s2 string) string {
	lines1 := strings.Split(s1, "\n")
	lines2 := strings.Split(s2, "\n")
	maxLines := len(lines1)
	if len(lines2) > maxLines {
		maxLines = len(lines2)
	}

	var out strings.Builder
	for i := 0; i < maxLines; i++ {
		var left, right string
		if i < len(lines1) {
			left = lines1[i]
		}
		if i < len(lines2) {
			right = lines2[i]
		}
		leftWidth := lipgloss.Width(left)
		if leftWidth < w1 {
			left += strings.Repeat(" ", w1-leftWidth)
		} else if leftWidth > w1 {
			left = strLimit(left, w1)
		}
		out.WriteString(left)
		out.WriteString(right)
		if i < maxLines-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}

func heightLimit(h int, s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func strLimit(s string, w int) string {
	runes := []rune(s)
	if len(runes) <= w {
		return s
	}
	return string(runes[:w])
}
