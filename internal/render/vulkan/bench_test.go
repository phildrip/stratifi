package vulkan

import "testing"

func BenchmarkRendererFrame(b *testing.B) {
	dev, err := NewDevice()
	if err != nil {
		b.Skipf("no Vulkan device: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		b.Fatalf("NewRenderer failed: %v", err)
	}
	defer ren.Close()

	target, err := ren.CreateTarget(1920, 1080)
	if err != nil {
		b.Fatalf("CreateTarget failed: %v", err)
	}
	defer ren.DestroyTarget(target)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ren.BeginFrame(target, 0.1, 0.1, 0.12, 1.0)
		ren.DrawColorQuad(10, 10, 940, 1060, 0.2, 0.3, 0.4, 1.0)
		ren.DrawColorQuad(960, 10, 940, 1060, 0.15, 0.25, 0.35, 1.0)
		ren.EndFrame()
		ren.WaitFrame(1000000000)
	}
}
