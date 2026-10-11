package vulkan

import (
	"testing"

	"stratifi/internal/backend/kms"
)

// TestRenderToScanoutBuffer proves zero-copy scanout: Vulkan renders a frame
// straight into a KMS dumb buffer (imported as the render target), and the
// pixels are visible through the dumb buffer's CPU mapping — no readback, no
// master required.
func TestRenderToScanoutBuffer(t *testing.T) {
	card, err := kms.OpenCard()
	if err != nil {
		t.Skipf("no DRM card: %v", err)
	}
	defer card.Close()

	const w, h = 64, 64
	so, err := card.CreateScanout(w, h)
	if err != nil {
		t.Skipf("scanout unavailable here: %v", err)
	}
	defer so.Close()

	fd, err := card.PrimeExport(so.Handle)
	if err != nil {
		t.Fatalf("PrimeExport: %v", err)
	}

	dev, err := NewDevice()
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	defer dev.Close()

	imported, err := dev.ImportDmaBufAsTarget(fd, w, h, so.Pitch, 0)
	if err != nil {
		t.Fatalf("ImportDmaBufAsTarget: %v", err)
	}
	defer dev.DestroyImportedBuffer(imported)

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	defer ren.Close()

	target, err := ren.CreateScanoutTarget(imported, w, h)
	if err != nil {
		t.Fatalf("CreateScanoutTarget: %v", err)
	}
	defer ren.DestroyTarget(target)

	// Fill the scanout black, then draw a red quad over the centre.
	if err := ren.BeginFrame(target, 0, 0, 0, 1); err != nil {
		t.Fatalf("BeginFrame: %v", err)
	}
	ren.DrawColorQuad(16, 16, 32, 32, 1, 0, 0, 1)
	if err := ren.EndFrame(); err != nil {
		t.Fatalf("EndFrame: %v", err)
	}
	if err := ren.WaitFrame(1000000000); err != nil {
		t.Fatalf("WaitFrame: %v", err)
	}

	// Read back through the KMS CPU mapping (pitch-aware).
	at := func(x, y uint32) (b, g, r, a byte) {
		off := int(y)*int(so.Pitch) + int(x)*4
		return so.Data[off], so.Data[off+1], so.Data[off+2], so.Data[off+3]
	}
	if b, g, r, _ := at(4, 4); b != 0 || g != 0 || r != 0 {
		t.Errorf("corner = (%d,%d,%d), want black background", b, g, r)
	}
	if b, g, r, _ := at(32, 32); b != 0 || g != 0 || r != 0xFF {
		t.Errorf("centre = (%d,%d,%d), want red quad (B0 G0 R255)", b, g, r)
	}
}
