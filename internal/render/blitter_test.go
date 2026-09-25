package render

import (
	"encoding/binary"
	"testing"
)

func TestBlitAndFill(t *testing.T) {
	dstW, dstH := int32(100), int32(100)
	dstStride := dstW * 4
	dst := make([]byte, dstStride*dstH)

	Clear(dst, 0xFF000000) // solid black
	if binary.LittleEndian.Uint32(dst[0:4]) != 0xFF000000 {
		t.Fatalf("Clear failed: expected 0xFF000000, got 0x%x", binary.LittleEndian.Uint32(dst[0:4]))
	}

	// Fill rect 10,10 to 30,30 with white
	FillRect(dst, dstStride, 10, 10, 20, 20, dstW, dstH, 0xFFFFFFFF)
	offset := (15*dstW + 15) * 4
	if binary.LittleEndian.Uint32(dst[offset:offset+4]) != 0xFFFFFFFF {
		t.Fatalf("FillRect failed: expected 0xFFFFFFFF at (15,15)")
	}

	// Blit a 10x10 red patch
	srcW, srcH := int32(10), int32(10)
	srcStride := srcW * 4
	src := make([]byte, srcStride*srcH)
	Clear(src, 0xFFFF0000) // red

	Blit(dst, dstStride, 50, 50, dstW, dstH, src, srcStride, srcW, srcH)
	offsetRed := (55*dstW + 55) * 4
	if binary.LittleEndian.Uint32(dst[offsetRed:offsetRed+4]) != 0xFFFF0000 {
		t.Fatalf("Blit failed: expected 0xFFFF0000 at (55,55)")
	}
}

func BenchmarkBlit(b *testing.B) {
	dstW, dstH := int32(1920), int32(1080)
	dstStride := dstW * 4
	dst := make([]byte, dstStride*dstH)

	srcW, srcH := int32(960), int32(540)
	srcStride := srcW * 4
	src := make([]byte, srcStride*srcH)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		Blit(dst, dstStride, 100, 100, dstW, dstH, src, srcStride, srcW, srcH)
	}
}

func BenchmarkFillRect(b *testing.B) {
	dstW, dstH := int32(1920), int32(1080)
	dstStride := dstW * 4
	dst := make([]byte, dstStride*dstH)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		FillRect(dst, dstStride, 50, 50, 800, 600, dstW, dstH, 0xFF336699)
	}
}
