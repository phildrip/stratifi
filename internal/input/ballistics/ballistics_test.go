package ballistics

import (
	"testing"
)

func TestLinearProfile(t *testing.T) {
	b := NewBallisticsEngine(LinearConfig(1.5))

	dx, dy := b.Accelerate(10, 20, 1000)
	if dx != 15 || dy != 30 {
		t.Fatalf("expected (15, 30), got (%f, %f)", dx, dy)
	}

	dx, dy = b.Accelerate(-4, 0, 2000)
	if dx != -6 || dy != 0 {
		t.Fatalf("expected (-6, 0), got (%f, %f)", dx, dy)
	}
}

func TestAdaptiveSigmoidProfile(t *testing.T) {
	cfg := DefaultConfig()
	b := NewBallisticsEngine(cfg)

	// Low speed movement: 1px per 10ms -> 0.1 px/ms (below 0.4 threshold)
	for i := 0; i < 5; i++ {
		dx, dy := b.Accelerate(1, 0, uint64(1000+i*10000))
		if dx < 0.9 || dx > 1.1 || dy != 0 {
			t.Fatalf("expected precision ~1.0, got (%f, %f)", dx, dy)
		}
	}

	// High speed flick movement: 50px per 2ms -> 25 px/ms (high speed)
	var lastDx float64
	for i := 0; i < 5; i++ {
		dx, _ := b.Accelerate(50, 0, uint64(100000+i*2000))
		lastDx = dx
	}

	// Should be accelerated significantly above raw 50 (up to 3.5x = 175)
	if lastDx <= 50.0 {
		t.Fatalf("expected accelerated dx > 50, got %f", lastDx)
	}
	if lastDx > 175.1 {
		t.Fatalf("expected accelerated dx <= 175, got %f", lastDx)
	}
}

func TestBallisticsReset(t *testing.T) {
	b := NewBallisticsEngine(DefaultConfig())

	// Build up high speed
	for i := 0; i < 5; i++ {
		b.Accelerate(100, 0, uint64(1000+i*1000))
	}

	if b.smoothedVel <= 0 {
		t.Fatalf("expected positive smoothed velocity")
	}

	b.Reset()

	if b.smoothedVel != 0 || b.lastTimeUsec != 0 {
		t.Fatalf("expected zeroed velocity after reset")
	}
}

func BenchmarkBallisticsAccelerate(b *testing.B) {
	engine := NewBallisticsEngine(DefaultConfig())

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = engine.Accelerate(12.5, -8.3, uint64(1000+i*8000))
	}
}
