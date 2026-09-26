package compositor

import (
	"bloxwm/internal/render"
)

// Quad is one solid-color rectangle: the flat record behind Clear,
// FillRect, and border strips.
type Quad struct {
	X, Y, W, H int32
	Color      uint32
}

// Blit is one client-surface blit: destination rect plus the surface to
// resolve through the executor's source hooks.
type Blit struct {
	SurfaceID    uint32
	DstX, DstY   int32
	DstW, DstH   int32
}

// DrawList is the backend-agnostic frame record. The frame loop builds it
// as flat contiguous data; an executor materializes it onto an Output
// (CPU blitter or Vulkan). The backing arrays are reused across frames —
// Build resets lengths, capacity grows to the high-water mark only.
type DrawList struct {
	Quads []Quad
	Blits []Blit
}

// NewDrawList creates an empty list with sensible initial capacity.
func NewDrawList() *DrawList {
	return &DrawList{
		Quads: make([]Quad, 0, 64),
		Blits: make([]Blit, 0, 8),
	}
}

// Reset empties the list (retains capacity).
func (dl *DrawList) Reset() {
	dl.Quads = dl.Quads[:0]
	dl.Blits = dl.Blits[:0]
}

// Quad appends one solid quad.
func (dl *DrawList) Quad(x, y, w, h int32, color uint32) {
	dl.Quads = append(dl.Quads, Quad{X: x, Y: y, W: w, H: h, Color: color})
}

// Build folds a scene into chrome quads + client blits. cfg carries the
// chrome metrics so the frame loop stays metric-agnostic.
func (dl *DrawList) Build(scene []SceneWindow, cfg FrameConfig, outW, outH int32, activeHint int) {
	dl.Reset()

	// Background first.
	dl.Quads = append(dl.Quads, Quad{X: 0, Y: 0, W: outW, H: outH, Color: BackgroundColor})

	for i := range scene {
		w := &scene[i]
		borderCol := uint32(BorderColorInactive)
		if w.Active {
			borderCol = uint32(BorderColorActive)
		}

		// Container fill.
		dl.Quads = append(dl.Quads, Quad{w.Rect.X, w.Rect.Y, w.Rect.W, w.Rect.H, ContainerColor})

		// Border: 4 strips.
		bw := cfg.BorderWidth
		r := w.Rect
		dl.Quads = append(dl.Quads, Quad{r.X, r.Y, r.W, bw, borderCol})                     // top
		dl.Quads = append(dl.Quads, Quad{r.X, r.Y + r.H - bw, r.W, bw, borderCol})          // bottom
		dl.Quads = append(dl.Quads, Quad{r.X, r.Y + bw, bw, r.H - 2*bw, borderCol})         // left
		dl.Quads = append(dl.Quads, Quad{r.X + r.W - bw, r.Y + bw, bw, r.H - 2*bw, borderCol}) // right

		// Titlebar fill (inside border).
		th := cfg.TitlebarHeight
		titleCol := uint32(TitlebarColorIdle)
		if w.Active {
			titleCol = uint32(TitlebarColor)
		}
		dl.Quads = append(dl.Quads, Quad{r.X + bw, r.Y + bw, r.W - 2*bw, th, titleCol})

		// Client blit beneath the titlebar.
		dl.Blits = append(dl.Blits, Blit{
			SurfaceID: w.SurfaceID,
			DstX:      r.X + bw,
			DstY:      r.Y + bw + th,
			DstW:      r.W - 2*bw,
			DstH:      r.H - bw - th - bw,
		})
	}
}

// AvailFn reports a surface's client buffer geometry (w, h, stride, ok).
// Stride MUST come from the source: client buffers may be padded, so
// stride is not derivable from width.
type AvailFn func(surfaceID uint32) (w, h, stride int32, ok bool)

// CopyFn copies a surface's pixels into dst under the source's lock.
type CopyFn func(surfaceID uint32, dst []byte) (n int, ok bool)

// CPUExecutor materializes a DrawList onto a CPU pixel Output using the
// blitter package. Owned by the frame loop's renderer goroutine.
type CPUExecutor struct {
	out     Output
	scratch []byte
}

// NewCPUExecutor builds an executor for the given output.
func NewCPUExecutor(out Output) *CPUExecutor {
	return &CPUExecutor{out: out}
}

// Execute renders the list: quads via the blitter, blits via snapshot
// copies into the executor-owned scratch.
func (e *CPUExecutor) Execute(dl *DrawList, avail AvailFn, copyFn CopyFn) {
	pixels := e.out.Pixels()
	stride := e.out.Stride()
	outW := e.out.Width()
	outH := e.out.Height()

	for i := range dl.Quads {
		q := &dl.Quads[i]
		if q.W <= 0 || q.H <= 0 {
			continue
		}
		// Full-coverage quads go through Clear (memset-speed).
		if q.X == 0 && q.Y == 0 && q.W >= outW && q.H >= outH {
			render.Clear(pixels, q.Color)
			continue
		}
		render.FillRect(pixels, stride, q.X, q.Y, q.W, q.H, outW, outH, q.Color)
	}

	for i := range dl.Blits {
		b := &dl.Blits[i]
		sw, sh, sStride, ok := avail(b.SurfaceID)
		if !ok || sw <= 0 || sh <= 0 || sStride <= 0 {
			render.FillRect(pixels, stride, b.DstX, b.DstY, b.DstW, b.DstH, outW, outH, PlaceholderColor)
			continue
		}
		if need := int(sStride) * int(sh); need > len(e.scratch) {
			e.scratch = make([]byte, need)
		}
		n, ok := copyFn(b.SurfaceID, e.scratch)
		if !ok || n <= 0 {
			render.FillRect(pixels, stride, b.DstX, b.DstY, b.DstW, b.DstH, outW, outH, PlaceholderColor)
			continue
		}
		render.Blit(pixels, stride, b.DstX, b.DstY, outW, outH, e.scratch[:n], sStride, sw, sh)
	}

	e.out.Damage(0, 0, outW, outH)
	_ = e.out.Present()
}
