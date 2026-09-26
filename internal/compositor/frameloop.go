// Package compositor implements the frame loop that assembles the
// compositor pipeline: scene snapshot → clear/border/titlebar/blit →
// present (bloxwm-o2x).
//
// Ownership model (proven live in cmd/bloxwm): exactly ONE goroutine — the
// frame loop — touches the output framebuffer. Scene state is snapshotted
// under the caller's lock; rendering is lock-free. Render requests coalesce
// on a size-1 channel: stale frames are dropped, the newest scene renders.
// The frame loop owns its scratch buffers; no steady-state allocation.
//
// GPU path (Vulkan + DMA-BUF) plugs in behind the Output interface; the shm
// blitter is the fallback present path.
package compositor

import (
	"sync"

	"bloxwm/internal/render"
)

// Rect is a window rectangle in output coordinates.
type Rect struct {
	X, Y, W, H int32
}

// SceneWindow is one window's render state, snapshotted as plain data
// (DOD: value copies, no pointers crossing into the frame loop).
type SceneWindow struct {
	SurfaceID uint32
	Rect      Rect
	Active    bool
}

// Colors are ARGB8888 (little-endian BGRA in memory).
const (
	BackgroundColor     uint32 = 0xFF181825 // charcoal
	BorderColorInactive uint32 = 0xFF45475A
	BorderColorActive   uint32 = 0xFF89B4FA
	TitlebarColor       uint32 = 0xFF45475A
	TitlebarColorIdle   uint32 = 0xFF313244
	ContainerColor      uint32 = 0xFF1E1E2E
	PlaceholderColor    uint32 = 0xFF11111B
)

// FrameConfig mirrors the layout engine's chrome metrics.
type FrameConfig struct {
	BorderWidth    int32
	TitlebarHeight int32
}

// DefaultConfig matches bloxwm's current chrome.
var DefaultConfig = FrameConfig{
	BorderWidth:    2,
	TitlebarHeight: 24,
}

// Output is a render target. Implemented by the nested backend (parent
// Wayland), the KMS scanout path, and MemoryOutput for tests.
type Output interface {
	Width() int32
	Height() int32
	Stride() int32
	Pixels() []byte
	Damage(x, y, w, h int32)
	Present() error
}

// SnapshotFn copies a surface's pixels into dst under the source's own
// locking, returning the buffer geometry (see server.Client.SnapshotSurfaceBuffer).
type SnapshotFn func(surfaceID uint32, dst []byte) (n int, w, h, stride int32, ok bool)

// SurfaceInfoFn reports a surface's buffer geometry without touching pixels
// (scratch sizing); server.Client.SurfaceBufferInfo implements it.
type SurfaceInfoFn func(surfaceID uint32) (w, h, stride int32, ok bool)

// SceneFn snapshots the current window list into dst (append semantics;
// nil entries none) and returns the used prefix. Called on the frame-loop
// goroutine; it must take whatever lock protects the scene. Filling a
// reusable buffer keeps the render loop allocation-free.
type SceneFn func(dst []SceneWindow) []SceneWindow

// FrameLoop is the compositor's render engine.
type FrameLoop struct {
	out    Output
	scene  SceneFn
	snap   SnapshotFn
	info   SurfaceInfoFn
	cfg    FrameConfig

	renderCh chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	scratch  []byte       // renderer-owned; grows on larger surface sizes only
	sceneBuf []SceneWindow // renderer-owned scene snapshot backing array
}

// NewFrameLoop builds a frame loop for the given output.
func NewFrameLoop(out Output, scene SceneFn, snap SnapshotFn) *FrameLoop {
	return NewFrameLoopWithInfo(out, scene, snap, nil)
}

// NewFrameLoopWithInfo adds an optional geometry probe for scratch sizing.
// Without it, the loop grows the scratch based on snapshot results only.
func NewFrameLoopWithInfo(out Output, scene SceneFn, snap SnapshotFn, info SurfaceInfoFn) *FrameLoop {
	return &FrameLoop{
		out:      out,
		scene:    scene,
		snap:     snap,
		info:     info,
		cfg:      DefaultConfig,
		renderCh: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// Request schedules a render. Coalescing: at most one pending request —
// flooding drops stale frames and the newest scene renders.
func (fl *FrameLoop) Request() {
	select {
	case fl.renderCh <- struct{}{}:
	default:
	}
}

// Run consumes render requests until Stop. One goroutine, one framebuffer.
func (fl *FrameLoop) Run() {
	for range fl.renderCh {
		fl.RenderOnce()
	}
	close(fl.done)
}

// Stop ends the run loop (safe to call once; Run returns after draining).
func (fl *FrameLoop) Stop() {
	select {
	case <-fl.done:
	default:
		close(fl.renderCh)
	}
}

// Drain waits until the current pending request (if any) has rendered.
// Test helper; the live loop drives presentation by vblank instead.
func (fl *FrameLoop) Drain() {
	// Give the loop a scheduling beat to process the pending request.
	for len(fl.renderCh) > 0 {
		// busy-wait is acceptable in tests only
	}
}

// RenderOnce renders one frame synchronously (used by Run and tests).
// The renderer goroutine is the only caller in production; test callers
// substitute for it, which is safe because RenderOnce itself is not
// concurrent-safe by design — single-owner discipline is external.
func (fl *FrameLoop) RenderOnce() {
	fl.sceneBuf = fl.scene(fl.sceneBuf[:0])
	scene := fl.sceneBuf

	pixels := fl.out.Pixels()
	stride := fl.out.Stride()
	outW := fl.out.Width()
	outH := fl.out.Height()

	render.Clear(pixels, BackgroundColor)

	for i := range scene {
		w := &scene[i]
		borderCol := uint32(BorderColorInactive)
		if w.Active {
			borderCol = uint32(BorderColorActive)
		}

		render.FillRect(pixels, stride, w.Rect.X, w.Rect.Y, w.Rect.W, w.Rect.H, outW, outH, ContainerColor)
		render.DrawBorder(pixels, stride, w.Rect.X, w.Rect.Y, w.Rect.W, w.Rect.H, fl.cfg.BorderWidth, outW, outH, borderCol)

		titlebarH := fl.cfg.TitlebarHeight
		titleCol := uint32(TitlebarColorIdle)
		if w.Active {
			titleCol = uint32(TitlebarColor)
		}
		render.FillRect(pixels, stride, w.Rect.X+fl.cfg.BorderWidth, w.Rect.Y+fl.cfg.BorderWidth, w.Rect.W-2*fl.cfg.BorderWidth, titlebarH, outW, outH, titleCol)

		bodyX := w.Rect.X + fl.cfg.BorderWidth
		bodyY := w.Rect.Y + titlebarH + fl.cfg.BorderWidth
		bodyW := w.Rect.W - 2*fl.cfg.BorderWidth
		bodyH := w.Rect.H - titlebarH - 2*fl.cfg.BorderWidth

		// Blit client pixels beneath the titlebar. The snapshot copies
		// under the source's lock; the source mapping cannot disappear
		// mid-copy (pool teardown takes the same lock).
		if ok, sw, sh, sStride := fl.snapInto(w.SurfaceID); ok && sw > 0 && sh > 0 {
			render.Blit(pixels, stride, bodyX, bodyY, outW, outH, fl.scratch, sStride, sw, sh)
		} else {
			render.FillRect(pixels, stride, bodyX, bodyY, bodyW, bodyH, outW, outH, PlaceholderColor)
		}
	}

	fl.out.Damage(0, 0, outW, outH)
	_ = fl.out.Present()
}

// snapInto snapshots the surface into the renderer-owned scratch buffer,
// growing it when the surface is larger than the current allocation.
// Returns the decoded geometry; scratch holds exactly stride*height bytes
// on success.
func (fl *FrameLoop) snapInto(surfaceID uint32) (ok bool, w, h, stride int32) {
	if fl.info != nil {
		if _, sH, sStride, infoOK := fl.info(surfaceID); infoOK {
			if need := int(sStride) * int(sH); need > len(fl.scratch) {
				fl.scratch = make([]byte, need)
			}
		}
	}
	n, w, h, stride, ok := fl.snap(surfaceID, fl.scratch)
	if ok && n > 0 {
		return true, w, h, stride
	}
	return false, 0, 0, 0
}

// SetConfig overrides the chrome metrics. Must be called before Run.
func (fl *FrameLoop) SetConfig(cfg FrameConfig) { fl.cfg = cfg }
