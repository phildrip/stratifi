package vulkan

import (
	"testing"
)

// TestUploadTextureLifecycle drives the CPU-upload texture path on live
// hardware: create device+renderer, upload a small pattern, destroy.
// Full pixel verification comes with the compositor GPU path; this pins
// command-buffer correctness (upload + layout transition must succeed).
func TestUploadTextureLifecycle(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	defer ren.Close()

	tex, err := ren.UploadTexture(8, 4, 8*4, make([]byte, 8*4*4))
	if err != nil {
		t.Fatalf("UploadTexture: %v", err)
	}
	if tex.Width != 8 || tex.Height != 4 {
		t.Errorf("texture dims = %dx%d", tex.Width, tex.Height)
	}
	ren.DestroyTexture(tex)

	// Re-upload over the same slot (cache replacement semantics).
	tex2, err := ren.UploadTexture(16, 16, 16*4, make([]byte, 16*16*4))
	if err != nil {
		t.Fatalf("UploadTexture (replace): %v", err)
	}
	defer ren.DestroyTexture(tex2)
}

// TestUploadTextureMultiple exercises several concurrent-ish texture
// uploads (the executor uploads per-surface).
func TestUploadTextureMultiple(t *testing.T) {
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

	var textures []*Texture
	for i := 0; i < 4; i++ {
		tex, err := ren.UploadTexture(uint32(16+i), 8, uint32(16+i)*4, make([]byte, (16+i)*8*4))
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		textures = append(textures, tex)
	}
	for _, tex := range textures {
		ren.DestroyTexture(tex)
	}
}
