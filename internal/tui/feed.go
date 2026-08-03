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
	messages     []MsgView
	viewport     viewport.Model
	width        int
	height       int
	focused      bool
	cursor       int // index into messages
	channelName  string
	channelTopic string
	memberCount  int
	showThread   bool
	threadMsgs   []MsgView
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
	// FeedStyle chrome is two columns of horizontal padding and no border. It
	// used to subtract three, which left the feed one column short of its
	// share of the terminal and the row's last cell showing through.
	m.viewport.Width = max(0, width-2)
	m.viewport.Height = height
}

func (m *FeedModel) SetMessages(msgs []MsgView) {
	// Stick to the bottom while the cursor is already there, which is where a
	// chat is read from. Once the reader has scrolled up, new traffic must not
	// yank them back down.
	atBottom := len(m.messages) == 0 || m.cursor >= len(m.messages)-1
	m.messages = msgs
	if atBottom {
		m.cursor = max(0, len(msgs)-1)
	}
	if m.cursor >= len(msgs) {
		m.cursor = max(0, len(msgs)-1)
	}
}

// SetChannel records what the header shows: the name, its topic and how many
// members it has. The member count lives here rather than in the sidebar row,
// where it was indistinguishable from the unread count.
func (m *FeedModel) SetChannel(name, topic string, members int) {
	m.channelName = name
	m.channelTopic = topic
	m.memberCount = members
}

func (m *FeedModel) SelectedMessage() *MsgView {
	if m.cursor >= 0 && m.cursor < len(m.messages) {
		return &m.messages[m.cursor]
	}
	return nil
}

// The cursor is an index into the messages; the viewport offset is a line
// count. They used to be set from each other, so a feed of three-line messages
// scrolled at a third of the speed the cursor moved and the selected message
// left the screen. View does the conversion now, because that is where the
// rendered line each message starts on is known.
func (m *FeedModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *FeedModel) MoveDown() {
	if m.cursor < len(m.messages)-1 {
		m.cursor++
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

func (m *FeedModel) PageDown() {
	page := m.viewport.Height
	if page < 1 {
		page = 10
	}
	m.cursor = min(m.cursor+page, len(m.messages)-1)
}

func (m *FeedModel) PageUp() {
	page := m.viewport.Height
	if page < 1 {
		page = 10
	}
	m.cursor = max(0, m.cursor-page)
}

func (m *FeedModel) Update(msg tea.Msg) (FeedModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return *m, cmd
}

func (m *FeedModel) View() string {
	msgs := m.messages
	if m.showThread {
		msgs = m.threadMsgs
	}

	// The header names the channel, so it stays on the first row whatever the
	// messages do. Only the messages are bottom-anchored.
	var header []string
	if m.channelName != "" {
		header = append(header, m.headerLine())
	}

	var lines []string

	// Where the selected message sits, in rendered lines rather than in
	// messages, so the viewport can be scrolled to keep it whole.
	top, bottom := 0, 0
	for i, msg := range msgs {
		rendered := strings.Split(
			strings.TrimRight(m.renderMessage(msg, i == m.cursor && m.focused), "\n"), "\n")
		if i == m.cursor {
			top = len(lines)
			bottom = len(lines) + len(rendered) - 1
		}
		lines = append(lines, rendered...)
		lines = append(lines, "")
	}

	// A chat is read from the bottom: with less to show than there is room
	// for, the blank rows belong above the first message, not below the last.
	if pad := m.viewport.Height - len(header) - len(lines); pad > 0 {
		lines = append(make([]string, pad), lines...)
		top += pad
		bottom += pad
	}

	lines = append(header, lines...)
	top += len(header)
	bottom += len(header)

	m.viewport.SetContent(strings.Join(lines, "\n"))

	offset := m.viewport.YOffset
	if bottom >= offset+m.viewport.Height {
		offset = bottom - m.viewport.Height + 1
	}
	if top < offset {
		offset = top
	}
	m.viewport.SetYOffset(offset)

	style := FeedStyle.Height(m.height)
	if m.focused {
		style = style.BorderForeground(accent)
	}
	// No .Width(): the viewport pads every row to its own width, so the block
	// sizes to padding(2)+viewport (=m.width).
	return style.Render(m.viewport.View())
}

// headerLine names the channel, how many members it has and its topic. The
// member count is here because the sidebar row has no room to say which of two
// numbers it is showing.
func (m *FeedModel) headerLine() string {
	head := MessageHeaderStyle.Render(m.channelName)
	var tail []string
	if m.memberCount > 0 {
		tail = append(tail, fmt.Sprintf("%d members", m.memberCount))
	}
	if m.channelTopic != "" {
		tail = append(tail, m.channelTopic)
	}
	if len(tail) == 0 {
		return head
	}
	return head + MessageTimestampStyle.Render("  ·  "+strings.Join(tail, "  ·  "))
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

	// Body, indented and wrapped here rather than left to the viewport: the
	// caller counts the lines a message renders to, so a body that grew an
	// extra row on the way to the screen would put the cursor on the wrong one.
	body := MessageBodyStyle
	if w := m.viewport.Width; w > 2 {
		body = body.Width(w)
	}
	for _, line := range strings.Split(msg.Body, "\n") {
		b.WriteString(body.Render(line))
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
