package server

import (
	"fmt"
	"sync"
	"sync/atomic"

	"stratifi/internal/wire"
)

// Server coordinates Wayland client connections and top-level compositor state.
type Server struct {
	wireServer *wire.Server
	clients    map[uint32]*Client
	clientMu   sync.RWMutex
	nextClient atomic.Uint32

	onCommit           func(s *Surface)
	onToplevel         func(c *Client, toplevel *XdgToplevel, xdgSurfaceID uint32)
	selectionMu        sync.Mutex
	selection          Selection // compositor clipboard: last set_selection wins
	onPopup            func(c *Client, popupID, surfaceID, parentXdgSurfaceID uint32)
	onPopupMapped      func(clientID, popupID, surfaceID, parentXdgSurfaceID uint32, x, y, w, h int32)
	onSubsurface       func(clientID, surfaceID, parentSurfaceID uint32, x, y, w, h, order int32)
	onSubsurfaceGone   func(clientID, surfaceID uint32)
	onCursor           func(clientID, surfaceID uint32, hx, hy, w, h int32, active bool)
	onLayerSurface     func(info LayerSurfaceInfo)
	onLayerSurfaceGone func(clientID, surfaceID uint32)
	onClientDestroy    func(clientID uint32)
	closed             atomic.Bool

	keymapFD   int
	keymapSize uint32

	captureGrabber FrameGrabber

	output Output
}

// Output is the compositor's virtual output geometry. Single source of
// truth: wl_output advertisement and capture buffer sizing both read it.
type Output struct {
	// Width/Height are logical pixels; RefreshHz is the advertised rate.
	Width, Height uint32
	RefreshHz     uint32
}

// SetOutput configures the virtual output geometry.
func (s *Server) SetOutput(o Output) { s.output = o }

// Output returns the configured virtual output geometry. Zero values
// default to the historical 960x640@60 demo geometry so existing callers
// keep working until the compositor core (stratifi-o2x) owns real outputs.
func (s *Server) Output() Output {
	if s.output.Width == 0 || s.output.Height == 0 {
		return Output{Width: 960, Height: 640, RefreshHz: 60}
	}
	return s.output
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

// SetOnPopupMapped is the popup lifecycle callback the shell uses:
// placed geometry relative to the parent body.
func (s *Server) SetOnPopup(fn func(c *Client, popupID, surfaceID, parentXdgSurfaceID uint32)) {
	s.onPopup = fn
}

// SetOnPopupMapped registers a callback when a popup is placed with final
// geometry relative to its parent body: (clientID, popupID, surfaceID,
// parentXdgSurfaceID, x, y, w, h). This is what the shell needs to draw it.
func (s *Server) SetOnPopupMapped(fn func(clientID, popupID, surfaceID, parentXdgSurfaceID uint32, x, y, w, h int32)) {
	s.onPopupMapped = fn
}

func (s *Server) onPopupCreated(c *Client, popupID, surfaceID, parentXdgSurfaceID uint32) {
	if s.onPopup != nil {
		s.onPopup(c, popupID, surfaceID, parentXdgSurfaceID)
	}
}

// SetOnSubsurface registers a callback when a subsurface's state is applied:
// its position (parent-surface-relative), current buffer size, and stack
// order among its parent's subsurfaces (bottom = 0). Fired on the
// subsurface's own commit when desync, or the parent's commit when sync.
func (s *Server) SetOnSubsurface(fn func(clientID, surfaceID, parentSurfaceID uint32, x, y, w, h, order int32)) {
	s.onSubsurface = fn
}

// SetOnSubsurfaceGone registers a callback when a subsurface is destroyed,
// so the shell can drop its scene entry.
func (s *Server) SetOnSubsurfaceGone(fn func(clientID, surfaceID uint32)) {
	s.onSubsurfaceGone = fn
}

// SetOnLayerSurface registers a callback with a layer surface's current
// state: fired on its first commit (after configure) and on every commit,
// so the shell can place it in the right layer.
func (s *Server) SetOnLayerSurface(fn func(info LayerSurfaceInfo)) {
	s.onLayerSurface = fn
}

// SetOnLayerSurfaceGone registers a callback when a layer surface is
// destroyed, so the shell can drop it.
func (s *Server) SetOnLayerSurfaceGone(fn func(clientID, surfaceID uint32)) {
	s.onLayerSurfaceGone = fn
}

// SetOnCursor registers a callback for the client's pointer cursor: the
// wl_surface id it set via wl_pointer.set_cursor (0 = none, use the
// compositor default), the hotspot, and the current image size. active is
// false when the client cleared its cursor or the surface is unmapped.
func (s *Server) SetOnCursor(fn func(clientID, surfaceID uint32, hx, hy, w, h int32, active bool)) {
	s.onCursor = fn
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

// setSelection stores the clipboard owner (last set_selection wins).
func (s *Server) setSelection(sel Selection) {
	s.selectionMu.Lock()
	s.selection = sel
	s.selectionMu.Unlock()
}

// clearSelectionIfOwner drops the clipboard when its owner's source goes
// away (client destroyed the source or disconnected).
func (s *Server) clearSelectionIfOwner(clientID, sourceID uint32) {
	s.selectionMu.Lock()
	if s.selection.OwnerID == clientID && s.selection.SourceID == sourceID {
		s.selection = Selection{}
	}
	s.selectionMu.Unlock()
}

// clipboardOwner resolves the selection owner client IF it still offers
// the requested mime type; nil otherwise.
func (s *Server) clipboardOwner(mimeType string) *Client {
	s.selectionMu.Lock()
	owner := s.selection.OwnerID
	sourceID := s.selection.SourceID
	s.selectionMu.Unlock()
	if owner == 0 {
		return nil
	}
	c := s.GetClient(owner)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	src, ok := c.dataSources.get(sourceID)
	supported := ok && src.Active && containsString(src.MimeTypes, mimeType)
	c.mu.Unlock()
	if !supported {
		return nil
	}
	return c
}

// GetSelection returns a snapshot of the current clipboard owner.
func (s *Server) GetSelection() Selection {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	return s.selection
}
