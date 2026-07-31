package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ComposeMode tracks what the compose bar is doing.
type ComposeMode int

const (
	ComposeNormal ComposeMode = iota
	ComposeReply
	ComposeEdit
	ComposeSearch
	ComposeNewDM
	ComposeNewChannel
)

// ComposeModel is the input bar at the bottom.
type ComposeModel struct {
	input     textinput.Model
	mode      ComposeMode
	replyToID int64
	editID    int64
	focused   bool
	width     int
}

func NewCompose() ComposeModel {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Prompt = ""
	ti.CharLimit = 4000
	ti.Width = 60
	ti.PromptStyle = InputPromptStyle
	ti.TextStyle = InputTextStyle
	ti.PlaceholderStyle = InputPlaceholderStyle
	ti.Cursor.Style = InputCursorStyle

	return ComposeModel{
		input: ti,
		mode:  ComposeNormal,
	}
}

func (m *ComposeModel) SetSize(width int) {
	m.width = width
	m.input.Width = width - 4
}

func (m *ComposeModel) SetMode(mode ComposeMode) {
	m.mode = mode
	switch mode {
	case ComposeReply:
		m.input.Placeholder = "Reply..."
	case ComposeEdit:
		m.input.Placeholder = "Edit message..."
	case ComposeSearch:
		m.input.Placeholder = "Search..."
	case ComposeNewDM:
		m.input.Placeholder = "Agent name..."
	case ComposeNewChannel:
		m.input.Placeholder = "Channel name..."
	default:
		m.input.Placeholder = "Type a message..."
	}
}

func (m *ComposeModel) Value() string {
	return m.input.Value()
}

func (m *ComposeModel) SetValue(v string) {
	m.input.SetValue(v)
}

func (m *ComposeModel) Focus() {
	m.focused = true
	m.input.Focus()
}

func (m *ComposeModel) Blur() {
	m.focused = false
	m.input.Blur()
}

func (m *ComposeModel) Reset() {
	m.input.SetValue("")
	m.mode = ComposeNormal
	m.replyToID = 0
	m.editID = 0
	m.input.Placeholder = "Type a message..."
}

func (m *ComposeModel) Update(msg tea.Msg) (ComposeModel, tea.Cmd) {
	if !m.focused {
		return *m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return *m, cmd
}

func (m *ComposeModel) View() string {
	prompt := ">"
	switch m.mode {
	case ComposeReply:
		prompt = "r>"
	case ComposeEdit:
		prompt = "e>"
	case ComposeSearch:
		prompt = "/"
	case ComposeNewDM:
		prompt = "@"
	case ComposeNewChannel:
		prompt = "#"
	}

	style := ComposeStyle.Width(m.width)
	if m.focused {
		style = style.BorderForeground(accent)
	}

	rendered := fmt.Sprintf("%s %s", ComposePromptStyle.Render(prompt), m.input.View())
	// Status bar hints
	hints := "enter: send  tab: switch  ctrl+t: thread  ctrl+n: dm  ctrl+k: channel  ?: help  ctrl+c: quit"
	rendered += "\n" + StatusBarStyle.Width(m.width-3).Render(hints)
	rendered = style.Render(rendered)

	return rendered
}

// Help renders the keybinding help overlay.
func HelpView(width, height int) string {
	help := `
Keybindings

Navigation
  j / ↓          Move down
  k / ↑          Move up
  gg             Scroll to top
  G              Scroll to bottom
  Tab            Next panel
  Shift+Tab      Previous panel

Actions
  Enter          Send message
  r              Reply to selected
  e              Edit your message
  d              Delete your message
  Ctrl+T         Thread view
  Ctrl+N         New direct message
  Ctrl+K         Create/join channel
  Ctrl+U         Upload file
  i              Toggle detail bar
  s              Toggle sidebar
  /              Search
  ?              Help (this)
  q / Ctrl+C     Quit

Status
  ⬤ working  ◐ idle+pending  ○ idle  ✖ dead
  ✓ sent  ✓✓ read  ⏳ pending
  * unread  [L] private channel`

	box := HelpStyle.Width(width - 4).Height(height - 4)
	rendered := box.Render(HelpTitleStyle.Render("Help") + "\n" + help)

	return PlaceOverlay(width, height, rendered)
}

// PlaceOverlay centers a string in a width x height area.
func PlaceOverlay(w, h int, content string) string {
	lines := strings.Split(content, "\n")
	contentHeight := len(lines)
	contentWidth := 0
	for _, l := range lines {
		if len(l) > contentWidth {
			contentWidth = len(l)
		}
	}

	top := max(0, (h-contentHeight)/2)
	var b strings.Builder
	for i := 0; i < top; i++ {
		b.WriteString("\n")
	}
	for _, line := range lines {
		b.WriteString(strings.Repeat(" ", max(0, (w-contentWidth)/2)))
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := top + contentHeight; i < h; i++ {
		b.WriteString("\n")
	}
	return b.String()
}
