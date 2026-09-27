package compositor

import (
	"fmt"
	"reflect"

	"bloxwm/internal/render/vulkan"
	"golang.org/x/sys/unix"
)

// vulkanExecutor materializes DrawList records as GPU draw calls.
// Client surfaces are fed via REAL DRM dumb buffers + PRIME export +
// ImportDmaBuf: the kernel moves content to the GPU, the Vulkan side just
// imports (the tested-green path). Chrome lands as color quads.
//
// It implements the same Executor interface as the CPU blitter — the
// frame loop is untouched by the GPU swap. Owned by the actor goroutine;
// its caches are single-owner.
type vulkanExecutor struct {
	dev    *vulkan.Device
	ren    *vulkan.Renderer
	target *vulkan.RenderTarget
	drmFD  int

	outW int32
	outH int32

	entries map[texKey]*texEntry
}

type texKey struct {
	clientID  uint32
	surfaceID uint32
}

// texEntry is one client surface's GPU-side backing: the dumb buffer
// (mapped, written per frame) plus its imported Vulkan view.
type texEntry struct {
	imported *vulkan.ImportedBuffer
	sb       *SurfaceDmaBuf
	w, h     uint32
}

// SurfaceDmaBuf is one client surface's kernel-side backing buffer.
type SurfaceDmaBuf struct {
	drmFD  int
	fd     int
	data   []byte
	Width  uint32
	Height uint32
	Stride uint32
}

// Close unmaps the dumb buffer and closes the exported fd.
func (s *SurfaceDmaBuf) Close() {
	if s.data != nil {
		_ = unix.Munmap(s.data)
		s.data = nil
	}
	_ = unix.Close(s.fd)
}

// write copies client pixels into the dumb buffer, converting stride.
func (s *SurfaceDmaBuf) write(src []byte, srcStride int32) {
	copyN := int(s.Stride)
	if copyN > int(srcStride) {
		copyN = int(srcStride)
	}
	for row := 0; row < int(s.Height); row++ {
		dstOff := row * int(s.Stride)
		srcOff := row * int(srcStride)
		if dstOff+copyN > len(s.data) || srcOff+copyN > len(src) {
			return
		}
		copy(s.data[dstOff:dstOff+copyN], src[srcOff:srcOff+copyN])
	}
}

// NewVulkanExecutor initializes the GPU executor: Vulkan device, renderer,
// offscreen target, and the DRM fd used for dumb-buffer allocation.
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

	drmFD := -1
	for _, p := range []string{"/dev/dri/card1", "/dev/dri/card0", "/dev/dri/card2"} {
		fd, oerr := unix.Open(p, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if oerr == nil {
			drmFD = fd
			break
		}
	}
	if drmFD < 0 {
		// The executor still works for chrome-only rendering.
		fmt.Println("[-] GPU executor: no DRM device; client blits disabled")
	}

	return &vulkanExecutor{
		dev:     dev,
		ren:     ren,
		target:  target,
		outW:    outW,
		outH:    outH,
		entries: make(map[texKey]*texEntry),
		drmFD:   drmFD,
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

// createDumbDmaBuf allocates a DRM dumb buffer and exports it to PRIME
// (same ioctls the vulkan package's tests exercise on live hardware).
func createDumbDmaBuf(drmFD int, width, height uint32) (fd int, pitch uint32, err error) {
	type drmModeCreateDumb struct {
		height, width, bpp, flags uint32
		handle, pitch             uint32
		size                      uint64
	}
	type drmPrimeHandle struct {
		handle, flags uint32
		fd            int32
	}
	const (
		ioctlCreateDumb = 0xc02064b2
		ioctlPrimeToFd  = 0xc00c642d
	)

	cd := drmModeCreateDumb{width: width, height: height, bpp: 32}
	if err := ioctl(drmFD, ioctlCreateDumb, unsafePtr(&cd)); err != nil {
		return -1, 0, err
	}
	pr := drmPrimeHandle{handle: cd.handle, fd: -1}
	if err := ioctl(drmFD, ioctlPrimeToFd, unsafePtr(&pr)); err != nil {
		return -1, 0, err
	}
	return int(pr.fd), pitch, nil
}

// uploadSurface mirrors fresh client content into a dumb buffer and
// imports it (or reuses the existing import at the same size).
func (e *vulkanExecutor) uploadSurface(clientID, surfaceID uint32, w, h uint32, copyFn CopyFn) *vulkan.ImportedBuffer {
	key := texKey{clientID, surfaceID}
	if entry, ok := e.entries[key]; ok {
		if entry.w == w && entry.h == h {
			return entry.imported
		}
		e.drop(key)
	}
	if e.drmFD < 0 {
		fmt.Printf("[-] gpu upload: no DRM fd\n")
		return nil
	}

	// Dumb buffer: the kernel maps this as GPU-backed scanout memory.
	cd := struct {
		height, width, bpp, flags uint32
		handle, pitch             uint32
		size                      uint64
	}{height: h, width: w, bpp: 32}
	if err := ioctl(e.drmFD, 0xc02064b2, unsafePtr(&cd)); err != nil {
		fmt.Printf("[-] gpu upload: dumb alloc failed: %v\n", err)
		return nil
	}
	pr := struct {
		handle, flags uint32
		fd            int32
	}{handle: cd.handle, flags: uint32(unix.O_RDWR | unix.O_CLOEXEC), fd: -1}
	if err := ioctl(e.drmFD, 0xc00c642d, unsafePtr(&pr)); err != nil {
		fmt.Printf("[-] gpu upload: prime export failed: %v\n", err)
		return nil
	}

	fd := int(pr.fd)
	data, err := unix.Mmap(fd, 0, int(cd.pitch)*int(h), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		fmt.Printf("[-] gpu upload: mmap failed: %v\n", err)
		_ = unix.Close(fd)
		return nil
	}
	sb := &SurfaceDmaBuf{drmFD: e.drmFD, fd: fd, data: data, Width: w, Height: h, Stride: cd.pitch}

	// Fetch fresh client pixels (client-stride layout) and convert.
	scratch := make([]byte, int(cd.pitch)*int(h))
	if n, ok := copyFn(clientID, surfaceID, scratch); !ok || n <= 0 {
		fmt.Printf("[-] gpu upload: no client pixels (n=%d ok=%v)\n", n, ok)
		sb.Close()
		return nil
	}
	sb.write(scratch, int32(cd.pitch))

	imported, err := e.dev.ImportDmaBuf(sb.fd, w, h, sb.Stride, 0)
	if err != nil {
		fmt.Printf("[-] gpu upload: import failed: %v\n", err)
		sb.Close()
		return nil
	}
	entry := &texEntry{imported: imported, sb: sb, w: w, h: h}
	e.entries[key] = entry
	return imported
}

func (e *vulkanExecutor) drop(key texKey) {
	if entry, ok := e.entries[key]; ok {
		e.dev.DestroyImportedBuffer(entry.imported)
		entry.sb.Close()
		delete(e.entries, key)
	}
}

// Execute renders the list on the GPU.
func (e *vulkanExecutor) Execute(dl *DrawList, avail AvailFn, copyFn CopyFn) {
	uploads := make([]*vulkan.ImportedBuffer, len(dl.Blits))
	for i := range dl.Blits {
		b := &dl.Blits[i]
		sw, sh, _, ok := avail(b.ClientID, b.SurfaceID)
		if !ok || sw <= 0 || sh <= 0 {
			continue
		}
		uploads[i] = e.uploadSurface(b.ClientID, b.SurfaceID, uint32(sw), uint32(sh), copyFn)
	}

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
		if uploads[i] != nil {
			e.ren.DrawTextureQuad(uploads[i],
				float32(b.DstX), float32(b.DstY), float32(b.DstW), float32(b.DstH),
				0, 0, 1, 1, 1.0)
		}
	}

	if err := e.ren.EndFrame(); err != nil {
		return
	}
	_ = e.ren.WaitFrame(1000000000)
}

// BridgeTo copies the GPU-composited frame into a CPU Output's pixels
// (stride-aware), then damages + presents it. Phase-3 bridge; KMS scanout
// (phase 4) presents the Vulkan target directly.
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

// copyRows copies h rows of srcStride bytes from src into a dst with its
// own stride.
func copyRows(dst []byte, dstStride, srcStride, h int, src []byte) {
	for row := 0; row < h; row++ {
		off := row * dstStride
		if off+srcStride > len(dst) || (row+1)*srcStride > len(src) {
			return
		}
		copy(dst[off:off+srcStride], src[row*srcStride:(row+1)*srcStride])
	}
}

// Close releases GPU resources.
func (e *vulkanExecutor) Close() {
	for k := range e.entries {
		e.drop(k)
	}
	e.ren.DestroyTarget(e.target)
	e.ren.Close()
	e.dev.Close()
}

// ioctl issues a raw DRM ioctl (mirrors the vulkan test helpers).
func ioctl(fd int, req uint32, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), arg)
	if errno != 0 {
		return errno
	}
	return nil
}

// unsafePtr returns the address of p's data for DRM ioctl arguments.
func unsafePtr(p interface{}) uintptr {
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Ptr {
		panic("ioctl arg must be a pointer")
	}
	return v.Pointer()
}

// readPixel extracts a BGRA pixel for assertions (test use only; kept
// here so the executor and tests share one implementation).
func readPixelFrom(data []byte, stride int32, x, y int32) uint32 {
	off := y*int32(stride) + x*4
	if int(off)+4 > len(data) {
		return 0
	}
	return uint32(data[off]) | uint32(data[off+1])<<8 | uint32(data[off+2])<<16 | uint32(data[off+3])<<24
}

// BridgeExecutor is the shell-side wiring: composites on the GPU, then
// bridges into the CPU output during phase 3 (KMS scanout removes this).
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
