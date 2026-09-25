package layout

import (
	"testing"
)

func TestLayoutComputeAndBalance(t *testing.T) {
	cfg := LayoutConfig{
		Gap:            10,
		BorderWidth:    4,
		TitlebarHeight: 24,
	}
	engine := NewEngine(cfg)

	// Allocate 3 windows
	w1 := engine.SlotMap.Allocate()
	w2 := engine.SlotMap.Allocate()
	w3 := engine.SlotMap.Allocate()

	_ = engine.AddWindow(w1)
	_ = engine.AddWindow(w2)
	_ = engine.AddWindow(w3)

	screen := Rect{X: 0, Y: 0, W: 1920, H: 1080}
	ids, rects := engine.Compute(screen)

	if len(ids) != 3 || len(rects) != 3 {
		t.Fatalf("expected 3 windows computed, got %d", len(rects))
	}

	// Verify all windows fit completely within screen bounds
	for i, r := range rects {
		if r.X < screen.X || r.Y < screen.Y || r.X+r.W > screen.W || r.Y+r.H > screen.H {
			t.Errorf("window %d out of bounds: %+v", ids[i], r)
		}
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("window %d has invalid size: %+v", ids[i], r)
		}
	}

	// Test Mouse Hit-Testing
	// Hit the titlebar of the first window
	titlebarPoint := Point{X: rects[0].X + 50, Y: rects[0].Y + 10}
	hit := engine.HitTest(titlebarPoint)
	if hit.Type != HitTitlebar || hit.WindowID != ids[0] {
		t.Errorf("expected HitTitlebar for %d, got %+v", ids[0], hit)
	}

	// Hit window body
	bodyPoint := Point{X: rects[0].X + 50, Y: rects[0].Y + 50}
	hit = engine.HitTest(bodyPoint)
	if hit.Type != HitWindowBody || hit.WindowID != ids[0] {
		t.Errorf("expected HitWindowBody for %d, got %+v", ids[0], hit)
	}

	// Hit left border
	borderPoint := Point{X: rects[0].X + 1, Y: rects[0].Y + 50}
	hit = engine.HitTest(borderPoint)
	if hit.Type != HitBorderLeft || hit.WindowID != ids[0] {
		t.Errorf("expected HitBorderLeft for %d, got %+v", ids[0], hit)
	}
}

func TestLayoutSwapAndMove(t *testing.T) {
	cfg := LayoutConfig{Gap: 5, BorderWidth: 2, TitlebarHeight: 20}
	engine := NewEngine(cfg)

	w1 := engine.SlotMap.Allocate()
	w2 := engine.SlotMap.Allocate()

	_ = engine.AddWindow(w1)
	_ = engine.AddWindow(w2)

	// Swap
	if !engine.SwapWindows(w1, w2) {
		t.Fatalf("SwapWindows failed")
	}

	screen := Rect{X: 0, Y: 0, W: 1920, H: 1080}
	ids, _ := engine.Compute(screen)
	if ids[0] != w2 || ids[1] != w1 {
		t.Errorf("expected swapped order [w2, w1], got %v", ids)
	}

	// Move w1 into a new column at the end
	if !engine.MoveWindowTo(w1, 10, 0) {
		t.Fatalf("MoveWindowTo failed")
	}

	ids, _ = engine.Compute(screen)
	if len(engine.Columns) < 2 {
		t.Errorf("expected at least 2 columns after move, got %d", len(engine.Columns))
	}

	// Remove w1
	if !engine.RemoveWindow(w1) {
		t.Fatalf("RemoveWindow failed")
	}
	ids, _ = engine.Compute(screen)
	if len(ids) != 1 || ids[0] != w2 {
		t.Errorf("expected only w2 remaining, got %v", ids)
	}
}

func BenchmarkLayoutComputeZeroAlloc(b *testing.B) {
	cfg := LayoutConfig{Gap: 10, BorderWidth: 4, TitlebarHeight: 24}
	engine := NewEngine(cfg)

	// Populate 16 windows across multiple columns
	for i := 0; i < 16; i++ {
		w := engine.SlotMap.Allocate()
		_ = engine.AddWindow(w)
	}

	screen := Rect{X: 0, Y: 0, W: 2560, H: 1440}

	// Warm-up to pre-allocate scratch slices
	engine.Compute(screen)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine.Compute(screen)
	}
}

func BenchmarkHitTestZeroAlloc(b *testing.B) {
	cfg := LayoutConfig{Gap: 10, BorderWidth: 4, TitlebarHeight: 24}
	engine := NewEngine(cfg)

	for i := 0; i < 16; i++ {
		w := engine.SlotMap.Allocate()
		_ = engine.AddWindow(w)
	}

	screen := Rect{X: 0, Y: 0, W: 2560, H: 1440}
	engine.Compute(screen)

	p := Point{X: 1280, Y: 720}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = engine.HitTest(p)
	}
}
