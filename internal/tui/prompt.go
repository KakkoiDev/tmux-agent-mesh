package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// PromptMode determines the kind of prompt.
type PromptMode int

const (
	PromptNone PromptMode = iota
	PromptText
	PromptConfirm
)

// PromptModel is a single-line text input overlay used for simple user prompts
// like creating/renaming channels or agents.
type PromptModel struct {
	active      bool
	mode        PromptMode
	title       string
	input       textinput.Model
	confirmVal  bool // used in confirm mode: starts false, 'y' sets true
	width       int
	height      int
}

func NewPrompt() PromptModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = ""
	ti.Width = 30
	ti.PromptStyle = InputPromptStyle
	ti.TextStyle = InputTextStyle
	ti.PlaceholderStyle = InputPlaceholderStyle
	ti.Cursor.Style = InputCursorStyle

	return PromptModel{
		input: ti,
	}
}

// ActivateText shows a text prompt.
func (m *PromptModel) ActivateText(title, placeholder string) {
	m.active = true
	m.mode = PromptText
	m.title = title
	m.input.Placeholder = placeholder
	m.input.SetValue("")
	m.input.Focus()
}

// ActivateConfirm shows a yes/no confirmation prompt.
func (m *PromptModel) ActivateConfirm(title string) {
	m.active = true
	m.mode = PromptConfirm
	m.title = title
	m.confirmVal = false
	m.input.Blur()
}

func (m *PromptModel) Deactivate() {
	m.active = false
	m.input.Blur()
}

func (m *PromptModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.input.Width = width - 10
}

func (m *PromptModel) Update(msg tea.Msg) (PromptModel, tea.Cmd) {
	if !m.active {
		return *m, nil
	}

	switch m.mode {
	case PromptText:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return *m, cmd
	case PromptConfirm:
		// Only respond to y/n/enter/esc
		return *m, nil
	}
	return *m, nil
}

func (m *PromptModel) View() string {
	if !m.active {
		return ""
	}

	var b strings.Builder
	b.WriteString(MessageHeaderStyle.Render(m.title))
	b.WriteString("\n\n")

	switch m.mode {
	case PromptText:
		b.WriteString(m.input.View())
	case PromptConfirm:
		yn := "n"
		if m.confirmVal {
			yn = "y"
		}
		label := fmt.Sprintf("[y/N]: %s", yn)
		b.WriteString(MessageBodyStyle.Render(label))
	}

	box := PickerStyle.Width(m.width - 6).Height(6)
	rendered := box.Render(b.String())

	return PlaceOverlay(m.width, m.height, rendered)
}
