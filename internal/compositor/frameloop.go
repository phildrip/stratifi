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
)

// Rect is a window rectangle in output coordinates.
type Rect struct {
	X, Y, W, H int32
}

// SceneWindow is one window's render state, snapshotted as plain data
// (DOD: value copies, no pointers crossing into the frame loop).
type SceneWindow struct {
	ClientID  uint32 // surface ids are per-client: (ClientID, SurfaceID) is the key
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
// locking, returning the buffer geometry. The key is (clientID, surfaceID):
// object ids are per-client, so the client scope is mandatory.
type SnapshotFn func(clientID, surfaceID uint32, dst []byte) (n int, w, h, stride int32, ok bool)

// SurfaceInfoFn reports a surface's buffer geometry without touching pixels
// (scratch sizing); server.Client.SurfaceBufferInfo implements it.
type SurfaceInfoFn func(clientID, surfaceID uint32) (w, h, stride int32, ok bool)

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
	avail  AvailFn
	copyFn CopyFn
	cfg    FrameConfig

	renderCh chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	sceneBuf []SceneWindow // renderer-owned scene snapshot backing array
	drawList DrawList      // renderer-owned flat frame record
	executor Executor      // defaults to CPU blitter; Vulkan swaps in later
}

// NewFrameLoop builds a frame loop for the given output using the CPU
// blitter executor.
func NewFrameLoop(out Output, scene SceneFn, snap SnapshotFn) *FrameLoop {
	return NewFrameLoopWithInfo(out, scene, snap, nil)
}

// NewFrameLoopWithInfo adds an optional geometry probe for scratch sizing.
// Without it, the loop grows the scratch based on snapshot results only.
func NewFrameLoopWithInfo(out Output, scene SceneFn, snap SnapshotFn, info SurfaceInfoFn) *FrameLoop {
	fl := &FrameLoop{
		out:      out,
		scene:    scene,
		snap:     snap,
		info:     info,
		cfg:      DefaultConfig,
		renderCh: make(chan struct{}, 1),
		done:     make(chan struct{}),
		executor: NewCPUExecutor(out),
	}
	// Cache the hook adapters once: they resolve fl.info/fl.snap at call
	// time without per-frame method-value allocation.
	fl.avail = fl.availHook
	fl.copyFn = fl.copyHook
	return fl
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

	fl.drawList.Build(scene, fl.cfg, fl.out.Width(), fl.out.Height(), 0)
	// The cached method values resolve fl.info/fl.snap at CALL time (so
	// SetInfo/SetSnapshot after construction take effect) without
	// allocating per render — a fresh method value each call cost
	// 2 allocs/op and tripped the zero-alloc gate.
	fl.executor.Execute(&fl.drawList, fl.avail, fl.copyFn)
}

// availHook/copyHook adapt the optional info/snapshot hooks for the
// executor, resolving the current fields on every call.
func (fl *FrameLoop) availHook(clientID, surfaceID uint32) (w, h, stride int32, ok bool) {
	if fl.info == nil {
		return 0, 0, 0, false
	}
	return fl.info(clientID, surfaceID)
}

func (fl *FrameLoop) copyHook(clientID, surfaceID uint32, dst []byte) (n int, ok bool) {
	if fl.snap == nil {
		return 0, false
	}
	n, _, _, _, ok = fl.snap(clientID, surfaceID, dst)
	return n, ok
}

// Executor materializes a DrawList onto the frame loop's output. The CPU
// blitter is the shm fallback; the Vulkan executor (bloxwm-o2x phase 3)
// consumes the same flat records as draw calls.
type Executor interface {
	Execute(dl *DrawList, avail AvailFn, copyFn CopyFn)
}

// SetConfig overrides the chrome metrics. Must be called before Run.
func (fl *FrameLoop) SetConfig(cfg FrameConfig) { fl.cfg = cfg }

// SetExecutor swaps the materializer (CPU blitter → Vulkan). Must be
// called before the first render.
func (fl *FrameLoop) SetExecutor(ex Executor) { fl.executor = ex }

// SetScene/SetSnapshot/SetInfo install the actor's data hooks. Must be
// called before Run/RenderOnce; the hooks then run on the calling
// goroutine (the actor), which owns the scene state — no locking.
func (fl *FrameLoop) SetScene(fn SceneFn)       { fl.scene = fn }
func (fl *FrameLoop) SetSnapshot(fn SnapshotFn) { fl.snap = fn }
func (fl *FrameLoop) SetInfo(fn SurfaceInfoFn)  { fl.info = fn }
