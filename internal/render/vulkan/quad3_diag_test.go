package vulkan

import "testing"

// TestSameRectTwice: two quads at the SAME rect (red, then blue) — blue
// must win if sequenced draws work.
func TestSameRectTwice(t *testing.T) {
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
		t.Fatal(err)
	}
	ren.DrawColorQuad(0, 0, 32, 32, 1, 0, 0, 1)
	ren.DrawColorQuad(0, 0, 32, 32, 0, 0, 1, 1)
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
	c, k := out[9*32*4+9*4:9*32*4+9*4+4], out[1*32*4+1*4:1*32*4+1*4+4]
	t.Logf("SAMEDUAL center=% x corner=% x", c, k)
}
