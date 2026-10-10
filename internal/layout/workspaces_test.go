package layout

import (
	"testing"
)

var wsCfg = LayoutConfig{Gap: 8, BorderWidth: 3, TitlebarHeight: 28}

// newStripWith builds a strip with n windows added to the active workspace,
// sharing one SlotMap (mirrors the shell: ids are strip-global).
func newStripWith(t *testing.T, n int) (*Workspaces, []WindowID) {
	t.Helper()
	ws := NewWorkspaces(wsCfg)
	sm := ws.Engine().SlotMap // the shell's single source of id validity
	ids := make([]WindowID, 0, n)
	for i := 0; i < n; i++ {
		id := sm.Allocate()
		if err := ws.AddWindow(id); err != nil {
			t.Fatalf("AddWindow %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	return ws, ids
}

func TestWorkspacesStartsWithOne(t *testing.T) {
	ws := NewWorkspaces(wsCfg)
	if ws.Count() != 1 {
		t.Fatalf("count = %d, want 1", ws.Count())
	}
	if ws.ActiveID() != 1 {
		t.Fatalf("active id = %d, want 1", ws.ActiveID())
	}
}

func TestWorkspacesSwitch(t *testing.T) {
	ws := NewWorkspaces(wsCfg)
	id2 := ws.NewWorkspace("2")
	if !ws.SwitchTo(id2) {
		t.Fatal("SwitchTo(new) returned false")
	}
	if ws.ActiveID() != id2 {
		t.Fatalf("active = %d, want %d", ws.ActiveID(), id2)
	}
	if ws.SwitchTo(id2) {
		t.Fatal("SwitchTo(already-active) returned true")
	}
	if ws.SwitchTo(WorkspaceID(99)) {
		t.Fatal("SwitchTo(unknown) returned true")
	}
	// Two workspaces: next/prev toggle between them.
	if got := ws.SwitchNext(); got != 1 {
		t.Errorf("next = %d, want 1 (wraparound)", got)
	}
	if got := ws.SwitchNext(); got != id2 {
		t.Errorf("next = %d, want %d", got, id2)
	}
	if got := ws.SwitchPrev(); got != 1 {
		t.Errorf("prev = %d, want 1", got)
	}
}

func TestWorkspacesIsolateWindows(t *testing.T) {
	ws, ids := newStripWith(t, 2)
	id2 := ws.NewWorkspace("2")
	ws.SwitchTo(id2)

	// New workspace is empty until something lands in it.
	if got := len(ws.WindowsOf(id2)); got != 0 {
		t.Fatalf("fresh workspace has %d windows, want 0", got)
	}
	// Original windows remain on ws 1.
	if got := len(ws.WindowsOf(1)); got != 2 {
		t.Fatalf("ws1 keeps %d windows, want 2", got)
	}
	// Switching back restores the view.
	ws.SwitchTo(1)
	winIDs, rects := ws.Compute(wsTestBounds)
	if len(winIDs) != 2 {
		t.Fatalf("ws1 compute = %d windows, want 2", len(winIDs))
	}
	for _, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("degenerate rect %+v", r)
		}
	}
	_ = ids
}

func TestWorkspacesMoveWindow(t *testing.T) {
	ws, ids := newStripWith(t, 2)
	id2 := ws.NewWorkspace("2")

	if !ws.MoveWindowToWorkspace(ids[0], id2) {
		t.Fatal("MoveWindowToWorkspace failed")
	}
	if got := len(ws.WindowsOf(1)); got != 1 {
		t.Fatalf("ws1 keeps %d windows, want 1", got)
	}
	if got := len(ws.WindowsOf(id2)); got != 1 {
		t.Fatalf("ws2 has %d windows, want 1", got)
	}

	// Moving to a nonexistent workspace is a no-op.
	if ws.MoveWindowToWorkspace(ids[0], WorkspaceID(42)) {
		t.Error("move to unknown workspace succeeded")
	}
}

// TestWorkspacesComputeFiltersTags: hidden workspaces' columns never leak
// into the active compute, and widths renormalize over visible columns.
func TestWorkspacesComputeFiltersTags(t *testing.T) {
	ws, _ := newStripWith(t, 3)
	id2 := ws.NewWorkspace("2")

	// Move two windows to ws2: visible ws1 compute must cover full width.
	if !ws.MoveWindowToWorkspace(65537, id2) || !ws.MoveWindowToWorkspace(65538, id2) {
		t.Fatal("moves failed")
	}
	ws.SwitchTo(1)
	winIDs, rects := ws.Compute(wsTestBounds)
	if len(winIDs) != 1 {
		t.Fatalf("ws1 visible windows = %d, want 1", len(winIDs))
	}
	if rects[0].W != wsTestBounds.W {
		t.Errorf("ws1 window width = %d, want full %d (renormalized)", rects[0].W, wsTestBounds.W)
	}

	// ws2 shows its own two.
	ws.SwitchTo(id2)
	winIDs, _ = ws.Compute(wsTestBounds)
	if len(winIDs) != 2 {
		t.Fatalf("ws2 visible windows = %d, want 2", len(winIDs))
	}
}

var wsTestBounds = Rect{X: 0, Y: 0, W: 1280, H: 720}
