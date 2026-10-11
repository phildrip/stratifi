// Package spa marshals SPA PODs (Simple Plugin API Plain Old Data), the
// parameter format PipeWire uses for stream negotiation (stratifi-0oy).
//
// POD layout (spa/pod.h): 8-byte header {uint32 bodySize, uint32 type} then
// the body; every pod is 8-aligned. Container headers (Struct/Object) cover
// only their body bytes.
package spa

import (
	"fmt"
)

// POD type ids (spa/utils/type.h).
const (
	TypeNone      uint32 = 1
	TypeBool      uint32 = 2
	TypeId        uint32 = 3
	TypeInt       uint32 = 4
	TypeLong      uint32 = 5
	TypeFloat     uint32 = 6
	TypeDouble    uint32 = 7
	TypeString    uint32 = 8
	TypeRectangle uint32 = 10
	TypeFraction  uint32 = 11
	TypeStruct    uint32 = 14
	TypeObject    uint32 = 15
	TypeFd        uint32 = 18
	TypeChoice    uint32 = 19
)

// Object type ids (SPA_TYPE_OBJECT_START = 0x40000).
const (
	TypeObjectPropInfo     uint32 = 0x40001
	TypeObjectProps        uint32 = 0x40002
	TypeObjectFormat       uint32 = 0x40003
	TypeObjectParamBuffers uint32 = 0x40004
	TypeObjectParamMeta    uint32 = 0x40005
	TypeObjectParamIO      uint32 = 0x40006
)

// Media types / subtypes (spa/param/format.h).
const (
	MediaTypeVideo  uint32 = 2
	MediaSubtypeRaw uint32 = 1
)

// Param ids (spa/param/param.h).
const (
	ParamEnumFormat uint32 = 3
	ParamFormat     uint32 = 4
	ParamBuffers    uint32 = 5
	ParamMeta       uint32 = 6
	ParamIO         uint32 = 7
)

// Format property keys (spa/param/format.h).
const (
	FormatKeyMediaType    uint32 = 1
	FormatKeyMediaSubtype uint32 = 2
	FormatKeyVideoFormat  uint32 = 0x20001
	FormatKeyVideoSize    uint32 = 0x20003
	FormatKeyVideoRate    uint32 = 0x20004
)

// ParamBuffers property keys (spa/param/buffers.h).
const (
	BuffersKeyBuffers  uint32 = 1
	BuffersKeyBlocks   uint32 = 2
	BuffersKeySize     uint32 = 3
	BuffersKeyStride   uint32 = 4
	BuffersKeyAlign    uint32 = 5
	BuffersKeyDataType uint32 = 6
)

// Video raw formats (spa/param/video/raw.h). BGRA matches the compositor's
// B8G8R8A8 Vulkan surfaces byte-for-byte.
const (
	VideoFormatBGRx uint32 = 8
	VideoFormatBGRA uint32 = 12
)

// Data types (spa/buffer/buffer.h).
const (
	DataMemPtr uint32 = 1
	DataMemFd  uint32 = 2
	DataDmaBuf uint32 = 3
)

// Choice kinds (spa/pod/pod.h).
const (
	ChoiceNone  uint32 = 0
	ChoiceRange uint32 = 1
	ChoiceStep  uint32 = 2
	ChoiceEnum  uint32 = 3
	ChoiceFlags uint32 = 4
)

// ---------------------------------------------------------------------------
// Builder
// ---------------------------------------------------------------------------

// Builder constructs a POD byte stream. Growth allocates only when capacity
// runs out; pre-allocate with Reserve for zero-alloc hot paths.
type Builder struct {
	buf   []byte
	marks []int
}

// Reserve pre-allocates capacity.
func (b *Builder) Reserve(n int) {
	if cap(b.buf) == 0 {
		b.buf = make([]byte, 0, n)
	}
}

// Reset empties the builder (retains capacity).
func (b *Builder) Reset() { b.buf = b.buf[:0]; b.marks = b.marks[:0] }

// Bytes returns the built bytes.
func (b *Builder) Bytes() []byte { return b.buf }

// Len returns the current byte count.
func (b *Builder) Len() int { return len(b.buf) }

func (b *Builder) pad8() {
	for len(b.buf)%8 != 0 {
		b.buf = append(b.buf, 0)
	}
}

func (b *Builder) u32(v uint32) {
	b.buf = append(b.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// podStart writes a {size=0, type} header placeholder and returns the size
// field offset for patching.
func (b *Builder) podStart(t uint32) int {
	b.pad8()
	off := len(b.buf)
	b.u32(0)
	b.u32(t)
	return off
}

func (b *Builder) patchSize(off int) {
	bodyLen := len(b.buf) - (off + 8)
	b.buf[off] = byte(bodyLen)
	b.buf[off+1] = byte(bodyLen >> 8)
	b.buf[off+2] = byte(bodyLen >> 16)
	b.buf[off+3] = byte(bodyLen >> 24)
}

// BeginStruct opens a Struct pod.
func (b *Builder) BeginStruct() {
	b.marks = append(b.marks, b.podStart(TypeStruct))
}

// BeginObject opens an Object pod.
func (b *Builder) BeginObject(objType, objId uint32) {
	b.marks = append(b.marks, b.podStart(TypeObject))
	b.u32(objType)
	b.u32(objId)
}

// Prop writes a property record inside an object: {key, flags}, then the
// complete value pod (written next), padded to 8 at PropEnd.
func (b *Builder) Prop(key uint32) {
	b.pad8()
	b.u32(key)
	b.u32(0) // flags
}

// PropEnd closes the property: value pods written since Prop are complete,
// so only the record-level 8-pad is needed.
func (b *Builder) PropEnd() {
	b.pad8()
}

// End closes the open container (Struct or Object).
func (b *Builder) End() {
	if len(b.marks) == 0 {
		panic("spa: End without container")
	}
	mark := b.marks[len(b.marks)-1]
	b.marks = b.marks[:len(b.marks)-1]
	b.patchSize(mark)
	b.pad8()
}

// sizedPod writes a fixed-size value pod.
func (b *Builder) sizedPod(t uint32, body []byte) {
	b.u32(uint32(len(body)))
	b.u32(t)
	b.buf = append(b.buf, body...)
	b.pad8()
}

// Int appends an Int pod.
func (b *Builder) Int(v int32) {
	var body [4]byte
	body[0] = byte(v)
	body[1] = byte(v >> 8)
	body[2] = byte(v >> 16)
	body[3] = byte(v >> 24)
	b.sizedPod(TypeInt, body[:])
}

// Id appends an Id pod.
func (b *Builder) Id(v uint32) {
	var body [4]byte
	body[0] = byte(v)
	body[1] = byte(v >> 8)
	body[2] = byte(v >> 16)
	body[3] = byte(v >> 24)
	b.sizedPod(TypeId, body[:])
}

// Bool appends a Bool pod.
func (b *Builder) Bool(v bool) {
	var body [4]byte
	if v {
		body[0] = 1
	}
	b.sizedPod(TypeBool, body[:])
}

// Fd appends an Fd pod.
func (b *Builder) Fd(v uint32) {
	var body [4]byte
	body[0] = byte(v)
	body[1] = byte(v >> 8)
	body[2] = byte(v >> 16)
	body[3] = byte(v >> 24)
	b.sizedPod(TypeFd, body[:])
}

// Long appends a Long pod.
func (b *Builder) Long(v int64) {
	var body [8]byte
	for i := 0; i < 8; i++ {
		body[i] = byte(v >> (8 * i))
	}
	b.sizedPod(TypeLong, body[:])
}

// Rectangle appends a Rectangle pod.
func (b *Builder) Rectangle(w, h uint32) {
	var body [8]byte
	body[0] = byte(w)
	body[1] = byte(w >> 8)
	body[2] = byte(w >> 16)
	body[3] = byte(w >> 24)
	body[4] = byte(h)
	body[5] = byte(h >> 8)
	body[6] = byte(h >> 16)
	body[7] = byte(h >> 24)
	b.sizedPod(TypeRectangle, body[:])
}

// Fraction appends a Fraction pod.
func (b *Builder) Fraction(num, den uint32) {
	var body [8]byte
	body[0] = byte(num)
	body[1] = byte(num >> 8)
	body[2] = byte(num >> 16)
	body[3] = byte(num >> 24)
	body[4] = byte(den)
	body[5] = byte(den >> 8)
	body[6] = byte(den >> 16)
	body[7] = byte(den >> 24)
	b.sizedPod(TypeFraction, body[:])
}

// String appends a String pod.
func (b *Builder) String(s string) {
	b.u32(uint32(len(s) + 1)) // NUL terminator counted in body
	b.u32(TypeString)
	b.buf = append(b.buf, s...)
	b.buf = append(b.buf, 0)
	b.pad8()
}

// ChoiceInt appends a Choice pod over Int values: body = {kind, flags} +
// value list (no child headers, per spa_pod_choice_body).
func (b *Builder) ChoiceInt(kind uint32, values ...uint32) {
	b.u32(uint32(8 + 4*len(values)))
	b.u32(TypeChoice)
	b.u32(kind)
	b.u32(0) // flags
	for _, v := range values {
		b.u32(v)
	}
	b.pad8()
}

// ---------------------------------------------------------------------------
// Reader
// ---------------------------------------------------------------------------

// ErrShortPod reports a pod that overruns its buffer.
var ErrShortPod = fmt.Errorf("spa: pod buffer overrun")

// Pod is a decoded pod view.
type Pod struct {
	Type     uint32
	BodySize uint32
	Body     []byte
}

// TotalSize is the byte span of the pod including header and padding.
func (p Pod) TotalSize() int {
	return (8 + int(p.BodySize) + 7) &^ 7
}

// ReadPod decodes the pod at buf[0].
func ReadPod(buf []byte) (Pod, int, error) {
	if len(buf) < 8 {
		return Pod{}, 0, ErrShortPod
	}
	bodySize := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	t := uint32(buf[4]) | uint32(buf[5])<<8 | uint32(buf[6])<<16 | uint32(buf[7])<<24
	total := (8 + int(bodySize) + 7) &^ 7
	if total > len(buf) {
		return Pod{}, 0, ErrShortPod
	}
	p := Pod{Type: t, BodySize: bodySize, Body: buf[8 : 8+int(bodySize)]}
	return p, total, nil
}

// Iter walks a sequence of pods (Struct bodies, object properties). Pods
// are 8-aligned relative to the start of the walked buffer.
type Iter struct {
	buf []byte
}

// NewIter starts walking buf.
func NewIter(buf []byte) *Iter { return &Iter{buf: buf} }

// Next returns the next pod and the byte span it consumed. Pods in the
// walked buffer are expected 8-aligned (guaranteed by the builder).
func (it *Iter) Next() (Pod, bool, error) {
	if len(it.buf) == 0 {
		return Pod{}, false, nil
	}
	p, total, err := ReadPod(it.buf)
	if err != nil {
		return Pod{}, false, err
	}
	it.buf = it.buf[total:]
	return p, true, nil
}
