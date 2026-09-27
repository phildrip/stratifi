package vulkan

import "testing"

// TestUploadRenderMinimal is the minimal sampling path: one texture quad
// covering the whole target, red pixels, readback must be red.
func TestUploadRenderMinimal(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer ren.Close()

	target, err := ren.CreateTarget(16, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer ren.DestroyTarget(target)

	stride := uint32(16 * 4)
	pix := make([]byte, stride*16)
	for i := 0; i < len(pix); i += 4 {
		copy(pix[i:i+4], []byte{0xFF, 0x00, 0x00, 0xFF}) // BGRA blue
	}

	tex, err := ren.UploadTexture(16, 16, stride, pix)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer ren.DestroyTexture(tex)

	if err := ren.BeginFrame(target, 0, 0, 0, 1); err != nil {
		t.Fatalf("begin: %v", err)
	}
	ren.DrawColorQuad(0, 0, 16, 16, 0, 0, 1, 1) // solid blue quad
	ren.DrawTextureView(tex, 0, 0, 16, 16, 1.0)
	if err := ren.EndFrame(); err != nil {
		t.Fatalf("end: %v", err)
	}
	if err := ren.WaitFrame(1000000000); err != nil {
		t.Fatalf("wait: %v", err)
	}

	out, ok := ren.ReadbackTarget(target)
	if !ok {
		t.Fatal("readback failed")
	}
	off := 9*16*4 + 9*4
	t.Logf("center pixel = % x", out[off:off+4])
	// The opaque-forcing shader writes alpha=1.0 regardless of the
	// texture's alpha byte.
	if p := out[off : off+4]; p[0] != 0xFF {
		t.Errorf("center = % x, want blue (B=0xFF, A=1.0)", p)
	}
}
