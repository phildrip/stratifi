package spikevk

import (
	"testing"
)

// openQuad is the textured-crate fixture: logical device plus a small target
// and the full textured pipeline over it.
func openQuad(t *testing.T, w, h uint32) (*GraphicsContext, *GpuFrame, *QuadRenderer, func()) {
	t.Helper()
	loader, err := AcquireLoader()
	if err != nil {
		t.Skipf("no vulkan loader: %v", err)
	}
	ctx, err := OpenInstance(loader)
	if err != nil {
		t.Skipf("no vulkan device: %v", err)
	}
	frame, err := NewGpuFrame(ctx, w, h)
	if err != nil {
		ctx.Close()
		t.Fatalf("NewGpuFrame: %v", err)
	}
	quad, err := NewQuadRenderer(frame)
	if err != nil {
		frame.Close()
		ctx.Close()
		t.Fatalf("NewQuadRenderer: %v", err)
	}
	return ctx, frame, quad, func() {
		frame.Close()
		ctx.Close()
	}
}

func solidPixels(w, h uint32, b, g, r, a byte) []byte {
	pix := make([]byte, w*h*4)
	fillSolid(pix, b, g, r, a)
	return pix
}

// TestQuadTwoTexturesVisible is the descriptor-aliasing regression at the Go
// level: two different textures drawn in one frame must each appear where
// drawn. Before the ring fix both tiles sampled the last-updated image.
func TestQuadTwoTexturesVisible(t *testing.T) {
	_, frame, quad, done := openQuad(t, 64, 32)
	defer done()
	defer quad.Close()

	red, err := quad.UploadTexture(8, 8, solidPixels(8, 8, 0x00, 0x00, 0xFF, 0x00))
	if err != nil {
		t.Fatalf("upload red: %v", err)
	}
	green, err := quad.UploadTexture(8, 8, solidPixels(8, 8, 0x00, 0xFF, 0x00, 0x00))
	if err != nil {
		t.Fatalf("upload green: %v", err)
	}
	defer quad.DestroyTexture(red)
	defer quad.DestroyTexture(green)

	pixels, _, err := quad.Render(
		[4]float32{0, 0, 0, 1},
		[]QuadDraw{
			{Tex: red, Dst: [4]float32{0, 0, 32, 32}, Src: [4]float32{0, 0, 1, 1}, Alpha: 1},
			{Tex: green, Dst: [4]float32{32, 0, 32, 32}, Src: [4]float32{0, 0, 1, 1}, Alpha: 1},
		},
		64, 32,
	)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	_ = frame

	at := func(x, y uint32) []byte {
		off := y*64*4 + x*4
		return pixels[off : off+4]
	}
	left, right := at(8, 8), at(40, 8)
	if left[0] != 0x00 || left[1] != 0x00 || left[2] != 0xFF {
		t.Errorf("left = % x, want red (B0 G0 R255)", left)
	}
	if right[0] != 0x00 || right[1] != 0xFF || right[2] != 0x00 {
		t.Errorf("right = % x, want green (B0 G255 R0)", right)
	}
}

// TestQuadUploadRoundtrip: a texture's pixels survive staging → image →
// shader → framebuffer. Catches silent upload corruption (wrong tiling,
// missing barrier, bad sampler) with exact bytes.
func TestQuadUploadRoundtrip(t *testing.T) {
	_, _, quad, done := openQuad(t, 32, 32)
	defer done()
	defer quad.Close()

	tex, err := quad.UploadTexture(8, 8, solidPixels(8, 8, 0x12, 0x34, 0x56, 0xFF))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer quad.DestroyTexture(tex)
	pixels, _, err := quad.Render(
		[4]float32{0, 0, 0, 1},
		[]QuadDraw{
			{Tex: tex, Dst: [4]float32{0, 0, 32, 32}, Src: [4]float32{0, 0, 1, 1}, Alpha: 1},
		},
		32, 32,
	)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	off := 16*32*4 + 16*4
	got := pixels[off : off+4]
	// B and G pass through the opaque-forcing shader untouched; R likewise.
	if got[0] != 0x12 || got[1] != 0x34 || got[2] != 0x56 {
		t.Errorf("centre = % x, want 12 34 56 (upload or sampling corrupt)", got)
	}
}

// fillSolid writes a solid BGRA colour over the buffer.
func fillSolid(pix []byte, b, g, r, a byte) {
	for i := 0; i < len(pix); i += 4 {
		pix[i+0] = b
		pix[i+1] = g
		pix[i+2] = r
		pix[i+3] = a
	}
}
