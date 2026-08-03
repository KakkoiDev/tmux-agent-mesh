//go:build ignore

package main

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	lip "charm.land/lipgloss/v2"
)

type model struct {
	width, height int
	activePane    int // 0=sidebar, 1=messages, 2=thread
	sidebarIdx    int
	msgIdx        int
}

var (
	workspaceStyle = lip.NewStyle().Background(lip.Color("#1a1b26")).Foreground(lip.Color("#a9b1d6")).Padding(0, 1).Width(4)
	sidebarStyle   = lip.NewStyle().Background(lip.Color("#1a1b26")).Foreground(lip.Color("#a9b1d6")).Width(22).Padding(0, 1)
	activeChannel  = lip.NewStyle().Background(lip.Color("#2f3340")).Foreground(lip.Color("#7aa2f7")).Bold(true)
	inactiveChan   = lip.NewStyle().Foreground(lip.Color("#565f89"))
	unreadBadge    = lip.NewStyle().Background(lip.Color("#f7768e")).Foreground(lip.Color("#1a1b26")).Padding(0, 1)
	headerStyle    = lip.NewStyle().Foreground(lip.Color("#565f89")).Bold(true)
	msgPane        = lip.NewStyle().Background(lip.Color("#24283b")).Padding(1)
	threadPane     = lip.NewStyle().Background(lip.Color("#1f2335")).Padding(1).Width(30)
	composeStyle   = lip.NewStyle().Background(lip.Color("#1a1b26")).Foreground(lip.Color("#565f89")).Padding(0, 1)
	helpStyle      = lip.NewStyle().Background(lip.Color("#1a1b26")).Foreground(lip.Color("#3b4261")).Padding(0, 1)
	timeStyle      = lip.NewStyle().Foreground(lip.Color("#565f89"))
	userStyle      = lip.NewStyle().Foreground(lip.Color("#7aa2f7")).Bold(true)
	bodyStyle      = lip.NewStyle().Foreground(lip.Color("#c0caf5"))
	reactionStyle  = lip.NewStyle().Background(lip.Color("#2f3340")).Padding(0, 1)
	divider        = lip.NewStyle().Foreground(lip.Color("#3b4261"))
	insertStyle    = lip.NewStyle().Foreground(lip.Color("#73daca"))
	normalStyle    = lip.NewStyle().Foreground(lip.Color("#a9b1d6"))
)

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.activePane == 0 { m.sidebarIdx++ }
			if m.activePane == 1 { m.msgIdx++ }
		case "k", "up":
			if m.activePane == 0 && m.sidebarIdx > 0 { m.sidebarIdx-- }
			if m.activePane == 1 && m.msgIdx > 0 { m.msgIdx-- }
		case "h", "left":
			if m.activePane > 0 { m.activePane-- }
		case "l", "right":
			if m.activePane < 2 { m.activePane++ }
		case "i":
			// insert mode — just shows in help bar
		}
	}
	return m, nil
}

func (m model) View() tea.View {
	if m.width == 0 { return tea.NewView("resizing...") }

	// Workspace rail
	workspaces := []string{"  D ", "  E ", "  T "}
	wsRail := workspaceStyle.Render(strings.Join(workspaces, "\n"))

	// Sidebar
	var sb strings.Builder
	sb.WriteString(headerStyle.Render("⚑ Threads") + "\n")
	sb.WriteString(headerStyle.Render("🔔 Activity") + "\n")
	sb.WriteString(divider.Render("──── Channels") + "\n")
	channels := []struct{ name, badge string }{
		{"# general", " 3 "}, {"# random", ""}, {"# dev", ""}, {"# design", ""},
	}
	for i, ch := range channels {
		line := "  " + ch.name
		if ch.badge != "" { line += " " + unreadBadge.Render(ch.badge) }
		if i == m.sidebarIdx {
			sb.WriteString(activeChannel.Render(line) + "\n")
		} else {
			sb.WriteString(inactiveChan.Render(line) + "\n")
		}
	}
	sb.WriteString(divider.Render("──── Direct Msgs") + "\n")
	dms := []string{"@ alice", "@ bob 2", "@ carol"}
	for _, dm := range dms {
		sb.WriteString(inactiveChan.Render("  "+dm) + "\n")
	}
	sidebar := sidebarStyle.Render(sb.String())

	// Message pane
	var msgs strings.Builder
	msgData := []struct{ user, time, body string }{
		{"alice", "15:15", "Hey team, the new build is ready! 🚀"},
		{"bob", "15:17", "Nice! I'll review the PR right after lunch"},
		{"alice", "15:18", "Thanks! The sidebar fix is the main thing to look at"},
		{"carol", "15:22", "LGTM on my end. The layout feels much snappier now ✨"},
	}
	for i, md := range msgData {
		if i == m.msgIdx {
			msgs.WriteString(lip.NewStyle().Background(lip.Color("#2f3340")).Padding(0, 1).Render(
				timeStyle.Render(md.time+"  ")+userStyle.Render(md.user)+"\n"+bodyStyle.Render(md.body),
			) + "\n")
		} else {
			msgs.WriteString(
				timeStyle.Render(md.time+"  ")+userStyle.Render(md.user)+"\n"+bodyStyle.Render(md.body)+"\n",
			)
		}
	}
	msgs.WriteString("\n")
	msgs.WriteString(reactionStyle.Render("👍 2  ❤️ 1  🚀 3") + "\n")
	msgs.WriteString("\n")
	msgContent := msgPane.Render(msgs.String())

	// Thread panel
	thread := threadPane.Render(
		headerStyle.Render("Thread") + "\n" +
			timeStyle.Render("15:15  ")+userStyle.Render("alice")+"\n" +
			bodyStyle.Render("Hey team, the new build is ready!")+"\n\n" +
			timeStyle.Render("15:20  ")+userStyle.Render("bob")+"\n" +
			bodyStyle.Render("What's new in this one?")+"\n\n" +
			timeStyle.Render("15:21  ")+userStyle.Render("alice")+"\n" +
			bodyStyle.Render("Mostly perf improvements. 6x faster scroll.")+"\n",
	)

	// Compose bar
	mode := "INSERT"
	compose := composeStyle.Render(
		fmt.Sprintf(" %s  %s | %s ⏎ send | Esc normal | Shift+⏎ newline",
			insertStyle.Render("◆"),
			normalStyle.Render(mode),
			normalStyle.Render("> _"),
		),
	)

	// Help bar
	help := helpStyle.Render(
		"j/k navigate | h/l panels | i insert | r react | e edit | / search | Ctrl+t find | Ctrl+y theme | ? help",
	)

	// Assemble
	contentWidth := m.width - 4
	sidebarW := 22
	if contentWidth < 80 { sidebarW = 16 }
	msgW := contentWidth - sidebarW - 30
	if msgW < 30 { msgW = contentWidth - sidebarW }

	content := lip.JoinHorizontal(lip.Top,
		sidebar,
		lip.NewStyle().Width(msgW).Render(msgContent),
		thread,
	)

	return tea.NewView(lip.JoinVertical(lip.Left,
		lip.JoinHorizontal(lip.Top, wsRail, content),
		compose,
		help,
	))
}

func main() {
	p := tea.NewProgram(model{sidebarIdx: 0, activePane: 1})
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}
