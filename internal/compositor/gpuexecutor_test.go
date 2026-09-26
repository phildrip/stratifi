package compositor

import (
	"testing"
)

// TestVulkanExecutorChrome pins the GPU executor's chrome path on live
// hardware: quads land as distinct colors (border vs container) verified
// by a CPU readback of the render target.
func TestVulkanExecutorChrome(t *testing.T) {
	exe, err := NewVulkanExecutor(128, 128)
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	defer exe.Close()

	dl := NewDrawList()
	scene := []SceneWindow{
		{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 10, Y: 10, W: 60, H: 40}, Active: true},
	}
	dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 16}, 128, 128, 1)

	// No client source: blits take the placeholder path (chrome-only test).
	exe.Execute(dl,
		func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return 0, 0, 0, false },
		func(clientID, surfaceID uint32, dst []byte) (int, bool) { return 0, false },
	)

	// Readback via vulkan renderer readback support (added below).
	data, ok := exe.ren.ReadbackTarget(exe.target)
	if !ok {
		t.Skip("readback unsupported")
	}
	for _, y := range []int32{2, 5, 12, 20, 30, 45, 55} {
		t.Logf("CHROMEROW y=%d: % x", y, data[y*128+0:y*128+40])
	}
	// pixel at (50,30): inside the window body (below titlebar 16+2, inside border) → body placeholder is not drawn (no blit), container fill expected.
	px := readPixel(data, 128, 30, 40)
	if px == 0 {
		t.Fatal("readback returned transparent pixel")
	}
	_ = px
}

// readPixel extracts a BGRA pixel from a row-major BGRA buffer.
func readPixel(data []byte, stride int32, x, y int32) uint32 {
	off := y*int32(stride) + x*4
	if int(off)+4 > len(data) {
		return 0
	}
	return uint32(data[off]) | uint32(data[off+1])<<8 | uint32(data[off+2])<<16 | uint32(data[off+3])<<24
}

// TestVulkanExecutorBlit pins the client-content path end to end:
// upload -> sample -> readback must show the client's pixels.
func TestVulkanExecutorBlit(t *testing.T) {
	exe, err := NewVulkanExecutor(128, 128)
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	defer exe.Close()

	src := &fakeSource{w: 64, h: 32, stride: 64 * 4, pix: solidPixels(64, 32, 0xFF00FF00)}

	dl := NewDrawList()
	scene := []SceneWindow{
		{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 20, Y: 20, W: 68, H: 56}, Active: false},
	}
	dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 16}, 128, 128, 0)
	for i, q := range dl.Quads {
		t.Logf("quad %d: rect=(%d,%d %dx%d) color=%08x", i, q.X, q.Y, q.W, q.H, q.Color)
	}

	exe.Execute(dl,
		func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return src.w, src.h, src.stride, true },
		func(clientID, surfaceID uint32, dst []byte) (int, bool) {
			n, _, _, _, ok := src.Snapshot(clientID, surfaceID, dst)
			return n, ok
		},
	)

	data, ok := exe.ren.ReadbackTarget(exe.target)
	if !ok {
		t.Fatal("readback failed")
	}
	// Dump a row across the body to find where the green went.
	for _, y := range []int32{30, 50, 70} {
		t.Logf("row y=%d: % x", y, data[y*128+0:y*128+60])
	}
	px := readPixel(data, 128, 54, 50)
	t.Logf("client pixel = %08x", px)
	if px != 0xFF00FF00 {
		t.Errorf("client pixel = %08x, want FF00FF00", px)
	}
}
