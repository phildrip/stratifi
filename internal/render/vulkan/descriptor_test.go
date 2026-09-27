package vulkan

import (
	"testing"

	"golang.org/x/sys/unix"
)

// fillSolid writes a solid BGRA colour over an RGBA-strided pixel buffer.
func fillSolid(pix []byte, b, g, r, a byte) {
	for i := 0; i < len(pix); i += 4 {
		pix[i+0] = b
		pix[i+1] = g
		pix[i+2] = r
		pix[i+3] = a
	}
}

func newTestRenderer(t *testing.T, w, h uint32) (*Renderer, *RenderTarget) {
	t.Helper()
	dev, err := NewDevice()
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	t.Cleanup(func() { _ = ren.Close() })

	target, err := ren.CreateTarget(w, h)
	if err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	t.Cleanup(func() { ren.DestroyTarget(target) })
	return ren, target
}

func readCenter(t *testing.T, ren *Renderer, target *RenderTarget, x, y uint32) []byte {
	t.Helper()
	if err := ren.WaitFrame(1000000000); err != nil {
		t.Fatalf("wait: %v", err)
	}
	out, ok := ren.ReadbackTarget(target)
	if !ok {
		t.Fatal("readback failed")
	}
	off := y*target.Width*4 + x*4
	return out[off : off+4]
}

// TestTwoTexturesVisibleInOneFrame pins the descriptor invariant behind every
// texture draw: descriptor writes are HOST operations, so sharing one set
// across draws makes every quad sample whichever image was written last. The
// ring must give each draw its own set. Exercises both texture paths (DMA-BUF
// view and CPU upload) so any future "reuse one set" change breaks here.
func TestTwoTexturesVisibleInOneFrame(t *testing.T) {
	ren, target := newTestRenderer(t, 64, 32)

	mk := func(b, g, r, a byte) *Texture {
		pix := make([]byte, 8*4*8)
		fillSolid(pix, b, g, r, a)
		tex, err := ren.UploadTexture(8, 8, 8*4, pix)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		return tex
	}
	red := mk(0x00, 0x00, 0xFF, 0xFF)
	blue := mk(0xFF, 0x00, 0x00, 0xFF)
	defer ren.DestroyTexture(red)
	defer ren.DestroyTexture(blue)

	if err := ren.BeginFrame(target, 0, 0, 0, 1); err != nil {
		t.Fatalf("begin: %v", err)
	}
	ren.DrawTextureView(red, 0, 0, 32, 32, 1.0)
	ren.DrawTextureView(blue, 32, 0, 32, 32, 1.0)
	if err := ren.EndFrame(); err != nil {
		t.Fatalf("end: %v", err)
	}

	left := readCenter(t, ren, target, 8, 8)
	right := readCenter(t, ren, target, 40, 8)
	if left[0] != 0x00 || left[2] != 0xFF {
		t.Errorf("left = % x, want red (B0 R255) — sampling the last-updated image?", left)
	}
	if right[0] != 0xFF || right[2] != 0x00 {
		t.Errorf("right = % x, want blue (B255 R0) — sampling the last-updated image?", right)
	}
}

// TestImportedTexturesVisibleInOneFrame is the same invariant through the
// DMA-BUF path the compositor uses, with two imported buffers in one frame.
func TestImportedTexturesVisibleInOneFrame(t *testing.T) {
	ren, target := newTestRenderer(t, 64, 32)

	red, _ := importSolid(t, 0x00, 0x00, 0xFF, 0xFF)
	blue, _ := importSolid(t, 0xFF, 0x00, 0x00, 0xFF)

	if err := ren.BeginFrame(target, 0, 0, 0, 1); err != nil {
		t.Fatalf("begin: %v", err)
	}
	ren.DrawTextureQuad(red, 0, 0, 32, 32, 0, 0, 1, 1, 1.0)
	ren.DrawTextureQuad(blue, 32, 0, 32, 32, 0, 0, 1, 1, 1.0)
	if err := ren.EndFrame(); err != nil {
		t.Fatalf("end: %v", err)
	}

	left := readCenter(t, ren, target, 8, 8)
	right := readCenter(t, ren, target, 40, 8)
	if left[0] != 0x00 || left[2] != 0xFF {
		t.Errorf("left = % x, want red (B0 R255) — sampling the last-updated image?", left)
	}
	if right[0] != 0xFF || right[2] != 0x00 {
		t.Errorf("right = % x, want blue (B255 R0) — sampling the last-updated image?", right)
	}
}

// importSolid allocates a real dumb buffer through the DRM device, fills it
// with a solid colour and imports it — the compositor's own texture path.
func importSolid(t *testing.T, b, g, r, a byte) (*ImportedBuffer, *Device) {
	t.Helper()
	drmFD, err := openDrmDevice()
	if err != nil {
		t.Skipf("no DRM device: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(drmFD) })

	const w, h = 8, 8
	dmaFD, pitch, err := createDrmDmaBuf(drmFD, w, h)
	if err != nil {
		t.Skipf("dumb buffer / PRIME unsupported: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(dmaFD) })

	data, err := unix.Mmap(dmaFD, 0, int(pitch)*h, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	t.Cleanup(func() { _ = unix.Munmap(data) })
	fillSolid(data, b, g, r, a)

	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	t.Cleanup(func() { dev.Close() })
	imported, err := dev.ImportDmaBuf(dmaFD, w, h, pitch, 0)
	if err != nil {
		t.Fatalf("ImportDmaBuf: %v", err)
	}
	t.Cleanup(func() { dev.DestroyImportedBuffer(imported) })
	return imported, dev
}
