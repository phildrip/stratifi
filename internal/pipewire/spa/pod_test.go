package spa

import (
	"bytes"
	"testing"
)

// u32be helpers to make expected-byte assertions readable.
func le32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// TestIntPodEncoding pins the exact Int pod bytes: header {4, TypeInt},
// body u32, pad to 8 → 12 bytes total.
func TestIntPodEncoding(t *testing.T) {
	var b Builder
	b.Int(42)
	want := append(le32(4), le32(TypeInt)...)
	want = append(want, le32(42)...)
	want = append(want, 0, 0, 0, 0)
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("Int pod = % x, want % x", b.Bytes(), want)
	}
}

// TestStringPodPinning checks body includes the NUL and total is padded.
func TestStringPodPinning(t *testing.T) {
	var b Builder
	b.String("hi")
	// body = 3 bytes ("hi\0"); pod total = 8+3 = 11 → pad to 16.
	want := append(le32(3), le32(TypeString)...)
	want = append(want, 'h', 'i', 0, 0, 0, 0, 0, 0)
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("String pod = % x, want % x", b.Bytes(), want)
	}
}

// TestStructRoundTrip builds Struct(Int, Id, Rectangle) and re-parses it.
func TestStructRoundTrip(t *testing.T) {
	var b Builder
	b.BeginStruct()
	b.Int(7)
	b.Id(0x1234)
	b.Rectangle(960, 640)
	b.End()

	buf := b.Bytes()
	pod, total, err := ReadPod(buf)
	if err != nil {
		t.Fatalf("ReadPod: %v", err)
	}
	if pod.Type != TypeStruct {
		t.Errorf("type = %x, want Struct", pod.Type)
	}
	// Each child pod is padded to 8 within the container:
	// Int 16 + Id 16 + Rect 16 = 48 body bytes.
	if total != 56 { // 8 header + 48 body
		t.Errorf("total = %d, want 56", total)
	}
	if pod.BodySize != 48 {
		t.Errorf("bodySize = %d, want 48", pod.BodySize)
	}

	// Walk children.
	it := NewIter(pod.Body)
	child, ok, err := it.Next()
	if err != nil || !ok {
		t.Fatalf("first child: %v ok=%v", err, ok)
	}
	if child.Type != TypeInt {
		t.Errorf("child 1 type = %x", child.Type)
	}
	child, ok, err = it.Next()
	if err != nil || !ok {
		t.Fatalf("second child: %v ok=%v", err, ok)
	}
	if child.Type != TypeId {
		t.Errorf("child 2 type = %x", child.Type)
	}
	child, ok, err = it.Next()
	if err != nil || !ok {
		t.Fatalf("third child: %v ok=%v", err, ok)
	}
	if child.Type != TypeRectangle {
		t.Errorf("child 3 type = %x", child.Type)
	}
	if _, ok, _ := it.Next(); ok {
		t.Error("expected struct exhausted")
	}
}

// TestObjectFormatRoundTrip builds the EnumFormat object the stream client
// sends and decodes it back, checking keys and values.
func TestObjectFormatRoundTrip(t *testing.T) {
	var b Builder
	b.BeginObject(TypeObjectFormat, ParamEnumFormat)
	b.Prop(FormatKeyMediaType)
	b.Id(MediaTypeVideo)
	b.PropEnd()
	b.Prop(FormatKeyMediaSubtype)
	b.Id(MediaSubtypeRaw)
	b.PropEnd()
	b.Prop(FormatKeyVideoFormat)
	b.Id(VideoFormatBGRA)
	b.PropEnd()
	b.Prop(FormatKeyVideoSize)
	b.Rectangle(960, 640)
	b.PropEnd()
	b.Prop(FormatKeyVideoRate)
	b.Fraction(60, 1)
	b.PropEnd()
	b.End()

	buf := b.Bytes()
	pod, _, err := ReadPod(buf)
	if err != nil {
		t.Fatalf("ReadPod: %v", err)
	}
	if pod.Type != TypeObject {
		t.Fatalf("type = %x", pod.Type)
	}

	// object body: {objType u32, objId u32} then props
	if len(pod.Body) < 8 {
		t.Fatalf("object body short: %d", len(pod.Body))
	}
	objType := leU32(pod.Body[:4])
	objId := leU32(pod.Body[4:8])
	if objType != TypeObjectFormat || objId != ParamEnumFormat {
		t.Errorf("object header = type %x id %x", objType, objId)
	}

	it := NewIter(pod.Body[8:])
	type kv struct {
		key uint32
		typ uint32
		val uint32
	}
	var props []kv
	for len(it.buf) > 0 {
		// prop record: {key u32, flags u32} then the value pod
		if len(it.buf) < 8 {
			t.Fatalf("prop record truncated: %d", len(it.buf))
		}
		key := leU32(it.buf[:4])
		it.buf = it.buf[8:]
		valuePod, total, err := ReadPod(it.buf)
		if err != nil {
			t.Fatalf("value pod: %v", err)
		}
		props = append(props, kv{key, valuePod.Type, leU32(valuePod.Body)})
		it.buf = it.buf[total:]
	}

	want := []kv{
		{FormatKeyMediaType, TypeId, MediaTypeVideo},
		{FormatKeyMediaSubtype, TypeId, MediaSubtypeRaw},
		{FormatKeyVideoFormat, TypeId, VideoFormatBGRA},
		{FormatKeyVideoSize, TypeRectangle, 960}, // rect w in body[0:4]
		{FormatKeyVideoRate, TypeFraction, 60},   // fraction num
	}
	if len(props) != len(want) {
		t.Fatalf("props = %v, want %d entries", props, len(want))
	}
	for i := range want {
		if props[i].key != want[i].key || props[i].typ != want[i].typ || props[i].val != want[i].val {
			t.Errorf("prop %d = %+v, want %+v", i, props[i], want[i])
		}
	}
}

// TestChoiceIntEncoding pins the Choice pod bytes: body {kind, flags, values}.
func TestChoiceIntEncoding(t *testing.T) {
	var b Builder
	b.ChoiceInt(ChoiceFlags, DataMemFd, DataMemPtr)
	// body = 8 (kind+flags) + 2*4 values = 16; total = 24, no trailing pad.
	want := append(le32(16), le32(TypeChoice)...)
	want = append(want, le32(ChoiceFlags)...)
	want = append(want, le32(0)...)
	want = append(want, le32(DataMemFd)...)
	want = append(want, le32(DataMemPtr)...)
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("Choice pod = % x, want % x", b.Bytes(), want)
	}
}

// TestShortPodError covers the overrun error path.
func TestShortPodError(t *testing.T) {
	if _, _, err := ReadPod([]byte{4, 0, 0, 0, 4, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Error("expected ErrShortPod for truncated body")
	}
	if _, _, err := ReadPod(nil); err == nil {
		t.Error("expected ErrShortPod for empty buffer")
	}
}

// TestLongEncoding pins 8-byte Long pods.
func TestLongEncoding(t *testing.T) {
	var b Builder
	b.Long(0x1F)
	want := append(le32(8), le32(TypeLong)...)
	want = append(want, 0x1F, 0, 0, 0, 0, 0, 0, 0)
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("Long pod = % x, want % x", b.Bytes(), want)
	}
}

func leU32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
