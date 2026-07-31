package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
)

// DetailModel shows message metadata and read receipts.
type DetailModel struct {
	visible  bool
	width    int
	height   int
	message  *MsgView
	receipts []store.Receipt
}

func NewDetail() DetailModel {
	return DetailModel{
		visible: false,
	}
}

func (m *DetailModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

func (m *DetailModel) Show(msg *MsgView, receipts []store.Receipt) {
	m.visible = true
	m.message = msg
	m.receipts = receipts
}

func (m *DetailModel) Hide() {
	m.visible = false
	m.message = nil
	m.receipts = nil
}

func (m *DetailModel) Toggle() {
	m.visible = !m.visible
	if !m.visible {
		m.message = nil
		m.receipts = nil
	}
}

func (m *DetailModel) View() string {
	if !m.visible || m.message == nil {
		return ""
	}

	var b strings.Builder

	b.WriteString(MessageHeaderStyle.Render("Details"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("━", m.width-2))
	b.WriteString("\n\n")

	// Message info
	b.WriteString(fmt.Sprintf("From:     %s\n", m.message.FromName))
	b.WriteString(fmt.Sprintf("ID:       #%d\n", m.message.ID))
	b.WriteString(fmt.Sprintf("Time:     %s\n", m.message.Timestamp.Format("15:04:05")))
	b.WriteString(fmt.Sprintf("Thread:   #%d\n", m.message.ThreadID))
	if m.message.Hops > 0 {
		b.WriteString(fmt.Sprintf("Hops:     %d\n", m.message.Hops))
	}
	if m.message.ExpectReply {
		b.WriteString("Reply:    expected\n")
	}
	b.WriteString("\n")

	// Read receipts
	b.WriteString(MessageHeaderStyle.Render("Read Receipts"))
	b.WriteString("\n")

	if len(m.receipts) == 0 {
		b.WriteString(DimText("  No receipts yet"))
		b.WriteString("\n")
	} else {
		for _, r := range m.receipts {
			ts := time.Unix(r.At, 0).Format("15:04:05")
			source := ""
			if r.Source == "drain" {
				source = "[drain]"
			} else {
				source = "[client]"
			}
			line := fmt.Sprintf("  %s  %s  %s %s",
				ts, r.Name, r.Harness, source)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	rendered := b.String()
	return DetailStyle.Width(m.width).Height(m.height).Render(rendered)
}

func DimText(s string) string {
	return SystemMessageStyle.Render(s)
}
