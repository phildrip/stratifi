package main

import (
	"sync/atomic"
	"testing"

	"bloxwm/internal/input"
	"bloxwm/internal/layout"
)

type fakeClient struct {
	surfaceID  uint32
	configured []layout.Rect
	closed     []uint32
}

func (f *fakeClient) ConfigureToplevel(_, _ uint32, w, h int32, _ []byte) {
	f.configured = append(f.configured, layout.Rect{W: w, H: h})
}

func (f *fakeClient) GetSurfaceIDForXdgSurface(uint32) uint32 { return f.surfaceID }

func (f *fakeClient) SendToplevelClose(toplevelID uint32) { f.closed = append(f.closed, toplevelID) }

type focusCall struct {
	winID     layout.WindowID
	clientID  uint32
	surfaceID uint32
}

type shellHarness struct {
	shell     *shellState
	focus     *[]focusCall
	client    *fakeClient
	sceneInfo *atomic.Pointer[[]input.WindowInfo]
}

func newShellHarness() *shellHarness {
	engine := layout.NewEngine(layout.LayoutConfig{Gap: 8, BorderWidth: 3, TitlebarHeight: 28})
	h := &shellHarness{
		focus:     &[]focusCall{},
		client:    &fakeClient{surfaceID: 3},
		sceneInfo: &atomic.Pointer[[]input.WindowInfo]{},
	}
	h.shell = newShellState(
		engine, layout.Rect{X: 12, Y: 12, W: 1256, H: 696}, h.sceneInfo,
		func(uint32) shellClient { return h.client },
		func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32) {
			*h.focus = append(*h.focus, focusCall{winID, clientID, surfaceID})
		},
	)
	return h
}

// windowAddedEvent is the event the server emits for a new xdg_toplevel.
func windowAddedEvent(clientID, xdgSurfaceID, toplevelID uint32) shellEvent {
	return shellEvent{
		kind:         evWindowAdded,
		clientID:     clientID,
		xdgSurfaceID: xdgSurfaceID,
		toplevelID:   toplevelID,
	}
}

// TestShellFocusesNewWindow is the regression for "typing does nothing":
// the router only forwards keys to its focused client, so the shell must
// request focus when a window appears — and again once it is mapped.
func TestShellFocusesNewWindow(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))

	if len(*h.focus) != 1 {
		t.Fatalf("focus requests = %d, want 1 on window add", len(*h.focus))
	}
	got := (*h.focus)[0]
	if got.clientID != 1 || got.surfaceID != 3 {
		t.Errorf("focus = client %d surface %d, want client 1 surface 3", got.clientID, got.surfaceID)
	}
	m, ok := h.shell.windows[got.winID]
	if !ok {
		t.Fatalf("focused winID %d not registered", got.winID)
	}
	if h.shell.activeWin != got.winID {
		t.Errorf("activeWin = %d, want %d", h.shell.activeWin, got.winID)
	}

	// A commit on the mapped surface re-asserts focus (the first request
	// may precede the client's wl_keyboard). SetFocus de-dupes upstream.
	h.shell.applyEvent(shellEvent{kind: evCommitted, clientID: 1, surfaceID: m.SurfaceID})
	if len(*h.focus) != 2 {
		t.Fatalf("focus requests = %d, want 2 after commit of the active window", len(*h.focus))
	}
}

// TestShellPublishesSceneOnWindowAdded: the router hit-tests against the
// published snapshot, so a new window must be visible to it immediately —
// otherwise pointer events (hover, click-to-focus) miss the window.
func TestShellPublishesSceneOnWindowAdded(t *testing.T) {
	h := newShellHarness()
	if h.sceneInfo.Load() != nil {
		t.Fatal("scene published before any window existed")
	}

	h.shell.applyEvent(windowAddedEvent(1, 16, 17))

	list := h.sceneInfo.Load()
	if list == nil || len(*list) != 1 {
		t.Fatalf("scene snapshot = %v, want one window", list)
	}
	w := (*list)[0]
	if w.ClientID != 1 || w.SurfaceID != 3 || w.WinID == 0 {
		t.Errorf("published window = %+v, want client 1 surface 3", w)
	}
}

// TestShellConfiguresClientBodySize: the client is told the tiled body
// size (window minus borders and titlebar).
func TestShellConfiguresClientBodySize(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))

	if len(h.client.configured) == 0 {
		t.Fatal("client was never configured")
	}
	body := h.client.configured[len(h.client.configured)-1]
	m := h.shell.windows[h.shell.activeWin]
	if want := int32(m.Rect.W - 2*3); body.W != want {
		t.Errorf("configured body width = %d, want %d", body.W, want)
	}
	if want := int32(m.Rect.H - 28 - 2*3); body.H != want {
		t.Errorf("configured body height = %d, want %d", body.H, want)
	}
}

// TestShellRefocusesSurvivorOnClientGone: when the focused client exits,
// focus falls to the first remaining window.
func TestShellRefocusesSurvivorOnClientGone(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	first := h.shell.activeWin

	h.client.surfaceID = 9
	h.shell.applyEvent(windowAddedEvent(2, 18, 19))
	second := h.shell.activeWin
	if second == first {
		t.Fatal("second window did not become active")
	}

	*h.focus = (*h.focus)[:0]
	h.shell.applyEvent(shellEvent{kind: evClientGone, clientID: 2})

	if _, ok := h.shell.windows[first]; !ok {
		t.Fatal("surviving window was dropped")
	}
	if _, ok := h.shell.windows[second]; ok {
		t.Fatal("departed window still registered")
	}
	if h.shell.activeWin != first {
		t.Fatalf("activeWin = %d, want survivor %d", h.shell.activeWin, first)
	}
	if len(*h.focus) != 1 || (*h.focus)[0].winID != first {
		t.Fatalf("focus = %+v, want the surviving window %d", *h.focus, first)
	}
}

// TestShellCloseActive asks the active window's client to close gracefully.
func TestShellCloseActive(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))

	h.shell.applyEvent(shellEvent{kind: evCloseActive})
	if len(h.client.closed) != 1 || h.client.closed[0] != 17 {
		t.Fatalf("closed = %v, want [17] (the active toplevel)", h.client.closed)
	}

	// No active window: silent no-op, never a nil dereference.
	h2 := newShellHarness()
	h2.shell.applyEvent(shellEvent{kind: evCloseActive})
	if len(h2.client.closed) != 0 {
		t.Fatalf("closed = %v, want nothing", h2.client.closed)
	}
}

// TestShellFocusCyclesWindows: next/prev walk creation order deterministically.
func TestShellFocusCyclesWindows(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	first := h.shell.activeWin
	h.shell.applyEvent(windowAddedEvent(2, 18, 19))
	second := h.shell.activeWin
	if second == first {
		t.Fatal("second window did not become active")
	}

	*h.focus = (*h.focus)[:0]
	h.shell.applyEvent(shellEvent{kind: evFocusNext})
	if h.shell.activeWin != first {
		t.Fatalf("next from %d = %d, want wrap to %d", second, h.shell.activeWin, first)
	}
	h.shell.applyEvent(shellEvent{kind: evFocusPrev})
	if h.shell.activeWin != second {
		t.Fatalf("prev from %d = %d, want %d", first, h.shell.activeWin, second)
	}
	// Focus requests follow the active window.
	if len(*h.focus) != 2 || (*h.focus)[0].winID != first || (*h.focus)[1].winID != second {
		t.Fatalf("focus trail = %+v, want [%d %d]", *h.focus, first, second)
	}

	// A lone window is a no-op (no divide by zero, no focus churn).
	h2 := newShellHarness()
	h2.shell.applyEvent(windowAddedEvent(1, 16, 17))
	*h2.focus = (*h2.focus)[:0]
	before := len(*h2.focus)
	h2.shell.applyEvent(shellEvent{kind: evFocusNext})
	if h2.shell.activeWin == 0 || len(*h2.focus) != before {
		// activeWin valid and no new request: single-window cycle is silent.
		t.Fatalf("lone-window cycle changed state")
	}
}
