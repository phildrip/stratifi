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
	engine *layout.Engine
	output layout.Rect

	windows      map[layout.WindowID]*WindowMapping
	surfaceToWin map[objRef]layout.WindowID
	activeWin    layout.WindowID

	sceneInfo    *atomic.Pointer[[]input.WindowInfo]
	clientOf     func(clientID uint32) shellClient
	requestFocus func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32)
}

func newShellState(
	engine *layout.Engine,
	output layout.Rect,
	sceneInfo *atomic.Pointer[[]input.WindowInfo],
	clientOf func(clientID uint32) shellClient,
	requestFocus func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32),
) *shellState {
	return &shellState{
		engine:       engine,
		output:       output,
		windows:      make(map[layout.WindowID]*WindowMapping),
		surfaceToWin: make(map[objRef]layout.WindowID),
		sceneInfo:    sceneInfo,
		clientOf:     clientOf,
		requestFocus: requestFocus,
	}
}

// publishScene pushes a lock-free WindowInfo list for the router's
// hit-testing (atomic.Pointer swap: readers never block).
func (s *shellState) publishScene() {
	list := make([]input.WindowInfo, 0, len(s.windows))
	for _, m := range s.windows {
		list = append(list, input.WindowInfo{
			WinID:      m.WinID,
			ClientID:   m.ClientID,
			SurfaceID:  m.SurfaceID,
			ToplevelID: m.ToplevelID,
			Rect:       m.Rect,
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
		_ = s.engine.AddWindow(winID)

		winIDs, rects := s.engine.Compute(s.output)
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
				_ = s.engine.RemoveWindow(winID)
				if s.activeWin == winID {
					s.activeWin = 0
				}
				fmt.Printf("[-] Window closed: WinID=%d ClientID=%d\n", winID, ev.clientID)
			}
		}

		// Recompute layout for the survivors and reconfigure them.
		winIDs, rects := s.engine.Compute(s.output)
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
