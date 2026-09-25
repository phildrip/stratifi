package render

import (
	"encoding/binary"
)

// Clear fills the entire framebuffer with a 32-bit ARGB/XRGB color.
func Clear(dst []byte, color uint32) {
	// Fill first 64 bytes
	var pattern [64]byte
	for i := 0; i < 64; i += 4 {
		binary.LittleEndian.PutUint32(pattern[i:i+4], color)
	}

	copied := copy(dst, pattern[:])
	for copied < len(dst) {
		copied += copy(dst[copied:], dst[:copied])
	}
}

// FillRect fills a rectangular region in the framebuffer with a solid color.
func FillRect(dst []byte, dstStride int32, x, y, w, h int32, dstMaxW, dstMaxH int32, color uint32) {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > dstMaxW {
		w = dstMaxW - x
	}
	if y+h > dstMaxH {
		h = dstMaxH - y
	}
	if w <= 0 || h <= 0 {
		return
	}

	var rowBuf [4096]byte
	rowBytes := int(w * 4)
	if rowBytes > len(rowBuf) {
		rowBytes = len(rowBuf)
	}
	for i := 0; i < rowBytes; i += 4 {
		binary.LittleEndian.PutUint32(rowBuf[i:i+4], color)
	}

	for row := int32(0); row < h; row++ {
		dstStart := int((y+row)*dstStride + x*4)
		dstEnd := dstStart + int(w*4)
		if dstEnd > len(dst) {
			break
		}

		filled := 0
		for filled < int(w*4) {
			toFill := int(w*4) - filled
			if toFill > len(rowBuf) {
				toFill = len(rowBuf)
			}
			copy(dst[dstStart+filled:dstStart+filled+toFill], rowBuf[:toFill])
			filled += toFill
		}
	}
}

// DrawBorder paints a colored border of thickness borderW around a rectangle.
func DrawBorder(dst []byte, dstStride int32, x, y, w, h int32, borderW int32, dstMaxW, dstMaxH int32, color uint32) {
	if borderW <= 0 || w <= 0 || h <= 0 {
		return
	}
	// Top border
	FillRect(dst, dstStride, x, y, w, borderW, dstMaxW, dstMaxH, color)
	// Bottom border
	FillRect(dst, dstStride, x, y+h-borderW, w, borderW, dstMaxW, dstMaxH, color)
	// Left border
	FillRect(dst, dstStride, x, y+borderW, borderW, h-2*borderW, dstMaxW, dstMaxH, color)
	// Right border
	FillRect(dst, dstStride, x+w-borderW, y+borderW, borderW, h-2*borderW, dstMaxW, dstMaxH, color)
}

// Blit performs a fast zero-allocation 2D scanline copy from src to dst with clipping.
func Blit(dst []byte, dstStride int32, dstX, dstY int32, dstMaxW, dstMaxH int32, src []byte, srcStride int32, srcW, srcH int32) {
	// Clip source to destination bounds
	if dstX < 0 {
		srcW += dstX
		dstX = 0
	}
	if dstY < 0 {
		srcH += dstY
		dstY = 0
	}
	if dstX+srcW > dstMaxW {
		srcW = dstMaxW - dstX
	}
	if dstY+srcH > dstMaxH {
		srcH = dstMaxH - dstY
	}
	if srcW <= 0 || srcH <= 0 {
		return
	}

	bytesPerRow := int(srcW * 4)
	for row := int32(0); row < srcH; row++ {
		srcOffset := int(row * srcStride)
		dstOffset := int((dstY+row)*dstStride + dstX*4)

		if srcOffset+bytesPerRow > len(src) || dstOffset+bytesPerRow > len(dst) {
			break
		}

		copy(dst[dstOffset:dstOffset+bytesPerRow], src[srcOffset:srcOffset+bytesPerRow])
	}
}
