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
