package spikevk

import "testing"

// BenchmarkTexturedFrame measures the compositor-shaped workload in pure Go:
// two client textures composited into a 1280x720 frame. Reports wall clock
// and allocations per frame for the go/no-go.
func BenchmarkTexturedFrame(b *testing.B) {
	loader, err := AcquireLoader()
	if err != nil {
		b.Skipf("no vulkan loader: %v", err)
	}
	ctx, err := OpenInstance(loader)
	if err != nil {
		b.Skipf("no vulkan device: %v", err)
	}
	defer ctx.Close()

	frame, err := NewGpuFrame(ctx, 1280, 720)
	if err != nil {
		b.Fatalf("NewGpuFrame: %v", err)
	}
	defer frame.Close()

	quad, err := NewQuadRenderer(frame)
	if err != nil {
		b.Fatalf("NewQuadRenderer: %v", err)
	}
	defer quad.Close()

	mk := func(cb, cg, cr byte) *QuadTexture {
		tex, err := quad.UploadTexture(64, 64, solidPixels(64, 64, cb, cg, cr, 0x00))
		if err != nil {
			b.Fatalf("upload: %v", err)
		}
		return tex
	}
	red := mk(0x00, 0x00, 0xFF)
	green := mk(0x00, 0xFF, 0x00)
	defer quad.DestroyTexture(red)
	defer quad.DestroyTexture(green)

	draws := []QuadDraw{
		{Tex: red, Dst: [4]float32{15, 43, 1250, 320}, Src: [4]float32{0, 0, 1, 1}, Alpha: 1},
		{Tex: green, Dst: [4]float32{15, 375, 1250, 320}, Src: [4]float32{0, 0, 1, 1}, Alpha: 1},
	}
	if _, _, err := quad.Render([4]float32{0.09, 0.09, 0.11, 1}, draws, 1280, 720); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	clear := [4]float32{0.09, 0.09, 0.11, 1}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = quad.Render(clear, draws, 1280, 720)
	}
}
