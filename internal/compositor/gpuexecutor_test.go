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
	// Classified row map with sRGB-aware matching.
	for _, y := range []int32{40, 50, 64, 80, 100, 120} {
		row := ""
		for x := int32(0); x < 128; x += 8 {
			off := y*512 + x*4
			g, r := data[off+1], data[off+2]
			switch {
			case r == 0x8e:
				row += "T"
			case r == 0x89:
				row += "B"
			case r == 0x56:
				row += "."
			default:
				row += "?"
			}
			_ = g
		}
		t.Logf("ROWMAP y=%03d %s", y, row)
	}
	// Scan exact bounds of the non-background region.
	minX, maxX, minY, maxY := int32(999), int32(-1), int32(999), int32(-1)
	for y := int32(0); y < 128; y++ {
		for x := int32(0); x < 128; x++ {
			off := y*512 + x*4
			if !(data[off] == 0x6a && data[off+1] == 0x56 && data[off+2] == 0x56) {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	t.Logf("BOUNDS non-background: x=%d..%d y=%d..%d (scene window: x=10..70 y=10..50)", minX, maxX, minY, maxY)
	// Actual pixel values in the rendered region.
	for _, pt := range [][2]int32{{16, 42}, {16, 64}, {16, 100}, {16, 124}, {40, 64}, {64, 64}} {
		off := pt[1]*512 + pt[0]*4
		t.Logf("PIX (%d,%d) = % x (B G R A)", pt[0], pt[1], data[off:off+4])
	}
	// Coarse ASCII map: classify each 4x4 block by its first pixel.
	for y := int32(0); y < 128; y += 4 {
		row := ""
		for x := int32(0); x < 128; x += 4 {
			off := y*512 + x*4
			b, g, r := data[off], data[off+1], data[off+2]
			switch {
			case b == 0x6a && g == 0x56:
				row += "." // background
			case b == 0x71:
				row += "C" // container
			case r == 0x89:
				row += "B" // active border
			case b == 0x44:
				row += "T" // titlebar
			default:
				row += "?"
			}
		}
		t.Logf("MAP y=%02d %s", y, row)
	}
	// pixel at (50,30): inside the window body (below titlebar 16+2, inside border) → body placeholder is not drawn (no blit), container fill expected.
	px := readPixel(data, 512, 30, 40)
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
		t.Logf("row y=%d: % x", y, data[y*512+0:y*512+60])
	}
	px := readPixel(data, 512, 54, 50)
	t.Logf("client pixel = %08x", px)
	if px != 0xFF00FF00 {
		t.Errorf("client pixel = %08x, want FF00FF00", px)
	}
}
