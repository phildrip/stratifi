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

	onCommit        func(s *Surface)
	onToplevel      func(c *Client, toplevel *XdgToplevel, xdgSurfaceID uint32)
	onClientDestroy func(clientID uint32)
	closed          atomic.Bool

	keymapFD   int
	keymapSize uint32

	captureGrabber FrameGrabber
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
		keymapFD:   -1,
	}, nil
}

// SetKeymap configures the shared keymap FD and size distributed to clients via wl_keyboard.
func (s *Server) SetKeymap(fd int, size uint32) {
	s.keymapFD = fd
	s.keymapSize = size
}

// SocketPath returns the active filesystem path of the Wayland socket.
func (s *Server) SocketPath() string {
	return s.wireServer.Path()
}

// SetOnCommit registers a callback invoked whenever a surface commits a new frame.
func (s *Server) SetOnCommit(fn func(s *Surface)) {
	s.onCommit = fn
}

// SetOnToplevel registers a callback invoked whenever a client requests a toplevel window.
func (s *Server) SetOnToplevel(fn func(c *Client, toplevel *XdgToplevel, xdgSurfaceID uint32)) {
	s.onToplevel = fn
}

// SetOnClientDestroy registers a callback invoked when a client disconnects.
func (s *Server) SetOnClientDestroy(fn func(clientID uint32)) {
	s.onClientDestroy = fn
}

// CountClients returns the number of currently connected clients.
func (s *Server) CountClients() int {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return len(s.clients)
}

// GetClient retrieves an active client by its ID.
func (s *Server) GetClient(clientID uint32) *Client {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.clients[clientID]
}

func (s *Server) onSurfaceCommitted(surf *Surface) {
	if s.onCommit != nil {
		s.onCommit(surf)
	}
}

func (s *Server) onToplevelCreated(c *Client, toplevel *XdgToplevel, xdgSurfaceID uint32) {
	if s.onToplevel != nil {
		s.onToplevel(c, toplevel, xdgSurfaceID)
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
			if err := c.Run(); err != nil {
				fmt.Printf("[-] Client %d error: %v\n", id, err)
			}
			s.clientMu.Lock()
			delete(s.clients, id)
			s.clientMu.Unlock()
			if s.onClientDestroy != nil {
				s.onClientDestroy(id)
			}
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
