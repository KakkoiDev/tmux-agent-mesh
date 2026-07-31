package tui

import (
	"fmt"
	"strings"

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
// sidebarChrome is the column budget consumed by SidebarStyle borders (2)
// plus horizontal padding (2); row renderers subtract it.
const sidebarChrome = 4

type SidebarModel struct {
	channels   []ChannelView
	agents     []AgentView
	cursor     int
	width      int
	height     int
	focused    bool
	showAgents bool
}

func NewSidebar() SidebarModel {
	return SidebarModel{
		showAgents: true,
	}
}

func (m *SidebarModel) SetSize(width, height int) {
	m.width = width
	m.height = height
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
	return *m, nil
}

func (m *SidebarModel) View() string {
	var b strings.Builder

	// Channels header with separator
	b.WriteString(SidebarTitleStyle.Render("CHANNELS"))
	b.WriteString("\n")
	sep := strings.Repeat("─", max(0, m.width-sidebarChrome))
	b.WriteString(SidebarSeparatorStyle.Render(sep))
	b.WriteString("\n")

	// Channel rows — clamp to available height
	avail := m.height - 1 // minus the header + separator line already rendered
	agentSection := 0
	if m.showAgents {
		agentSection = 2 + len(m.agents) // blank line + MEMBERS header + agent rows
	}
	maxCh := avail - 1 - agentSection // -1 spare for header
	if maxCh < 0 {
		maxCh = 0
	}
	if maxCh > len(m.channels) {
		maxCh = len(m.channels)
	}

	for i := 0; i < maxCh; i++ {
		ch := m.channels[i]
		atCursor := m.focused && i == m.cursor
		line := m.renderChannel(ch, atCursor, ch.Selected && !atCursor)
		b.WriteString(line)
		b.WriteString("\n")
	}

	if m.showAgents {
		b.WriteString("\n")
		b.WriteString(SidebarTitleStyle.Render("MEMBERS"))
		b.WriteString("\n")

		for _, a := range m.agents {
			line := m.renderAgent(a)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	content := b.String()

	// Pad to height so the block fills its column
	lines := strings.Split(content, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}

	style := SidebarStyle
	if m.focused {
		style = style.Background(focusBg)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m *SidebarModel) renderChannel(ch ChannelView, cursor, active bool) string {
	prefix := " "
	if cursor {
		prefix = "> "
	}

	name := ch.Name
	if ch.Kind == "dm" {
		name = "@" + name[3:] // strip "dm-" prefix for display
	} else {
		name = "#" + name
	}

	// Build the trailing markers first so the name can be truncated to fit.
	var suffix []string
	if ch.Unread > 0 {
		suffix = append(suffix, fmt.Sprintf("(%d)", ch.Unread))
	}
	if ch.Visibility == "private" {
		suffix = append(suffix, "[L]")
	}
	suffixStr := ""
	if len(suffix) > 0 {
		suffixStr = " " + strings.Join(suffix, " ")
	}

	// Content width: sidebar width minus borders and horizontal padding.
	contentW := m.width - sidebarChrome
	avail := contentW - len(prefix) - lipgloss.Width(suffixStr)
	if avail < 1 {
		name = ""
	} else if len([]rune(name)) > avail {
		name = Truncate(name, avail)
	}

	line := prefix + name + suffixStr

	if cursor {
		return ChannelSelectedStyle.Render(line)
	}
	if active {
		return ChannelActiveStyle.Render(line)
	}
	return ChannelItemStyle.Render(line)
}

func (m *SidebarModel) renderAgent(a AgentView) string {
	dot := AgentDot(a)
	name := Truncate(a.Name, max(1, m.width-sidebarChrome-3))
	line := fmt.Sprintf(" %s %s", dot, name)
	return AgentItemStyle.Render(line)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
