package main

import (
	"fmt"
	"sort"
	"sync/atomic"

	"stratifi/internal/input"
	"stratifi/internal/ipc"
	"stratifi/internal/layout"
)

// shellClient is the subset of a Wayland client the shell drives while
// laying out windows. Narrowing it keeps the actor testable without a live
// server: tests supply a fake instead of a *server.Client.
type shellClient interface {
	ConfigureToplevel(xdgSurfaceID, toplevelID uint32, width, height int32, states []byte)
	GetSurfaceIDForXdgSurface(xdgSurfaceID uint32) uint32
	SendToplevelClose(toplevelID uint32)
}

// shellState is the actor's scene state. The actor goroutine owns it
// exclusively: server callbacks and the input pump deliver plain-data
// shellEvents, which are applied here one at a time — no locks, no
// cross-goroutine method calls. Dependencies that reach outward (client
// lookup, focus requests, the lock-free scene snapshot for hit-testing)
// are injected as functions so tests can record them.
type shellState struct {
	engine     *layout.Engine     // == workspaces.Engine(); Config/metrics access
	workspaces *layout.Workspaces // the virtual-desktop strip (niri model)
	output     layout.Rect

	windows       map[layout.WindowID]*WindowMapping
	surfaceToWin  map[objRef]layout.WindowID
	activeWin     layout.WindowID
	fullscreenWin layout.WindowID // 0 = none

	// Popups keyed by (clientID, popupID): placed over the parent window
	// body from the configure geometry the server computed.
	popups map[popupKey]*popupEntry

	// Subsurfaces keyed by (clientID, child surface id): composited at an
	// offset from the parent window's body, above it, below popups.
	subsurfaces map[subsurfaceKey]*subsurfaceEntry

	// Cursors keyed by client id: the shape each client set via
	// wl_pointer.set_cursor. The hovered client's cursor is drawn; when it
	// has none, the compositor default is used.
	cursors map[uint32]cursorEntry

	// Layer surfaces (panels/bars/locks) keyed by (clientID, surfaceID).
	// They render in their layer and reserve exclusive zones the tiling
	// must avoid.
	layers map[layerKey]*layerEntry

	sceneInfo    *atomic.Pointer[[]input.WindowInfo]
	launcher     *atomic.Pointer[[]ipc.Window]
	clientOf     func(clientID uint32) shellClient
	requestFocus func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32)
}

func newShellState(
	workspaces *layout.Workspaces,
	output layout.Rect,
	sceneInfo *atomic.Pointer[[]input.WindowInfo],
	launcher *atomic.Pointer[[]ipc.Window],
	clientOf func(clientID uint32) shellClient,
	requestFocus func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32),
) *shellState {
	return &shellState{
		engine:       workspaces.Engine(),
		workspaces:   workspaces,
		output:       output,
		windows:      make(map[layout.WindowID]*WindowMapping),
		surfaceToWin: make(map[objRef]layout.WindowID),
		popups:       make(map[popupKey]*popupEntry),
		subsurfaces:  make(map[subsurfaceKey]*subsurfaceEntry),
		cursors:      make(map[uint32]cursorEntry),
		layers:       make(map[layerKey]*layerEntry),
		sceneInfo:    sceneInfo,
		launcher:     launcher,
		clientOf:     clientOf,
		requestFocus: requestFocus,
	}
}

// publishScene pushes a lock-free WindowInfo list for the router's
// hit-testing (atomic.Pointer swap: readers never block).
func (s *shellState) publishScene() {
	list := make([]input.WindowInfo, 0, len(s.windows)+len(s.popups)+len(s.layers))
	// Top/overlay layers hit-test above windows (a bar over a fullscreen
	// window must still receive clicks). Published first: the router returns
	// the first match, so front = topmost.
	for _, p := range s.layerPlacements(layerOverlay, layerTop) {
		list = append(list, input.WindowInfo{ClientID: p.clientID, SurfaceID: p.surfaceID, Rect: p.rect})
	}
	fs := s.fullscreenActive()
	for _, m := range s.windows {
		if !s.sceneVisible(m.WinID) {
			continue // hidden on another workspace, or behind fullscreen
		}
		rect := m.Rect
		if m.WinID == fs {
			rect = s.output // fullscreen owns the whole output
		}
		list = append(list, input.WindowInfo{
			WinID:      m.WinID,
			ClientID:   m.ClientID,
			SurfaceID:  m.SurfaceID,
			ToplevelID: m.ToplevelID,
			Rect:       rect,
		})
	}
	// Popups publish after windows: the router hit-tests in order, so a
	// popup over its parent wins clicks on its own area. Rects smaller
	// than 2x2 are unmapped noise; skip them. A fullscreen window's own
	// popups (menus) stay reachable; other windows' do not.
	for _, p := range s.popups {
		if p.w < 2 || p.h < 2 {
			continue
		}
		if fs != 0 && p.parentWin != fs {
			continue
		}
		list = append(list, input.WindowInfo{
			ClientID:  p.clientID,
			SurfaceID: p.surfaceID,
			Rect:      layout.Rect{X: p.x, Y: p.y, W: p.w, H: p.h},
		})
	}
	// Background/bottom layers sit beneath the windows: published last so a
	// window overlapping them wins the hit-test.
	for _, p := range s.layerPlacements(layerBackground, layerBottom) {
		list = append(list, input.WindowInfo{ClientID: p.clientID, SurfaceID: p.surfaceID, Rect: p.rect})
	}
	s.sceneInfo.Store(&list)
	s.publishLauncher()
}

// publishLauncher stores a lock-free window list (id + labels) for the IPC
// launcher. All windows are listed, across workspaces, so the switcher can
// jump anywhere; a window on another workspace is switched to on focus.
func (s *shellState) publishLauncher() {
	if s.launcher == nil {
		return
	}
	list := make([]ipc.Window, 0, len(s.windows))
	for _, m := range s.windows {
		list = append(list, ipc.Window{WinID: uint32(m.WinID), AppID: m.AppID, Title: m.Title})
	}
	s.launcher.Store(&list)
}

// focusActive points keyboard focus at the active window. The router owns
// focus state, so this queues a request (applied on the input consumer
// goroutine). Called when a window appears and again once its surface is
// mapped, so the client has a keyboard by the time events arrive.
func (s *shellState) focusActive() {
	m, ok := s.windows[s.activeWin]
	if !ok || m.SurfaceID == 0 {
		return
	}
	s.requestFocus(s.activeWin, m.ClientID, m.SurfaceID, m.ToplevelID)
}

// relayoutAll recomputes the active workspace's tiling, repositions every
// visible window, and re-asserts focus — used after workspace switches and
// window moves between workspaces. Hidden windows keep their last rect but
// are excluded from the scene (Compute only emits the active tag).
func (s *shellState) relayoutAll() {
	winIDs, rects := s.workspaces.ComputeScrollable(s.tilingArea())
	for i, id := range winIDs {
		if m, ok := s.windows[id]; ok {
			m.Rect = rects[i]
			s.configure(m)
		}
	}
	if _, ok := s.windows[s.activeWin]; !ok {
		// Active window moved away: focus the first visible one.
		s.activeWin = 0
		if len(winIDs) > 0 {
			s.activeWin = winIDs[0]
		}
	}
	s.publishScene()
	s.focusActive()
}

// configure tells a client the body size available for its window.
func (s *shellState) configure(m *WindowMapping) {
	// A fullscreen window's body is the whole output, not its tiled rect —
	// so relayout (which reconfigures every visible window) cannot clobber
	// the fullscreen size back to the tiled one.
	if m.WinID == s.fullscreenWin {
		s.configureBody(m, s.output.W, s.output.H)
		return
	}
	bodyW := m.Rect.W - 2*s.engine.Config.BorderWidth
	bodyH := m.Rect.H - s.engine.Config.TitlebarHeight - 2*s.engine.Config.BorderWidth
	s.configureBody(m, bodyW, bodyH)
}

// configureBody tells a client the exact body size to render at (used for
// fullscreen, where the body is the whole output rather than the tiled rect).
func (s *shellState) configureBody(m *WindowMapping, bodyW, bodyH int32) {
	c := s.clientOf(m.ClientID)
	if c == nil {
		return
	}
	c.ConfigureToplevel(m.XdgSurfaceID, m.ToplevelID, bodyW, bodyH, nil)
}

// applyEvent mutates scene state for one server event.
func (s *shellState) applyEvent(ev shellEvent) {
	switch ev.kind {
	case evWindowAdded:
		winID := s.engine.SlotMap.Allocate()
		_ = s.workspaces.AddWindow(winID)
		// Focus the column that just received the window (niri: new windows
		// open in the active column, scrolled into view).
		s.workspaces.Engine().SetActiveColumn(s.columnIndexOf(winID))

		winIDs, rects := s.workspaces.ComputeScrollable(s.tilingArea())
		for i, id := range winIDs {
			if m, ok := s.windows[id]; ok {
				m.Rect = rects[i]
				s.configure(m)
			}
		}

		var surfaceID uint32
		if c := s.clientOf(ev.clientID); c != nil {
			surfaceID = c.GetSurfaceIDForXdgSurface(ev.xdgSurfaceID)
		}
		assignedRect := layout.Rect{}
		for i, id := range winIDs {
			if id == winID {
				assignedRect = rects[i]
				break
			}
		}
		mapping := &WindowMapping{
			WinID:        winID,
			ClientID:     ev.clientID,
			SurfaceID:    surfaceID,
			XdgSurfaceID: ev.xdgSurfaceID,
			ToplevelID:   ev.toplevelID,
			Title:        ev.title,
			AppID:        ev.appID,
			Rect:         assignedRect,
		}
		s.windows[winID] = mapping
		s.surfaceToWin[objRef{ev.clientID, surfaceID}] = winID
		s.surfaceToWin[objRef{ev.clientID, ev.xdgSurfaceID}] = winID
		s.activeWin = winID

		s.configure(mapping)
		fmt.Printf("[+] New window registered: WinID=%d client=%d surface=%d Title=%q AppID=%q (size: %dx%d)\n",
			winID, ev.clientID, surfaceID, ev.title, ev.appID,
			assignedRect.W-2*s.engine.Config.BorderWidth,
			assignedRect.H-s.engine.Config.TitlebarHeight-2*s.engine.Config.BorderWidth)

		// Publish before focusing: the router must be able to hit-test the
		// new window, and focus alone does not make pointer events land.
		s.publishScene()
		s.focusActive()

	case evPopupAdded:
		// Place over the parent window's body: the server's geometry is
		// relative to the parent body origin, so add the body's output
		// rect (the window's content area inside border+titlebar).
		key := popupKey{ev.clientID, ev.popupID}
		if _, dup := s.popups[key]; dup {
			return
		}
		bodyRect, parentWin := s.popupParentBody(ev.xdgSurfaceID)
		if parentWin == 0 {
			return // parent gone: drop the popup
		}
		s.popups[key] = &popupEntry{
			clientID:  ev.clientID,
			surfaceID: ev.surfaceID,
			parentWin: parentWin,
			x:         bodyRect.X + ev.px,
			y:         bodyRect.Y + ev.py,
			w:         ev.pw,
			h:         ev.ph,
		}
		s.publishScene()

	case evSubsurfaceAdded:
		// Position is relative to the parent SURFACE origin, which is the
		// window's client-body origin (inside border + titlebar). Store the
		// offset relative to the body so a relayout repositions it too.
		winID, ok := s.surfaceToWin[objRef{ev.clientID, ev.parentSurfaceID}]
		if !ok {
			return // parent window gone: drop
		}
		s.subsurfaces[subsurfaceKey{ev.clientID, ev.surfaceID}] = &subsurfaceEntry{
			clientID:  ev.clientID,
			surfaceID: ev.surfaceID,
			parentWin: winID,
			ox:        ev.px,
			oy:        ev.py,
			w:         ev.pw,
			h:         ev.ph,
			order:     ev.ssOrder,
		}
		fmt.Printf("[+] Subsurface: client=%d surface=%d parent_win=%d offset=(%d,%d) size=%dx%d order=%d\n",
			ev.clientID, ev.surfaceID, winID, ev.px, ev.py, ev.pw, ev.ph, ev.ssOrder)
		s.publishScene()

	case evSubsurfaceGone:
		delete(s.subsurfaces, subsurfaceKey{ev.clientID, ev.surfaceID})
		s.publishScene()

	case evCursorSet:
		// The hovered client's cursor is drawn; a cleared cursor (active
		// false) falls back to the compositor default.
		s.cursors[ev.clientID] = cursorEntry{
			clientID:  ev.clientID,
			surfaceID: ev.surfaceID,
			hx:        ev.px,
			hy:        ev.py,
			w:         ev.pw,
			h:         ev.ph,
			active:    ev.cursorOn,
		}

	case evLayerAdded:
		key := layerKey{ev.clientID, ev.surfaceID}
		s.layers[key] = &layerEntry{
			clientID:  ev.clientID,
			surfaceID: ev.surfaceID,
			layer:     ev.layerIndex,
			namespace: ev.namespace,
			w:         ev.pw,
			h:         ev.ph,
			anchor:    ev.anchor,
			exclusive: ev.exclusive,
			margin:    ev.margin,
			keyboard:  ev.keyboard,
		}
		// A new exclusive zone changes the tiling area.
		s.relayoutAll()
		// Exclusive keyboard interactivity takes focus on map (locks).
		if ev.keyboard == 1 {
			s.requestFocus(0, ev.clientID, ev.surfaceID, 0)
		}

	case evLayerGone:
		delete(s.layers, layerKey{ev.clientID, ev.surfaceID})
		s.relayoutAll() // the reserved zone is released

	case evCommitted:
		if winID, ok := s.surfaceToWin[objRef{ev.clientID, ev.surfaceID}]; ok {
			if m, exists := s.windows[winID]; exists {
				m.SurfaceID = ev.surfaceID
				// Re-assert focus once the surface is mapped: the initial
				// focus request may precede the client's wl_keyboard, and
				// SetFocus is a no-op if unchanged.
				if winID == s.activeWin {
					s.focusActive()
				}
			}
		}

	case evClientGone:
		for winID, m := range s.windows {
			if m.ClientID == ev.clientID {
				delete(s.windows, winID)
				delete(s.surfaceToWin, objRef{m.ClientID, m.SurfaceID})
				delete(s.surfaceToWin, objRef{m.ClientID, m.XdgSurfaceID})
				_ = s.workspaces.RemoveWindow(winID)
				if s.activeWin == winID {
					s.activeWin = 0
				}
				if s.fullscreenWin == winID {
					s.fullscreenWin = 0
				}
				fmt.Printf("[-] Window closed: WinID=%d ClientID=%d\n", winID, ev.clientID)
			}
		}
		// A dead client's popups die with it; stale rects must never render.
		for key, p := range s.popups {
			if p.clientID == ev.clientID {
				delete(s.popups, key)
			}
		}
		// Subsurfaces die with their client too.
		for key, ss := range s.subsurfaces {
			if ss.clientID == ev.clientID {
				delete(s.subsurfaces, key)
			}
		}
		// And so does the client's cursor shape.
		delete(s.cursors, ev.clientID)
		// Layer surfaces die with their client; release their zones.
		for key, l := range s.layers {
			if l.clientID == ev.clientID {
				delete(s.layers, key)
			}
		}

		// Recompute layout for the survivors and reconfigure them.
		winIDs, rects := s.workspaces.ComputeScrollable(s.tilingArea())
		for i, id := range winIDs {
			if m, ok := s.windows[id]; ok {
				m.Rect = rects[i]
				s.configure(m)
			}
		}

		// Focus falls to the first remaining window: a plain channel
		// command the router applies on its own loop — no cross-goroutine
		// method calls, no lock-order contracts.
		if len(winIDs) > 0 {
			if m, ok := s.windows[winIDs[0]]; ok {
				s.activeWin = winIDs[0]
				s.requestFocus(winIDs[0], m.ClientID, m.SurfaceID, m.ToplevelID)
			}
		}

		s.publishScene()

	case evColumnNext:
		s.workspaces.MoveActiveColumn(1)
		s.relayoutAll()
		s.focusColumnActive()

	case evColumnPrev:
		s.workspaces.MoveActiveColumn(-1)
		s.relayoutAll()
		s.focusColumnActive()

	case evFullscreen:
		s.toggleFullscreen()

	case evWindowMeta:
		// Titles/app-ids arrive after the window is mapped: update the
		// mapping and refresh the launcher list.
		for _, m := range s.windows {
			if m.ToplevelID == ev.toplevelID && m.ClientID == ev.clientID {
				m.Title = ev.title
				m.AppID = ev.appID
			}
		}
		s.publishLauncher()

	case evFocusWindow:
		if _, ok := s.windows[ev.winID]; !ok {
			return
		}
		// Jump to the window's workspace if it lives elsewhere.
		if ws := s.engine.WorkspaceOf(ev.winID); ws != 0 && ws != uint32(s.workspaces.ActiveID()) {
			s.workspaces.SwitchTo(layout.WorkspaceID(ws))
		}
		s.activeWin = ev.winID
		s.relayoutAll()
		s.focusActive()

	case evFocusWindowUp:
		s.focusWithinColumn(-1)

	case evFocusWindowDown:
		s.focusWithinColumn(1)

	case evMoveWindowUp:
		s.moveWithinColumn(-1)

	case evMoveWindowDown:
		s.moveWithinColumn(1)

	case evMoveColLeft:
		s.moveColumn(-1)

	case evMoveColRight:
		s.moveColumn(1)

	case evWorkspaceNext:
		s.workspaces.SwitchNext()
		s.relayoutAll()

	case evWorkspacePrev:
		s.workspaces.SwitchPrev()
		s.relayoutAll()

	case evWorkspaceSwitch:
		s.workspaces.SwitchToNumber(ev.wsIndex)
		s.relayoutAll()

	case evWorkspaceMove:
		if m, ok := s.windows[s.activeWin]; ok {
			if s.workspaces.MoveWindowToWorkspace(m.WinID, s.workspaces.Ensure(ev.wsIndex)) {
				s.relayoutAll()
			}
		}

	case evCloseActive:
		m, ok := s.windows[s.activeWin]
		if !ok {
			return
		}
		if c := s.clientOf(m.ClientID); c != nil {
			c.SendToplevelClose(m.ToplevelID)
		}

	case evFocusNext, evFocusPrev:
		if len(s.windows) < 2 {
			return
		}
		ids := make([]layout.WindowID, 0, len(s.windows))
		for id := range s.windows {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		at := 0
		for i, id := range ids {
			if id == s.activeWin {
				at = i
				break
			}
		}
		if ev.kind == evFocusNext {
			at = (at + 1) % len(ids)
		} else {
			at = (at - 1 + len(ids)) % len(ids)
		}
		s.activeWin = ids[at]
		s.focusActive()
	}
}

// popupKey scopes popup lookups: object ids are per-client.
type popupKey struct {
	clientID uint32
	popupID  uint32
}

// popupEntry is the actor's record of one popup surface: which window it
// parents onto and where it sits inside the output.
type popupEntry struct {
	clientID   uint32
	surfaceID  uint32
	parentWin  layout.WindowID
	x, y, w, h int32
}

// Layer-surface layer constants (zwlr_layer_shell_v1).
const (
	layerBackground = 0
	layerBottom     = 1
	layerTop        = 2
	layerOverlay    = 3
)

// Anchor bit constants (zwlr_layer_surface_v1).
const (
	anchorTop    = 1
	anchorBottom = 2
	anchorLeft   = 4
	anchorRight  = 8
)

// layerKey scopes layer-surface lookups by client and surface id.
type layerKey struct {
	clientID  uint32
	surfaceID uint32
}

// layerEntry is the actor's record of one layer surface.
type layerEntry struct {
	clientID  uint32
	surfaceID uint32
	layer     uint32
	namespace string
	w, h      int32
	anchor    uint32
	exclusive int32
	margin    [4]int32 // top, right, bottom, left
	keyboard  uint32
}

// placeLayer resolves a layer surface to an output rect from its anchors,
// size and margins. out is the full output (layer surfaces position against
// the output, not the tiling area).
func placeLayer(out layout.Rect, l *layerEntry) layout.Rect {
	mt, mr, mb, ml := l.margin[0], l.margin[1], l.margin[2], l.margin[3]
	w, h := l.w, l.h
	var x, y int32
	switch {
	case l.anchor&anchorLeft != 0 && l.anchor&anchorRight != 0:
		x = out.X + ml
		if w == 0 {
			w = out.W - ml - mr
		}
	case l.anchor&anchorLeft != 0:
		x = out.X + ml
	case l.anchor&anchorRight != 0:
		x = out.X + out.W - mr - w
	default:
		x = out.X + (out.W-w)/2
	}
	switch {
	case l.anchor&anchorTop != 0 && l.anchor&anchorBottom != 0:
		y = out.Y + mt
		if h == 0 {
			h = out.H - mt - mb
		}
	case l.anchor&anchorTop != 0:
		y = out.Y + mt
	case l.anchor&anchorBottom != 0:
		y = out.Y + out.H - mb - h
	default:
		y = out.Y + (out.H-h)/2
	}
	return layout.Rect{X: x, Y: y, W: w, H: h}
}

// tilingArea returns the output minus any exclusive zones reserved by layer
// surfaces (e.g. a top bar): the region the tiling and maximized windows use.
func (s *shellState) tilingArea() layout.Rect {
	out := s.output
	for _, l := range s.layers {
		if l.exclusive <= 0 {
			continue
		}
		z := l.exclusive
		switch {
		case l.anchor&anchorTop != 0 && l.anchor&anchorBottom == 0:
			out.Y += z
			out.H -= z
		case l.anchor&anchorBottom != 0 && l.anchor&anchorTop == 0:
			out.H -= z
		case l.anchor&anchorLeft != 0 && l.anchor&anchorRight == 0:
			out.X += z
			out.W -= z
		case l.anchor&anchorRight != 0 && l.anchor&anchorLeft == 0:
			out.W -= z
		}
	}
	if out.W < 1 {
		out.W = 1
	}
	if out.H < 1 {
		out.H = 1
	}
	return out
}

// cursorEntry is one client's cursor shape: the wl_surface it set plus the
// hotspot and the image size (0 size = not yet mapped).
type cursorEntry struct {
	clientID  uint32
	surfaceID uint32
	hx, hy    int32
	w, h      int32
	active    bool
}

// subsurfaceKey scopes subsurface lookups by client and child surface id.
type subsurfaceKey struct {
	clientID  uint32
	surfaceID uint32
}

// subsurfaceEntry is the actor's record of one wl_subsurface: which window
// it belongs to, its offset from that window's body origin, its size, and
// its stack order among the parent's subsurfaces.
type subsurfaceEntry struct {
	clientID  uint32
	surfaceID uint32
	parentWin layout.WindowID
	ox, oy    int32 // offset from the parent body origin
	w, h      int32
	order     int32
}

// fullscreenActive returns the fullscreen window id, or 0 if none is set or
// the window has since disappeared (so a stale id can never blank the scene).
func (s *shellState) fullscreenActive() layout.WindowID {
	if s.fullscreenWin == 0 {
		return 0
	}
	if _, ok := s.windows[s.fullscreenWin]; !ok {
		return 0
	}
	return s.fullscreenWin
}

// sceneVisible reports whether a window is currently on screen: on the
// active workspace, and — while something is fullscreen — only if it IS the
// fullscreen window.
func (s *shellState) sceneVisible(winID layout.WindowID) bool {
	if !s.workspaces.Visible(winID) {
		return false
	}
	if fs := s.fullscreenActive(); fs != 0 && winID != fs {
		return false
	}
	return true
}

// toggleFullscreen makes the active window fill the output (hiding the
// other windows on its workspace), or restores the tiling if it already is.
func (s *shellState) toggleFullscreen() {
	if s.fullscreenWin != 0 {
		s.fullscreenWin = 0
		s.relayoutAll()
		return
	}
	if _, ok := s.windows[s.activeWin]; !ok {
		return
	}
	s.fullscreenWin = s.activeWin
	s.relayoutAll() // configure() sizes the fullscreen window to the output
}

// focusWithinColumn moves focus to the window delta rows away in the active
// window's column (niri: the column's stack), clamped to the stack.
func (s *shellState) focusWithinColumn(delta int) {
	col, row, ok := s.engine.WindowPos(s.activeWin)
	if !ok {
		return
	}
	wins := s.engine.Columns[col].Windows
	dst := row + delta
	if dst < 0 || dst >= len(wins) {
		return
	}
	s.activeWin = wins[dst]
	s.focusActive()
}

// moveWithinColumn reorders the active window by delta rows in its column.
func (s *shellState) moveWithinColumn(delta int) {
	if !s.engine.MoveWindowWithinColumn(s.activeWin, delta) {
		return
	}
	s.relayoutAll()
}

// moveColumn reorders the active column by delta (niri: move column
// left/right), keeping focus on the moved column.
func (s *shellState) moveColumn(delta int) {
	if !s.workspaces.ReorderActiveColumn(delta) {
		return
	}
	s.relayoutAll()
}

// bodyRectOf returns a window's client-body rectangle inside the output
// (inside the border and titlebar), or zero if the window is unknown.
func (s *shellState) bodyRectOf(winID layout.WindowID) layout.Rect {
	m, ok := s.windows[winID]
	if !ok {
		return layout.Rect{}
	}
	if winID == s.fullscreenWin {
		return s.output // fullscreen body is the whole output, no chrome
	}
	bw := s.engine.Config.BorderWidth
	th := s.engine.Config.TitlebarHeight
	return layout.Rect{
		X: m.Rect.X + bw,
		Y: m.Rect.Y + bw + th,
		W: m.Rect.W - 2*bw,
		H: m.Rect.H - bw - th - bw,
	}
}

// subsurfacePlacement is one subsurface resolved to an absolute output rect
// (parent body origin + stored offset), ready for the scene.
type subsurfacePlacement struct {
	clientID, surfaceID uint32
	parentWin           layout.WindowID
	rect                layout.Rect
	order               int32
}

// layerPlacements returns the layer surfaces resolved to output rects,
// bottom-to-top within a given set of layers. The scene draws background/
// bottom below windows and top/overlay above them.
func (s *shellState) layerPlacements(layers ...uint32) []subsurfacePlacement {
	out := make([]subsurfacePlacement, 0, len(s.layers))
	for _, l := range s.layers {
		in := false
		for _, want := range layers {
			if l.layer == want {
				in = true
				break
			}
		}
		if !in || l.w < 2 || l.h < 2 {
			continue
		}
		r := placeLayer(s.output, l)
		out = append(out, subsurfacePlacement{clientID: l.clientID, surfaceID: l.surfaceID, rect: r})
	}
	return out
}

// visibleSubsurfaces returns the subsurfaces on visible workspaces, resolved
// to output rects and ordered bottom-to-top within each parent. The scene fn
// and tests both read placement through this.
func (s *shellState) visibleSubsurfaces() []subsurfacePlacement {
	subs := make([]*subsurfaceEntry, 0, len(s.subsurfaces))
	for _, ss := range s.subsurfaces {
		if !s.sceneVisible(ss.parentWin) {
			continue
		}
		if ss.w < 2 || ss.h < 2 {
			continue // unmapped noise
		}
		subs = append(subs, ss)
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].parentWin != subs[j].parentWin {
			return subs[i].parentWin < subs[j].parentWin
		}
		return subs[i].order < subs[j].order
	})
	out := make([]subsurfacePlacement, 0, len(subs))
	for _, ss := range subs {
		body := s.bodyRectOf(ss.parentWin)
		out = append(out, subsurfacePlacement{
			clientID:  ss.clientID,
			surfaceID: ss.surfaceID,
			parentWin: ss.parentWin,
			rect:      layout.Rect{X: body.X + ss.ox, Y: body.Y + ss.oy, W: ss.w, H: ss.h},
			order:     ss.order,
		})
	}
	return out
}

// popupParentBody maps a parent xdg_surface to its window's client-body
// rectangle inside the output and the window id. Zero ids mean the parent
// is unknown or already gone.
func (s *shellState) popupParentBody(parentXdgSurfaceID uint32) (layout.Rect, layout.WindowID) {
	for _, m := range s.windows {
		if m.XdgSurfaceID == parentXdgSurfaceID {
			bw := s.engine.Config.BorderWidth
			th := s.engine.Config.TitlebarHeight
			return layout.Rect{
				X: m.Rect.X + bw,
				Y: m.Rect.Y + bw + th,
				W: m.Rect.W - 2*bw,
				H: m.Rect.H - bw - th - bw,
			}, m.WinID
		}
	}
	return layout.Rect{}, 0
}

// columnIndexOf returns the visible column index holding win on the active
// workspace, or the last column when not found.
func (s *shellState) columnIndexOf(win layout.WindowID) int {
	tag := uint32(s.workspaces.ActiveID())
	idx := 0
	found := -1
	for i := range s.engine.Columns {
		if s.engine.Columns[i].Workspace != tag {
			continue
		}
		for _, id := range s.engine.Columns[i].Windows {
			if id == win {
				found = idx
			}
		}
		idx++
	}
	if found < 0 {
		return idx - 1
	}
	return found
}

// focusColumnActive focuses the first window of the active column so
// keyboard focus follows the viewport (niri: focus and scroll together).
func (s *shellState) focusColumnActive() {
	tag := uint32(s.workspaces.ActiveID())
	idx := 0
	target := s.workspaces.ActiveColumn()
	for i := range s.engine.Columns {
		col := &s.engine.Columns[i]
		if col.Workspace != tag {
			continue
		}
		if idx == target && len(col.Windows) > 0 {
			s.activeWin = col.Windows[0]
			s.focusActive()
			return
		}
		idx++
	}
}
