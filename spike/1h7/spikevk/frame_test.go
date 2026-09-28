package spikevk

import (
	"testing"
	"time"
)

// openGraphics is the shared fixture: loader + logical device + queue.
func openGraphics(t *testing.T) (*GraphicsContext, func()) {
	t.Helper()
	loader, err := AcquireLoader()
	if err != nil {
		t.Skipf("no vulkan loader: %v", err)
	}
	ctx, err := OpenInstance(loader)
	if err != nil {
		t.Skipf("no vulkan device (ICD absent?): %v", err)
	}
	return ctx, func() { ctx.Close() }
}

// TestFrameClearCopyReadback is the frame-slice milestone: in pure Go, build
// a target, clear it to solid red, copy to a mapped staging buffer, and read
// it back. Endpoints (1.0, 0.0) are fixed points of the sRGB encode, so the
// expected BGRA bytes are exact: (B0, G0, R255, A255).
func TestFrameClearCopyReadback(t *testing.T) {
	ctx, done := openGraphics(t)
	defer done()

	frame, err := NewGpuFrame(ctx, 8, 8)
	if err != nil {
		t.Fatalf("NewGpuFrame: %v", err)
	}
	defer frame.Close()

	pixels, elapsed, err := frame.Render(1.0, 0.0, 0.0, 1.0)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	t.Logf("8x8 pure-Go frame: %v", elapsed)

	want := [4]byte{0x00, 0x00, 0xFF, 0xFF}
	for i := 0; i < len(pixels); i += 4 {
		got := [4]byte{pixels[i], pixels[i+1], pixels[i+2], pixels[i+3]}
		if got != want {
			t.Fatalf("pixel %d = % x, want % x (clear/copy/readback broken)", i/4, got, want)
		}
	}
}

// TestFrameRepeatedRenders exercises the fence discipline the C core once
// violated: wait → reset → record → submit → wait, across frames. A fence
// left unsignalled (the readback-target bug) would time out here.
func TestFrameRepeatedRenders(t *testing.T) {
	ctx, done := openGraphics(t)
	defer done()

	frame, err := NewGpuFrame(ctx, 16, 16)
	if err != nil {
		t.Fatalf("NewGpuFrame: %v", err)
	}
	defer frame.Close()

	var total time.Duration
	const frames = 20
	for i := 0; i < frames; i++ {
		_, elapsed, err := frame.Render(0.0, 1.0, 0.0, 1.0)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		total += elapsed
	}
	t.Logf("%d frames of 16x16: %v total, %v/frame", frames, total, total/frames)
}

// TestFrameCompositorSize is the timing number the bead asks for: one full
// pure-Go frame at the compositor's production resolution.
func TestFrameCompositorSize(t *testing.T) {
	ctx, done := openGraphics(t)
	defer done()

	frame, err := NewGpuFrame(ctx, 1280, 720)
	if err != nil {
		t.Fatalf("NewGpuFrame: %v", err)
	}
	defer frame.Close()

	pixels, elapsed, err := frame.Render(0.09, 0.09, 0.11, 1.0)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(pixels) != 1280*720*4 {
		t.Fatalf("readback size = %d, want %d", len(pixels), 1280*720*4)
	}
	t.Logf("1280x720 pure-Go frame (clear+copy+readback): %v", elapsed)
}

// TestFallbackAbsentLoader is the acceptance half of bloxwm-1h7: with no
// loader present, resolution fails cleanly and the binary is alive to take
// the CPU path. loaderFrom is the same code path with injected names.
func TestFallbackAbsentLoader(t *testing.T) {
	if _, err := loaderFrom([]string{"libvulkan.so.999"}); err == nil {
		t.Fatal("expected an error resolving a nonexistent loader")
	}
	// ...and the real loader still resolves afterwards (no poisoned state).
	if _, err := AcquireLoader(); err != nil {
		t.Skipf("no real loader on this machine: %v", err)
	}
}

// TestMissingSymbolIsNamedError: resolving a symbol the loader does not know
// must produce an error carrying the NAME — the Go equivalent of a symbol
// the C core could only find at build time.
func TestMissingSymbolIsNamedError(t *testing.T) {
	loader, err := AcquireLoader()
	if err != nil {
		t.Skipf("no vulkan loader: %v", err)
	}
	ctx, err := OpenInstance(loader)
	if err != nil {
		t.Skipf("no device: %v", err)
	}
	defer ctx.Close()
	if p := loader.Resolve(ctx.Instance, "vkNoSuchFunctionXYZ"); p != nil {
		t.Fatal("expected nil for an unknown symbol")
	}
	if err := errMissing("vkNoSuchFunctionXYZ"); err.Error() != "vulkan entry point not resolvable at runtime: vkNoSuchFunctionXYZ" {
		t.Fatalf("error text changed: %q", err)
	}
}
