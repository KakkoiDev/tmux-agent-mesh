package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// High-contrast dark theme: terminal-default background, bright white text
// everywhere, colored highlights (yellow/cyan) for focus, selection, and
// status. Colors are fixed (not adaptive) so the TUI renders the same
// regardless of the terminal's color scheme.
var (
	// Palette
	white    = lipgloss.Color("#FFFFFF") // primary text — bright white
	soft     = lipgloss.Color("#E8E8E8") // secondary text (timestamps)
	softDim  = lipgloss.Color("#E0E0E0") // system text
	accent   = lipgloss.Color("#22D3EE") // cyan — focus borders, headers
	yellow   = lipgloss.Color("#FFD400") // yellow — selection, prompts
	onYellow = lipgloss.Color("#111111") // black text on yellow selection
	amber    = lipgloss.Color("#FFC107") // amber — pending, unread
	green    = lipgloss.Color("#00E676") // green — working, read
	red      = lipgloss.Color("#FF5252") // red — dead/error
	border   = lipgloss.Color("#6E7681") // visible panel borders
	panelBg  = lipgloss.Color("#161B22") // panel tint (sidebar, overlays)
	focusBg  = lipgloss.Color("#232B3A") // brighter panel tint when focused
	statusBg = lipgloss.Color("#23272E") // status bar background
	placeBg  = lipgloss.Color("#9CA3AF") // input placeholders
)

// Base style for the whole app.
var BaseStyle = lipgloss.NewStyle().
	Foreground(white)

// Sidebar — subtle dark tint so it reads as a distinct panel. Thick vertical
// borders frame it in bright cyan so the panel is always visible; the
// background brightens further when the sidebar has focus.
var SidebarStyle = lipgloss.NewStyle().
	Width(18).
	BorderLeft(true).
	BorderRight(true).
	BorderStyle(lipgloss.ThickBorder()).
	BorderForeground(accent).
	Background(panelBg).
	Padding(0, 1)

// SidebarTitleStyle — bold cyan section headers.
var SidebarTitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(accent)

// SidebarSeparatorStyle — muted separator line under headers.
var SidebarSeparatorStyle = lipgloss.NewStyle().
	Foreground(border)

// ChannelItemStyle — bright white channel rows.
var ChannelItemStyle = lipgloss.NewStyle().
	Foreground(white).
	Padding(0, 0)

// ChannelSelectedStyle — full-width yellow pill for the selected channel.
// No side padding: the pill must fit the content area exactly
// (SidebarStyle chrome: 2 borders + 2 padding).
var ChannelSelectedStyle = lipgloss.NewStyle().
	Bold(true).
	Background(yellow).
	Foreground(onYellow)

// ChannelActiveStyle — muted highlight for the active channel when sidebar
// is not focused (so the user can still see which channel is open).
var ChannelActiveStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(yellow)

// ChannelUnreadStyle — bold yellow unread counts.
var ChannelUnreadStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(yellow)

// ChannelPrivateStyle — cyan private-channel markers.
var ChannelPrivateStyle = lipgloss.NewStyle().
	Foreground(accent)

// AgentRosterStyle — spacing above the agent roster.
var AgentRosterStyle = lipgloss.NewStyle().
	MarginTop(1)

// AgentItemStyle — bright white agent rows.
var AgentItemStyle = lipgloss.NewStyle().
	Foreground(white).
	Padding(0, 0)

// Status dots — single-width text-size glyphs from one consistent family
// (U+25xx geometric shapes, U+2715 ✕). Variable-width glyphs (emoji like
// ⏳, "large" variants like ⬤) would overflow the sidebar.
var (
	AgentWorkingDot = lipgloss.NewStyle().
			Bold(true).
			Foreground(green).
			SetString("●") // U+25CF BLACK CIRCLE

	AgentIdlePendingDot = lipgloss.NewStyle().
				Bold(true).
				Foreground(amber).
				SetString("◐") // U+25D0 CIRCLE WITH LEFT HALF BLACK

	AgentIdleDot = lipgloss.NewStyle().
			Foreground(placeBg).
			SetString("○") // U+25CB WHITE CIRCLE

	AgentDeadDot = lipgloss.NewStyle().
			Bold(true).
			Foreground(red).
			SetString("✕") // U+2715 MULTIPLICATION X

	AgentDispatchedDot = lipgloss.NewStyle().
				Bold(true).
				Foreground(amber).
				SetString("◔") // U+25D4 CIRCLE WITH UPPER RIGHT QUADRANT BLACK
)

// Feed — message area on the terminal-default background with a thick left
// separator border that turns cyan when the feed has focus.
var FeedStyle = lipgloss.NewStyle().
	BorderLeft(true).
	BorderStyle(lipgloss.ThickBorder()).
	BorderForeground(border).
	Padding(0, 1)

// MessageHeaderStyle — bold cyan sender/channel headers.
var MessageHeaderStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(accent)

// MessageTimestampStyle — bright white timestamps.
var MessageTimestampStyle = lipgloss.NewStyle().
	Foreground(soft)

// MessageBodyStyle — bright white message bodies.
var MessageBodyStyle = lipgloss.NewStyle().
	Foreground(white).
	PaddingLeft(1)

// MessageOwnStyle — bold yellow header for your own messages.
var MessageOwnStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(yellow)

// MessageCursorStyle — bright cyan selection marker for the focused message.
var MessageCursorStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(accent)

// SystemMessageStyle — bright italic system lines.
var SystemMessageStyle = lipgloss.NewStyle().
	Foreground(softDim).
	Italic(true)

// Receipt icons.
var (
	ReceiptSentStyle = lipgloss.NewStyle().
				Foreground(soft).
				SetString("✓")

	ReceiptReadStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(green).
				SetString("✓✓")

	ReceiptPendingStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(amber).
				SetString("◔") // U+25D4 — same family/size as ● ○ ◐
)

// Compose bar — clearly separated from the feed by a thick top border that
// turns cyan when compose has focus.
var ComposeStyle = lipgloss.NewStyle().
	BorderTop(true).
	BorderStyle(lipgloss.ThickBorder()).
	BorderForeground(border).
	Padding(0, 1)

// ComposePromptStyle — bold yellow prompt (> r> e> / @ #).
var ComposePromptStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(yellow)

// Detail bar — subtle dark panel with a thick left border.
var DetailStyle = lipgloss.NewStyle().
	Background(panelBg).
	BorderLeft(true).
	BorderStyle(lipgloss.ThickBorder()).
	BorderForeground(border).
	Padding(0, 1).
	Width(30)

// Help overlay.
var HelpStyle = lipgloss.NewStyle().
	Background(panelBg).
	Border(lipgloss.ThickBorder()).
	BorderForeground(accent).
	Padding(1, 2)

// HelpTitleStyle — bold cyan centered title.
var HelpTitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(accent).
	Align(lipgloss.Center)

// Search bar.
var SearchStyle = lipgloss.NewStyle().
	BorderBottom(true).
	BorderStyle(lipgloss.ThickBorder()).
	BorderForeground(accent).
	Padding(0, 1)

// Thread panel — subtle dark panel with a thick left border.
var ThreadStyle = lipgloss.NewStyle().
	Background(panelBg).
	BorderLeft(true).
	BorderStyle(lipgloss.ThickBorder()).
	BorderForeground(border).
	PaddingLeft(1)

// Overlay (search results).
var OverlayStyle = lipgloss.NewStyle().
	Background(panelBg).
	Border(lipgloss.ThickBorder()).
	BorderForeground(accent).
	Padding(1, 2)

// Picker overlay.
var PickerStyle = lipgloss.NewStyle().
	Background(panelBg).
	Border(lipgloss.ThickBorder()).
	BorderForeground(accent).
	Padding(1, 2)

// PickerSelectedStyle — high-contrast yellow selected row.
var PickerSelectedStyle = lipgloss.NewStyle().
	Bold(true).
	Background(yellow).
	Foreground(onYellow)

// StatusBarStyle — dark hints bar at the bottom of the compose area.
var StatusBarStyle = lipgloss.NewStyle().
	Background(statusBg).
	Foreground(white).
	Padding(0, 1).
	Height(1)

// Text input styles — bright white text on the terminal-default background.
var (
	InputPromptStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(yellow)

	InputTextStyle = lipgloss.NewStyle().
			Foreground(white)

	InputPlaceholderStyle = lipgloss.NewStyle().
				Foreground(placeBg)

	InputCursorStyle = lipgloss.NewStyle().
				Background(accent).
				Foreground(white)
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
