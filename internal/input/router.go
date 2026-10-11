package input

import (
	"math"
	"sync/atomic"

	"bloxwm/internal/layout"
	"bloxwm/internal/server"
)

// WindowInfo provides spatial and protocol routing targets for a window.
type WindowInfo struct {
	WinID      layout.WindowID
	ClientID   uint32
	SurfaceID  uint32
	ToplevelID uint32
	Rect       layout.Rect
}

// Router coordinates hardware/synthetic input events, hit-testing against
// window geometry, and distributing Wayland pointer/keyboard events to clients.
// Focus state is single-owner: mutations happen only on the event-consumer
// goroutine (ProcessEvent / SetFocus from there); cross-goroutine focus
// changes queue through focusReq.
type Router struct {
	// Cursor position and the hovered client are read by the compositor
	// actor goroutine every frame while being written on the input
	// consumer goroutine: store them atomically (float64 bits in a
	// uint64) so the read is race-free.
	cursorX atomic.Uint64
	cursorY atomic.Uint64
	hovered atomic.Uint32

	hoverWinID     layout.WindowID
	hoverClientID  uint32
	hoverSurfaceID uint32

	focusWinID     layout.WindowID
	focusClientID  uint32
	focusSurfaceID uint32
	focusToplevel  uint32

	// Keybindings: consulted before a key reaches the client. A matched
	// binding fires onAction and consumes the keystroke (press, repeats
	// and release), so the client never sees half of it.
	keys     *Matcher
	onAction func(action string, args []string)

	lookupWindow  func(x, y float64) (info WindowInfo, hit bool)
	getClient     func(clientID uint32) *server.Client
	onFocusChange func(oldWin, newWin layout.WindowID)
	focusReq      chan focusTarget
}

// focusTarget is one queued cross-goroutine focus command.
type focusTarget struct {
	winID      layout.WindowID
	clientID   uint32
	surfaceID  uint32
	toplevelID uint32
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
		focusReq:      make(chan focusTarget, 8),
	}
}

// CursorPosition returns the current global cursor coordinates.
func (r *Router) CursorPosition() (float64, float64) {
	return math.Float64frombits(r.cursorX.Load()), math.Float64frombits(r.cursorY.Load())
}

// setCursor stores the cursor position atomically.
func (r *Router) setCursor(x, y float64) {
	r.cursorX.Store(math.Float64bits(x))
	r.cursorY.Store(math.Float64bits(y))
}

// HoverClient returns the client id currently under the pointer (0 = none).
// The actor reads it to pick the cursor image the hovered client set.
func (r *Router) HoverClient() uint32 { return r.hovered.Load() }

// FocusWindowID returns the currently focused window ID.
func (r *Router) FocusWindowID() layout.WindowID {
	return r.focusWinID
}

// SetFocus explicitly transfers keyboard focus to target window.
func (r *Router) SetFocus(targetWin layout.WindowID, clientID, surfaceID, toplevelID uint32) {
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
	r.focusToplevel = toplevelID

	if r.focusClientID != 0 && r.focusSurfaceID != 0 {
		newClient := r.getClient(r.focusClientID)
		if newClient != nil {
			newClient.SendKeyboardEnter(r.focusSurfaceID, nil)
			newClient.GiveClipboardOffer()
		}
	}

	if r.onFocusChange != nil {
		r.onFocusChange(oldWin, targetWin)
	}
}

// ProcessEvent routes a single InputEvent to the appropriate subsystem.
// Pending cross-goroutine focus commands drain first (they apply on this
// goroutine, honoring the router's single-owner focus state). Operates
// with zero heap allocations (0 allocs/op).
func (r *Router) ProcessEvent(ev InputEvent) {
	// Apply queued focus commands first (they supersede stale pointer
	// focus moves).
	for {
		select {
		case ft := <-r.focusReq:
			r.SetFocus(ft.winID, ft.clientID, ft.surfaceID, ft.toplevelID)
			continue
		default:
		}
		break
	}

	timeMs := uint32(ev.TimeUsec / 1000)

	switch ev.Type {
	case EventPointerMotion:
		cx, cy := r.CursorPosition()
		r.HandlePointerMotion(timeMs, cx+ev.X, cy+ev.Y)
	case EventPointerMotionAbsolute:
		r.HandlePointerMotion(timeMs, ev.X, ev.Y)
	case EventPointerButton:
		r.HandlePointerButton(timeMs, ev.Code, ev.State)
	case EventKey:
		r.HandleKeyboardKey(timeMs, ev.Code, ev.State)
	}
}

// EventFocusTarget is a cross-goroutine focus command: the owner (compositor
// actor) goroutine asks the router to move keyboard focus. The router owns
// its focus state; commands queue through its own event channel and apply
// on the input-consumer goroutine — no cross-goroutine method calls.
type InputEventKind uint8

const (
	EventFocusTarget InputEventKind = 0xFF
)

// RequestFocus queues a focus transfer handled on the router's own loop.
func (r *Router) RequestFocus(winID layout.WindowID, clientID, surfaceID, toplevelID uint32) {
	select {
	case r.focusReq <- focusTarget{winID: winID, clientID: clientID, surfaceID: surfaceID, toplevelID: toplevelID}:
	default:
		// A pending focus request supersedes this one (drop stale).
	}
}

// HandlePointerMotion processes absolute pointer position changes.
func (r *Router) HandlePointerMotion(timeMs uint32, x, y float64) {
	r.setCursor(x, y)

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
			r.hovered.Store(info.ClientID)

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
			r.hovered.Store(0)
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
		cx, cy := r.CursorPosition()
		info, hit := r.lookupWindow(cx, cy)
		if hit && info.WinID != r.focusWinID {
			r.SetFocus(info.WinID, info.ClientID, info.SurfaceID, info.ToplevelID)
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
	if r.keys != nil && r.onAction != nil {
		if action, args, consumed := r.keys.HandleKey(code, state); consumed {
			if action != "" {
				r.onAction(action, args)
			}
			return
		}
	} else if r.keys != nil {
		// Bindings without a handler: track mods but forward everything,
		// so a miswired router degrades to no bindings instead of eating keys.
		r.keys.HandleKey(code, state)
	}
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

// Focus returns the current keyboard focus target for diagnostics and
// tests: (windowID, clientID, surfaceID).
func (r *Router) Focus() (layout.WindowID, uint32, uint32) {
	return r.focusWinID, r.focusClientID, r.focusSurfaceID
}

// FocusToplevel reports the focused window's toplevel id (0 when unfocused),
// for actions addressed at the window itself (close).
func (r *Router) FocusToplevel() uint32 {
	return r.focusToplevel
}

// SetBindings installs the compositor keybindings (nil clears them).
func (r *Router) SetBindings(m *Matcher) {
	r.keys = m
}

// SetActionHandler installs the callback fired when a binding matches.
// It runs on the input-consumer goroutine: keep it to channel sends and
// socket writes, never actor state.
func (r *Router) SetActionHandler(fn func(action string, args []string)) {
	r.onAction = fn
}
