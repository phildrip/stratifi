package kms

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestPrimeExportNoMaster: a dumb buffer's PRIME fd must be obtainable
// without DRM master (allocation and export are not modesetting). The fd is
// what the Vulkan importer consumes for zero-copy scanout.
func TestPrimeExportNoMaster(t *testing.T) {
	card := openTestCard(t)

	so, err := card.CreateScanout(64, 64)
	if err != nil {
		t.Skipf("scanout needs more privilege here: %v", err)
	}
	defer so.Close()

	if so.Handle == 0 {
		t.Fatal("scanout has zero GEM handle")
	}
	fd, err := card.PrimeExport(so.Handle)
	if err != nil {
		t.Fatalf("PrimeExport: %v", err)
	}
	defer unix.Close(fd)
	if fd < 0 {
		t.Fatal("prime fd negative")
	}

	// The fd must be a real dma-buf: fstat it.
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		t.Fatalf("fstat prime fd: %v", err)
	}
	if st.Size == 0 && so.Size == 0 {
		t.Error("both fd and scanout report zero size")
	}
}
