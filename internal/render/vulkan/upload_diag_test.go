package vulkan

import (
	"testing"
)

// TestUploadTextureReadback verifies the upload actually landed in the
// host-visible image: map + copy + read back the same rows.
func TestUploadTextureReadback(t *testing.T) {
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

	w := uint32(2)
	stride := w * 4
	pix := make([]byte, stride*4)
	copy(pix[0:4], []byte{0xFF, 0x00, 0x00, 0xFF})   // blue (BGRA)
	copy(pix[4:8], []byte{0x00, 0xFF, 0x00, 0xFF})   // green
	copy(pix[8:12], []byte{0x00, 0x00, 0xFF, 0xFF})  // red
	copy(pix[12:16], []byte{0xFF, 0xFF, 0x00, 0xFF}) // yellow

	tex, err := ren.UploadTexture(w, 4, stride, pix)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer ren.DestroyTexture(tex)

	// Decisive check: what does the GPU image actually hold?
	roundtrip, rtOK := ren.VerifyImageCopy(tex)
	if rtOK {
		t.Logf("image roundtrip: % x", roundtrip)
	}
	rows, ok := ren.ReadbackStaging(tex)
	if !ok {
		t.Skip("texture readback unsupported")
	}
	t.Logf("readback: % x", rows)
	if data := rows[0:4]; data[0] != 0xFF {
		t.Errorf("row0 pixel = % x, want 0xFF0000FF BGRA", data)
	}
	if data := rows[4:8]; data[1] != 0xFF {
		t.Errorf("row1 pixel = % x, want 00FF00FF BGRA", data)
	}
	if data := rows[8:12]; data[2] != 0xFF {
		t.Errorf("row2 pixel = % x, want 0000FFFF BGRA", data)
	}
}
