package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
)

// Server listens on a unix socket and handles mesh protocol requests.
type Server struct {
	Addr    string
	Store   *store.Store
	Handler *Handler

	ln       net.Listener
	watchers map[string][]chan<- Event
	mu       sync.Mutex
}

// Event is a server-push notification.
type Event struct {
	Event   string `json:"event"`
	Channel string `json:"channel,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// New creates a server wired to the store.
func New(s *store.Store) *Server {
	return &Server{
		Store: s,
		Handler: &Handler{
			Store: s,
			Caps:  store.DefaultCaps(),
		},
		watchers: make(map[string][]chan<- Event),
	}
}

// Listen opens the unix socket and accepts connections.
func (s *Server) Listen(addr string) error {
	s.Addr = addr
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", addr, err)
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.ln = ln

	log.Printf("mesh server listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

// Close shuts down the listener.
func (s *Server) Close() error {
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

// Push sends an event to all watchers of a channel.
func (s *Server) Push(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.watchers[ev.Channel] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe adds a watch channel for a set of channels.
func (s *Server) Subscribe(channels []string, ch chan<- Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range channels {
		s.watchers[c] = append(s.watchers[c], ch)
	}
}

// Unsubscribe removes a watch channel.
func (s *Server) Unsubscribe(ch chan<- Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, watchers := range s.watchers {
		filtered := watchers[:0]
		for _, w := range watchers {
			if w != ch {
				filtered = append(filtered, w)
			}
		}
		if len(filtered) == 0 {
			delete(s.watchers, name)
		} else {
			s.watchers[name] = filtered
		}
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	s.serve(conn)
}

// Serve reads JSON Lines from r and writes responses to w.
func (s *Server) Serve(r io.Reader, w io.Writer) {
	s.serveStream(r, w, nil)
}

func (s *Server) serve(conn io.ReadWriter) {
	s.serveStream(conn, conn, nil)
}

func (s *Server) serveStream(r io.Reader, w io.Writer, watchCh chan<- Event) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	encoder := json.NewEncoder(w)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			encoder.Encode(errResponse("", "invalid", err.Error()))
			continue
		}

		if req.Method == "watch" {
			go s.handleWatch(req, encoder, watchCh)
			return // watch keeps the connection open
		}

		resp := s.Handler.Handle(req)
		if err := encoder.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) handleWatch(req Request, encoder *json.Encoder, ch chan<- Event) {
	var p struct {
		Channels []string `json:"channels"`
	}
	json.Unmarshal(req.Params, &p)

	events := make(chan Event, 64)
	s.Subscribe(p.Channels, events)
	defer s.Unsubscribe(events)

	greeting := map[string]string{"protocol": "mesh-1.0.0", "server": "tmux-agent-mesh/1.0.0"}
	encoder.Encode(greeting)

	for ev := range events {
		if err := encoder.Encode(ev); err != nil {
			return
		}
	}
}

// ServeStdio runs the server over stdin/stdout (SSH forced-command mode).
func (s *Server) ServeStdio() {
	greeting := map[string]string{"protocol": "mesh-1.0.0", "server": "tmux-agent-mesh/1.0.0"}
	encoder := json.NewEncoder(os.Stdout)
	encoder.Encode(greeting)
	s.Serve(os.Stdin, os.Stdout)
}
