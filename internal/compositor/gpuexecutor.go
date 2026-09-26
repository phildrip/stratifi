package compositor

import (
	"fmt"
	"sync"

	"bloxwm/internal/render/vulkan"
)

// vulkanExecutor materializes DrawList records as GPU draw calls:
// quads → vk.DrawColorQuad, blits → cached per-surface CPU uploads →
// vk.DrawTextureQuad. It implements the same Executor interface as the
// CPU blitter — the frame loop is untouched by the GPU swap.
//
// Owned by the renderer goroutine (actor): its texture cache and the
// Vulkan command recording are single-owner, no locking beyond the
// vulkan package's internal locks.
type vulkanExecutor struct {
	dev    *vulkan.Device
	ren    *vulkan.Renderer
	target *vulkan.RenderTarget
	outW   int32
	outH   int32

	mu       sync.Mutex
	textures map[texKey]*vulkan.Texture
}

// Ensure the texture cache exists (lazily initialized by the owner).
func (e *vulkanExecutor) texFor(k texKey) *vulkan.Texture {
	return e.textures[k]
}

func (e *vulkanExecutor) texSet(k texKey, t *vulkan.Texture) {
	if e.textures == nil {
		e.textures = make(map[texKey]*vulkan.Texture)
	}
	e.textures[k] = t
}

func (e *vulkanExecutor) texDrop(k texKey) {
	if e.textures != nil {
		delete(e.textures, k)
	}
}

type texKey struct {
	clientID  uint32
	surfaceID uint32
}

// NewVulkanExecutor initializes the GPU executor: a Vulkan device, a
// renderer, and an offscreen render target at the output size.
func NewVulkanExecutor(outW, outH int32) (*vulkanExecutor, error) {
	dev, err := vulkan.NewDevice()
	if err != nil {
		return nil, fmt.Errorf("vulkan executor: %w", err)
	}
	ren, err := vulkan.NewRenderer(dev)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("vulkan executor: %w", err)
	}
	target, err := ren.CreateTarget(uint32(outW), uint32(outH))
	if err != nil {
		ren.Close()
		dev.Close()
		return nil, fmt.Errorf("vulkan executor: %w", err)
	}
	return &vulkanExecutor{
		dev:    dev,
		ren:    ren,
		target: target,
		outW:   outW,
		outH:   outH,
	}, nil
}

// colorToFloats converts ARGB8888 to normalized RGBA floats.
func colorToFloats(c uint32) (r, g, b, a float32) {
	const s = 1.0 / 255.0
	b = float32(c&0xFF) * s
	g = float32((c>>8)&0xFF) * s
	r = float32((c>>16)&0xFF) * s
	a = float32((c>>24)&0xFF) * s
	return
}

// Execute renders the list on the GPU. The chrome lands as color quads;
// each blit uploads (or re-uses) its surface texture.
func (e *vulkanExecutor) Execute(dl *DrawList, avail AvailFn, copyFn CopyFn) {
	e.ren.BeginFrame(e.target, 0.09, 0.09, 0.11, 1.0)

	for i := range dl.Quads {
		q := &dl.Quads[i]
		if q.W <= 0 || q.H <= 0 {
			continue
		}
		r, g, b, a := colorToFloats(q.Color)
		e.ren.DrawColorQuad(float32(q.X), float32(q.Y), float32(q.W), float32(q.H), r, g, b, a)
	}

	for i := range dl.Blits {
		b := &dl.Blits[i]
		sw, sh, sStride, ok := avail(b.ClientID, b.SurfaceID)
		if !ok || sw <= 0 || sh <= 0 || sStride <= 0 {
			continue
		}

		tex := e.texFor(texKey{b.ClientID, b.SurfaceID})
		// (Re)upload when missing or resized; commit-driven re-upload is
		// handled by the shell requesting a render after commit, and the
		// executor re-uploads every frame for correctness in v1 (zero-
		// reupload caching keyed on size+stride comes with damage tracking).
		if tex == nil || tex.Width != uint32(sw) || tex.Height != uint32(sh) || tex.Stride != uint32(sStride) {
			if old := e.texFor(texKey{b.ClientID, b.SurfaceID}); old != nil {
				e.ren.DestroyTexture(old)
			}
			if need := int(sStride) * int(sh); need > 0 {
				scratch := make([]byte, need)
				if n, ok := copyFn(b.ClientID, b.SurfaceID, scratch); ok && n > 0 {
					tex, err := e.ren.UploadTexture(uint32(sw), uint32(sh), uint32(sStride), scratch)
					if err != nil {
						continue
					}
					e.texSet(texKey{b.ClientID, b.SurfaceID}, tex)
				}
			}
		}

		if tex := e.texFor(texKey{b.ClientID, b.SurfaceID}); tex != nil {
			e.ren.DrawTextureView(tex,
				float32(b.DstX), float32(b.DstY), float32(b.DstW), float32(b.DstH),
				1.0)
		}
	}

	if err := e.ren.EndFrame(); err != nil {
		return
	}
	_ = e.ren.WaitFrame(1000000000)
}

// BridgeTo copies the GPU-composited frame into a CPU Output's pixels
// (stride-aware), then damages + presents it. This is the phase-3 bridge
// while the presentation path is still the parent Wayland shm surface;
// KMS scanout (phase 4) presents the Vulkan target directly.
func (e *vulkanExecutor) BridgeTo(out Output) {
	gpu, ok := e.ren.ReadbackTarget(e.target)
	if !ok || len(gpu) == 0 {
		return
	}
	dst := out.Pixels()
	dstStride := int(out.Stride())
	copyRows(dst, dstStride, int(e.outW*4), int(e.outH), gpu)
	out.Damage(0, 0, e.outW, e.outH)
	_ = out.Present()
}

// copyRows copies h rows of srcW bytes from src to a wider-stride dst.
func copyRows(dst []byte, dstStride, srcStride, h int, src []byte) {
	for row := 0; row < h; row++ {
		off := row * dstStride
		n := srcStride
		if int(off)+n > len(dst) || (row+1)*srcStride > len(src) {
			return
		}
		copy(dst[off:off+n], src[row*srcStride:(row+1)*srcStride])
	}
}

// Close releases GPU resources.
func (e *vulkanExecutor) Close() {
	e.ren.DestroyTarget(e.target)
	e.ren.Close()
	e.dev.Close()
}

// BridgeExecutor composites on the GPU, then bridges into the CPU output.
// Implements Executor.
type BridgeExecutor struct {
	Inner Executor
	Out   Output
}

func (b BridgeExecutor) Execute(dl *DrawList, avail AvailFn, copyFn CopyFn) {
	if gpu, ok := b.Inner.(*vulkanExecutor); ok {
		gpu.Execute(dl, avail, copyFn)
		gpu.BridgeTo(b.Out)
		return
	}
	b.Inner.Execute(dl, avail, copyFn)
}
