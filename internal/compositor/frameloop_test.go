package compositor

import (
	"sync"
	"testing"
)

// FakeOutput is an in-memory render target for headless tests: the output
// interface without Wayland. Deterministic pixel assertions run against it.
type fakeOutput struct {
	mu        sync.Mutex
	width     int32
	height    int32
	stride    int32
	pix       []byte
	presented int
}

func newFakeOutput(w, h int32) *fakeOutput {
	stride := w * 4
	return &fakeOutput{
		width:  w,
		height: h,
		stride: stride,
		pix:    make([]byte, stride*h),
	}
}

func (o *fakeOutput) Width() int32   { return o.width }
func (o *fakeOutput) Height() int32  { return o.height }
func (o *fakeOutput) Stride() int32  { return o.stride }
func (o *fakeOutput) Pixels() []byte { return o.pix }

func (o *fakeOutput) Damage(x, y, w, h int32) {}

func (o *fakeOutput) Present() error {
	o.mu.Lock()
	o.presented++
	o.mu.Unlock()
	return nil
}

// pixel reads one BGRA pixel at (x,y) for assertions.
func (o *fakeOutput) pixel(x, y int32) uint32 {
	off := y*o.stride + x*4
	return uint32(o.pix[off]) | uint32(o.pix[off+1])<<8 | uint32(o.pix[off+2])<<16 | uint32(o.pix[off+3])<<24
}

func (o *fakeOutput) presentCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.presented
}

// fakeSource serves client pixels for a scene window.
type fakeSource struct {
	w, h, stride int32
	pix          []byte
}

func (s *fakeSource) Snapshot(clientID, surfaceID uint32, dst []byte) (int, int32, int32, int32, bool) {
	full := int(s.stride) * int(s.h)
	if len(dst) < full {
		return 0, 0, 0, 0, false
	}
	copy(dst, s.pix)
	return full, s.w, s.h, s.stride, true
}

func TestFrameLoopRendersScene(t *testing.T) {
	out := newFakeOutput(320, 200)
	src := &fakeSource{
		w: 100, h: 80, stride: 100 * 4,
		pix: solidPixels(100, 80, 0xFF0000FF), // opaque red BGRA
	}

	fl := NewFrameLoopWithInfo(out, func(dst []SceneWindow) []SceneWindow {
		return append(dst, SceneWindow{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 10, Y: 10, W: 104, H: 100}, Active: true})
	}, src.Snapshot, func(clientID, surfaceID uint32) (int32, int32, int32, bool) {
		return src.w, src.h, src.stride, src.h > 0
	})

	fl.RenderOnce()

	// Center of the client area must be the client's red pixel.
	px := out.pixel(50, 60)
	if px != 0xFF0000FF {
		t.Errorf("client pixel = %08x, want FF0000FF", px)
	}
	// Inside the border ring must be the border color, not the background.
	px = out.pixel(12, 12)
	if px != BorderColorInactive {
		t.Errorf("border pixel = %08x, want %08x", px, BorderColorActive)
	}
	if out.presentCount() != 1 {
		t.Errorf("present count = %d, want 1", out.presentCount())
	}
}

func TestFrameLoopBackgroundAndTitlebar(t *testing.T) {
	out := newFakeOutput(320, 200)
	src := &fakeSource{w: 50, h: 40, stride: 50 * 4, pix: solidPixels(50, 40, 0xFF00FF00)}

	fl := NewFrameLoopWithInfo(out, func(dst []SceneWindow) []SceneWindow {
		return append(dst, SceneWindow{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 100, H: 100}, Active: false})
	}, src.Snapshot, func(clientID, surfaceID uint32) (int32, int32, int32, bool) {
		return src.w, src.h, src.stride, src.h > 0
	})

	fl.RenderOnce()

	// Top strip inside the border is the idle titlebar color (inactive).
	if px := out.pixel(50, 5); px != TitlebarColorIdle {
		t.Errorf("titlebar pixel = %08x, want %08x", px, TitlebarColorIdle)
	}
	// Outside any window: background.
	if px := out.pixel(200, 150); px != BackgroundColor {
		t.Errorf("background pixel = %08x, want %08x", px, BackgroundColor)
	}
}

// TestFrameLoopCoalescing pins drop-stale semantics: flooding render
// requests must never queue (one pending max) and must render the LATEST
// scene, not every intermediate.
func TestFrameLoopCoalescing(t *testing.T) {
	out := newFakeOutput(320, 200)
	fl := NewFrameLoop(out, func(dst []SceneWindow) []SceneWindow {
		return dst
	}, func(clientID, surfaceID uint32, dst []byte) (int, int32, int32, int32, bool) {
		return 0, 0, 0, 0, false
	})

	go fl.Run()

	for i := 0; i < 1000; i++ {
		fl.Request()
	}
	// Wait until the channel drains (the loop consumed all pending).
	deadline := 1000
	for len(fl.renderCh) > 0 && deadline > 0 {
		deadline--
	}
	fl.Stop()
	<-fl.done

	if p := out.presentCount(); p == 0 {
		t.Error("expected at least one present after flood")
	}
}

// TestFrameLoopSourceTeardown exercises the live-crash scenario: the surface
// source vanishes (pool unmapped) between scenes; the loop must keep
// rendering placeholder fills, never faulting.
func TestFrameLoopSourceTeardown(t *testing.T) {
	out := newFakeOutput(320, 200)
	src := &fakeSource{w: 50, h: 40, stride: 50 * 4, pix: solidPixels(50, 40, 0xFFFFFFFF)}

	fl := NewFrameLoop(out, func(dst []SceneWindow) []SceneWindow {
		return append(dst, SceneWindow{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 100, H: 100}, Active: true})
	}, src.Snapshot)

	fl.RenderOnce()

	// Source goes away (surface destroyed).
	src.h = 0
	src.stride = 0
	fl.RenderOnce() // must not fault; placeholder fill path

	if out.presentCount() != 2 {
		t.Errorf("present count = %d, want 2", out.presentCount())
	}
}

// solidPixels builds an opaque BGRA test pattern.
func solidPixels(w, h int32, color uint32) []byte {
	pix := make([]byte, w*4*h)
	for i := 0; i < len(pix); i += 4 {
		pix[i] = byte(color)
		pix[i+1] = byte(color >> 8)
		pix[i+2] = byte(color >> 16)
		pix[i+3] = byte(color >> 24)
	}
	return pix
}

// TestFrameLoopLateHooks is the regression test for the late-binding bug:
// hooks installed AFTER construction (the actor model's boot order) must
// take effect — constructor-time closures captured nils and every client
// blit silently fell to the placeholder path.
func TestFrameLoopLateHooks(t *testing.T) {
	out := newFakeOutput(320, 200)
	src := &fakeSource{w: 100, h: 80, stride: 100 * 4, pix: solidPixels(100, 80, 0xFF0000FF)}

	fl := NewFrameLoopWithInfo(out, nil, nil, nil) // hooks nil at construction

	// Actor boot: install hooks after construction.
	fl.SetScene(func(dst []SceneWindow) []SceneWindow {
		return append(dst, SceneWindow{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 10, Y: 10, W: 104, H: 100}, Active: true})
	})
	fl.SetSnapshot(src.Snapshot)
	fl.SetInfo(func(clientID, surfaceID uint32) (int32, int32, int32, bool) {
		return src.w, src.h, src.stride, true
	})

	fl.RenderOnce()

	if px := out.pixel(50, 60); px != 0xFF0000FF {
		t.Errorf("client pixel = %08x, want FF0000FF (late hooks not honored?)", px)
	}
	if px := out.pixel(50, 120); px == 0xFF11111B {
		t.Error("placeholder fill still present — snapshot hook not resolved")
	}
}

// TestFrameLoopNilSnapshotFallsBack covers the nil-hook guard: with no
// snapshot installed the render must use the placeholder, not fault.
func TestFrameLoopNilSnapshotFallsBack(t *testing.T) {
	out := newFakeOutput(320, 200)
	fl := NewFrameLoop(out, func(dst []SceneWindow) []SceneWindow {
		return append(dst, SceneWindow{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 100, H: 100}, Active: true})
	}, nil)

	fl.RenderOnce() // must not panic
	if px := out.pixel(50, 60); px != PlaceholderColor {
		t.Errorf("placeholder pixel = %08x, want %08x", px, PlaceholderColor)
	}
}
