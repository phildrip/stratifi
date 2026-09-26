package compositor

import "testing"

// TestBlitUsesSourceStride pins the padded-stride bug class: a client
// buffer with stride > width*4 must blit with the SOURCE stride, not
// width*4 (foot pads buffers; the live bug showed skew).
func TestBlitUsesSourceStride(t *testing.T) {
	out := newFakeOutput(64, 40)
	// 8-wide client with 48-byte stride (padding after each 32-byte row).
	src := &fakeSource{w: 8, h: 4, stride: 48}
	src.pix = make([]byte, 48*4)
	// Paint each row: row N gets color N+1 in its first 8 pixels.
	for row := int32(0); row < 4; row++ {
		color := uint32(0xFF000000 + uint32(0x40*(row+1)))
		for x := int32(0); x < 8; x++ {
			off := row*48 + x*4
			src.pix[off] = byte(color)
			src.pix[off+1] = byte(color >> 8)
			src.pix[off+2] = byte(color >> 16)
			src.pix[off+3] = byte(color >> 24)
		}
	}

	dl := NewDrawList()
	scene := []SceneWindow{{SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 12, H: 8}, Active: false}}
	dl.Build(scene, FrameConfig{BorderWidth: 0, TitlebarHeight: 0}, 64, 40, 0)

	exe := NewCPUExecutor(out)
	exe.Execute(dl,
		func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return src.w, src.h, src.stride, true },
		func(clientID, surfaceID uint32, dst []byte) (int, bool) {
			n, _, _, _, ok := src.Snapshot(clientID, surfaceID, dst)
			return n, ok
		},
	)

	// Row 0's last client pixel (x=7) must be color 1 — if the executor
	// used width*4 as stride, rows would skew and this pixel would show
	// row 1's color.
	px := out.pixel(7, 0)
	if px != 0xFF000040 {
		t.Errorf("row0 pixel = %08x, want FF000040 (source stride ignored?)", px)
	}
	px = out.pixel(7, 3)
	if px != 0xFF000100 { // row 3 color = 0x40*4
		t.Errorf("row3 pixel = %08x, want FF000100", px)
	}
}
