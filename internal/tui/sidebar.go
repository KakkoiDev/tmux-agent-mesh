package tui

import (
	"fmt"
	"strings"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
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
	ID int64
	// Name is the row in the channels table. Label is what the row shows: for a
	// channel the two are the same, for a DM the label is who is on the other
	// end, which the sidebar cannot work out from "dm:<session>:<session>".
	Name        string
	Label       string
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
	channels       []ChannelView
	agents         []AgentView
	channelMembers []store.MemberInfo
	cursor         int
	width          int
	height         int
	focused        bool
	showAgents     bool
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

func (m *SidebarModel) SetChannelMembers(members []store.MemberInfo) {
	m.channelMembers = members
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
	if m.cursor < len(m.channels)-1 {
		m.cursor++
	}
}

func (m *SidebarModel) ScrollToTop() {
	m.cursor = 0
}

func (m *SidebarModel) ScrollToBottom() {
	if len(m.channels) > 0 {
		m.cursor = len(m.channels) - 1
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
	// contentW is the usable width inside borders+padding. It subtracted two
	// more than the chrome costs, so the rule under CHANNELS stopped two
	// columns short of the rows beneath it.
	contentW := max(0, m.width-sidebarChrome)
	sep := strings.Repeat("─", contentW)
	b.WriteString(SidebarSeparatorStyle.Render(sep))
	b.WriteString("\n")

	// Channel rows — clamp to available height
	avail := m.height - 1 // minus the header + separator line already rendered
	membersSection := 0
	if len(m.channelMembers) > 0 {
		membersSection = 2 + len(m.channelMembers) // blank line + MEMBERS header + rows
	}
	agentSection := 0
	if m.showAgents {
		agentSection = 2 + len(m.agents) // blank line + AGENTS header + agent rows
	}
	maxCh := avail - 1 - membersSection - agentSection // -1 spare for header
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

	// The members of the open channel. This block used to be unlabelled and sat
	// above a roster titled MEMBERS, so the two lists read as one list with the
	// wrong heading on it.
	if len(m.channelMembers) > 0 {
		b.WriteString("\n")
		b.WriteString(SidebarTitleStyle.Render("MEMBERS"))
		b.WriteString("\n")
		for _, mem := range m.channelMembers {
			b.WriteString(m.renderMember(mem))
			b.WriteString("\n")
		}
	}

	// Everyone registered on the mesh, in or out of this channel.
	if m.showAgents {
		b.WriteString("\n")
		b.WriteString(SidebarTitleStyle.Render("AGENTS"))
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

	// Width without the two border columns lipgloss draws outside it. Without
	// it the block was as wide as its widest row, so a sidebar of short channel
	// names pulled its right border in and the feed started wherever that left off.
	style := SidebarStyle.Width(max(0, m.width-2))
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

	// A DM's row name is "dm:<session>:<session>". Slicing the prefix off it
	// printed both raw session ids into a 22-column column; the label the model
	// resolved is who is on the other end.
	name := ch.Label
	if name == "" {
		name = ch.Name
	}
	if ch.Kind == "dm" {
		name = "@" + name
	} else {
		name = "#" + name
	}

	// Trailing markers: the lock, then the unread count. The member count used
	// to render in the same parentheses as the unread count, so "#general (4)"
	// had two readings and no way to tell them apart. It is in the feed header
	// now, where there is room to name it.
	var suffix []string
	if ch.Visibility == "private" && ch.Kind != "dm" {
		suffix = append(suffix, "[L]")
	}
	if ch.Unread > 0 {
		suffix = append(suffix, fmt.Sprintf("(%d)", ch.Unread))
	}

	// Content width: sidebar width minus borders and horizontal padding.
	contentW := m.width - sidebarChrome
	nameRunes := []rune(name)
	// Build suffix greedily: add markers until we'd crowd the name below 3 chars.
	suffixStr := ""
	for _, s := range suffix {
		candidate := suffixStr + " " + s
		if suffixStr == "" {
			candidate = " " + s
		}
		nameAvail := contentW - len(prefix) - lipgloss.Width(candidate)
		if nameAvail < 3 && suffixStr != "" {
			break // stop adding more markers
		}
		if nameAvail < 1 {
			break
		}
		if len(nameRunes) > nameAvail {
			break // name would truncate, stop adding
		}
		suffixStr = candidate
	}

	avail := contentW - len(prefix) - lipgloss.Width(suffixStr)
	if avail < 1 {
		name = ""
	} else if len(nameRunes) > avail {
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

func (m *SidebarModel) renderMember(mem store.MemberInfo) string {
	// Single-width glyphs only: the name budget below leaves room for one column
	// of status glyph. The human's used to be an emoji, which is two, so a member
	// with a full-width name wrapped onto a second row.
	dot := "●"
	if mem.TurnState == "idle" {
		dot = "○"
	}
	if mem.Harness == "human" {
		dot = "◆"
	}
	name := mem.Name
	if mem.Role == "owner" {
		name += " (owner)"
	}
	line := fmt.Sprintf(" %s %s", dot, Truncate(name, max(1, m.width-sidebarChrome-3)))
	return ChannelItemStyle.Render(line)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
