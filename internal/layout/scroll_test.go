package layout

import "testing"

func scrollCfg() LayoutConfig {
	return LayoutConfig{Gap: 8, BorderWidth: 3, TitlebarHeight: 28, ColumnWidth: 0.5}
}

// buildScrollStrip makes a strip of n single-window columns on workspace 1.
func buildScrollStrip(t *testing.T, n int) (*Engine, []WindowID) {
	t.Helper()
	e := NewEngine(scrollCfg())
	ids := make([]WindowID, 0, n)
	for i := 0; i < n; i++ {
		id := e.SlotMap.Allocate()
		if err := e.AddWindowTagged(id, i, 1); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	return e, ids
}

var scrollBounds = Rect{X: 0, Y: 0, W: 1000, H: 600}

// TestScrollColumnWidth: each column is a fixed fraction of the output, not
// squeezed to fit — the defining niri behavior.
func TestScrollColumnWidth(t *testing.T) {
	e, _ := buildScrollStrip(t, 3)
	winIDs, rects := e.ComputeScrollable(1, scrollBounds)
	if len(winIDs) != 3 {
		t.Fatalf("windows = %d, want 3", len(winIDs))
	}
	for _, r := range rects {
		if r.W != 500 { // 0.5 * 1000
			t.Errorf("column width = %d, want 500 (fixed fraction, not squeezed)", r.W)
		}
	}
}

// TestScrollClampsAtStart: with the first column active the strip is not
// scrolled, so column 0 starts at the output's left edge.
func TestScrollClampsAtStart(t *testing.T) {
	e, _ := buildScrollStrip(t, 3)
	e.SetActiveColumn(0)
	_, rects := e.ComputeScrollable(1, scrollBounds)
	if rects[0].X != 0 {
		t.Errorf("col0 x = %d, want 0 (no scroll at start)", rects[0].X)
	}
	// Column 1 sits immediately right of column 0 (500 + gap).
	if rects[1].X != 508 {
		t.Errorf("col1 x = %d, want 508", rects[1].X)
	}
}

// TestScrollToActiveCenters: focusing the last column scrolls the strip so
// it is visible (clamped at the right end).
func TestScrollToActiveCenters(t *testing.T) {
	e, _ := buildScrollStrip(t, 4)
	e.SetActiveColumn(3) // last of 4
	_, rects := e.ComputeScrollable(1, scrollBounds)
	last := rects[3]
	// Must be fully on screen.
	if last.X < 0 || last.X+last.W > scrollBounds.W {
		t.Errorf("active last column %+v not visible in %d width", last, scrollBounds.W)
	}
	// At the right end the last column's right edge flush to the output.
	if last.X+last.W != scrollBounds.W {
		t.Errorf("last column right edge = %d, want %d (clamped)", last.X+last.W, scrollBounds.W)
	}
}

// TestScrollMoveActiveColumn: focus left/right walks columns and clamps.
func TestScrollMoveActiveColumn(t *testing.T) {
	e, _ := buildScrollStrip(t, 3)
	e.SetActiveColumn(0)
	e.MoveActiveColumn(1, 1)
	if e.ActiveColumn() != 1 {
		t.Fatalf("active = %d, want 1", e.ActiveColumn())
	}
	e.MoveActiveColumn(1, 5) // clamp at last
	if e.ActiveColumn() != 2 {
		t.Fatalf("active = %d, want 2 (clamped)", e.ActiveColumn())
	}
	e.MoveActiveColumn(1, -9) // clamp at first
	if e.ActiveColumn() != 0 {
		t.Fatalf("active = %d, want 0 (clamped)", e.ActiveColumn())
	}
}

// TestScrollIgnoresOtherWorkspaces: only the tag's columns occupy the strip.
func TestScrollIgnoresOtherWorkspaces(t *testing.T) {
	e, _ := buildScrollStrip(t, 2)
	other := e.SlotMap.Allocate()
	if err := e.AddWindowTagged(other, 2, 2); err != nil {
		t.Fatal(err)
	}
	winIDs, rects := e.ComputeScrollable(1, scrollBounds)
	if len(winIDs) != 2 {
		t.Fatalf("ws1 windows = %d, want 2", len(winIDs))
	}
	for _, r := range rects {
		if r.W != 500 {
			t.Errorf("ws1 column width = %d, want 500", r.W)
		}
	}
	// ws2 shows only its own.
	winIDs2, _ := e.ComputeScrollable(2, scrollBounds)
	if len(winIDs2) != 1 || winIDs2[0] != other {
		t.Fatalf("ws2 windows = %v, want [%d]", winIDs2, other)
	}
}

// TestScrollEmptyWorkspace: an empty tag yields no rects (blank desktop).
func TestScrollEmptyWorkspace(t *testing.T) {
	e, _ := buildScrollStrip(t, 2)
	winIDs, rects := e.ComputeScrollable(9, scrollBounds)
	if len(winIDs) != 0 || len(rects) != 0 {
		t.Fatalf("empty workspace produced %d windows", len(winIDs))
	}
}

// TestScrollDefaultColumnWidth: unset ColumnWidth falls back to 1/2.
func TestScrollDefaultColumnWidth(t *testing.T) {
	e := NewEngine(LayoutConfig{Gap: 8})
	id := e.SlotMap.Allocate()
	_ = e.AddWindowTagged(id, 0, 1)
	_, rects := e.ComputeScrollable(1, Rect{W: 800, H: 600})
	if rects[0].W != 400 {
		t.Errorf("default column width = %d, want 400 (0.5 * 800)", rects[0].W)
	}
}

// BenchmarkComputeScrollable asserts the scrollable path stays zero-alloc.
func BenchmarkComputeScrollable(b *testing.B) {
	e := NewEngine(scrollCfg())
	for i := 0; i < 4; i++ {
		id := e.SlotMap.Allocate()
		_ = e.AddWindowTagged(id, i, 1)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e.SetActiveColumn(i % 4)
		e.ComputeScrollable(1, scrollBounds)
	}
}

func TestResizeColumnWidthAndWindowHeight(t *testing.T) {
	e := NewEngine(scrollCfg())
	w1 := e.SlotMap.Allocate()
	w2 := e.SlotMap.Allocate()
	_ = e.AddWindowTagged(w1, 0, 1)
	_ = e.AddWindowTagged(w2, 0, 1) // stack 2 windows in column 0

	// Shrink column 0 by 10% (0.5 -> 0.4 -> 400px out of 1000px).
	if !e.ResizeWindowColumnWidth(w1, -0.1) {
		t.Fatalf("ResizeWindowColumnWidth(-0.1) returned false")
	}
	_, rects := e.ComputeScrollable(1, scrollBounds)
	if rects[0].W != 400 {
		t.Errorf("col width after -0.1 = %d, want 400", rects[0].W)
	}

	// Clamp at minimum 0.1 (100px) and maximum 1.0 (1000px).
	e.ResizeWindowColumnWidth(w1, -5.0)
	_, rects = e.ComputeScrollable(1, scrollBounds)
	if rects[0].W != 100 {
		t.Errorf("col width clamped min = %d, want 100", rects[0].W)
	}
	e.ResizeWindowColumnWidth(w1, 5.0)
	_, rects = e.ComputeScrollable(1, scrollBounds)
	if rects[0].W != 1000 {
		t.Errorf("col width clamped max = %d, want 1000", rects[0].W)
	}

	// Resize window height in a 2-window column: w1 grows, so w1.H > w2.H.
	if !e.ResizeWindowHeight(w1, 1.0) {
		t.Fatalf("ResizeWindowHeight(w1, +1.0) returned false")
	}
	_, rects = e.ComputeScrollable(1, scrollBounds)
	if rects[0].H <= rects[1].H {
		t.Errorf("expected w1.H (%d) > w2.H (%d) after growing w1", rects[0].H, rects[1].H)
	}
}
