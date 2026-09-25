// Package pipewire implements the minimal pure Go PipeWire native-protocol
// client the compositor needs for screen sharing (bloxwm-0oy).
//
// Framing (pw protocol-native): each packet is a 16-byte header
// {id u32, opcode u32, size u32, seq u32} followed by the pod body; the
// size field covers the pod bytes (header+body, 8-aligned). Ancillary fds
// travel via SCM_RIGHTS.
package pipewire

import (
	"fmt"
)

// HeaderSize is the pw native protocol message header length.
const HeaderSize = 16

// Header is a decoded pw native message header.
type Header struct {
	ID     uint32 // object/resource id
	Opcode uint32 // top byte of word 2
	Size   uint32 // body byte span (low 24 bits of word 2)
	Seq    uint32 // word 3
	NFDs   uint32 // ancillary fd count (word 4)
}

// ErrShortHeader reports a truncated message header.
var ErrShortHeader = fmt.Errorf("pipewire: message shorter than header")

// ErrBodyOverrun reports a declared body size larger than the buffer.
var ErrBodyOverrun = fmt.Errorf("pipewire: declared body overruns buffer")

// DecodeHeader parses the 16-byte header at buf[0] (protocol v3):
//
//	word0: object id
//	word1: opcode in the top byte, body size in the low 24 bits
//	word2: message seq
//	word3: ancillary fd count
//
// It returns the header and the total packet span (header + body size).
func DecodeHeader(buf []byte) (Header, int, error) {
	if len(buf) < HeaderSize {
		return Header{}, 0, ErrShortHeader
	}
	w1 := leU32(buf[4:8])
	h := Header{
		ID:     leU32(buf[0:4]),
		Opcode: w1 >> 24,
		Size:   w1 & 0xffffff,
		Seq:    leU32(buf[8:12]),
		NFDs:   leU32(buf[12:16]),
	}
	if int(h.Size) > len(buf)-HeaderSize {
		return h, 0, ErrBodyOverrun
	}
	return h, HeaderSize + int(h.Size), nil
}

// Writer frames pw native messages over a reusable byte buffer.
type Writer struct {
	buf   []byte
	seq   uint32
	fds   []int
	fdBuf [4]int
}

// Init prepares the writer with a preallocated buffer (zero-alloc path).
func (w *Writer) Init(buf []byte, fdSlots *[4]int) {
	w.buf = buf[:0]
	w.fds = fdSlots[:0]
}

// Reset empties the buffer and staged fds (retains capacity).
func (w *Writer) Reset() {
	w.buf = w.buf[:0]
	w.fds = w.fds[:0]
}

// Bytes returns the framed message.
func (w *Writer) Bytes() []byte { return w.buf }

// FDs returns the staged ancillary descriptors.
func (w *Writer) FDs() []int { return w.fds }

// Begin frames a message with the given resource id and opcode, leaving the
// size placeholder for Finish. Body pods follow.
func (w *Writer) Begin(id, opcode uint32) {
	w.seq++
	w.buf = append(w.buf,
		byte(id), byte(id>>8), byte(id>>16), byte(id>>24),
		0, 0, 0, byte(opcode), // opcode in top byte, size patched by Finish
		byte(w.seq), byte(w.seq>>8), byte(w.seq>>16), byte(w.seq>>24),
		0, 0, 0, 0, // fd count (set with message body)
	)
}

// Finish patches the size (low 24 bits of word 2) to the body span.
func (w *Writer) Finish() {
	size := len(w.buf) - HeaderSize
	w.buf[4] = byte(size)
	w.buf[5] = byte(size >> 8)
	w.buf[6] = byte(size >> 16)
}

// PutU32 appends a little-endian uint32 to the body.
func (w *Writer) PutU32(v uint32) {
	w.buf = append(w.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// PutU64 appends a little-endian uint64 to the body.
func (w *Writer) PutU64(v uint64) {
	for i := 0; i < 8; i++ {
		w.buf = append(w.buf, byte(v>>(8*i)))
	}
}

// PutBytes appends raw bytes (caller handles alignment).
func (w *Writer) PutBytes(b []byte) { w.buf = append(w.buf, b...) }

// StageFD queues a descriptor for SCM_RIGHTS delivery with the message.
func (w *Writer) StageFD(fd int) { w.fds = append(w.fds, fd) }

func leU32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
