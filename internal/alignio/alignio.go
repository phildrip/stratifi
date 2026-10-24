// Package alignio holds the small, protocol-agnostic alignment arithmetic
// shared by the wire, D-Bus and PipeWire/SPA codecs. Each protocol keeps its
// own semantics — Wayland pads to 4 relative to the message, D-Bus pads
// relative to the message start, SPA continues on 8 — but the math is the
// same, so it lives here once.
package alignio

// AlignUp rounds n up to the next multiple of align. align must be a power of
// two (all three protocols use 1, 2, 4 or 8).
func AlignUp(n, align int) int {
	return (n + align - 1) &^ (align - 1)
}

// Pad returns how many zero bytes to append to n to reach an align boundary.
func Pad(n, align int) int {
	return AlignUp(n, align) - n
}

// PadTo appends zero bytes to buf until its length is a multiple of align and
// returns the result.
func PadTo(buf []byte, align int) []byte {
	for n := Pad(len(buf), align); n > 0; n-- {
		buf = append(buf, 0)
	}
	return buf
}
