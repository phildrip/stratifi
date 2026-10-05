package vulkan

import (
	"testing"

	"bloxwm/internal/backend/kms"
)

// TestImportScanoutAsTarget proves the zero-copy scanout path: a KMS dumb
// buffer (linear) prime-exported and imported as a COLOR_ATTACHMENT image.
// Neither allocation nor export needs DRM master, so this runs under sway.
func TestImportScanoutAsTarget(t *testing.T) {
	card, err := kms.OpenCard()
	if err != nil {
		t.Skipf("no DRM card: %v", err)
	}
	defer card.Close()

	so, err := card.CreateScanout(64, 64)
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

	// Dumb buffers are linear: DRM_FORMAT_MOD_LINEAR (0).
	imported, err := dev.ImportDmaBufAsTarget(fd, 64, 64, so.Pitch, 0)
	if err != nil {
		t.Fatalf("ImportDmaBufAsTarget (linear renderable): %v", err)
	}
	defer dev.DestroyImportedBuffer(imported)

	if imported.Width != 64 || imported.Height != 64 {
		t.Errorf("imported size = %dx%d, want 64x64", imported.Width, imported.Height)
	}
}
