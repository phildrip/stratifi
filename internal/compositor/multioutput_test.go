package compositor

import "testing"

func TestMultiOutputJoinAndMirror(t *testing.T) {
	out1 := NewMemOutput(64, 32)
	out2 := NewMemOutput(64, 32)

	// 1. Join policy: 128x32 combined canvas split across out1 (0..64) and out2 (64..128).
	joined := NewMultiOutput([]*MemOutput{out1, out2}, false)
	if joined.Width() != 128 || joined.Height() != 32 {
		t.Fatalf("joined size = %dx%d, want 128x32", joined.Width(), joined.Height())
	}
	// Write a red pixel at x=10, y=5 (lands on out1) and a blue pixel at x=74, y=5 (lands on out2 at x=10, y=5).
	stride := int(joined.Stride())
	pix := joined.Pixels()
	off1 := 5*stride + 10*4
	pix[off1+0], pix[off1+1], pix[off1+2], pix[off1+3] = 0x00, 0x00, 0xFF, 0xFF
	off2 := 5*stride + 74*4
	pix[off2+0], pix[off2+1], pix[off2+2], pix[off2+3] = 0xFF, 0x00, 0x00, 0xFF

	joined.Damage(0, 0, 128, 32)
	if err := joined.Present(); err != nil {
		t.Fatalf("joined.Present: %v", err)
	}
	if p1 := out1.PixelAt(10, 5); p1 != 0xFFFF0000 {
		t.Fatalf("out1.PixelAt(10,5) = %#08x, want 0xFFFF0000", p1)
	}
	if p2 := out2.PixelAt(10, 5); p2 != 0xFF0000FF {
		t.Fatalf("out2.PixelAt(10,5) = %#08x, want 0xFF0000FF", p2)
	}

	// 2. Mirror policy: 64x32 canvas mirrored identically onto both out1 and out2.
	mirrored := NewMultiOutput([]*MemOutput{out1, out2}, true)
	if mirrored.Width() != 64 || mirrored.Height() != 32 {
		t.Fatalf("mirrored size = %dx%d, want 64x32", mirrored.Width(), mirrored.Height())
	}
	mpix := mirrored.Pixels()
	moff := 3*int(mirrored.Stride()) + 4*4
	mpix[moff+0], mpix[moff+1], mpix[moff+2], mpix[moff+3] = 0x00, 0xFF, 0x00, 0xFF
	if err := mirrored.Present(); err != nil {
		t.Fatalf("mirrored.Present: %v", err)
	}
	if out1.PixelAt(4, 3) != 0xFF00FF00 || out2.PixelAt(4, 3) != 0xFF00FF00 {
		t.Fatalf("mirror mismatch: out1=%#08x out2=%#08x, want both 0xFF00FF00", out1.PixelAt(4, 3), out2.PixelAt(4, 3))
	}

	allocs := testing.AllocsPerRun(100, func() {
		joined.Damage(0, 0, 128, 32)
		_ = joined.Present()
	})
	if allocs != 0 {
		t.Fatalf("joined.Present allocs = %v, want 0", allocs)
	}
}

func BenchmarkMultiOutputPresent(b *testing.B) {
	out1 := NewMemOutput(1920, 1080)
	out2 := NewMemOutput(1920, 1080)
	joined := NewMultiOutput([]*MemOutput{out1, out2}, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		joined.Damage(0, 0, 3840, 1080)
		_ = joined.Present()
	}
}
