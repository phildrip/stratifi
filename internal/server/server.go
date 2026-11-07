package server

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"stratifi/internal/wire"
)

// Server coordinates Wayland client connections and top-level compositor state.
type Server struct {
	wireServer *wire.Server
	clients    map[uint32]*Client
	clientMu   sync.RWMutex
	nextClient atomic.Uint32

	onCommit         func(s *Surface)
	onToplevel       func(c *Client, toplevel *XdgToplevel, xdgSurfaceID uint32)
	onToplevelMeta   func(clientID, toplevelID uint32, title, appID string)
	toplevelProvider func() []Toplevel
	toplevelGeometry func(key uint32) (x, y, w, h int32, ok bool)
	selectionMu      sync.Mutex
	selection        Selection // compositor clipboard: last set_selection wins
	onPopup          func(c *Client, popupID, surfaceID, parentXdgSurfaceID uint32)
	onPopupMapped    func(clientID, popupID, surfaceID, parentXdgSurfaceID uint32, x, y, w, h int32)
	onSubsurface     func(clientID, surfaceID, parentSurfaceID uint32, x, y, w, h, order int32)
	onSubsurfaceGone func(clientID, surfaceID uint32)
	onCursor         func(clientID, surfaceID uint32, hx, hy, w, h int32, active bool)

	// Idle notifications (ext-idle-notify-v1) & inhibitors (zwp-idle-inhibit-v1):
	// timers over global input activity, so idle daemons can lock/blank the
	// session while video/call apps can inhibit regular idle notifications.
	idleMu             sync.Mutex
	idleNotifs         []idleNotif
	idleInhibitors     []idleInhibitor
	lastActivity       time.Time
	lastInhibitEnd     time.Time
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
	// Width/Height are mode pixels; RefreshHz is the advertised rate.
	Width, Height uint32
	RefreshHz     uint32
	// Scale is the configured output scale factor (0 or 1.0 = unscaled).
	Scale float32
}

// IntScale returns the integer scale factor advertised via wl_output.scale
// (ceil(Scale), at least 1).
func (o Output) IntScale() int32 {
	if o.Scale <= 1.0 {
		return 1
	}
	return int32(math.Ceil(float64(o.Scale)))
}

// LogicalSize returns the logical output dimensions advertised via
// zxdg_output_v1.logical_size (Width/Scale, Height/Scale).
func (o Output) LogicalSize() (int32, int32) {
	sc := float64(o.Scale)
	if sc <= 0 {
		sc = 1.0
	}
	w := int32(math.Round(float64(o.Width) / sc))
	h := int32(math.Round(float64(o.Height) / sc))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// SetOutput configures the virtual output geometry and notifies any connected
// clients that have bound wl_output or zxdg_output_v1.
func (s *Server) SetOutput(o Output) {
	s.clientMu.Lock()
	s.output = o
	out := s.outputLocked()
	clients := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientMu.Unlock()

	for _, c := range clients {
		c.SendOutputUpdate(out)
	}
}

// Output returns the configured virtual output geometry. Zero values
// default to the historical 960x640@60 demo geometry so existing callers
// keep working until the compositor core (stratifi-o2x) owns real outputs.
func (s *Server) Output() Output {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.outputLocked()
}

func (s *Server) outputLocked() Output {
	o := s.output
	if o.Width == 0 || o.Height == 0 {
		o.Width = 960
		o.Height = 640
		o.RefreshHz = 60
	}
	if o.Scale <= 0 {
		o.Scale = 1.0
	}
	return o
}

// NewServer creates a new Wayland server listening on the specified socket name.
func NewServer(socketName string) (*Server, error) {
	ws, err := wire.Listen(socketName)
	if err != nil {
		return nil, fmt.Errorf("failed to create wire server: %w", err)
	}

	s := &Server{
		wireServer: ws,
		clients:    make(map[uint32]*Client),
		keymapFD:   -1,
	}
	go s.runIdleTimers()
	return s, nil
}

// SetKeymap configures the shared keymap FD and size distributed to clients via wl_keyboard.
// Any already-connected clients that have bound wl_keyboard immediately receive a new
// wl_keyboard.keymap event.
func (s *Server) SetKeymap(fd int, size uint32) {
	s.clientMu.Lock()
	s.keymapFD = fd
	s.keymapSize = size
	if fd < 0 || size == 0 {
		s.clientMu.Unlock()
		return
	}
	clients := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientMu.Unlock()

	for _, c := range clients {
		c.SendKeyboardKeymap(fd, size)
	}
}

// Keymap returns the currently configured keymap FD and byte size.
func (s *Server) Keymap() (int, uint32) {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.keymapFD, s.keymapSize
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

// SetOnToplevelMeta registers a callback for title/app-id changes after a
// toplevel is created (clients set them a moment after mapping).
func (s *Server) SetOnToplevelMeta(fn func(clientID, toplevelID uint32, title, appID string)) {
	s.onToplevelMeta = fn
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

type idleNotif struct {
	client         *Client
	objectID       uint32
	timeout        time.Duration
	idled          bool
	obeyInhibitors bool
}

type idleInhibitor struct {
	clientID  uint32
	objectID  uint32
	surfaceID uint32
}

// IsIdleInhibited reports whether any client holds an active zwp_idle_inhibitor_v1.
func (s *Server) IsIdleInhibited() bool {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	return len(s.idleInhibitors) > 0
}

// NotifyInputActivity resets the idle timers and immediately reports resumed
// to any notification that had fired — resumed is a consequence of activity,
// so it cannot wait for the next poll (which may land past the timeout). The
// compositor calls this on any input event.
func (s *Server) NotifyInputActivity() {
	s.idleMu.Lock()
	s.lastActivity = time.Now()
	type fire struct {
		c  *Client
		id uint32
	}
	var resumed []fire
	for i := range s.idleNotifs {
		if s.idleNotifs[i].idled {
			s.idleNotifs[i].idled = false
			resumed = append(resumed, fire{s.idleNotifs[i].client, s.idleNotifs[i].objectID})
		}
	}
	s.idleMu.Unlock()
	for _, f := range resumed {
		f.c.sendIdleNotification(f.id, false)
	}
}

func (s *Server) addIdleNotif(c *Client, objectID uint32, timeoutMs uint32, obeyInhibitors bool) {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	if s.lastActivity.IsZero() {
		s.lastActivity = time.Now()
	}
	s.idleNotifs = append(s.idleNotifs, idleNotif{
		client:         c,
		objectID:       objectID,
		timeout:        time.Duration(timeoutMs) * time.Millisecond,
		obeyInhibitors: obeyInhibitors,
	})
}

func (s *Server) removeIdleNotif(objectID uint32) {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	out := s.idleNotifs[:0]
	for _, n := range s.idleNotifs {
		if n.objectID != objectID {
			out = append(out, n)
		}
	}
	s.idleNotifs = out
}

func (s *Server) addIdleInhibitor(clientID, objectID, surfaceID uint32) {
	s.idleMu.Lock()
	s.idleInhibitors = append(s.idleInhibitors, idleInhibitor{
		clientID:  clientID,
		objectID:  objectID,
		surfaceID: surfaceID,
	})
	type fire struct {
		c  *Client
		id uint32
	}
	var resumed []fire
	for i := range s.idleNotifs {
		if s.idleNotifs[i].obeyInhibitors && s.idleNotifs[i].idled {
			s.idleNotifs[i].idled = false
			resumed = append(resumed, fire{s.idleNotifs[i].client, s.idleNotifs[i].objectID})
		}
	}
	s.idleMu.Unlock()
	for _, f := range resumed {
		f.c.sendIdleNotification(f.id, false)
	}
}

func (s *Server) removeIdleInhibitor(clientID, objectID uint32) {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	wasInhibited := len(s.idleInhibitors) > 0
	out := s.idleInhibitors[:0]
	for _, inh := range s.idleInhibitors {
		if !(inh.clientID == clientID && inh.objectID == objectID) {
			out = append(out, inh)
		}
	}
	s.idleInhibitors = out
	if wasInhibited && len(s.idleInhibitors) == 0 {
		s.lastInhibitEnd = time.Now()
	}
}

func (s *Server) removeSurfaceIdleInhibitors(clientID, surfaceID uint32) {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	wasInhibited := len(s.idleInhibitors) > 0
	out := s.idleInhibitors[:0]
	for _, inh := range s.idleInhibitors {
		if !(inh.clientID == clientID && inh.surfaceID == surfaceID) {
			out = append(out, inh)
		}
	}
	s.idleInhibitors = out
	if wasInhibited && len(s.idleInhibitors) == 0 {
		s.lastInhibitEnd = time.Now()
	}
}

// dropClientIdle removes a disconnecting client's notifications and inhibitors
// so the ticker never sends to or remains inhibited by a dead connection.
func (s *Server) dropClientIdle(clientID uint32) {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	out := s.idleNotifs[:0]
	for _, n := range s.idleNotifs {
		if n.client.ID() != clientID {
			out = append(out, n)
		}
	}
	s.idleNotifs = out

	wasInhibited := len(s.idleInhibitors) > 0
	inhOut := s.idleInhibitors[:0]
	for _, inh := range s.idleInhibitors {
		if inh.clientID != clientID {
			inhOut = append(inhOut, inh)
		}
	}
	s.idleInhibitors = inhOut
	if wasInhibited && len(s.idleInhibitors) == 0 {
		s.lastInhibitEnd = time.Now()
	}
}

// runIdleTimers fires idled/resumed on registered notifications. Started once
// per server; exits when the server closes.
func (s *Server) runIdleTimers() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if s.closed.Load() {
			return
		}
		s.idleMu.Lock()
		now := time.Now()
		inputIdleFor := now.Sub(s.lastActivity)
		regularBase := s.lastActivity
		if s.lastInhibitEnd.After(regularBase) {
			regularBase = s.lastInhibitEnd
		}
		regularIdleFor := now.Sub(regularBase)
		inhibited := len(s.idleInhibitors) > 0

		type fire struct {
			c     *Client
			id    uint32
			idled bool
		}
		var fires []fire
		for i := range s.idleNotifs {
			n := &s.idleNotifs[i]
			if n.idled {
				continue
			}
			if n.obeyInhibitors {
				if !inhibited && regularIdleFor >= n.timeout {
					n.idled = true
					fires = append(fires, fire{n.client, n.objectID, true})
				}
			} else if inputIdleFor >= n.timeout {
				n.idled = true
				fires = append(fires, fire{n.client, n.objectID, true})
			}
		}
		s.idleMu.Unlock()
		for _, f := range fires {
			f.c.sendIdleNotification(f.id, f.idled)
		}
	}
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
			s.dropClientIdle(id)
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
