package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ThreadModel is the thread overlay.
type ThreadModel struct {
	active   bool
	root     *MsgView
	replies  []MsgView
	viewport viewport.Model
	cursor   int
	width    int
	height   int
}

func NewThread() ThreadModel {
	vp := viewport.New(60, 20)
	return ThreadModel{
		viewport: vp,
	}
}

func (m *ThreadModel) Open(root MsgView, replies []MsgView) {
	m.active = true
	m.root = &root
	m.replies = replies
	m.cursor = 0
}

func (m *ThreadModel) Close() {
	m.active = false
	m.root = nil
	m.replies = nil
}

func (m *ThreadModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width - 6
	m.viewport.Height = height - 4
}

func (m *ThreadModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *ThreadModel) MoveDown() {
	if m.cursor < len(m.replies) {
		m.cursor++
	}
}

func (m *ThreadModel) Update(msg tea.Msg) (ThreadModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return *m, cmd
}

func (m *ThreadModel) View() string {
	if !m.active || m.root == nil {
		return ""
	}

	box := OverlayStyle.Width(m.width - 4).Height(m.height - 2)

	var b strings.Builder

	title := "Thread #" + fmt.Sprintf("%d", m.root.ThreadID)
	b.WriteString(MessageHeaderStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width-8))
	b.WriteString("\n\n")

	// Root message pinned
	b.WriteString(m.renderMsg(*m.root, false))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("·", m.width-8))
	b.WriteString("\n\n")

	// Replies
	for i, reply := range m.replies {
		rendered := m.renderMsg(reply, i == m.cursor)
		indented := ThreadStyle.Render(rendered)
		b.WriteString(indented)
		b.WriteString("\n")
	}

	m.viewport.SetContent(b.String())
	rendered := box.Render(m.viewport.View())

	return PlaceOverlay(m.width, m.height, rendered)
}

func (m *ThreadModel) renderMsg(msg MsgView, selected bool) string {
	cursor := " "
	if selected {
		cursor = ">"
	}
	ts := msg.Timestamp.Format("15:04:05")
	sender := msg.FromName
	if msg.IsOwn {
		sender = "you"
	}

	return fmt.Sprintf("%s %s  %s\n%s",
		cursor,
		MessageHeaderStyle.Render(sender),
		MessageTimestampStyle.Render(ts),
		MessageBodyStyle.Render(msg.Body),
	)
}
