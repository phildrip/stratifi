package vulkan

import "testing"

// TestTwoQuadReadback bisects: background quad + red quad — if the red
// lands on top, sequenced color quads rasterize; if uniform, only the
// first draw executes.
func TestTwoColorQuadsReadback(t *testing.T) {
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
	ren.DrawColorQuad(0, 0, 32, 32, 1, 0, 0, 1) // background red
	ren.DrawColorQuad(8, 8, 16, 16, 0, 0, 1, 1) // inner blue
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
	t.Logf("TWOQUAD center=% x corner=% x", out[9*32*4+9*4:9*32*4+9*4+4], out[1*32*4+1*4:1*32*4+1*4+4])
	for row := 6; row < 14; row += 2 {
		t.Logf("TWOQUAD row%02d: % x", row, out[row*32*4+6*4:row*32*4+6*4+24])
	}
}
