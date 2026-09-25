package server

import (
	"fmt"
	"sync"
	"sync/atomic"

	"bloxwm/internal/wire"
)

// Server coordinates Wayland client connections and top-level compositor state.
type Server struct {
	wireServer *wire.Server
	clients    map[uint32]*Client
	clientMu   sync.RWMutex
	nextClient atomic.Uint32

	onCommit func(s *Surface)
	closed   atomic.Bool
}

// NewServer creates a new Wayland server listening on the specified socket name.
func NewServer(socketName string) (*Server, error) {
	ws, err := wire.Listen(socketName)
	if err != nil {
		return nil, fmt.Errorf("failed to create wire server: %w", err)
	}

	return &Server{
		wireServer: ws,
		clients:    make(map[uint32]*Client),
	}, nil
}

// SocketPath returns the active filesystem path of the Wayland socket.
func (s *Server) SocketPath() string {
	return s.wireServer.Path()
}

// SetOnCommit registers a callback invoked whenever a surface commits a new frame.
func (s *Server) SetOnCommit(fn func(s *Surface)) {
	s.onCommit = fn
}

func (s *Server) onSurfaceCommitted(surf *Surface) {
	if s.onCommit != nil {
		s.onCommit(surf)
	}
}

// Run accepts incoming client connections until closed.
func (s *Server) Run() error {
	for !s.closed.Load() {
		conn, err := s.wireServer.Accept()
		if err != nil {
			if s.closed.Load() {
				return nil
			}
			return fmt.Errorf("accept error: %w", err)
		}

		clientID := s.nextClient.Add(1)
		client := newClient(clientID, conn, s)

		s.clientMu.Lock()
		s.clients[clientID] = client
		s.clientMu.Unlock()

		go func(c *Client, id uint32) {
			_ = c.Run()
			s.clientMu.Lock()
			delete(s.clients, id)
			s.clientMu.Unlock()
		}(client, clientID)
	}
	return nil
}

// Close shuts down the socket and terminates all connected clients.
func (s *Server) Close() error {
	s.closed.Store(true)
	err := s.wireServer.Close()

	s.clientMu.Lock()
	for _, c := range s.clients {
		_ = c.Close()
	}
	s.clients = make(map[uint32]*Client)
	s.clientMu.Unlock()

	return err
}
