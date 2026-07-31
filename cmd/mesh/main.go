// Command mesh is the single Go binary for tmux-agent-mesh. It provides:
//
//	mesh serve          — unix socket server
//	mesh serve-stdio    — stdin/stdout for SSH forced-command
//	mesh tui            — Bubble Tea terminal Slack
//
// All other subcommands are handled by the bash CLI (scripts/mesh.sh).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/server"
	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
	"github.com/KakkoiDev/tmux-agent-mesh/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mesh <serve|serve-stdio|tui>\n")
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		runServe(args)
	case "serve-stdio":
		runServeStdio(args)
	case "tui":
		runTUI()
	default:
		fmt.Fprintf(os.Stderr, "mesh: unknown command %q (try serve, serve-stdio, or tui)\n", cmd)
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

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "/var/run/mesh/mesh.sock", "unix socket path")
	dataDir := fs.String("data", os.Getenv("HOME")+"/.tmux-agent-mesh", "data directory")
	fs.Parse(args)

	s, err := store.Open(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mesh serve: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	srv := server.New(s)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Close()
		os.Exit(0)
	}()

	if err := srv.Listen(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "mesh serve: %v\n", err)
		os.Exit(1)
	}
}

func runServeStdio(args []string) {
	fs := flag.NewFlagSet("serve-stdio", flag.ExitOnError)
	dataDir := fs.String("data", os.Getenv("HOME")+"/.tmux-agent-mesh", "data directory")
	fs.Parse(args)

	s, err := store.Open(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mesh serve-stdio: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	srv := server.New(s)
	srv.ServeStdio()
}
