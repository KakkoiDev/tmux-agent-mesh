package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// SearchResult is one search hit.
type SearchResult struct {
	ID          int64
	ChannelName string
	FromName    string
	Body        string
	CreatedAt   int64
}

// SearchModel is the search bar + results overlay.
type SearchModel struct {
	active     bool
	input      textinput.Model
	results    []SearchResult
	cursor     int
	viewport   viewport.Model
	width      int
	height     int
}

func NewSearch() SearchModel {
	ti := textinput.New()
	ti.Placeholder = "Search messages..."
	ti.Width = 40

	vp := viewport.New(60, 10)

	return SearchModel{
		input:    ti,
		viewport: vp,
	}
}

func (m *SearchModel) Activate() {
	m.active = true
	m.input.Focus()
	m.input.SetValue("")
	m.results = nil
	m.cursor = 0
}

func (m *SearchModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.results = nil
}

func (m *SearchModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.input.Width = width - 8
	m.viewport.Width = width - 8
	m.viewport.Height = height - 4
}

func (m *SearchModel) SetResults(results []SearchResult) {
	m.results = results
	if m.cursor >= len(results) {
		m.cursor = max(0, len(results)-1)
	}
}

func (m *SearchModel) SelectedResult() *SearchResult {
	if m.cursor >= 0 && m.cursor < len(m.results) {
		return &m.results[m.cursor]
	}
	return nil
}

func (m *SearchModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *SearchModel) MoveDown() {
	if m.cursor < len(m.results)-1 {
		m.cursor++
	}
}

func (m *SearchModel) Update(msg tea.Msg) (SearchModel, tea.Cmd) {
	if !m.active {
		return *m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.viewport, _ = m.viewport.Update(msg)
	return *m, cmd
}

func (m *SearchModel) View() string {
	if !m.active {
		return ""
	}

	var b strings.Builder

	// Search bar
	bar := fmt.Sprintf("/ %s", m.input.View())
	bar = SearchStyle.Width(m.width - 4).Render(bar)
	b.WriteString(bar)
	b.WriteString("\n\n")

	// Results
	if m.results == nil {
		b.WriteString(DimText("  Type to search..."))
	} else if len(m.results) == 0 {
		b.WriteString(DimText("  No results found."))
	} else {
		for i, r := range m.results {
			cursor := " "
			if i == m.cursor {
				cursor = ">"
			}
			snippet := r.Body
			if len(snippet) > 60 {
				snippet = snippet[:57] + "..."
			}
			line := fmt.Sprintf("%s %s  %s  %s",
				cursor,
				MessageHeaderStyle.Render("#"+r.ChannelName),
				r.FromName,
				MessageTimestampStyle.Render(snippet),
			)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	m.viewport.SetContent(b.String())

	box := OverlayStyle.Width(m.width - 4).Height(m.height - 2)
	rendered := box.Render(m.viewport.View())

	return PlaceOverlay(m.width, m.height, rendered)
}
