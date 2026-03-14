package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

// HandlerFunc is the signature for command handlers registered with Server.
// It receives the command arguments and returns a response payload or error.
type HandlerFunc func(ctx context.Context, args map[string]string) (interface{}, error)

// Server accepts management connections on a Unix socket and dispatches them
// to registered command handlers.
type Server struct {
	socketPath string
	handlers   map[string]HandlerFunc
}

// NewServer creates a Server that will listen on socketPath.
func NewServer(socketPath string) *Server {
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]HandlerFunc),
	}
}

// Register adds a handler for the given command name (e.g. "host.list").
func (s *Server) Register(command string, h HandlerFunc) {
	s.handlers[command] = h
}

// Serve starts accepting connections until ctx is cancelled.
// It creates the socket directory if needed and removes a stale socket file.
func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll("/var/run/agenkit", 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}
	// Remove stale socket from a previous run.
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.socketPath, err)
	}
	defer func() { _ = ln.Close() }()

	log.Printf("INFO: api server listening on %s", s.socketPath)

	// Close the listener when context is cancelled to unblock Accept.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("WARNING: api accept error: %v", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		log.Printf("WARNING: api set read deadline: %v", err)
		return
	}

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		s.writeResponse(conn, Response{Error: fmt.Sprintf("failed to decode request: %v", err)})
		return
	}

	h, ok := s.handlers[req.Command]
	if !ok {
		s.writeResponse(conn, Response{Error: fmt.Sprintf("unknown command: %s", req.Command)})
		return
	}

	data, err := h(ctx, req.Args)
	if err != nil {
		s.writeResponse(conn, Response{Error: err.Error()})
		return
	}
	s.writeResponse(conn, Response{OK: true, Data: data})
}

func (s *Server) writeResponse(conn net.Conn, resp Response) {
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		log.Printf("WARNING: api set write deadline: %v", err)
		return
	}
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		log.Printf("WARNING: api write response: %v", err)
	}
}
