package wire

import (
	"encoding/binary"
	"fmt"
	"math"

	"stratifi/internal/alignio"
)

// HeaderSize is the fixed 8-byte header for every Wayland wire message.
const HeaderSize = 8

// Header represents the 8-byte Wayland message header.
type Header struct {
	ObjectID uint32
	Opcode   uint16
	Size     uint16
}

// Fixed represents Wayland's 24.8 signed fixed-point number.
type Fixed int32

// ToFixed converts a float64 to Wayland 24.8 fixed point.
func ToFixed(v float64) Fixed {
	return Fixed(math.Round(v * 256.0))
}

// ToFloat64 converts Wayland 24.8 fixed point to float64.
func (f Fixed) ToFloat64() float64 {
	return float64(f) / 256.0
}

// Reader provides zero-allocation decoding of Wayland wire messages.
// It parses directly from a byte slice without copying.
type Reader struct {
	buf    []byte
	offset int
	fds    []int
	fdIdx  int
}

// Init initializes the Reader with a message payload and associated FDs.
func (r *Reader) Init(buf []byte, fds []int) {
	r.buf = buf
	r.offset = 0
	r.fds = fds
	r.fdIdx = 0
}

// ReadHeader decodes the 8-byte header from the buffer.
func (r *Reader) ReadHeader() (Header, error) {
	if len(r.buf)-r.offset < HeaderSize {
		return Header{}, fmt.Errorf("buffer too short for header: have %d, need %d", len(r.buf)-r.offset, HeaderSize)
	}

	objID := binary.LittleEndian.Uint32(r.buf[r.offset : r.offset+4])
	sizeOpcode := binary.LittleEndian.Uint32(r.buf[r.offset+4 : r.offset+8])
	r.offset += HeaderSize

	size := uint16(sizeOpcode >> 16)
	opcode := uint16(sizeOpcode & 0xffff)

	if int(size) > len(r.buf) {
		return Header{}, fmt.Errorf("message size (%d) exceeds available buffer (%d)", size, len(r.buf))
	}

	return Header{
		ObjectID: objID,
		Opcode:   opcode,
		Size:     size,
	}, nil
}

// ReadUint32 reads a 32-bit unsigned integer.
func (r *Reader) ReadUint32() (uint32, error) {
	if len(r.buf)-r.offset < 4 {
		return 0, fmt.Errorf("buffer too short for uint32 at offset %d", r.offset)
	}
	v := binary.LittleEndian.Uint32(r.buf[r.offset : r.offset+4])
	r.offset += 4
	return v, nil
}

// ReadInt32 reads a 32-bit signed integer.
func (r *Reader) ReadInt32() (int32, error) {
	u, err := r.ReadUint32()
	return int32(u), err
}

// ReadFixed reads a 24.8 signed fixed-point value.
func (r *Reader) ReadFixed() (Fixed, error) {
	u, err := r.ReadUint32()
	return Fixed(int32(u)), err
}

// ReadBytes returns a slice pointing directly into the input buffer for string/array data.
// It avoids allocating a Go string on the hot path.
func (r *Reader) ReadBytes() ([]byte, error) {
	length, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, nil
	}

	// Calculate padded length to 32-bit (4-byte) boundary
	paddedLen := alignio.AlignUp(int(length), 4)
	if len(r.buf)-r.offset < paddedLen {
		return nil, fmt.Errorf("buffer too short for bytes: need %d, have %d", paddedLen, len(r.buf)-r.offset)
	}

	data := r.buf[r.offset : r.offset+int(length)]
	r.offset += paddedLen
	return data, nil
}

// ReadString reads a NUL-terminated string without allocating if possible.
func (r *Reader) ReadString() (string, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", nil
	}
	// Strip trailing NUL byte
	if b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return string(b), nil
}

// ReadArray reads a Wayland array (length-prefixed, 32-bit padded byte slice).
func (r *Reader) ReadArray() ([]byte, error) {
	return r.ReadBytes()
}

// ReadFD reads the next file descriptor received via SCM_RIGHTS.
func (r *Reader) ReadFD() (int, error) {
	if r.fdIdx >= len(r.fds) {
		return -1, fmt.Errorf("no file descriptor available (idx %d, total %d)", r.fdIdx, len(r.fds))
	}
	fd := r.fds[r.fdIdx]
	r.fdIdx++
	return fd, nil
}

// Remaining returns the number of unparsed bytes in the current message.
func (r *Reader) Remaining() int {
	return len(r.buf) - r.offset
}

// Writer formats Wayland wire messages into a contiguous buffer with zero heap allocations.
type Writer struct {
	buf     []byte
	offset  int
	fds     []int
	fdCount int
}

// Init initializes the Writer with pre-allocated buffer slices.
func (w *Writer) Init(buf []byte, fds []int) {
	w.buf = buf
	w.offset = 0
	w.fds = fds
	w.fdCount = 0
}

// BeginMessage reserves space for the 8-byte header and returns the starting offset.
func (w *Writer) BeginMessage(objectID uint32, opcode uint16) int {
	start := w.offset
	w.offset += HeaderSize
	// Header will be finalized in EndMessage
	return start
}

// EndMessage patches the header at startOffset with the final message size.
func (w *Writer) EndMessage(startOffset int, objectID uint32, opcode uint16) error {
	totalLen := w.offset - startOffset
	if totalLen > 0xffff {
		return fmt.Errorf("message size %d exceeds uint16 max", totalLen)
	}

	binary.LittleEndian.PutUint32(w.buf[startOffset:startOffset+4], objectID)
	sizeOpcode := (uint32(totalLen) << 16) | uint32(opcode)
	binary.LittleEndian.PutUint32(w.buf[startOffset+4:startOffset+8], sizeOpcode)
	return nil
}

// WriteUint32 appends a 32-bit unsigned integer.
func (w *Writer) WriteUint32(v uint32) error {
	if len(w.buf)-w.offset < 4 {
		return fmt.Errorf("buffer overflow writing uint32")
	}
	binary.LittleEndian.PutUint32(w.buf[w.offset:w.offset+4], v)
	w.offset += 4
	return nil
}

// WriteInt32 appends a 32-bit signed integer.
func (w *Writer) WriteInt32(v int32) error {
	return w.WriteUint32(uint32(v))
}

// WriteFixed appends a 24.8 signed fixed-point value.
func (w *Writer) WriteFixed(v Fixed) error {
	return w.WriteUint32(uint32(v))
}

// WriteString appends a NUL-terminated string with 32-bit padding.
func (w *Writer) WriteString(s string) error {
	sLen := len(s) + 1 // including trailing NUL
	paddedLen := alignio.AlignUp(sLen, 4)
	totalNeed := 4 + paddedLen

	if len(w.buf)-w.offset < totalNeed {
		return fmt.Errorf("buffer overflow writing string")
	}

	// Write length
	binary.LittleEndian.PutUint32(w.buf[w.offset:w.offset+4], uint32(sLen))
	w.offset += 4

	// Copy string bytes
	copy(w.buf[w.offset:w.offset+len(s)], s)
	// Zero out remainder (NUL and padding bytes)
	for i := len(s); i < paddedLen; i++ {
		w.buf[w.offset+i] = 0
	}
	w.offset += paddedLen
	return nil
}

// WriteArray appends a Wayland array with 32-bit length prefix and 32-bit padding.
func (w *Writer) WriteArray(data []byte) error {
	paddedLen := (len(data) + 3) &^ 3
	totalNeed := 4 + paddedLen

	if len(w.buf)-w.offset < totalNeed {
		return fmt.Errorf("buffer overflow writing array")
	}

	binary.LittleEndian.PutUint32(w.buf[w.offset:w.offset+4], uint32(len(data)))
	w.offset += 4
	copy(w.buf[w.offset:w.offset+len(data)], data)
	for i := len(data); i < paddedLen; i++ {
		w.buf[w.offset+i] = 0
	}
	w.offset += paddedLen
	return nil
}

// WriteFD stages a file descriptor to be sent out-of-band via SCM_RIGHTS.
func (w *Writer) WriteFD(fd int) error {
	if w.fdCount >= len(w.fds) {
		return fmt.Errorf("fd capacity exceeded")
	}
	w.fds[w.fdCount] = fd
	w.fdCount++
	return nil
}

// Bytes returns the written message slice.
func (w *Writer) Bytes() []byte {
	return w.buf[:w.offset]
}

// FDs returns the attached file descriptors.
func (w *Writer) FDs() []int {
	return w.fds[:w.fdCount]
}

// Reset resets the write position to zero.
func (w *Writer) Reset() {
	w.offset = 0
	w.fdCount = 0
}
