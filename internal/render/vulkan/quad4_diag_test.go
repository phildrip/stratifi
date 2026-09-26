package vulkan

import "testing"

// TestSplitFrame: red full-frame, then blue LEFT half. If push constants
// sequence correctly: left blue, right red. If only the first pc applies:
// everything red. If only the first draw runs: everything red.
func TestSplitFrame(t *testing.T) {
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
	ren.DrawColorQuad(0, 0, 16, 32, 0, 0, 1, 1)
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
	l := out[9*32*4+9*4 : 9*32*4+9*4+4]
	r := out[9*32*4+25*4 : 9*32*4+25*4+4]
	t.Logf("SPLIT left=% x right=% x", l, r)
}
