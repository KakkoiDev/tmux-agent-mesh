package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentView is the display representation of an agent in the roster.
type AgentView struct {
	SessionID string
	Name      string
	TurnState string
	Pending   int
	Dead      bool
}

// ChannelView is the display representation of a channel.
type ChannelView struct {
	ID          int64
	Name        string
	Kind        string
	Visibility  string
	Unread      int
	MemberCount int
	Selected    bool
}

// SidebarModel is the channel list + agent roster panel.
type SidebarModel struct {
	channels      []ChannelView
	agents        []AgentView
	cursor        int
	viewport      viewport.Model
	width         int
	height        int
	focused       bool
	showAgents    bool
}

func NewSidebar() SidebarModel {
	vp := viewport.New(20, 40)
	return SidebarModel{
		viewport:   vp,
		showAgents: true,
	}
}

func (m *SidebarModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = height
}

func (m *SidebarModel) SetChannels(channels []ChannelView) {
	m.channels = channels
}

func (m *SidebarModel) SetAgents(agents []AgentView) {
	m.agents = agents
}

func (m *SidebarModel) CursorChannel() *ChannelView {
	if m.cursor >= 0 && m.cursor < len(m.channels) {
		return &m.channels[m.cursor]
	}
	return nil
}

func (m *SidebarModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *SidebarModel) MoveDown() {
	max := len(m.channels) - 1
	if m.showAgents {
		max += len(m.agents) + 2 // +2 for spacer and header
	}
	if m.cursor < max && m.cursor < len(m.channels)-1 {
		m.cursor++
	}
}

func (m *SidebarModel) Update(msg tea.Msg) (SidebarModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return *m, cmd
}

func (m *SidebarModel) View() string {
	var b strings.Builder

	// Channels header
	b.WriteString(SidebarTitleStyle.Render("CHANNELS"))
	b.WriteString("\n")

	for i, ch := range m.channels {
		line := m.renderChannel(ch, i == m.cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}

	if m.showAgents {
		b.WriteString("\n")
		b.WriteString(SidebarTitleStyle.Render("AGENTS"))
		b.WriteString("\n")

		for i, a := range m.agents {
			line := m.renderAgent(a)
			b.WriteString(line)
			b.WriteString("\n")
			_ = i
		}
	}

	content := b.String()
	m.viewport.SetContent(content)

	style := SidebarStyle.Width(m.width).Height(m.height)
	if m.focused {
		style = style.BorderForeground(highlight)
	}
	return style.Render(m.viewport.View())
}

func (m *SidebarModel) renderChannel(ch ChannelView, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}

	name := ch.Name
	if ch.Kind == "dm" {
		name = "@" + name[3:] // strip "dm-" prefix for display
		if len(name) > 14 {
			name = name[:14]
		}
	} else {
		name = "#" + name
	}

	var parts []string
	parts = append(parts, prefix+name)

	if ch.Unread > 0 {
		parts = append(parts, ChannelUnreadStyle.Render(fmt.Sprintf("(%d)", ch.Unread)))
	}
	if ch.Visibility == "private" {
		parts = append(parts, ChannelPrivateStyle.Render("[L]"))
	}

	line := strings.Join(parts, " ")

	// Add member count
	if ch.Kind == "channel" && ch.MemberCount > 0 {
		memberStr := fmt.Sprintf("%d", ch.MemberCount)
		line += strings.Repeat(" ", max(0, m.width-lipgloss.Width(line)-lipgloss.Width(memberStr)-2))
		line += ChannelPrivateStyle.Render(memberStr)
	}

	if selected && m.focused {
		return ChannelSelectedStyle.Render(line)
	}
	return ChannelItemStyle.Render(line)
}

func (m *SidebarModel) renderAgent(a AgentView) string {
	dot := AgentDot(a)
	name := Truncate(a.Name, 16)
	line := fmt.Sprintf("  %s %s", dot, name)
	return AgentItemStyle.Render(line)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
