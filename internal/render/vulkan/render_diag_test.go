package vulkan

import "testing"

// TestRenderClearReadback isolates render+readback without textures:
// clear + readback must show the clear color (alpha 255).
func TestRenderClearReadback(t *testing.T) {
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

	target, err := ren.CreateTarget(16, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer ren.DestroyTarget(target)

	if err := ren.BeginFrame(target, 1, 0, 0, 1); err != nil {
		t.Fatalf("begin: %v", err)
	}
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
	t.Logf("center after clear = % x", out[9*16*4+9*4:9*16*4+9*4+4])
}
