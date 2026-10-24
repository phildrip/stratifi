// Package dbus implements a minimal pure Go D-Bus client sufficient for the
// xdg-desktop-portal ScreenCast flow used by the compositor's screen-sharing
// bridge (stratifi-bro).
//
// Wire format per the D-Bus specification:
//   - Little-endian; all alignment offsets are relative to the message start.
//   - Message layout: byte 0 endianness, byte 1 type, byte 2 flags, byte 3
//     version, u32 bodyLen @4, u32 serial @8, u32 fieldsArrayLen @12, then
//     the fields array entries (element alignment 8, entries 8-aligned from
//     offset 16), then the body 8-aligned relative to the message start.
//   - Unix auth: "\0AUTH EXTERNAL <hex-uid>\r\n" → OK → BEGIN.
package dbus

import (
	"encoding/binary"
	"fmt"
	"math"
	"stratifi/internal/alignio"
	"strings"
)

// Message type codes.
const (
	TypeMethodCall   byte = 1
	TypeMethodReturn byte = 2
	TypeError        byte = 3
	TypeSignal       byte = 4
)

// Header field codes.
const (
	FieldPath        byte = 1
	FieldInterface   byte = 2
	FieldMember      byte = 3
	FieldErrorName   byte = 4
	FieldReplySerial byte = 5
	FieldDestination byte = 6
	FieldSender      byte = 7
	FieldSignature   byte = 8
)

// Well-known portal names.
const (
	DestPortal      = "org.freedesktop.portal.Desktop"
	PathPortal      = "/org/freedesktop/portal/desktop"
	IfaceRequest    = "org.freedesktop.portal.Request"
	IfaceScreenCast = "org.freedesktop.portal.ScreenCast"
)

// Message flags.
const (
	FlagNoReplyExpected byte = 1
	FlagNoAutoStart     byte = 2
)

// Msg is one D-Bus message.
type Msg struct {
	Type        byte
	Flags       byte
	Serial      uint32
	Body        []byte
	BodySig     string
	Path        string
	Interface   string
	Member      string
	ErrorName   string
	ReplySerial uint32
	Destination string
	Sender      string
}

// ---------------------------------------------------------------------------
// Encoder
// ---------------------------------------------------------------------------

// Writer marshals exactly one D-Bus message. Alignment is relative to the
// message start, which is buffer offset 0 (one message per writer).
type Writer struct {
	buf []byte
}

// Reset empties the buffer for the next message.
func (w *Writer) Reset() { w.buf = w.buf[:0] }

// Bytes returns the marshalled content so far.
func (w *Writer) Bytes() []byte { return w.buf }

// Align pads to an n-byte boundary relative to the message start.
func (w *Writer) Align(n int) { w.buf = alignio.PadTo(w.buf, n) }

// PutByte appends one raw byte (no alignment). Named PutByte to avoid the
// io.ByteWriter signature convention this package does not follow.
func (w *Writer) PutByte(b byte) { w.buf = append(w.buf, b) }

// WriteUint32 appends a little-endian uint32 (4-aligned).
func (w *Writer) WriteUint32(v uint32) {
	w.Align(4)
	var t [4]byte
	binary.LittleEndian.PutUint32(t[:], v)
	w.buf = append(w.buf, t[:]...)
}

// WriteUint64 appends a little-endian uint64 (8-aligned).
func (w *Writer) WriteUint64(v uint64) {
	w.Align(8)
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], v)
	w.buf = append(w.buf, t[:]...)
}

// WriteDouble appends an IEEE-754 float64 (8-aligned).
func (w *Writer) WriteDouble(v float64) { w.WriteUint64(math.Float64bits(v)) }

// WriteBoolean appends a bool as a uint32 payload.
func (w *Writer) WriteBoolean(v bool) {
	if v {
		w.WriteUint32(1)
	} else {
		w.WriteUint32(0)
	}
}

// WriteString appends a string or object path.
func (w *Writer) WriteString(s string) {
	w.WriteUint32(uint32(len(s)))
	w.buf = append(w.buf, s...)
	w.buf = append(w.buf, 0)
}

// WriteSignature appends a signature ('g').
func (w *Writer) WriteSignature(s string) {
	if len(s) > 255 {
		panic("dbus: signature longer than 255 bytes")
	}
	w.buf = append(w.buf, byte(len(s)))
	w.buf = append(w.buf, s...)
	w.buf = append(w.buf, 0)
}

// BeginDict opens an a{sv} array and returns the content-start marker for
// FinishDict. The u32 length prefix is aligned to the element alignment (8),
// matching the D-Bus rule that the length word of an array is aligned to the
// array's element alignment. Values are variant-boxed strings only.
func (w *Writer) BeginDict() int {
	w.Align(8)
	w.buf = append(w.buf, 0, 0, 0, 0)
	return len(w.buf)
}

// DictEntry appends one {string, variant(string)} entry to an open dict.
func (w *Writer) DictEntry(key, val string) {
	w.Align(8) // entries align to the struct element alignment (8)
	w.WriteString(key)
	// Variant: signature 's', then the boxed string (4-aligned).
	w.Align(4)
	w.buf = append(w.buf, byte(len("s")))
	w.buf = append(w.buf, "s"...)
	w.buf = append(w.buf, 0)
	w.WriteString(val)
}

// DictEntryUint appends one {string, variant(uint32)} entry to an open dict.
func (w *Writer) DictEntryUint(key string, val uint32) {
	w.Align(8) // dict entries align to struct alignment 8
	w.WriteString(key)
	w.Align(4)
	w.buf = append(w.buf, byte(len("u")))
	w.buf = append(w.buf, "u"...)
	w.buf = append(w.buf, 0)
	w.WriteUint32(val)
}

// FinishDict patches the dict length placeholder.
func (w *Writer) FinishDict(start int) {
	binary.LittleEndian.PutUint32(w.buf[start-4:start], uint32(len(w.buf)-start))
}

// putHeaderField writes one (byte code, variant) entry 8-aligned.
func (w *Writer) putHeaderField(code byte, sig byte, write func()) {
	w.Align(8)
	w.buf = append(w.buf, code, 0, 0, 0) // byte + pad to signature position
	w.buf = append(w.buf, byte(len([]byte{sig})), sig, 0)
	write()
}

// Build serialises m (with pre-encoded body) into a complete message.
func (w *Writer) Build(m *Msg) {
	w.buf = w.buf[:0]

	var head [12]byte
	head[0] = 'l'
	head[1] = m.Type
	head[2] = m.Flags
	head[3] = 1 // version
	binary.LittleEndian.PutUint32(head[4:8], uint32(len(m.Body)))
	binary.LittleEndian.PutUint32(head[8:12], m.Serial)
	w.buf = append(w.buf, head[:]...)

	// Fields array: u32 length placeholder at offset 12, entries from 16.
	w.buf = append(w.buf, 0, 0, 0, 0)

	if m.Path != "" {
		w.putHeaderField(FieldPath, 'o', func() { w.WriteString(m.Path) })
	}
	if m.Interface != "" {
		w.putHeaderField(FieldInterface, 's', func() { w.WriteString(m.Interface) })
	}
	if m.Member != "" {
		w.putHeaderField(FieldMember, 's', func() { w.WriteString(m.Member) })
	}
	if m.ErrorName != "" {
		w.putHeaderField(FieldErrorName, 's', func() { w.WriteString(m.ErrorName) })
	}
	if m.ReplySerial != 0 {
		w.putHeaderField(FieldReplySerial, 'u', func() { w.WriteUint32(m.ReplySerial) })
	}
	if m.Destination != "" {
		w.putHeaderField(FieldDestination, 's', func() { w.WriteString(m.Destination) })
	}
	if m.Sender != "" {
		w.putHeaderField(FieldSender, 's', func() { w.WriteString(m.Sender) })
	}
	if m.BodySig != "" {
		w.putHeaderField(FieldSignature, 'g', func() { w.WriteSignature(m.BodySig) })
	}

	binary.LittleEndian.PutUint32(w.buf[12:16], uint32(len(w.buf)-16))

	// Body: 8-aligned relative to the message start.
	w.Align(8)
	w.buf = append(w.buf, m.Body...)
}

// ---------------------------------------------------------------------------
// Decoder
// ---------------------------------------------------------------------------

// Reader decodes message bytes; alignment is relative to buffer start.
type Reader struct {
	buf []byte
	off int
}

// NewReader binds a reader to a received message body.
func NewReader(body []byte) *Reader { return &Reader{buf: body} }

// Init binds the reader to a buffer (a whole message or a sub-range whose
// start offset shares the required alignment with the message start).
func (r *Reader) Init(buf []byte) { r.buf, r.off = buf, 0 }

// Remaining returns unread byte count.
func (r *Reader) Remaining() int { return len(r.buf) - r.off }

// Offset returns the current read position (alignment reference).
func (r *Reader) Offset() int { return r.off }

// Skip advances n bytes without interpreting them.
func (r *Reader) Skip(n int) ([]byte, error) {
	if r.off+n > len(r.buf) {
		return nil, fmt.Errorf("dbus: skip overrun (%d > %d)", r.off+n, len(r.buf))
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

func (r *Reader) readAt(n, a int) ([]byte, error) {
	for r.off%a != 0 {
		r.off++
	}
	if r.off+n > len(r.buf) {
		return nil, fmt.Errorf("dbus: truncated read (need %d at %d, have %d)", n, r.off, len(r.buf))
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

// ReadByte consumes one raw byte.
func (r *Reader) ReadByte() (byte, error) {
	if r.off >= len(r.buf) {
		return 0, fmt.Errorf("dbus: truncated byte read")
	}
	b := r.buf[r.off]
	r.off++
	return b, nil
}

// ReadUint32 consumes an aligned uint32.
func (r *Reader) ReadUint32() (uint32, error) {
	b, err := r.readAt(4, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// ReadUint64 consumes an aligned uint64.
func (r *Reader) ReadUint64() (uint64, error) {
	b, err := r.readAt(8, 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

// ReadBoolean consumes a bool.
func (r *Reader) ReadBoolean() (bool, error) {
	v, err := r.ReadUint32()
	return v != 0, err
}

// ReadString consumes a string/object path.
func (r *Reader) ReadString() (string, error) {
	l, err := r.ReadUint32()
	if err != nil {
		return "", err
	}
	if r.off+int(l)+1 > len(r.buf) {
		return "", fmt.Errorf("dbus: truncated string (len %d at %d)", l, r.off)
	}
	s := string(r.buf[r.off : r.off+int(l)])
	r.off += int(l) + 1
	return s, nil
}

// ReadSignature consumes a signature.
func (r *Reader) ReadSignature() (string, error) {
	if r.off >= len(r.buf) {
		return "", fmt.Errorf("dbus: truncated signature")
	}
	l := int(r.buf[r.off])
	r.off++
	if r.off+l+1 > len(r.buf) {
		return "", fmt.Errorf("dbus: truncated signature body")
	}
	s := string(r.buf[r.off : r.off+l])
	r.off += l + 1
	return s, nil
}

// fixedHeaderLen is the byte offset where fields array content begins.
const fixedHeaderLen = 16

// MessageTotalSpan returns the full byte length of a complete message from
// its fixed header: header(12) + fields array (len + content) + body.
// Used by the connection's rolling accumulator to frame exact messages.
func MessageTotalSpan(buf []byte) (int, error) {
	if len(buf) < fixedHeaderLen {
		return 0, fmt.Errorf("dbus: message too short (%d bytes)", len(buf))
	}
	bodyLen := int(binary.LittleEndian.Uint32(buf[4:8]))
	fieldsLen := int(binary.LittleEndian.Uint32(buf[12:16]))
	fieldsEnd := fixedHeaderLen + fieldsLen
	bodyStart := fieldsEnd
	for bodyStart%8 != 0 {
		bodyStart++
	}
	return bodyStart + bodyLen, nil
}

// DecodeHeader parses the header + field array into m. m.Body is the
// body sub-slice; callers drive typed reads from it with a Reader.
func DecodeHeader(buf []byte) (*Msg, error) {
	if len(buf) < fixedHeaderLen {
		return nil, fmt.Errorf("dbus: message too short (%d bytes)", len(buf))
	}
	if buf[0] != 'l' {
		return nil, fmt.Errorf("dbus: unsupported endianness %q", buf[0])
	}
	m := &Msg{Type: buf[1], Flags: buf[2]}
	if buf[3] != 1 {
		return nil, fmt.Errorf("dbus: unsupported protocol version %d", buf[3])
	}
	bodyLen := binary.LittleEndian.Uint32(buf[4:8])
	m.Serial = binary.LittleEndian.Uint32(buf[8:12])

	fieldsLen := binary.LittleEndian.Uint32(buf[12:16])
	fieldsEnd := fixedHeaderLen + int(fieldsLen)
	if fieldsEnd > len(buf) {
		return nil, fmt.Errorf("dbus: header fields overrun")
	}

	// Entries are 8-aligned relative to the message start; the fields
	// sub-slice begins at offset 16 (itself 8-aligned), so alignment
	// arithmetic inside the sub-slice matches message-relative alignment.
	fr := &Reader{buf: buf[fixedHeaderLen:fieldsEnd]}
	for fr.Remaining() > 0 {
		b, err := fr.readAt(1, 8) // struct byte, entry-start aligned
		if err != nil {
			return nil, err
		}
		code := b[0]
		// Encoder layout: byte code, 3 pad bytes, variant signature at the
		// next 4-boundary (relative to the message start; the sub-buffer
		// origin is itself 4-aligned).
		for fr.off%4 != 0 {
			fr.off++
		}
		sig, err := fr.ReadSignature() // variant signature
		if err != nil {
			return nil, err
		}
		switch sig {
		case "s", "o":
			v, err := fr.ReadString()
			if err != nil {
				return nil, err
			}
			switch code {
			case FieldPath:
				m.Path = v
			case FieldInterface:
				m.Interface = v
			case FieldMember:
				m.Member = v
			case FieldErrorName:
				m.ErrorName = v
			case FieldDestination:
				m.Destination = v
			case FieldSender:
				m.Sender = v
			}
		case "u":
			v, err := fr.ReadUint32()
			if err != nil {
				return nil, err
			}
			if code == FieldReplySerial {
				m.ReplySerial = v
			}
		case "g":
			v, err := fr.ReadSignature()
			if err != nil {
				return nil, err
			}
			if code == FieldSignature {
				m.BodySig = v
			}
		default:
			return nil, fmt.Errorf("dbus: unsupported header field sig %q", sig)
		}
	}

	bodyStart := fieldsEnd
	for bodyStart%8 != 0 {
		bodyStart++
	}
	if int(bodyLen) > len(buf)-bodyStart {
		return nil, fmt.Errorf("dbus: body overrun (declared %d, available %d)", bodyLen, len(buf)-bodyStart)
	}
	m.Body = buf[bodyStart : bodyStart+int(bodyLen)]
	return m, nil
}

// ValidObjectPath reports whether s is a syntactically valid D-Bus object path.
func ValidObjectPath(s string) bool {
	if !strings.HasPrefix(s, "/") {
		return false
	}
	if s == "/" {
		return true
	}
	for _, elem := range strings.Split(s[1:], "/") {
		if elem == "" {
			return false
		}
		for _, c := range elem {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
	}
	return true
}
