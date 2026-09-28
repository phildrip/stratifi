package spikevk

import (
	"testing"
)

// TestOpenInstanceOnLiveHardware is the spike checkpoint: resolve the loader
// at runtime, create an instance, enumerate physical devices and read the
// device name through ABI-transcribed structs. Everything above this slice
// is the same discipline repeated.
func TestOpenInstanceOnLiveHardware(t *testing.T) {
	loader, err := AcquireLoader()
	if err != nil {
		t.Skipf("no vulkan loader: %v", err)
	}
	ctx, err := OpenInstance(loader)
	if err != nil {
		t.Skipf("no vulkan device (loader present, ICD absent?): %v", err)
	}
	if ctx.DeviceName == "" {
		t.Fatal("device name empty: struct transcription suspect")
	}
	t.Logf("instance=%#x physical=%#x device=%q", ctx.Instance, ctx.Physical, ctx.DeviceName)

	// The C core prints the same name; both must agree that a device exists.
	if ctx.Physical == 0 {
		t.Fatal("physical device handle is zero")
	}

	ctx.Close()
	if ctx.Instance != 0 {
		t.Fatal("instance not cleared on close")
	}
}
