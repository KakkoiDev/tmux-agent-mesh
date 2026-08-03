package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// PickerMode determines what we are picking.
type PickerMode int

const (
	PickerNone PickerMode = iota
	PickerChannel
	PickerAgent
	PickerMember
	PickerRule
)

// PickerItem is one selectable item in the picker.
type PickerItem struct {
	ID int64
	// Key is what the caller acts on: a session id, a rule, an empty string
	// for a channel. The name is for reading, and two agents may share one.
	Key    string
	Name   string
	Detail string // extra info like member count or harness
}

// PickerModel is the fuzzy-find picker overlay.
type PickerModel struct {
	active   bool
	mode     PickerMode
	input    textinput.Model
	items    []PickerItem
	filtered []PickerItem
	cursor   int
	width    int
	height   int
}

func NewPicker() PickerModel {
	ti := textinput.New()
	ti.Placeholder = "Filter..."
	ti.Prompt = ""
	ti.Width = 30
	ti.PromptStyle = InputPromptStyle
	ti.TextStyle = InputTextStyle
	ti.PlaceholderStyle = InputPlaceholderStyle
	ti.Cursor.Style = InputCursorStyle

	return PickerModel{
		input: ti,
	}
}

func (m *PickerModel) Activate(mode PickerMode, items []PickerItem) {
	m.active = true
	m.mode = mode
	m.items = items
	m.filtered = items
	m.cursor = 0
	m.input.SetValue("")
	m.input.Focus()

	switch mode {
	case PickerChannel:
		m.input.Placeholder = "Channel name..."
	case PickerAgent:
		m.input.Placeholder = "Agent name..."
	case PickerMember:
		m.input.Placeholder = "Member name..."
	case PickerRule:
		m.input.Placeholder = "Rule..."
	}
}

func (m *PickerModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.items = nil
	m.filtered = nil
}

func (m *PickerModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.input.Width = width - 10
}

func (m *PickerModel) Selected() *PickerItem {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return &m.filtered[m.cursor]
	}
	return nil
}

func (m *PickerModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *PickerModel) MoveDown() {
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
	}
}

func (m *PickerModel) filter() {
	query := strings.ToLower(m.input.Value())
	if query == "" {
		m.filtered = m.items
		return
	}

	var out []PickerItem
	for _, item := range m.items {
		if strings.Contains(strings.ToLower(item.Name), query) {
			out = append(out, item)
		}
	}
	m.filtered = out
	if m.cursor >= len(out) {
		m.cursor = max(0, len(out)-1)
	}
}

func (m *PickerModel) Update(msg tea.Msg) (PickerModel, tea.Cmd) {
	if !m.active {
		return *m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filter()
	return *m, cmd
}

func (m *PickerModel) View() string {
	if !m.active {
		return ""
	}

	var b strings.Builder

	modeLabel := "Select Channel"
	switch m.mode {
	case PickerAgent:
		modeLabel = "Select Agent"
	case PickerMember:
		modeLabel = "Remove Member"
	case PickerRule:
		modeLabel = "Access Rules"
	}
	b.WriteString(MessageHeaderStyle.Render(modeLabel))
	b.WriteString("\n\n")

	b.WriteString(m.input.View())
	b.WriteString("\n\n")

	for i, item := range m.filtered {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		line := fmt.Sprintf("%s %s", cursor, item.Name)
		if item.Detail != "" {
			line += "  " + MessageTimestampStyle.Render(item.Detail)
		}
		if i == m.cursor {
			line = PickerSelectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	box := PickerStyle.Width(m.width - 6).Height(m.height - 4)
	maxItems := m.height - 8
	if len(m.filtered) > maxItems {
		box = box.Height(maxItems + 6)
	}
	rendered := box.Render(b.String())

	return PlaceOverlay(m.width, m.height, rendered)
}
