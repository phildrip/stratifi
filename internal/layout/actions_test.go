package layout

import "testing"

// TestReorderActiveColumn: move-column-left/right swaps the active column
// with its neighbour in the strip (unlike MoveActiveColumn, which only moves
// focus) and keeps focus on the moved column.
func TestReorderActiveColumn(t *testing.T) {
	e, ids := buildScrollStrip(t, 3)
	// Columns 0,1,2 hold ids 0,1,2. Focus column 1, move it left.
	e.SetActiveColumn(1)
	if !e.ReorderActiveColumn(1, -1) {
		t.Fatal("reorder left returned false")
	}
	// Strip is now [id1, id0, id2]; focus stayed on id1 at index 0.
	if e.ActiveColumn() != 0 {
		t.Errorf("active column = %d, want 0 (focus follows the moved column)", e.ActiveColumn())
	}
	order := []WindowID{e.Columns[0].Windows[0], e.Columns[1].Windows[0], e.Columns[2].Windows[0]}
	want := []WindowID{ids[1], ids[0], ids[2]}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}

	// Moving the leftmost column further left is a no-op.
	if e.ReorderActiveColumn(1, -1) {
		t.Error("move off the left end should return false")
	}

	// A single-column workspace cannot reorder.
	e2, _ := buildScrollStrip(t, 1)
	e2.SetActiveColumn(0)
	if e2.ReorderActiveColumn(1, 1) {
		t.Error("single column should not reorder")
	}
}

// TestMoveWindowWithinColumn: reorder within a column's stack, clamped, with
// height weights following their windows.
func TestMoveWindowWithinColumn(t *testing.T) {
	e := NewEngine(scrollCfg())
	var ids []WindowID
	for i := 0; i < 3; i++ {
		id := e.SlotMap.Allocate()
		if err := e.AddWindowTagged(id, 0, 1); err != nil { // all in column 0
			t.Fatalf("add: %v", err)
		}
		ids = append(ids, id)
	}
	// Distinct weights so we can prove they follow the reorder.
	e.Columns[0].HeightWeights = []float32{0.5, 1.0, 1.5}

	col, row, ok := e.WindowPos(ids[1])
	if !ok || col != 0 || row != 1 {
		t.Fatalf("WindowPos = (%d,%d,%v), want (0,1,true)", col, row, ok)
	}
	if !e.MoveWindowWithinColumn(ids[1], -1) {
		t.Fatal("move up returned false")
	}
	if e.Columns[0].Windows[0] != ids[1] || e.Columns[0].Windows[1] != ids[0] {
		t.Errorf("order = %v, want [%d %d %d]", e.Columns[0].Windows, ids[1], ids[0], ids[2])
	}
	if got := e.Columns[0].HeightWeights; got[0] != 1.0 || got[1] != 0.5 {
		t.Errorf("weights = %v, want [1.0 0.5 1.5] (follow their windows)", got)
	}

	// Already at the top: moving up again is a no-op.
	if e.MoveWindowWithinColumn(ids[1], -1) {
		t.Error("move past the top should return false")
	}
}
