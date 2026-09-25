package input

import (
	"bloxwm/internal/layout"
	"bloxwm/internal/server"
)

// WindowInfo provides spatial and protocol routing targets for a window.
type WindowInfo struct {
	WinID     layout.WindowID
	ClientID  uint32
	SurfaceID uint32
	Rect      layout.Rect
}

// Router coordinates hardware/synthetic input events, hit-testing against
// window geometry, and distributing Wayland pointer/keyboard events to clients.
type Router struct {
	cursorX float64
	cursorY float64

	hoverWinID     layout.WindowID
	hoverClientID  uint32
	hoverSurfaceID uint32

	focusWinID     layout.WindowID
	focusClientID  uint32
	focusSurfaceID uint32

	lookupWindow  func(x, y float64) (info WindowInfo, hit bool)
	getClient     func(clientID uint32) *server.Client
	onFocusChange func(oldWin, newWin layout.WindowID)
}

// NewRouter constructs a new input event router.
func NewRouter(
	lookupWindow func(x, y float64) (WindowInfo, bool),
	getClient func(clientID uint32) *server.Client,
	onFocusChange func(oldWin, newWin layout.WindowID),
) *Router {
	return &Router{
		lookupWindow:  lookupWindow,
		getClient:     getClient,
		onFocusChange: onFocusChange,
	}
}

// CursorPosition returns the current global cursor coordinates.
func (r *Router) CursorPosition() (float64, float64) {
	return r.cursorX, r.cursorY
}

// FocusWindowID returns the currently focused window ID.
func (r *Router) FocusWindowID() layout.WindowID {
	return r.focusWinID
}

// SetFocus explicitly transfers keyboard focus to target window.
func (r *Router) SetFocus(targetWin layout.WindowID, clientID, surfaceID uint32) {
	if r.focusWinID == targetWin {
		return
	}

	oldWin := r.focusWinID
	if r.focusClientID != 0 && r.focusSurfaceID != 0 {
		oldClient := r.getClient(r.focusClientID)
		if oldClient != nil {
			oldClient.SendKeyboardLeave(r.focusSurfaceID)
		}
	}

	r.focusWinID = targetWin
	r.focusClientID = clientID
	r.focusSurfaceID = surfaceID

	if r.focusClientID != 0 && r.focusSurfaceID != 0 {
		newClient := r.getClient(r.focusClientID)
		if newClient != nil {
			newClient.SendKeyboardEnter(r.focusSurfaceID, nil)
		}
	}

	if r.onFocusChange != nil {
		r.onFocusChange(oldWin, targetWin)
	}
}

// ProcessEvent routes a single InputEvent to the appropriate subsystem.
// Operates with zero heap allocations (0 allocs/op).
func (r *Router) ProcessEvent(ev InputEvent) {
	timeMs := uint32(ev.TimeUsec / 1000)

	switch ev.Type {
	case EventPointerMotion:
		r.HandlePointerMotion(timeMs, r.cursorX+ev.X, r.cursorY+ev.Y)
	case EventPointerMotionAbsolute:
		r.HandlePointerMotion(timeMs, ev.X, ev.Y)
	case EventPointerButton:
		r.HandlePointerButton(timeMs, ev.Code, ev.State)
	case EventKey:
		r.HandleKeyboardKey(timeMs, ev.Code, ev.State)
	}
}

// HandlePointerMotion processes absolute pointer position changes.
func (r *Router) HandlePointerMotion(timeMs uint32, x, y float64) {
	r.cursorX = x
	r.cursorY = y

	info, hit := r.lookupWindow(x, y)

	if !hit || info.WinID != r.hoverWinID {
		// Pointer left previous window
		if r.hoverClientID != 0 && r.hoverSurfaceID != 0 {
			prevClient := r.getClient(r.hoverClientID)
			if prevClient != nil {
				prevClient.SendPointerLeave(r.hoverSurfaceID)
			}
		}

		if hit {
			r.hoverWinID = info.WinID
			r.hoverClientID = info.ClientID
			r.hoverSurfaceID = info.SurfaceID

			newClient := r.getClient(info.ClientID)
			if newClient != nil {
				sx := x - float64(info.Rect.X)
				sy := y - float64(info.Rect.Y)
				newClient.SendPointerEnter(info.SurfaceID, sx, sy)
			}
		} else {
			r.hoverWinID = 0
			r.hoverClientID = 0
			r.hoverSurfaceID = 0
		}
		return
	}

	// Pointer moved within the same window
	if r.hoverClientID != 0 && r.hoverSurfaceID != 0 {
		client := r.getClient(r.hoverClientID)
		if client != nil {
			sx := x - float64(info.Rect.X)
			sy := y - float64(info.Rect.Y)
			client.SendPointerMotion(timeMs, sx, sy)
		}
	}
}

// HandlePointerButton processes button press and release events.
func (r *Router) HandlePointerButton(timeMs uint32, button, state uint32) {
	if state == ButtonStatePressed {
		info, hit := r.lookupWindow(r.cursorX, r.cursorY)
		if hit && info.WinID != r.focusWinID {
			r.SetFocus(info.WinID, info.ClientID, info.SurfaceID)
		}
	}

	if r.hoverClientID != 0 {
		client := r.getClient(r.hoverClientID)
		if client != nil {
			client.SendPointerButton(timeMs, button, state)
		}
	}
}

// HandleKeyboardKey processes key press and release events.
func (r *Router) HandleKeyboardKey(timeMs uint32, code, state uint32) {
	if r.focusClientID != 0 {
		client := r.getClient(r.focusClientID)
		if client != nil {
			client.SendKeyboardKey(timeMs, code, state)
		}
	}
}

// HandleKeyboardModifiers processes modifier state updates.
func (r *Router) HandleKeyboardModifiers(depressed, latched, locked, group uint32) {
	if r.focusClientID != 0 {
		client := r.getClient(r.focusClientID)
		if client != nil {
			client.SendKeyboardModifiers(depressed, latched, locked, group)
		}
	}
}
