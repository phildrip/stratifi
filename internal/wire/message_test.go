package wire

import (
	"math"
	"testing"
)

func TestWireReadWriteRoundTrip(t *testing.T) {
	var rawBuf [1024]byte
	var rawFDs [8]int

	var w Writer
	w.Init(rawBuf[:], rawFDs[:])

	// Encode a synthetic Wayland event (e.g. wl_display.error)
	// args: object_id (uint32), code (uint32), message (string)
	start := w.BeginMessage(1, 0)
	if err := w.WriteUint32(42); err != nil {
		t.Fatalf("WriteUint32 failed: %v", err)
	}
	if err := w.WriteUint32(1); err != nil {
		t.Fatalf("WriteUint32 failed: %v", err)
	}
	if err := w.WriteString("invalid object"); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := w.WriteFixed(ToFixed(12.75)); err != nil {
		t.Fatalf("WriteFixed failed: %v", err)
	}
	if err := w.WriteFD(9); err != nil {
		t.Fatalf("WriteFD failed: %v", err)
	}
	if err := w.EndMessage(start, 1, 0); err != nil {
		t.Fatalf("EndMessage failed: %v", err)
	}

	encoded := w.Bytes()
	fds := w.FDs()

	// Decode using Reader
	var r Reader
	r.Init(encoded, fds)

	hdr, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}

	if hdr.ObjectID != 1 {
		t.Errorf("expected ObjectID 1, got %d", hdr.ObjectID)
	}
	if hdr.Opcode != 0 {
		t.Errorf("expected Opcode 0, got %d", hdr.Opcode)
	}
	if int(hdr.Size) != len(encoded) {
		t.Errorf("expected Size %d, got %d", len(encoded), hdr.Size)
	}

	objID, err := r.ReadUint32()
	if err != nil || objID != 42 {
		t.Errorf("expected uint32 42, got %d (err %v)", objID, err)
	}

	code, err := r.ReadUint32()
	if err != nil || code != 1 {
		t.Errorf("expected code 1, got %d (err %v)", code, err)
	}

	msg, err := r.ReadString()
	if err != nil || msg != "invalid object" {
		t.Errorf("expected string 'invalid object', got '%s' (err %v)", msg, err)
	}

	fixed, err := r.ReadFixed()
	if err != nil || math.Abs(fixed.ToFloat64()-12.75) > 0.001 {
		t.Errorf("expected fixed 12.75, got %f (err %v)", fixed.ToFloat64(), err)
	}

	fd, err := r.ReadFD()
	if err != nil || fd != 9 {
		t.Errorf("expected fd 9, got %d (err %v)", fd, err)
	}
}

func BenchmarkWireEncoding(b *testing.B) {
	var rawBuf [512]byte
	var rawFDs [4]int
	var w Writer

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		w.Init(rawBuf[:], rawFDs[:])
		start := w.BeginMessage(1, 2)
		_ = w.WriteUint32(100)
		_ = w.WriteInt32(-50)
		_ = w.WriteFixed(ToFixed(100.5))
		_ = w.EndMessage(start, 1, 2)
	}
}

func BenchmarkWireDecoding(b *testing.B) {
	var rawBuf [512]byte
	var rawFDs [4]int
	var w Writer
	w.Init(rawBuf[:], rawFDs[:])
	start := w.BeginMessage(1, 2)
	_ = w.WriteUint32(100)
	_ = w.WriteInt32(-50)
	_ = w.WriteFixed(ToFixed(100.5))
	_ = w.EndMessage(start, 1, 2)
	data := w.Bytes()

	var r Reader
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r.Init(data, nil)
		_, _ = r.ReadHeader()
		_, _ = r.ReadUint32()
		_, _ = r.ReadInt32()
		_, _ = r.ReadFixed()
	}
}
