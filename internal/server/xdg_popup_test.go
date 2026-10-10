package server

import "testing"

// base returns a client-menu positioner: right edge of the anchor rect,
// gravity below-right, flip+slide allowed (what every toolkit sends).
func base() XdgPositioner {
	return XdgPositioner{
		Width:  100,
		Height: 32,
		AnchorRect: [4]int32{
			80, 8, 40, 20, // a 40x20 button at (80,8) in the parent
		},
		AnchorMask:           AnchorBottom,
		GravityMask:          AnchorBottom | AnchorRight,
		ConstraintAdjustment: AdjustFlipX | AdjustFlipY | AdjustSlideX | AdjustSlideY,
	}
}

// TestPlacePopupBelowRight: the menu opens at the button's bottom-left
// corner, growing right/down when there is room (the common case).
func TestPlacePopupBelowRight(t *testing.T) {
	p := PlacePopup(base(), 400, 300, 400, 300)
	if p.X != 80 || p.Y != 28 { // anchor rect bottom-left: (80, 8+20)
		t.Errorf("position = %s, want (80,28 100x32)", p)
	}
	if p.Flipped {
		t.Error("flipped with room below")
	}
}

// TestPlacePopupFlipsUpAtBottom: near the screen bottom the menu flips to
// above the button instead.
func TestPlacePopupFlipsUpAtBottom(t *testing.T) {
	pos := base()
	pos.AnchorRect = [4]int32{80, 280, 40, 20} // button near the 300-tall bottom
	p := PlacePopup(pos, 400, 300, 400, 300)
	if !p.Flipped {
		t.Errorf("placement %s, expected vertical flip", p)
	}
	// Flip: gravity is now above the anchor point => popup bottom at anchor.
	if p.Y != 300-32 { // clamped to the output bottom
		t.Errorf("y = %d, want %d (clamped above the button)", p.Y, 300-32)
	}
}

// TestPlacePopupFlipsLeftAtRight: near the screen's right edge the menu
// mirrors its gravity: rightward growth becomes leftward growth from the
// anchor point, so the popup's right edge lands at the anchor.
func TestPlacePopupFlipsLeftAtRight(t *testing.T) {
	pos := base()
	pos.AnchorRect = [4]int32{380, 8, 20, 20} // button at the far right of 400
	p := PlacePopup(pos, 400, 300, 400, 300)
	if !p.Flipped {
		t.Errorf("placement %s, expected horizontal flip", p)
	}
	if p.X != 380-100 { // grow leftward from the anchor's left edge (380)
		t.Errorf("x = %d, want %d", p.X, 380-100)
	}
}

// TestPlacePopupSlidesAtEdge: without flip permission the popup slides to
// stay visible.
func TestPlacePopupSlidesAtEdge(t *testing.T) {
	pos := base()
	pos.ConstraintAdjustment = AdjustSlideX | AdjustSlideY
	pos.AnchorRect = [4]int32{380, 8, 20, 20}
	p := PlacePopup(pos, 400, 300, 400, 300)
	if p.X != 400-100 {
		t.Errorf("x = %d, want slid to %d", p.X, 400-100)
	}
}

// TestPlacePopupTopLeftGravityOfTopLeftAnchor: top-left anchor with
// top-left gravity grows the popup up-left of the anchor corner. Without
// slide adjustments the spec allows negative (partially offscreen) coords,
// and the compositor must not clamp uninvited.
func TestPlacePopupTopLeftGravityOfTopLeftAnchor(t *testing.T) {
	pos := base()
	pos.ConstraintAdjustment = 0 // no flip, no slide
	pos.AnchorMask = AnchorTop | AnchorLeft
	pos.GravityMask = AnchorTop | AnchorLeft
	pos.AnchorRect = [4]int32{10, 10, 20, 20}
	p := PlacePopup(pos, 400, 300, 400, 300)
	// Anchor corner (10,10); top-left gravity puts the popup's bottom-right
	// at the anchor: spans (-90,-22)..(10,10).
	if p.X != 10-100 || p.Y != 10-32 {
		t.Errorf("placement = %s, want (%d,%d 100x32)", p, 10-100, 10-32)
	}
}

// TestPlacePopupOffsetApplied: client-requested offsets shift placement.
func TestPlacePopupOffsetApplied(t *testing.T) {
	pos := base()
	pos.OffsetX, pos.OffsetY = 5, 7
	p := PlacePopup(pos, 400, 300, 400, 300)
	if p.X != 85 || p.Y != 35 {
		t.Errorf("position = %s, want (85,35 100x32)", p)
	}
}

// TestPlacePopupDegenerateSize: a client that commits before setting a size
// still gets sane geometry from the anchor rect (never crashes).
func TestPlacePopupDegenerateSize(t *testing.T) {
	pos := base()
	pos.Width, pos.Height = 0, 0
	p := PlacePopup(pos, 400, 300, 400, 300)
	if p.Width <= 0 || p.Height <= 0 {
		t.Errorf("degenerate size gave %s", p)
	}
}

// TestPlacePopupLargerThanOutput: a popup wider than the screen cannot be
// made fully visible; slide pins it to the near edges it can reach.
// Documented contract: over-wide popups saturate at (0,0) with slide.
func TestPlacePopupLargerThanOutput(t *testing.T) {
	pos := base()
	pos.Width, pos.Height = 500, 400
	pos.ConstraintAdjustment = AdjustSlideX | AdjustSlideY
	p := PlacePopup(pos, 400, 300, 400, 300)
	if p.X != 0 || p.Y != 0 {
		t.Errorf("over-wide popup = %s, want saturated at (0,0)", p)
	}
}

// TestPlacePopupDegenerateAndClamps are regression checks that survived the
// slide-clamp split above.
func TestPlacePopupDegenerateAndClamps(t *testing.T) {
	// Degenerate size falls back to the anchor rect extent.
	pos := base()
	pos.Width, pos.Height = 0, 0
	p := PlacePopup(pos, 400, 300, 400, 300)
	if p.Width <= 0 || p.Height <= 0 {
		t.Errorf("degenerate size gave %s", p)
	}
}
