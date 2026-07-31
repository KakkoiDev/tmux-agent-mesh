package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Base colors
	subtle    = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#FF6B6B", Dark: "#FF6B6B"}
	green     = lipgloss.AdaptiveColor{Light: "#50FA7B", Dark: "#50FA7B"}
	yellow    = lipgloss.AdaptiveColor{Light: "#F1FA8C", Dark: "#F1FA8C"}
	red       = lipgloss.AdaptiveColor{Light: "#FF5555", Dark: "#FF5555"}
	grey      = lipgloss.AdaptiveColor{Light: "#6272A4", Dark: "#6272A4"}
	dim       = lipgloss.AdaptiveColor{Light: "#44475A", Dark: "#44475A"}
	bgDark    = lipgloss.AdaptiveColor{Light: "#F8F8F2", Dark: "#282A36"}
	fgDark    = lipgloss.AdaptiveColor{Light: "#282A36", Dark: "#F8F8F2"}

	// Styles
	BaseStyle = lipgloss.NewStyle().
			Foreground(fgDark).
			Background(bgDark)

	SidebarStyle = lipgloss.NewStyle().
			Width(20).
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(subtle).
			Padding(0, 1)

	SidebarTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(highlight).
				MarginBottom(1)

	ChannelItemStyle = lipgloss.NewStyle().
				Padding(0, 0)

	ChannelSelectedStyle = lipgloss.NewStyle().
				Background(highlight).
				Foreground(bgDark).
				Padding(0, 1)

	ChannelUnreadStyle = lipgloss.NewStyle().
				Foreground(yellow)

	ChannelPrivateStyle = lipgloss.NewStyle().
				Foreground(subtle)

	AgentRosterStyle = lipgloss.NewStyle().
				MarginTop(1)

	AgentItemStyle = lipgloss.NewStyle().
			Padding(0, 0)

	AgentWorkingDot = lipgloss.NewStyle().
			Foreground(green).
			SetString("●")

	AgentIdlePendingDot = lipgloss.NewStyle().
				Foreground(yellow).
				SetString("◐")

	AgentIdleDot = lipgloss.NewStyle().
			Foreground(subtle).
			SetString("○")

	AgentDeadDot = lipgloss.NewStyle().
			Foreground(red).
			SetString("✕")

	AgentDispatchedDot = lipgloss.NewStyle().
				Foreground(yellow).
				SetString("⏳")

	FeedStyle = lipgloss.NewStyle().
			Padding(0, 1)

	MessageHeaderStyle = lipgloss.NewStyle().
				Foreground(highlight).
				Bold(true)

	MessageTimestampStyle = lipgloss.NewStyle().
				Foreground(subtle)

	MessageBodyStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	MessageOwnStyle = lipgloss.NewStyle().
			Foreground(special)

	SystemMessageStyle = lipgloss.NewStyle().
				Foreground(dim).
				Italic(true)

	ReceiptSentStyle = lipgloss.NewStyle().
				Foreground(subtle).
				SetString("✓")

	ReceiptReadStyle = lipgloss.NewStyle().
				Foreground(green).
				SetString("✓✓")

	ReceiptPendingStyle = lipgloss.NewStyle().
				Foreground(yellow).
				SetString("⏳")

	ComposeStyle = lipgloss.NewStyle().
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(subtle).
			Padding(0, 1)

	ComposePromptStyle = lipgloss.NewStyle().
				Foreground(highlight).
				Bold(true)

	DetailStyle = lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(subtle).
			Padding(0, 1).
			Width(30)

	HelpStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(1, 2)

	HelpTitleStyle = lipgloss.NewStyle().
			Foreground(highlight).
			Bold(true).
			Align(lipgloss.Center)

	SearchStyle = lipgloss.NewStyle().
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(subtle).
			Padding(0, 1)

	ThreadStyle = lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(subtle).
			PaddingLeft(1)

	OverlayStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(1, 2)

	PickerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(1, 2)

	PickerSelectedStyle = lipgloss.NewStyle().
				Background(highlight).
				Foreground(bgDark)

	StatusBarStyle = lipgloss.NewStyle().
			Background(subtle).
			Foreground(bgDark).
			Padding(0, 1).
			Height(1)
)

// AgentDot returns the colored dot for an agent's state.
func AgentDot(agent AgentView) string {
	if agent.Dead {
		return AgentDeadDot.String()
	}
	switch agent.TurnState {
	case "working":
		if agent.Pending > 0 {
			return AgentIdlePendingDot.String()
		}
		return AgentWorkingDot.String()
	case "idle":
		if agent.Pending > 0 {
			return AgentIdlePendingDot.String()
		}
		return AgentIdleDot.String()
	default:
		if agent.Pending > 0 {
			return AgentIdlePendingDot.String()
		}
		return AgentIdleDot.String()
	}
}

// ReceiptIcon returns the icon for a message's delivery state.
func ReceiptIcon(delivered bool, read bool, pending bool) string {
	if read {
		return ReceiptReadStyle.String()
	}
	if delivered {
		return ReceiptSentStyle.String()
	}
	if pending {
		return ReceiptPendingStyle.String()
	}
	return ""
}

func Truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
