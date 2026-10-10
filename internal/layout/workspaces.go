package layout

// workspaces.go — a horizontal strip of virtual desktops (the niri model):
// ONE engine owns all columns; each column carries a Workspace tag.
// ComputeTagged emits only the active tag's columns, so switching is a tag
// change, not a window shuffle. Window ids stay global — the shell's
// SlotMap remains the single source of truth for id validity.

import "fmt"

// WorkspaceID is 1-based, stable, never reused.
type WorkspaceID uint32

// Workspaces manages the strip over the single engine. The actor goroutine
// owns all mutation (single-owner, like the shell).
type Workspaces struct {
	engine    *Engine
	names     []string // index 0..N-1 -> display name
	activeIdx int
	nextID    uint32
}

// NewWorkspaces creates the strip with one active workspace (id 1).
func NewWorkspaces(cfg LayoutConfig) *Workspaces {
	ws := &Workspaces{engine: NewEngine(cfg), nextID: 1}
	ws.newSlot("1")
	return ws
}

// newSlot appends a workspace id/name pair; its columns are created lazily
// on the first AddWindow.
func (ws *Workspaces) newSlot(name string) WorkspaceID {
	id := ws.nextID
	ws.nextID++
	ws.names = append(ws.names, name)
	return WorkspaceID(id)
}

// NewWorkspace appends a workspace and returns its ID.
func (ws *Workspaces) NewWorkspace(name string) WorkspaceID { return ws.newSlot(name) }

// Count reports the number of workspaces in the strip.
func (ws *Workspaces) Count() int { return len(ws.names) }

// ActiveID returns the active workspace's ID.
func (ws *Workspaces) ActiveID() WorkspaceID { return WorkspaceID(ws.activeIdx + 1) }

// Engine exposes the single engine backing the strip (shell integration).
func (ws *Workspaces) Engine() *Engine { return ws.engine }

// IndexOf resolves an ID to its strip position, -1 when absent.
func (ws *Workspaces) IndexOf(id WorkspaceID) int {
	if id == 0 || int(id) > len(ws.names) {
		return -1
	}
	return int(id) - 1
}

// SwitchTo activates the workspace with the given ID. Returns false for
// unknown IDs or when already active.
func (ws *Workspaces) SwitchTo(id WorkspaceID) bool {
	idx := ws.IndexOf(id)
	if idx < 0 || idx == ws.activeIdx {
		return idx >= 0 && idx != ws.activeIdx
	}
	ws.activeIdx = idx
	return true
}

// SwitchNext/SwitchPrev walk the strip with wraparound.
func (ws *Workspaces) SwitchNext() WorkspaceID {
	ws.activeIdx = (ws.activeIdx + 1) % len(ws.names)
	return ws.ActiveID()
}

func (ws *Workspaces) SwitchPrev() WorkspaceID {
	ws.activeIdx = (ws.activeIdx - 1 + len(ws.names)) % len(ws.names)
	return ws.ActiveID()
}

// AddWindow places a window on the active workspace: reuse its existing
// tagged column when the workspace already has one, else create one.
func (ws *Workspaces) AddWindow(id WindowID) error {
	active := uint32(ws.activeIdx + 1)
	colIdx := ws.activeColumnIdx()
	if colIdx < 0 {
		colIdx = len(ws.engine.Columns) // fresh tagged column at the end
	}
	return ws.engine.AddWindowTagged(id, colIdx, active)
}

func (ws *Workspaces) activeColumnIdx() int {
	active := uint32(ws.activeIdx + 1)
	for i := range ws.engine.Columns {
		if ws.engine.Columns[i].Workspace == active {
			return i
		}
	}
	return -1
}

// RemoveWindow drops a window from wherever it lives.
func (ws *Workspaces) RemoveWindow(id WindowID) bool {
	return ws.engine.RemoveWindow(id)
}

// MoveWindowToWorkspace detaches a window and appends it to the target
// workspace's column (created when absent). switching the active idx
// is NOT implied: the window moves, focus stays put.
func (ws *Workspaces) MoveWindowToWorkspace(win WindowID, target WorkspaceID) bool {
	if ws.IndexOf(target) < 0 {
		return false
	}
	if !ws.engine.RemoveWindow(win) {
		return false
	}
	// Append to the target's column, or a fresh tagged one.
	targetIdx := len(ws.engine.Columns)
	for i := range ws.engine.Columns {
		if ws.engine.Columns[i].Workspace == uint32(target) {
			targetIdx = -1
			break
		}
	}
	if targetIdx >= 0 {
		return ws.engine.AddWindowTagged(win, targetIdx, uint32(target)) == nil
	}
	// Reuse the existing tagged column: append to the shallowest match.
	colIdx := -1
	for i := range ws.engine.Columns {
		if ws.engine.Columns[i].Workspace == uint32(target) &&
			(colIdx < 0 || len(ws.engine.Columns[i].Windows) < len(ws.engine.Columns[colIdx].Windows)) {
			colIdx = i
		}
	}
	return ws.engine.AddWindowTagged(win, colIdx, uint32(target)) == nil
}

// WindowsOf lists a workspace's window ids (diagnostics/tests).
func (ws *Workspaces) WindowsOf(id WorkspaceID) []WindowID {
	return ws.engine.WindowsTagged(uint32(id))
}

// Compute returns the active workspace's tiling.
func (ws *Workspaces) Compute(bounds Rect) ([]WindowID, []Rect) {
	return ws.engine.ComputeTagged(uint32(ws.activeIdx+1), bounds)
}

// String renders the strip for diagnostics.
func (ws *Workspaces) String() string {
	names := ""
	for i, n := range ws.names {
		if i > 0 {
			names += ","
		}
		marker := " "
		if i == ws.activeIdx {
			marker = "*"
		}
		names += fmt.Sprintf("%s[%d]%s", marker, i+1, n)
	}
	return names
}

// Ensure returns the ID for a 1-based workspace number, creating empty
// intermediate slots so `workspace N` works like sway/i3 (bindings may name
// workspaces that do not exist yet).
func (ws *Workspaces) Ensure(n int) WorkspaceID {
	if n < 1 {
		n = 1
	}
	for len(ws.names) < n {
		ws.newSlot(fmt.Sprintf("%d", len(ws.names)+1))
	}
	return WorkspaceID(n)
}

// SwitchToNumber ensures the numbered workspace exists and activates it.
func (ws *Workspaces) SwitchToNumber(n int) WorkspaceID {
	id := ws.Ensure(n)
	ws.SwitchTo(id)
	return id
}

// Visible reports whether a window is on the active workspace (drawn and
// hit-testable). Windows on other workspaces keep their state but are not
// in the scene.
func (ws *Workspaces) Visible(win WindowID) bool {
	return ws.engine.ContainsTagged(uint32(ws.activeIdx+1), win)
}
