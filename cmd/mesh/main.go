package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
	"github.com/KakkoiDev/tmux-agent-mesh/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: mesh <command>")
		fmt.Fprintln(os.Stderr, "  serve       Start the mesh server")
		fmt.Fprintln(os.Stderr, "  tui         Start the terminal UI")
		fmt.Fprintln(os.Stderr, "  send        Send a message")
		fmt.Fprintln(os.Stderr, "  roster      List agents")
		fmt.Fprintln(os.Stderr, "  inbox       Show pending messages")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "tui":
		runTUI()
	case "serve":
		fmt.Fprintln(os.Stderr, "mesh serve: not yet implemented")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "mesh %s: not yet implemented\n", os.Args[1])
		os.Exit(1)
	}
}

func runTUI() {
	dir := os.Getenv("MESH_DATA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "mesh tui: cannot find home directory: %v\n", err)
			os.Exit(1)
		}
		dir = filepath.Join(home, ".tmux-agent-mesh")
	}

	s, err := store.Open(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mesh tui: cannot open store at %s: %v\n", dir, err)
		os.Exit(1)
	}
	defer s.Close()

	m := tui.New(s)
	p := tea.NewProgram(&m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mesh tui: %v\n", err)
		os.Exit(1)
	}
}
