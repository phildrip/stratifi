package layout

// scroll.go — the niri scrollable-tiling viewport.
//
// The engine keeps its columns in a horizontal strip. Unlike the squeeze
// Compute (which shrinks every column to fit the output), the scrollable
// viewport gives each column a fixed fraction of the output width and
// slides the strip so the ACTIVE column stays visible — niri's model. The
// strip is laid out in "content space" (0..totalWidth) and mapped to screen
// space through a clamped offset, so scrolling never reveals empty space
// past the first/last column.

// DefaultColumnWidth is the fraction of the output a column occupies when
// Config.ColumnWidth is unset.
const DefaultColumnWidth = 0.5

// columnFrac resolves a column's width fraction (per-column override or the
// config default).
func (e *Engine) columnFrac(col *Column) float32 {
	if col.WidthFrac > 0 {
		return col.WidthFrac
	}
	if e.Config.ColumnWidth > 0 {
		return e.Config.ColumnWidth
	}
	return DefaultColumnWidth
}

// contentWidth returns the strip's total width in content space for the
// columns matching tag.
func (e *Engine) contentWidth(tag uint32, outW int32) int32 {
	total := int32(0)
	n := 0
	for i := range e.Columns {
		if e.Columns[i].Workspace != tag {
			continue
		}
		total += int32(e.columnFrac(&e.Columns[i]) * float32(outW))
		n++
	}
	if n > 1 {
		total += int32(n-1) * e.Config.Gap
	}
	return total
}

// scrollOffset computes the horizontal content-space offset that keeps the
// active column centered and visible, clamped so the strip never scrolls
// past its ends. Returns the offset (>= 0) and the active column's content x.
func (e *Engine) scrollOffset(tag uint32, outW int32, activeCol int) (offset int32, activeX int32) {
	visIdx := -1
	x := int32(0)
	activeW := int32(0)
	for i := range e.Columns {
		if e.Columns[i].Workspace != tag {
			continue
		}
		visIdx++
		w := int32(e.columnFrac(&e.Columns[i]) * float32(outW))
		if visIdx == activeCol {
			activeX = x
			activeW = w
			break
		}
		x += w + e.Config.Gap
	}

	// Center the active column, then clamp to [0, totalWidth - outW].
	total := e.contentWidth(tag, outW)
	offset = activeX - (outW-activeW)/2
	if offset < 0 {
		offset = 0
	}
	if maxOff := total - outW; offset > maxOff {
		if maxOff < 0 {
			maxOff = 0
		}
		offset = maxOff
	}
	return offset, activeX
}

// ComputeScrollable lays out the tag's columns at fixed widths and scrolls
// the strip so the active column (engine ActiveCol, clamped to the visible
// range) is on screen. Same zero-alloc scratch reuse as Compute.
func (e *Engine) ComputeScrollable(tag uint32, bounds Rect) ([]WindowID, []Rect) {
	e.windowIDs = e.windowIDs[:0]
	e.windowRects = e.windowRects[:0]

	if bounds.W <= 0 || bounds.H <= 0 {
		return e.windowIDs, e.windowRects
	}

	// Visible column count.
	numCols := 0
	for i := range e.Columns {
		if e.Columns[i].Workspace == tag {
			numCols++
		}
	}
	if numCols == 0 {
		return e.windowIDs, e.windowRects
	}

	active := e.ActiveCol
	if active < 0 {
		active = 0
	}
	if active >= numCols {
		active = numCols - 1
	}

	offset, _ := e.scrollOffset(tag, bounds.W, active)

	visIdx := -1
	contentX := int32(0)
	for cIdx := range e.Columns {
		col := &e.Columns[cIdx]
		if col.Workspace != tag {
			continue
		}
		visIdx++
		colW := int32(e.columnFrac(col) * float32(bounds.W))
		screenX := bounds.X + contentX - offset
		e.computeColumn(col, screenX, colW, bounds)
		contentX += colW + e.Config.Gap
	}
	return e.windowIDs, e.windowRects
}

// SetActiveColumn moves focus to a visible column index (clamped).
func (e *Engine) SetActiveColumn(idx int) {
	if idx < 0 {
		idx = 0
	}
	e.ActiveCol = idx
}

// MoveActiveColumn walks the active column by delta (for focus left/right),
// clamped to the visible range for tag.
func (e *Engine) MoveActiveColumn(tag uint32, delta int) {
	n := 0
	for i := range e.Columns {
		if e.Columns[i].Workspace == tag {
			n++
		}
	}
	if n == 0 {
		e.ActiveCol = 0
		return
	}
	e.ActiveCol += delta
	if e.ActiveCol < 0 {
		e.ActiveCol = 0
	}
	if e.ActiveCol >= n {
		e.ActiveCol = n - 1
	}
}

// ActiveColumn returns the focused column index.
func (e *Engine) ActiveColumn() int { return e.ActiveCol }

// ReorderActiveColumn swaps the active column with its neighbour delta
// positions away within the same workspace tag, keeping focus on the moved
// column. Returns false when there is no neighbour (a single-column
// workspace, or a move past the end). This is the niri "move column
// left/right" operation — distinct from MoveActiveColumn, which only moves
// the focus/viewport index without changing the strip order.
func (e *Engine) ReorderActiveColumn(tag uint32, delta int) bool {
	idx := make([]int, 0, len(e.Columns))
	for i := range e.Columns {
		if e.Columns[i].Workspace == tag {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return false
	}
	pos := e.ActiveCol
	if pos < 0 {
		pos = 0
	}
	if pos >= len(idx) {
		pos = len(idx) - 1
	}
	dst := pos + delta
	if dst < 0 || dst >= len(idx) {
		return false
	}
	a, b := idx[pos], idx[dst]
	e.Columns[a], e.Columns[b] = e.Columns[b], e.Columns[a]
	e.ActiveCol = dst
	return true
}

// ResizeWindowColumnWidth adjusts the scrollable width fraction of the column
// containing win by deltaFrac, clamped to [0.1, 1.0]. Zero heap allocations.
func (e *Engine) ResizeWindowColumnWidth(win WindowID, deltaFrac float32) bool {
	colIdx, _, ok := e.WindowPos(win)
	if !ok {
		return false
	}
	col := &e.Columns[colIdx]
	next := e.columnFrac(col) + deltaFrac
	if next < 0.1 {
		next = 0.1
	}
	if next > 1.0 {
		next = 1.0
	}
	col.WidthFrac = next
	return true
}

// ResizeWindowHeight adjusts the vertical height weight of win within its
// column by deltaWeight, clamped to [0.1, 10.0]. Returns false if win is
// unknown or is the only window in its column. Zero heap allocations.
func (e *Engine) ResizeWindowHeight(win WindowID, deltaWeight float32) bool {
	colIdx, rowIdx, ok := e.WindowPos(win)
	if !ok {
		return false
	}
	col := &e.Columns[colIdx]
	if len(col.Windows) < 2 || rowIdx >= len(col.HeightWeights) {
		return false
	}
	next := col.HeightWeights[rowIdx] + deltaWeight
	if next < 0.1 {
		next = 0.1
	}
	if next > 10.0 {
		next = 10.0
	}
	col.HeightWeights[rowIdx] = next
	return true
}
