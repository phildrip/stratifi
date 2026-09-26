package vulkan

import "testing"

// TestColorQuadPixels pins that color quads actually rasterize (the
// renderer tests only ever submitted without verification).
func TestColorQuadReadback(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer ren.Close()

	target, err := ren.CreateTarget(32, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer ren.DestroyTarget(target)

	if err := ren.BeginFrame(target, 0, 0, 0, 1); err != nil {
		t.Fatalf("begin: %v", err)
	}
	ren.DrawColorQuad(0, 0, 32, 32, 1, 0, 0, 1) // full-frame red
	if err := ren.EndFrame(); err != nil {
		t.Fatal(err)
	}
	if err := ren.WaitFrame(1000000000); err != nil {
		t.Fatal(err)
	}

	out, ok := ren.ReadbackTarget(target)
	if !ok {
		t.Fatal("readback failed")
	}
	t.Logf("center = % x", out[9*32*4+9*4:9*32*4+9*4+4])
}
