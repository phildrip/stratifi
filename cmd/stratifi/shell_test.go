package main

import (
	"sync/atomic"
	"testing"

	"stratifi/internal/input"
	"stratifi/internal/layout"
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
	workspaces := layout.NewWorkspaces(layout.LayoutConfig{Gap: 8, BorderWidth: 3, TitlebarHeight: 28})
	h := &shellHarness{
		focus:     &[]focusCall{},
		client:    &fakeClient{surfaceID: 3},
		sceneInfo: &atomic.Pointer[[]input.WindowInfo]{},
	}
	h.shell = newShellState(
		workspaces, layout.Rect{X: 12, Y: 12, W: 1256, H: 696}, h.sceneInfo,
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

// TestShellPopupPlacesOverParentBody: a popup's parent-relative configure
// geometry maps into output coordinates via the window's content area.
func TestShellPopupPlacesOverParentBody(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	m := h.shell.windows[h.shell.activeWin]

	// The engine placed the window; compute the expected body rect the same
	// way applyEvent does (client area inside border+titlebar).
	bw, th := h.shell.engine.Config.BorderWidth, h.shell.engine.Config.TitlebarHeight
	want := layout.Rect{X: m.Rect.X + bw, Y: m.Rect.Y + bw + th}

	// Popup 10px into the body, 100x30.
	h.shell.applyEvent(shellEvent{
		kind:         evPopupAdded,
		clientID:     1,
		surfaceID:    90,
		popupID:      55,
		xdgSurfaceID: 16,
		px:           10,
		py:           10,
		pw:           100,
		ph:           30,
	})

	list := h.sceneInfo.Load()
	if list == nil {
		t.Fatal("scene not published")
	}
	found := false
	for _, info := range *list {
		if info.SurfaceID == 90 {
			found = true
			if info.Rect.X != want.X+10 || info.Rect.Y != want.Y+10 {
				t.Errorf("popup rect = %+v, want body+(10,10) = X%d Y%d", info.Rect, want.X+10, want.Y+10)
			}
			if info.Rect.W != 100 || info.Rect.H != 30 {
				t.Errorf("popup size = %dx%d, want 100x30", info.Rect.W, info.Rect.H)
			}
		}
	}
	if !found {
		t.Fatal("popup missing from the scene")
	}
}

// TestShellPopupDroppedWhenParentGone: a popup whose parent window is gone
// (client closed under it) is dropped, never renders at a stale rect.
func TestShellPopupDroppedWhenParentGone(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	h.client.surfaceID = 9
	h.shell.applyEvent(windowAddedEvent(2, 18, 19))
	second := h.shell.activeWin
	_ = second
	_ = h

	// Popup pointing at the FIRST window's xdg surface, then close its client.
	h.shell.applyEvent(shellEvent{
		kind:         evPopupAdded,
		clientID:     1,
		surfaceID:    90,
		popupID:      55,
		xdgSurfaceID: 16,
		px:           5, py: 5, pw: 60, ph: 20,
	})
	if _, ok := h.shell.popups[popupKey{1, 55}]; !ok {
		t.Fatal("popup not registered")
	}

	// Client 1 disappears: its popup must go too.
	h.shell.applyEvent(shellEvent{kind: evClientGone, clientID: 1})
	if _, ok := h.shell.popups[popupKey{1, 55}]; ok {
		t.Error("popup outlived its parent client")
	}
}

// TestShellWorkspaceSwitch: switching hides the old workspace's windows
// from the scene and shows the target's; the active window refocuses.
func TestShellWorkspaceSwitch(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	first := h.shell.activeWin

	// Switch to a fresh workspace 2: scene empties.
	h.shell.applyEvent(shellEvent{kind: evWorkspaceSwitch, wsIndex: 2})
	if h.shell.workspaces.ActiveID() != 2 {
		t.Fatalf("active ws = %d, want 2", h.shell.workspaces.ActiveID())
	}
	if list := h.sceneInfo.Load(); list == nil || len(*list) != 0 {
		t.Fatalf("scene = %v, want empty on fresh ws2", list)
	}

	// Add a window on ws2.
	h.client.surfaceID = 9
	h.shell.applyEvent(windowAddedEvent(2, 18, 19))
	if list := h.sceneInfo.Load(); len(*list) != 1 {
		t.Fatalf("ws2 scene = %d windows, want 1", len(*list))
	}

	// Switch back to ws1: the first window reappears, the ws2 one hides.
	h.shell.applyEvent(shellEvent{kind: evWorkspaceSwitch, wsIndex: 1})
	list := h.sceneInfo.Load()
	if len(*list) != 1 || (*list)[0].WinID != first {
		t.Fatalf("ws1 scene = %v, want only window %d", list, first)
	}
}

// TestShellWorkspaceMove: moving the active window to another workspace
// removes it from the current scene and lands it on the target.
func TestShellWorkspaceMove(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	first := h.shell.activeWin

	h.shell.applyEvent(shellEvent{kind: evWorkspaceMove, wsIndex: 2})
	if list := h.sceneInfo.Load(); len(*list) != 0 {
		t.Fatalf("scene after move = %v, want empty", list)
	}
	if h.shell.workspaces.ActiveID() != 1 {
		t.Errorf("active ws = %d, want 1 (move does not switch)", h.shell.workspaces.ActiveID())
	}
	if got := h.shell.workspaces.WindowsOf(2); len(got) != 1 || got[0] != first {
		t.Fatalf("ws2 windows = %v, want [%d]", got, first)
	}
}

// TestShellWorkspaceCycle: workspace_next/prev walk the strip (the actions
// bound to swipe gestures).
func TestShellWorkspaceCycle(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	// Ensure a second workspace exists so next has somewhere to go.
	h.shell.applyEvent(shellEvent{kind: evWorkspaceSwitch, wsIndex: 2})
	h.shell.applyEvent(shellEvent{kind: evWorkspaceSwitch, wsIndex: 1})

	h.shell.applyEvent(shellEvent{kind: evWorkspaceNext})
	if h.shell.workspaces.ActiveID() != 2 {
		t.Fatalf("after next, active = %d, want 2", h.shell.workspaces.ActiveID())
	}
	h.shell.applyEvent(shellEvent{kind: evWorkspacePrev})
	if h.shell.workspaces.ActiveID() != 1 {
		t.Fatalf("after prev, active = %d, want 1", h.shell.workspaces.ActiveID())
	}
	// The window is visible again on ws1.
	if list := h.sceneInfo.Load(); len(*list) != 1 {
		t.Fatalf("ws1 scene = %d windows, want 1", len(*list))
	}
}

// TestShellSubsurfacePlacesAndFollowsParent: a subsurface sits at its offset
// from the parent window's body, and follows the parent when the layout
// moves it.
func TestShellSubsurfacePlacesAndFollowsParent(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17)) // parent surface id 3
	parent := h.shell.activeWin

	h.shell.applyEvent(shellEvent{
		kind:            evSubsurfaceAdded,
		clientID:        1,
		surfaceID:       70,
		parentSurfaceID: 3,
		px:              10,
		py:              20,
		pw:              64,
		ph:              64,
	})

	body := h.shell.bodyRectOf(parent)
	subs := h.shell.visibleSubsurfaces()
	if len(subs) != 1 {
		t.Fatalf("visible subsurfaces = %d, want 1", len(subs))
	}
	want := layout.Rect{X: body.X + 10, Y: body.Y + 20, W: 64, H: 64}
	if subs[0].rect != want {
		t.Errorf("subsurface rect = %+v, want %+v", subs[0].rect, want)
	}

	// Adding a second window reflows the column: the parent shrinks and the
	// subsurface must move with it (offset preserved).
	h.shell.applyEvent(windowAddedEvent(1, 18, 19))
	newBody := h.shell.bodyRectOf(parent)
	if newBody == body {
		t.Skip("layout did not move the parent; nothing to verify")
	}
	subs = h.shell.visibleSubsurfaces()
	want = layout.Rect{X: newBody.X + 10, Y: newBody.Y + 20, W: 64, H: 64}
	if subs[0].rect != want {
		t.Errorf("after reflow: rect = %+v, want %+v", subs[0].rect, want)
	}
}

// TestShellSubsurfaceDroppedWithParentOrClient: subsurfaces whose parent
// window is unknown, or whose client vanished, never render.
func TestShellSubsurfaceDroppedWithParentOrClient(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))

	// Unknown parent surface: dropped.
	h.shell.applyEvent(shellEvent{
		kind: evSubsurfaceAdded, clientID: 1, surfaceID: 70,
		parentSurfaceID: 999, px: 1, py: 1, pw: 8, ph: 8,
	})
	if len(h.shell.subsurfaces) != 0 {
		t.Fatal("subsurface with unknown parent was registered")
	}

	// Real parent, then the client dies: the subsurface goes with it.
	h.shell.applyEvent(shellEvent{
		kind: evSubsurfaceAdded, clientID: 1, surfaceID: 70,
		parentSurfaceID: 3, px: 1, py: 1, pw: 8, ph: 8,
	})
	if len(h.shell.subsurfaces) != 1 {
		t.Fatal("subsurface not registered")
	}
	h.shell.applyEvent(shellEvent{kind: evClientGone, clientID: 1})
	if len(h.shell.subsurfaces) != 0 {
		t.Error("subsurface outlived its client")
	}
}

// TestShellSubsurfaceOrder: siblings render bottom-to-top by stack order.
func TestShellSubsurfaceOrder(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	h.shell.applyEvent(shellEvent{
		kind: evSubsurfaceAdded, clientID: 1, surfaceID: 70, parentSurfaceID: 3,
		px: 0, py: 0, pw: 32, ph: 32, ssOrder: 1,
	})
	h.shell.applyEvent(shellEvent{
		kind: evSubsurfaceAdded, clientID: 1, surfaceID: 71, parentSurfaceID: 3,
		px: 0, py: 0, pw: 32, ph: 32, ssOrder: 0,
	})
	subs := h.shell.visibleSubsurfaces()
	if len(subs) != 2 {
		t.Fatalf("subsurfaces = %d, want 2", len(subs))
	}
	if subs[0].surfaceID != 71 || subs[1].surfaceID != 70 {
		t.Errorf("order = [%d %d], want [71 70] (order 0 first)", subs[0].surfaceID, subs[1].surfaceID)
	}
}

// TestShellCursorSetAndCleared: the shell tracks the cursor shape a client
// sets, and drops it when the client clears it or disconnects.
func TestShellCursorSetAndCleared(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(shellEvent{
		kind: evCursorSet, clientID: 1, surfaceID: 20,
		px: 7, py: 4, pw: 24, ph: 24, cursorOn: true,
	})
	ce, ok := h.shell.cursors[1]
	if !ok || !ce.active || ce.surfaceID != 20 || ce.hx != 7 || ce.hy != 4 || ce.w != 24 || ce.h != 24 {
		t.Fatalf("cursor entry = %+v, want active 24x24 surface 20 hotspot (7,4)", ce)
	}

	// Client clears its cursor (set_cursor with surface 0).
	h.shell.applyEvent(shellEvent{kind: evCursorSet, clientID: 1, cursorOn: false})
	if h.shell.cursors[1].active {
		t.Error("cursor still active after clear")
	}

	// Client disappears: its cursor goes too.
	h.shell.applyEvent(shellEvent{kind: evClientGone, clientID: 1})
	if _, ok := h.shell.cursors[1]; ok {
		t.Error("cursor outlived its client")
	}
}

// TestShellFullscreen: toggling fullscreen makes the active window own the
// whole output, hides the rest, and restores the tiling on toggle-off.
func TestShellFullscreen(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	first := h.shell.activeWin
	h.shell.applyEvent(windowAddedEvent(1, 18, 19))
	second := h.shell.activeWin // active = most recently added

	h.shell.applyEvent(shellEvent{kind: evFullscreen})
	if got := h.shell.fullscreenActive(); got != second {
		t.Fatalf("fullscreen = %d, want %d", got, second)
	}
	if !h.shell.sceneVisible(second) {
		t.Error("fullscreen window not visible")
	}
	if h.shell.sceneVisible(first) {
		t.Error("other window still visible behind fullscreen")
	}

	// Hit-test scene: only the fullscreen window, across the whole output.
	list := h.sceneInfo.Load()
	if list == nil || len(*list) != 1 {
		t.Fatalf("scene = %v, want exactly the fullscreen window", list)
	}
	if (*list)[0].WinID != second || (*list)[0].Rect != h.shell.output {
		t.Errorf("scene entry = %+v, want win %d at output %+v", (*list)[0], second, h.shell.output)
	}

	// Toggle off: both visible again.
	h.shell.applyEvent(shellEvent{kind: evFullscreen})
	if h.shell.fullscreenActive() != 0 {
		t.Error("fullscreen not cleared on toggle-off")
	}
	if !h.shell.sceneVisible(first) || !h.shell.sceneVisible(second) {
		t.Error("windows not restored after exiting fullscreen")
	}
	if list := h.sceneInfo.Load(); list == nil || len(*list) != 2 {
		t.Errorf("scene after restore = %v, want 2 windows", list)
	}
}

// TestShellFullscreenClearedWhenClientGone: a fullscreen window whose client
// disconnects must not leave the scene blanked.
func TestShellFullscreenClearedWhenClientGone(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	h.shell.applyEvent(shellEvent{kind: evFullscreen})
	if h.shell.fullscreenActive() == 0 {
		t.Fatal("not fullscreen")
	}
	h.shell.applyEvent(shellEvent{kind: evClientGone, clientID: 1})
	if h.shell.fullscreenActive() != 0 {
		t.Error("fullscreen survived its window's client")
	}
	if h.shell.sceneVisible(0) {
		t.Error("sceneVisible(0) should be false")
	}
}

// TestShellFocusWithinColumn: focus moves up/down the active column's stack.
func TestShellFocusWithinColumn(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	w0 := h.shell.activeWin
	h.shell.applyEvent(windowAddedEvent(1, 18, 19))
	w1 := h.shell.activeWin
	h.shell.applyEvent(windowAddedEvent(1, 20, 21))
	w2 := h.shell.activeWin

	// All three stacked in one column, newest focused (bottom).
	if _, row, _ := h.shell.engine.WindowPos(w2); row != 2 {
		t.Fatalf("w2 row = %d, want 2", row)
	}
	h.shell.applyEvent(shellEvent{kind: evFocusWindowUp})
	if h.shell.activeWin != w1 {
		t.Errorf("focus up -> %d, want %d", h.shell.activeWin, w1)
	}
	h.shell.applyEvent(shellEvent{kind: evFocusWindowUp})
	if h.shell.activeWin != w0 {
		t.Errorf("focus up -> %d, want %d", h.shell.activeWin, w0)
	}
	// Already at the top: clamped.
	h.shell.applyEvent(shellEvent{kind: evFocusWindowUp})
	if h.shell.activeWin != w0 {
		t.Errorf("focus up past top -> %d, want %d (clamped)", h.shell.activeWin, w0)
	}
	h.shell.applyEvent(shellEvent{kind: evFocusWindowDown})
	if h.shell.activeWin != w1 {
		t.Errorf("focus down -> %d, want %d", h.shell.activeWin, w1)
	}
}

// TestShellMoveWithinColumn: the active window reorders within its column.
func TestShellMoveWithinColumn(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	w0 := h.shell.activeWin
	h.shell.applyEvent(windowAddedEvent(1, 18, 19))
	w1 := h.shell.activeWin
	h.shell.applyEvent(windowAddedEvent(1, 20, 21))
	w2 := h.shell.activeWin

	h.shell.applyEvent(shellEvent{kind: evMoveWindowUp}) // w2 up past w1
	order := h.shell.engine.Columns[0].Windows
	want := []layout.WindowID{w0, w2, w1}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// TestPlaceLayer: anchoring, size and margins resolve to output rects.
func TestPlaceLayer(t *testing.T) {
	out := layout.Rect{X: 0, Y: 0, W: 1000, H: 800}
	cases := []struct {
		name string
		l    layerEntry
		want layout.Rect
	}{
		{"top bar fills width", layerEntry{anchor: anchorTop | anchorLeft | anchorRight, w: 0, h: 30},
			layout.Rect{X: 0, Y: 0, W: 1000, H: 30}},
		{"top bar with margins", layerEntry{anchor: anchorTop | anchorLeft | anchorRight, w: 0, h: 30,
			margin: [4]int32{5, 10, 0, 10}},
			layout.Rect{X: 10, Y: 5, W: 980, H: 30}},
		{"bottom-right corner", layerEntry{anchor: anchorBottom | anchorRight, w: 200, h: 100},
			layout.Rect{X: 800, Y: 700, W: 200, H: 100}},
		{"centered", layerEntry{anchor: 0, w: 100, h: 50},
			layout.Rect{X: 450, Y: 375, W: 100, H: 50}},
		{"left edge", layerEntry{anchor: anchorLeft, w: 40, h: 200},
			layout.Rect{X: 0, Y: 300, W: 40, H: 200}},
	}
	for _, c := range cases {
		if got := placeLayer(out, &c.l); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestTilingAreaExclusiveZones: reserved zones shrink the area windows tile in.
func TestTilingAreaExclusiveZones(t *testing.T) {
	h := newShellHarness()
	out := h.shell.output
	if got := h.shell.tilingArea(); got != out {
		t.Fatalf("no layers: tiling area %+v, want output %+v", got, out)
	}
	h.shell.layers[layerKey{1, 1}] = &layerEntry{layer: layerTop, anchor: anchorTop, exclusive: 30}
	h.shell.layers[layerKey{2, 2}] = &layerEntry{layer: layerTop, anchor: anchorLeft, exclusive: 40}
	got := h.shell.tilingArea()
	want := layout.Rect{X: out.X + 40, Y: out.Y + 30, W: out.W - 40, H: out.H - 30}
	if got != want {
		t.Errorf("tiling area = %+v, want %+v", got, want)
	}
}

// TestShellLayerReservesTilingSpace: adding a top bar re-tiles the windows
// below the reserved zone; removing it restores the full area.
func TestShellLayerReservesTilingSpace(t *testing.T) {
	h := newShellHarness()
	h.shell.applyEvent(windowAddedEvent(1, 16, 17))
	full := h.shell.windows[h.shell.activeWin].Rect

	h.shell.applyEvent(shellEvent{
		kind: evLayerAdded, clientID: 9, surfaceID: 50, layerIndex: layerTop,
		namespace: "bar", anchor: anchorTop | anchorLeft | anchorRight,
		pw: h.shell.output.W, ph: 30, exclusive: 30,
	})
	if _, ok := h.shell.layers[layerKey{9, 50}]; !ok {
		t.Fatal("layer surface not registered")
	}
	shrunk := h.shell.windows[h.shell.activeWin].Rect
	if shrunk.Y != h.shell.output.Y+30 || shrunk.H != full.H-30 {
		t.Errorf("after bar: window rect %+v, want Y=%d H=%d", shrunk, h.shell.output.Y+30, full.H-30)
	}

	// The bar is published for input (front of the list: it wins the hit-test).
	list := h.sceneInfo.Load()
	if list == nil || len(*list) == 0 || (*list)[0].SurfaceID != 50 || (*list)[0].WinID != 0 {
		t.Errorf("scene list = %v, want the bar (surface 50, WinID 0) first", list)
	}

	h.shell.applyEvent(shellEvent{kind: evLayerGone, clientID: 9, surfaceID: 50})
	if len(h.shell.layers) != 0 {
		t.Error("layer surface survived removal")
	}
	if got := h.shell.windows[h.shell.activeWin].Rect; got != full {
		t.Errorf("after removing bar: window rect %+v, want %+v", got, full)
	}
}
