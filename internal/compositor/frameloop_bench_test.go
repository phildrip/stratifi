package compositor

import (
	"testing"
)

// BenchmarkFrameLoopTwoWindows is the assembled-loop zero-alloc gate
// (bloxwm-o2x): scene snapshot + clear + chrome + blit + present on a
// memory output must run at 0 allocs/op once the renderer goroutine owns
// its scratch.
func BenchmarkFrameLoopTwoWindows(b *testing.B) {
	out := newFakeOutput(1920, 1080)
	srcs := map[uint32]*fakeSource{
		1: {w: 900, h: 1000, stride: 900 * 4, pix: solidPixels(900, 1000, 0xFF202020)},
		2: {w: 900, h: 1000, stride: 900 * 4, pix: solidPixels(900, 1000, 0xFF303030)},
	}
	fl := NewFrameLoopWithInfo(out,
		func(dst []SceneWindow) []SceneWindow {
			return append(dst,
				SceneWindow{SurfaceID: 1, Rect: Rect{X: 4, Y: 4, W: 956, H: 1068}, Active: true},
				SceneWindow{SurfaceID: 2, Rect: Rect{X: 964, Y: 4, W: 952, H: 1068}, Active: false})
		},
		func(surfaceID uint32, dst []byte) (int, int32, int32, int32, bool) {
			return srcs[surfaceID].Snapshot(surfaceID, dst)
		},
		func(surfaceID uint32) (int32, int32, int32, bool) {
			s := srcs[surfaceID]
			return s.w, s.h, s.stride, true
		},
	)

	// Warm the scratch so the loop itself is allocation-free.
	fl.RenderOnce()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fl.RenderOnce()
	}
}
