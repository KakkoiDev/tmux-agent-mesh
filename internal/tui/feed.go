package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// MsgView is the display representation of a message.
type MsgView struct {
	ID          int64
	From        string
	FromName    string
	Body        string
	Timestamp   time.Time
	Hops        int
	ExpectReply bool
	ReplyToID   int64
	ThreadID    int64
	IsOwn       bool
	IsSystem    bool
	Delivered   bool
	Read        bool
	Pending     bool
	Selected    bool
}

// FeedModel is the scrollable message area.
type FeedModel struct {
	messages    []MsgView
	viewport    viewport.Model
	width       int
	height      int
	focused     bool
	cursor      int // index into messages
	channelName string
	showThread  bool
	threadMsgs  []MsgView
}

func NewFeed() FeedModel {
	vp := viewport.New(80, 20)
	return FeedModel{
		viewport: vp,
	}
}

func (m *FeedModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width - 2
	m.viewport.Height = height
}

func (m *FeedModel) SetMessages(msgs []MsgView) {
	m.messages = msgs
	if m.cursor >= len(msgs) {
		m.cursor = max(0, len(msgs)-1)
	}
}

func (m *FeedModel) SelectedMessage() *MsgView {
	if m.cursor >= 0 && m.cursor < len(m.messages) {
		return &m.messages[m.cursor]
	}
	return nil
}

func (m *FeedModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
		m.viewport.SetYOffset(m.cursor)
	}
}

func (m *FeedModel) MoveDown() {
	if m.cursor < len(m.messages)-1 {
		m.cursor++
		m.viewport.SetYOffset(max(0, m.cursor-m.viewport.Height+3))
	}
}

func (m *FeedModel) ScrollToTop() {
	m.cursor = 0
	m.viewport.GotoTop()
}

func (m *FeedModel) ScrollToBottom() {
	if len(m.messages) > 0 {
		m.cursor = len(m.messages) - 1
	}
	m.viewport.GotoBottom()
}

func (m *FeedModel) Update(msg tea.Msg) (FeedModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return *m, cmd
}

func (m *FeedModel) View() string {
	var b strings.Builder

	// Channel name header
	if m.channelName != "" {
		header := MessageHeaderStyle.Render(m.channelName)
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(strings.Repeat("━", m.width-2))
		b.WriteString("\n")
	}

	msgs := m.messages
	if m.showThread {
		msgs = m.threadMsgs
	}

	for i, msg := range msgs {
		line := m.renderMessage(msg, i == m.cursor && m.focused)
		b.WriteString(line)
		b.WriteString("\n")
	}

	content := b.String()
	m.viewport.SetContent(content)

	style := FeedStyle.Width(m.width).Height(m.height)
	if m.focused {
		style = style.BorderForeground(accent)
	}
	return style.Render(m.viewport.View())
}

func (m *FeedModel) renderMessage(msg MsgView, selected bool) string {
	var b strings.Builder

	// Selection marker
	cursor := " "
	if selected {
		cursor = MessageCursorStyle.Render(">")
	}

	// Timestamp
	ts := msg.Timestamp.Format("15:04")

	// Sender
	sender := msg.FromName
	if msg.IsOwn {
		sender = "you"
	}

	// System messages dimmed
	if msg.IsSystem {
		b.WriteString(SystemMessageStyle.Render(
			fmt.Sprintf("%s %s  %s  %s", cursor, ts, sender, msg.Body),
		))
		return b.String()
	}

	// Normal message
	header := fmt.Sprintf("%s %s  %s", cursor, ts, sender)
	if msg.IsOwn {
		b.WriteString(MessageOwnStyle.Render(header))
	} else {
		b.WriteString(MessageHeaderStyle.Render(header))
	}
	b.WriteString("\n")

	// Body (indented)
	bodyLines := strings.Split(msg.Body, "\n")
	for _, line := range bodyLines {
		b.WriteString(MessageBodyStyle.Render(line))
		b.WriteString("\n")
	}

	// Read receipts
	var parts []string
	icon := ReceiptIcon(msg.Delivered, msg.Read, msg.Pending)
	if icon != "" {
		parts = append(parts, icon)
	}
	if msg.ExpectReply {
		parts = append(parts, "(expects reply)")
	}
	if msg.Hops > 0 {
		parts = append(parts, fmt.Sprintf("hop %d", msg.Hops))
	}
	if len(parts) > 0 {
		receiptLine := MessageTimestampStyle.Render(strings.Join(parts, " · "))
		b.WriteString(MessageBodyStyle.Render(receiptLine))
		b.WriteString("\n")
	}

	return b.String()
}

// ThreadView returns the rendered thread for a given root message.
func (m *FeedModel) ThreadView(root MsgView, replies []MsgView) string {
	var b strings.Builder

	// Root message
	b.WriteString(MessageHeaderStyle.Render("Thread"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("━", m.width-2))
	b.WriteString("\n\n")

	b.WriteString(m.renderMessage(root, false))
	b.WriteString("\n")

	// Replies with indentation
	for _, reply := range replies {
		rendered := m.renderMessage(reply, false)
		indented := ThreadStyle.Render(rendered)
		b.WriteString(indented)
		b.WriteString("\n")
	}

	return b.String()
}
