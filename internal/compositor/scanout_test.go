package compositor

import (
	"testing"

	"stratifi/internal/backend/kms"
)

// fakeInner records Execute calls for the ScanoutExecutor test.
type fakeInner struct {
	calls int
}

func (f *fakeInner) Execute(dl *DrawList, avail AvailFn, copyFn CopyFn) {
	f.calls++
}

// TestScanoutExecutorFlips: the scanout executor must run the inner GPU
// execute and then flip, every frame, with no bridge/readback involved.
func TestScanoutExecutorFlips(t *testing.T) {
	inner := &fakeInner{}
	flips := 0
	ex := ScanoutExecutor{Inner: inner, Flip: func() error { flips++; return nil }}

	dl := NewDrawList()
	ex.Execute(dl, nil, nil)
	ex.Execute(dl, nil, nil)

	if inner.calls != 2 {
		t.Errorf("inner Execute calls = %d, want 2", inner.calls)
	}
	if flips != 2 {
		t.Errorf("flips = %d, want 2 (one per frame)", flips)
	}
}

// TestScanoutExecutorNilFlip: a nil Flip must not panic (degraded present).
func TestScanoutExecutorNilFlip(t *testing.T) {
	inner := &fakeInner{}
	ex := ScanoutExecutor{Inner: inner}
	ex.Execute(NewDrawList(), nil, nil)
	if inner.calls != 1 {
		t.Errorf("inner Execute calls = %d, want 1", inner.calls)
	}
}

// TestVulkanExecutorOnScanoutDmaBuf: build a GPU executor around a real KMS
// scanout dma-buf (no master needed) and render a frame into it.
func TestVulkanExecutorOnScanoutDmaBuf(t *testing.T) {
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

	exe, err := NewVulkanExecutorOnDmaBuf(fd, 64, 64, so.Pitch, 64, 64)
	if err != nil {
		t.Skipf("scanout render target unsupported here: %v", err)
	}
	defer exe.Close()

	// Render one frame straight into scanout memory.
	dl := NewDrawList()
	scene := []SceneWindow{
		{ClientID: 1, SurfaceID: 1, Rect: Rect{X: 0, Y: 0, W: 64, H: 64}, Active: false},
	}
	dl.Build(scene, FrameConfig{BorderWidth: 0, TitlebarHeight: 0}, 64, 64, 0)
	exe.Execute(dl,
		func(c, s uint32) (int32, int32, int32, bool) { return 0, 0, 0, false },
		func(c, s uint32, dst []byte) (int, bool) { return 0, false },
	)

	// The frame must have landed in the dumb buffer (background clear).
	// Background is dark (0.09); any non-zero pixel proves the GPU wrote it.
	nonzero := 0
	for i := 0; i < len(so.Data); i += 4 {
		if so.Data[i] != 0 || so.Data[i+1] != 0 || so.Data[i+2] != 0 {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Error("scanout dumb buffer untouched: GPU did not render into it")
	}
}
