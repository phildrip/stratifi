package spikevk

import "testing"

// BenchmarkFrameSteadyState measures the hot loop the bead asks about:
// repeated full frames at the compositor's resolution, wall clock and
// allocations per frame. The first call absorbs creation costs.
func BenchmarkFrameSteadyState(b *testing.B) {
	ctx, done := openGraphicsBench(b)
	defer done()

	frame, err := NewGpuFrame(ctx, 1280, 720)
	if err != nil {
		b.Fatalf("NewGpuFrame: %v", err)
	}
	defer frame.Close()

	if _, _, err := frame.Render(0.09, 0.09, 0.11, 1.0); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := frame.Render(0.09, 0.09, 0.11, 1.0); err != nil {
			b.Fatalf("frame %d: %v", i, err)
		}
	}
}

func openGraphicsBench(b *testing.B) (*GraphicsContext, func()) {
	b.Helper()
	loader, err := AcquireLoader()
	if err != nil {
		b.Skipf("no vulkan loader: %v", err)
	}
	ctx, err := OpenInstance(loader)
	if err != nil {
		b.Skipf("no vulkan device: %v", err)
	}
	return ctx, func() { ctx.Close() }
}
