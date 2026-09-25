package layout

import (
	"fmt"
)

// HitType indicates what part of the layout was clicked or hovered.
type HitType uint8

const (
	HitNone HitType = iota
	HitWindowBody
	HitTitlebar
	HitBorderLeft
	HitBorderRight
	HitBorderTop
	HitBorderBottom
	HitDividerCol
	HitDividerBlock
)

// HitResult contains the result of a mouse hit-test.
type HitResult struct {
	Type     HitType
	WindowID WindowID
	ColIdx   int
	BlockIdx int
}

// Column represents a vertical stack of window blocks.
type Column struct {
	WidthWeight   float32
	Windows       []WindowID
	HeightWeights []float32
}

// LayoutConfig configures margins and borders.
type LayoutConfig struct {
	Gap           int32 // Pixel gap between windows
	BorderWidth   int32 // Window border width
	TitlebarHeight int32 // Height of titlebar for mouse grabbing
}

// Engine manages the block/grid layout math and mouse interaction state.
type Engine struct {
	SlotMap *SlotMap
	Config  LayoutConfig
	Columns []Column

	// Scratch cache for zero-alloc layout calculation
	windowRects []Rect
	windowIDs   []WindowID
}

// NewEngine creates an initialized layout engine.
func NewEngine(cfg LayoutConfig) *Engine {
	return &Engine{
		SlotMap:     NewSlotMap(64),
		Config:      cfg,
		Columns:     make([]Column, 0, 8),
		windowRects: make([]Rect, 0, 64),
		windowIDs:   make([]WindowID, 0, 64),
	}
}

// AddWindow inserts a newly created window into the best available column.
func (e *Engine) AddWindow(id WindowID) error {
	if !e.SlotMap.Validate(id) {
		return fmt.Errorf("invalid WindowID: %d", id)
	}

	// If no columns, create the first one
	if len(e.Columns) == 0 {
		e.Columns = append(e.Columns, Column{
			WidthWeight:   1.0,
			Windows:       []WindowID{id},
			HeightWeights: []float32{1.0},
		})
		return nil
	}

	// Add to column with the fewest windows
	bestCol := 0
	minWins := len(e.Columns[0].Windows)
	for i := 1; i < len(e.Columns); i++ {
		if len(e.Columns[i].Windows) < minWins {
			minWins = len(e.Columns[i].Windows)
			bestCol = i
		}
	}

	col := &e.Columns[bestCol]
	col.Windows = append(col.Windows, id)
	col.HeightWeights = append(col.HeightWeights, 1.0)
	return nil
}

// RemoveWindow removes a window from the layout and rebalances column weights.
func (e *Engine) RemoveWindow(id WindowID) bool {
	for cIdx := range e.Columns {
		col := &e.Columns[cIdx]
		for wIdx := range col.Windows {
			if col.Windows[wIdx] == id {
				col.Windows = append(col.Windows[:wIdx], col.Windows[wIdx+1:]...)
				col.HeightWeights = append(col.HeightWeights[:wIdx], col.HeightWeights[wIdx+1:]...)

				// If column is now empty, remove the column unless it's the last one
				if len(col.Windows) == 0 && len(e.Columns) > 1 {
					e.Columns = append(e.Columns[:cIdx], e.Columns[cIdx+1:]...)
				}
				return true
			}
		}
	}
	return false
}

// SwapWindows swaps the layout positions of two windows.
func (e *Engine) SwapWindows(a, b WindowID) bool {
	var (
		c1, w1 = -1, -1
		c2, w2 = -1, -1
	)

	for c := range e.Columns {
		for w := range e.Columns[c].Windows {
			if e.Columns[c].Windows[w] == a {
				c1, w1 = c, w
			}
			if e.Columns[c].Windows[w] == b {
				c2, w2 = c, w
			}
		}
	}

	if c1 == -1 || c2 == -1 {
		return false
	}

	e.Columns[c1].Windows[w1] = b
	e.Columns[c2].Windows[w2] = a
	return true
}

// MoveWindowTo moves a window to a specific column and block index.
func (e *Engine) MoveWindowTo(id WindowID, targetCol int, targetRow int) bool {
	if !e.RemoveWindow(id) {
		return false
	}

	if targetCol >= len(e.Columns) {
		// Create new column at the end
		e.Columns = append(e.Columns, Column{
			WidthWeight:   1.0,
			Windows:       []WindowID{id},
			HeightWeights: []float32{1.0},
		})
		return true
	}

	if targetCol < 0 {
		targetCol = 0
	}

	col := &e.Columns[targetCol]
	if targetRow < 0 {
		targetRow = 0
	}
	if targetRow > len(col.Windows) {
		targetRow = len(col.Windows)
	}

	col.Windows = append(col.Windows[:targetRow], append([]WindowID{id}, col.Windows[targetRow:]...)...)
	col.HeightWeights = append(col.HeightWeights[:targetRow], append([]float32{1.0}, col.HeightWeights[targetRow:]...)...)
	return true
}

// ResizeColumn modifies a column's width weight based on pixel drag delta.
func (e *Engine) ResizeColumn(colIdx int, deltaPixels int32, screenWidth int32) {
	if colIdx < 0 || colIdx >= len(e.Columns)-1 || screenWidth <= 0 {
		return
	}
	deltaWeight := float32(deltaPixels) / float32(screenWidth)
	if e.Columns[colIdx].WidthWeight+deltaWeight > 0.1 && e.Columns[colIdx+1].WidthWeight-deltaWeight > 0.1 {
		e.Columns[colIdx].WidthWeight += deltaWeight
		e.Columns[colIdx+1].WidthWeight -= deltaWeight
	}
}

// Compute calculates the exact screen rectangles for all windows.
// It populates the pre-allocated internal slices with zero heap allocations.
func (e *Engine) Compute(bounds Rect) ([]WindowID, []Rect) {
	e.windowIDs = e.windowIDs[:0]
	e.windowRects = e.windowRects[:0]

	numCols := len(e.Columns)
	if numCols == 0 || bounds.W <= 0 || bounds.H <= 0 {
		return e.windowIDs, e.windowRects
	}

	var totalWeight float32
	for i := range e.Columns {
		totalWeight += e.Columns[i].WidthWeight
	}
	if totalWeight <= 0 {
		totalWeight = 1.0
	}

	totalGapsW := int32(numCols-1) * e.Config.Gap
	availW := bounds.W - totalGapsW
	if availW < 0 {
		availW = 0
	}

	curX := bounds.X
	for cIdx := range e.Columns {
		col := &e.Columns[cIdx]
		colW := int32((col.WidthWeight / totalWeight) * float32(availW))
		if cIdx == numCols-1 {
			// Final column absorbs any integer rounding error
			colW = (bounds.X + bounds.W) - curX
		}

		numWins := len(col.Windows)
		if numWins > 0 {
			var colHeightWeight float32
			for _, hw := range col.HeightWeights {
				colHeightWeight += hw
			}
			if colHeightWeight <= 0 {
				colHeightWeight = 1.0
			}

			totalGapsH := int32(numWins-1) * e.Config.Gap
			availH := bounds.H - totalGapsH
			if availH < 0 {
				availH = 0
			}

			curY := bounds.Y
			for wIdx := range col.Windows {
				winH := int32((col.HeightWeights[wIdx] / colHeightWeight) * float32(availH))
				if wIdx == numWins-1 {
					winH = (bounds.Y + bounds.H) - curY
				}

				e.windowIDs = append(e.windowIDs, col.Windows[wIdx])
				e.windowRects = append(e.windowRects, Rect{
					X: curX,
					Y: curY,
					W: colW,
					H: winH,
				})

				curY += winH + e.Config.Gap
			}
		}

		curX += colW + e.Config.Gap
	}

	return e.windowIDs, e.windowRects
}

// HitTest determines what layout component is beneath a screen coordinate point.
func (e *Engine) HitTest(p Point) HitResult {
	border := e.Config.BorderWidth
	if border < 4 {
		border = 4 // Minimum hit area for comfortable mouse grabbing
	}

	for i := range e.windowRects {
		r := e.windowRects[i]
		if !r.Contains(p) {
			continue
		}

		winID := e.windowIDs[i]

		// Check titlebar area
		if e.Config.TitlebarHeight > 0 && p.Y < r.Y+e.Config.TitlebarHeight {
			return HitResult{
				Type:     HitTitlebar,
				WindowID: winID,
			}
		}

		// Check edge borders
		if p.X < r.X+border {
			return HitResult{Type: HitBorderLeft, WindowID: winID}
		}
		if p.X >= r.X+r.W-border {
			return HitResult{Type: HitBorderRight, WindowID: winID}
		}
		if p.Y < r.Y+border {
			return HitResult{Type: HitBorderTop, WindowID: winID}
		}
		if p.Y >= r.Y+r.H-border {
			return HitResult{Type: HitBorderBottom, WindowID: winID}
		}

		return HitResult{Type: HitWindowBody, WindowID: winID}
	}

	return HitResult{Type: HitNone}
}
