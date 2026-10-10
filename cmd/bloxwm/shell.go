package main

import (
	"fmt"
	"sort"
	"sync/atomic"

	"bloxwm/internal/input"
	"bloxwm/internal/layout"
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

	windows      map[layout.WindowID]*WindowMapping
	surfaceToWin map[objRef]layout.WindowID
	activeWin    layout.WindowID

	// Popups keyed by (clientID, popupID): placed over the parent window
	// body from the configure geometry the server computed.
	popups map[popupKey]*popupEntry

	sceneInfo    *atomic.Pointer[[]input.WindowInfo]
	clientOf     func(clientID uint32) shellClient
	requestFocus func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32)
}

func newShellState(
	workspaces *layout.Workspaces,
	output layout.Rect,
	sceneInfo *atomic.Pointer[[]input.WindowInfo],
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
		sceneInfo:    sceneInfo,
		clientOf:     clientOf,
		requestFocus: requestFocus,
	}
}

// publishScene pushes a lock-free WindowInfo list for the router's
// hit-testing (atomic.Pointer swap: readers never block).
func (s *shellState) publishScene() {
	list := make([]input.WindowInfo, 0, len(s.windows)+len(s.popups))
	for _, m := range s.windows {
		if !s.workspaces.Visible(m.WinID) {
			continue // hidden on another workspace
		}
		list = append(list, input.WindowInfo{
			WinID:      m.WinID,
			ClientID:   m.ClientID,
			SurfaceID:  m.SurfaceID,
			ToplevelID: m.ToplevelID,
			Rect:       m.Rect,
		})
	}
	// Popups publish after windows: the router hit-tests in order, so a
	// popup over its parent wins clicks on its own area. Rects smaller
	// than 2x2 are unmapped noise; skip them.
	for _, p := range s.popups {
		if p.w < 2 || p.h < 2 {
			continue
		}
		list = append(list, input.WindowInfo{
			ClientID:  p.clientID,
			SurfaceID: p.surfaceID,
			Rect:      layout.Rect{X: p.x, Y: p.y, W: p.w, H: p.h},
		})
	}
	s.sceneInfo.Store(&list)
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
	winIDs, rects := s.workspaces.ComputeScrollable(s.output)
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
	c := s.clientOf(m.ClientID)
	if c == nil {
		return
	}
	bodyW := m.Rect.W - 2*s.engine.Config.BorderWidth
	bodyH := m.Rect.H - s.engine.Config.TitlebarHeight - 2*s.engine.Config.BorderWidth
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

		winIDs, rects := s.workspaces.ComputeScrollable(s.output)
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
				fmt.Printf("[-] Window closed: WinID=%d ClientID=%d\n", winID, ev.clientID)
			}
		}
		// A dead client's popups die with it; stale rects must never render.
		for key, p := range s.popups {
			if p.clientID == ev.clientID {
				delete(s.popups, key)
			}
		}

		// Recompute layout for the survivors and reconfigure them.
		winIDs, rects := s.workspaces.ComputeScrollable(s.output)
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
