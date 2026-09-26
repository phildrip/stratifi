package compositor

import (
	"sync"
	"testing"
)

// The DrawList is the backend-agnostic render record: the frame loop
// builds flat quads/blits (DOD: contiguous, value types), and an executor
// materializes them. CPU (blitter) and GPU (Vulkan) executors consume the
// same list — chrome drawing becomes identical on both paths.

func TestDrawListBuilderFoldsScene(t *testing.T) {
	dl := NewDrawList()
	defer dl.Reset()

	scene := []SceneWindow{
		{SurfaceID: 1, Rect: Rect{X: 10, Y: 10, W: 104, H: 100}, Active: true},
		{SurfaceID: 2, Rect: Rect{X: 200, Y: 10, W: 104, H: 100}, Active: false},
	}
	dl.Build(scene, FrameConfig{BorderWidth: 3, TitlebarHeight: 28}, 320, 200, 2)

	// Expected quad count per window: container fill + 4 border strips +
	// titlebar fill = 6 quads; plus 1 background quad = 13.
	if got := len(dl.Quads); got != 13 {
		t.Errorf("quads = %d, want 13", got)
	}
	// First quad: background covering the whole output.
	if q := dl.Quads[0]; q.X != 0 || q.Y != 0 || q.W != 320 || q.H != 200 || q.Color != BackgroundColor {
		t.Errorf("background quad = %+v", q)
	}
	// Window 1 (active) border color vs window 2 (inactive).
	if q := dl.Quads[1]; q.Color != ContainerColor || q.X != 10 {
		t.Errorf("container quad = %+v", q)
	}
}

func TestDrawListBlitEntries(t *testing.T) {
	dl := NewDrawList()
	scene := []SceneWindow{
		{SurfaceID: 7, Rect: Rect{X: 0, Y: 0, W: 100, H: 100}, Active: true},
	}
	dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 24}, 320, 200, 1)

	if len(dl.Blits) != 1 {
		t.Fatalf("blits = %d, want 1", len(dl.Blits))
	}
	b := dl.Blits[0]
	if b.SurfaceID != 7 {
		t.Errorf("blit surface = %d", b.SurfaceID)
	}
	if b.DstX != 2 || b.DstY != 26 { // border + titlebar offsets
		t.Errorf("blit dst = %d,%d", b.DstX, b.DstY)
	}
	if b.DstW != 96 || b.DstH != 72 { // 100 - 2*border(2) - titlebar(24) = 72
		t.Errorf("blit size = %dx%d", b.DstW, b.DstH)
	}
}

// TestCPUExecutorPixels pins that the executor produces the same pixels
// the legacy direct-draw path did: client content inside the body rect,
// chrome colors around it.
func TestCPUExecutorPixels(t *testing.T) {
	out := newFakeOutput(320, 200)
	src := &fakeSource{w: 100, h: 80, stride: 100 * 4, pix: solidPixels(100, 80, 0xFF0000FF)}

	scene := []SceneWindow{
		{SurfaceID: 1, Rect: Rect{X: 10, Y: 10, W: 104, H: 100}, Active: true},
	}
	var dl *DrawList
	func() {
		d := NewDrawList()
		d.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 24}, 320, 200, 1)
		dl = d
	}()

	exe := NewCPUExecutor(out)
	avail := func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return src.w, src.h, src.stride, true }
	copyFn := func(clientID, surfaceID uint32, dst []byte) (int, bool) {
		copy(dst, src.pix)
		return len(src.pix), true
	}
	exe.Execute(dl, avail, copyFn)

	if px := out.pixel(50, 60); px != 0xFF0000FF {
		t.Errorf("client pixel = %08x", px)
	}
	if px := out.pixel(10, 60); px != BorderColorActive {
		t.Errorf("border strip pixel = %08x, want %08x", px, BorderColorActive)
	}
	if px := out.pixel(50, 15); px != TitlebarColor {
		t.Errorf("titlebar pixel = %08x, want %08x", px, TitlebarColor)
	}
	if out.presentCount() != 1 {
		t.Errorf("present = %d", out.presentCount())
	}
}

// TestDrawListReuse pins zero-alloc accumulation: Build on a reused list
// must not grow unboundedly and must reset between frames.
func TestDrawListReuse(t *testing.T) {
	dl := NewDrawList()
	scene := []SceneWindow{{SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 50, H: 50}, Active: false}}
	dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 24}, 320, 200, 1)
	n1 := len(dl.Quads)
	dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 24}, 320, 200, 1)
	if len(dl.Quads) != n1 {
		t.Errorf("quads not reset between frames: %d then %d", n1, len(dl.Quads))
	}
}

// TestConcurrentDrawListBuild guards the build path used from multiple
// goroutines when the compositor tests -race discipline on the flat
// storage (the frame loop is single-owner, but the API must be honest
// about needing external sync).
func TestConcurrentDrawListBuild(t *testing.T) {
	var mu sync.Mutex
	dl := NewDrawList()
	scene := []SceneWindow{{SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 50, H: 50}, Active: false}}

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				mu.Lock()
				dl.Build(scene, FrameConfig{BorderWidth: 2, TitlebarHeight: 24}, 320, 200, 1)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}
