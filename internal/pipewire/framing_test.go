package pipewire

import (
	"testing"

	"bloxwm/internal/pipewire/spa"
)

// TestHeaderRoundTrip builds a message, decodes it, and verifies the layout.
func TestHeaderRoundTrip(t *testing.T) {
	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 256), &fdSlots)

	w.Begin(7, 3)
	w.PutU32(0xdeadbeef)
	w.Finish()

	buf := w.Bytes()
	h, total, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if h.ID != 7 || h.Opcode != 3 {
		t.Errorf("header id/op = %d/%d", h.ID, h.Opcode)
	}
	if h.NFDs != 0 {
		t.Errorf("n_fds = %d, want 0", h.NFDs)
	}
	if total != len(buf) {
		t.Errorf("total = %d, want %d", total, len(buf))
	}
	if leU32(buf[16:20]) != 0xdeadbeef {
		t.Errorf("body payload mismatch: % x", buf[20:24])
	}
}

// TestFramingWithPod verifies header.Size covers the pod span so the
// reader can slice the pod buffer directly.
func TestFramingWithPod(t *testing.T) {
	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)

	w.Begin(1, 2)
	w.PutU32(4)           // pod body size
	w.PutU32(spa.TypeInt) // pod type
	w.PutU32(0x42)        // int value
	w.PutU32(0)           // pad to 8
	w.Finish()

	buf := w.Bytes()
	h, total, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	pod, podTotal, err := spa.ReadPod(buf[HeaderSize:total])
	if err != nil {
		t.Fatalf("ReadPod: %v", err)
	}
	if pod.Type != spa.TypeInt || leU32(pod.Body) != 0x42 {
		t.Errorf("pod = %+v", pod)
	}
	if HeaderSize+podTotal != total {
		t.Errorf("pod span %d != header size %d", podTotal, h.Size)
	}
}

// TestSeqIncrements verifies per-message sequence numbers.
func TestSeqIncrements(t *testing.T) {
	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)

	w.Begin(1, 0)
	w.Finish()
	w.Begin(1, 1)
	w.Finish()
	w.Begin(1, 2)
	w.Finish()

	if h0, _, _ := DecodeHeader(w.Bytes()); h0.Seq != 1 {
		t.Errorf("first seq = %d", h0.Seq)
	}
	if h1, _, _ := DecodeHeader(w.Bytes()[HeaderSize:]); h1.Seq != 2 {
		t.Errorf("second seq = %d", h1.Seq)
	}
	if h2, _, _ := DecodeHeader(w.Bytes()[2*HeaderSize:]); h2.Seq != 3 {
		t.Errorf("third seq = %d", h2.Seq)
	}
}

// TestStageFD verifies ancillary fd staging.
func TestStageFD(t *testing.T) {
	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 64), &fdSlots)
	w.StageFD(11)
	w.StageFD(12)
	if got := w.FDs(); len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Errorf("fds = %v", got)
	}
	w.Reset()
	if len(w.FDs()) != 0 {
		t.Error("fds not cleared on reset")
	}
}

// TestShortHeaderErrors covers framing error paths.
func TestShortHeaderErrors(t *testing.T) {
	if _, _, err := DecodeHeader(make([]byte, 15)); err == nil {
		t.Error("expected ErrShortHeader")
	}
	full := make([]byte, 32)
	full[4] = 0x20 // size in low 24 bits of word 1
	if _, _, err := DecodeHeader(full); err == nil {
		t.Error("expected ErrBodyOverrun")
	}
}

// BenchmarkFramingFrame verifies the zero-alloc frame path.
func BenchmarkFramingFrame(b *testing.B) {
	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 256), &fdSlots)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Reset()
		w.Begin(3, 5)
		w.PutU32(1)
		w.PutU32(2)
		w.Finish()
	}
}
