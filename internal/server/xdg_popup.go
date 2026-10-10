package server

// xdg_popup.go — placement of popup surfaces from an xdg_positioner state.
//
// The algorithm is a deliberate simplification (documented contract):
// pick an anchor point on the parent's anchor rect, grow the popup from
// that edge per gravity, then flip any dimension that would leave the
// output bounds (the basic LEFT/RIGHT/TOP/BOTTOM constraint adjustment).
// No per-edge splitting (SLIDE/RESIZE adjustments are honored as flips).
// This is enough for menus and tooltips; per-edge splitting can come later
// via reactive repositioning.

import "fmt"

// Anchor edge bits (xdg_positioner.set_anchor / set_gravity wire values).
const (
	AnchorTop    uint32 = 1 << 0
	AnchorBottom uint32 = 1 << 1
	AnchorLeft   uint32 = 1 << 2
	AnchorRight  uint32 = 1 << 3
)

// Constraint adjustment bits (set_constraint_adjustment).
const (
	AdjustSlideX  uint32 = 1 << 0
	AdjustSlideY  uint32 = 1 << 1
	AdjustFlipX   uint32 = 1 << 2
	AdjustFlipY   uint32 = 1 << 3
	AdjustResizeX uint32 = 1 << 4
	AdjustResizeY uint32 = 1 << 5
)

// Placement is a computed popup geometry relative to the parent surface
// origin (what xdg_popup.configure carries).
type Placement struct {
	X, Y          int32
	Width, Height int32
	Flipped       bool // gravity was inverted by a flip adjustment
}

// String renders the placement for logs and test output.
func (p Placement) String() string {
	return fmt.Sprintf("(%d,%d %dx%d flipped=%v)", p.X, p.Y, p.Width, p.Height, p.Flipped)
}

// PlacePopup computes where a popup sits relative to its parent origin.
//
//	parentW/parentH: parent surface size (the window body).
//	outputW/outputH: available screen area in parent-relative coordinates —
//	                 the caller translates the anchor rect into that space,
//	                 so this function needs no global state.
func PlacePopup(pos XdgPositioner, parentW, parentH, outputW, outputH int32) Placement {
	pw := pos.Width
	ph := pos.Height
	if pw <= 0 || ph <= 0 {
		// Degenerate size request: fall back to the anchor rect's extent.
		pw = max32(pos.AnchorRect[2], 1)
		ph = max32(pos.AnchorRect[3], 1)
	}

	// Anchor point chosen inside the anchor rect per anchorMask.
	ax, ay := anchorPoint(pos.AnchorMask, pos.AnchorRect)

	flipped := false
	x := ax + pos.OffsetX
	y := ay + pos.OffsetY

	// Grow away from the parent for each direction gravity allows. Gravity
	// top => popup sits above the anchor; bottom => below; none => below
	// (the common menu case).
	if pos.GravityMask&AnchorTop != 0 {
		y -= ph
	}
	if pos.GravityMask&AnchorLeft != 0 {
		x -= pw
	}

	// Flip when the popup would leave the output and the client allows it:
	// invert the gravity along that axis (grow the other way).
	if x+pw > outputW && pos.ConstraintAdjustment&AdjustFlipX != 0 {
		x, flipped = mirrorX(ax, pos.GravityMask, pos.OffsetX, pw), true
	}
	if y+ph > outputH && pos.ConstraintAdjustment&AdjustFlipY != 0 {
		y, flipped = mirrorY(ay, pos.GravityMask, pos.OffsetY, ph), true
	}
	// Slide/resize constraint adjustments: shift (or resize) so the popup
	// stays inside the output. Without them the spec ALLOWS partial
	// offscreen popups with negative coordinates — the compositor must not
	// silently clamp what the client asked to keep.
	slideX := pos.ConstraintAdjustment&AdjustSlideX != 0
	slideY := pos.ConstraintAdjustment&AdjustSlideY != 0
	if slideX {
		switch {
		case x < 0:
			x = 0
		case x+pw > outputW:
			// Over the right edge: pull back flush with it when it fits,
			// else pin to the near (left) edge — "as close as possible"
			// (spec: slide moves the popup the minimum amount, saturating
			// when the popup is wider than the output).
			x = max32(outputW-pw, 0)
		}
	}
	if slideY {
		switch {
		case y < 0:
			y = 0
		case y+ph > outputH:
			y = max32(outputH-ph, 0)
		}
	}

	return Placement{X: x, Y: y, Width: pw, Height: ph, Flipped: flipped}
}

// mirrorX inverts the horizontal gravity: leftward growth becomes
// rightward placement at the anchor, and vice versa.
func mirrorX(anchorX int32, gravity uint32, offsetX, width int32) int32 {
	if gravity&AnchorLeft != 0 {
		// was growing leftward; mirror to rightward from the anchor.
		return anchorX + offsetX
	}
	// default rightward growth mirrored to leftward.
	return anchorX - offsetX - width
}

// mirrorY inverts the vertical gravity (top growth becomes bottom).
func mirrorY(anchorY int32, gravity uint32, offsetY, height int32) int32 {
	if gravity&AnchorTop != 0 {
		return anchorY + offsetY
	}
	return anchorY - offsetY - height
}

// anchorPoint picks the anchor-position point inside the anchor rect.
// Combinations resolve to corners/edges; a zero mask means the rect's
// top-left corner (the spec default for an unrestricted anchor).
func anchorPoint(mask uint32, rect [4]int32) (int32, int32) {
	rx, ry, rw, rh := rect[0], rect[1], rect[2], rect[3]
	x := rx
	if mask&AnchorRight != 0 {
		x = rx + rw
	}
	y := ry
	if mask&AnchorBottom != 0 {
		y = ry + rh
	}
	return x, y
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func clamp32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
