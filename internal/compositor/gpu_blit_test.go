package compositor

import "testing"

// solidAlphaSource builds client pixels in the given byte order (BGRA with a
// caller-chosen alpha byte), matching how a real client fills an shm buffer.
func solidAlphaSource(w, h int32, b, g, r, a byte) *fakeSource {
	src := &fakeSource{w: w, h: h, stride: w * 4, pix: make([]byte, w*4*h)}
	for i := 0; i < len(src.pix); i += 4 {
		src.pix[i+0] = b
		src.pix[i+1] = g
		src.pix[i+2] = r
		src.pix[i+3] = a
	}
	return src
}

func blitScene(w, h int32) []SceneWindow {
	return []SceneWindow{
		{ClientID: 1, SurfaceID: 3, Rect: Rect{X: 15, Y: 43, W: w + 2, H: h + 38}, Active: false},
	}
}

// TestGPUBlitZeroAlphaClient: clients such as foot commit XRGB8888 buffers
// whose alpha byte is never written (0). Compositor surfaces are opaque, so
// the content must still be visible rather than blended away.
func TestGPUBlitZeroAlphaClient(t *testing.T) {
	exe, err := NewVulkanExecutor(1280, 720)
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	defer exe.Close()

	const w, h = 1248, 624
	src := solidAlphaSource(w, h, 0x00, 0xFF, 0x00, 0x00) // green, alpha 0

	dl := NewDrawList()
	dl.Build(blitScene(w, h), FrameConfig{BorderWidth: 2, TitlebarHeight: 16}, 1280, 720, 0)
	exe.Execute(dl,
		func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return w, h, w * 4, true },
		func(clientID, surfaceID uint32, dst []byte) (int, bool) {
			n, _, _, _, ok := src.Snapshot(clientID, surfaceID, dst)
			return n, ok
		},
	)

	data, ok := exe.ren.ReadbackTarget(exe.target)
	if !ok {
		t.Fatal("readback failed")
	}
	px := readPixel(data, 1280*4, 600, 300)
	if px&0xFF != 0x00 || (px>>8)&0xFF != 0xFF || (px>>16)&0xFF != 0x00 {
		t.Errorf("client body = %08x, want opaque green FF00FF00", px)
	}
}

// TestGPUBlitMultiFrame reproduces the live frame loop: repeated Execute calls
// plus a readback each frame (the phase-3 bridge). A readback that resets the
// shared frame fence leaves it unsignalled, so the next begin_frame times out,
// inPass stays 0, and every later frame renders nothing.
func TestGPUBlitMultiFrame(t *testing.T) {
	exe, err := NewVulkanExecutor(1280, 720)
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	defer exe.Close()

	const w, h = 1248, 624
	src := solidAlphaSource(w, h, 0x00, 0xFF, 0x00, 0x00)

	dl := NewDrawList()
	scene := blitScene(w, h)
	avail := func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return w, h, w * 4, true }
	copyFn := func(clientID, surfaceID uint32, dst []byte) (int, bool) {
		n, _, _, _, ok := src.Snapshot(clientID, surfaceID, dst)
		return n, ok
	}

	for frame := 0; frame < 120; frame++ {
		dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 16}, 1280, 720, 0)
		exe.Execute(dl, avail, copyFn)
		data, ok := exe.ren.ReadbackTarget(exe.target)
		if !ok {
			t.Fatalf("frame %d: readback failed", frame)
		}
		if px := readPixel(data, 1280*4, 600, 300); px&0xFF != 0x00 || (px>>8)&0xFF != 0xFF || (px>>16)&0xFF != 0x00 {
			t.Fatalf("frame %d: client body = %08x, want opaque green (frame loop stalled)", frame, px)
		}
	}
}
