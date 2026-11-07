package alignio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAlignUp(t *testing.T) {
	cases := []struct{ n, align, want int }{
		{0, 4, 0}, {1, 4, 4}, {3, 4, 4}, {4, 4, 4}, {5, 4, 8},
		{0, 8, 0}, {7, 8, 8}, {8, 8, 8}, {9, 8, 16},
		{6, 2, 6}, {7, 2, 8}, {3, 1, 3},
	}
	for _, c := range cases {
		if got := AlignUp(c.n, c.align); got != c.want {
			t.Errorf("AlignUp(%d,%d) = %d, want %d", c.n, c.align, got, c.want)
		}
		if got := Pad(c.n, c.align); got != c.want-c.n {
			t.Errorf("Pad(%d,%d) = %d, want %d", c.n, c.align, got, c.want-c.n)
		}
	}
}

func TestPadTo(t *testing.T) {
	if got := PadTo([]byte("abc"), 4); len(got) != 4 || string(got) != "abc\x00" {
		t.Errorf("PadTo(abc,4) = %q, want abc\\x00", got)
	}
	if got := PadTo([]byte("abcd"), 4); len(got) != 4 {
		t.Errorf("PadTo(abcd,4) = %q, want unchanged", got)
	}
	if got := PadTo(nil, 8); len(got) != 0 {
		t.Errorf("PadTo(nil,8) = %q, want empty", got)
	}
}

func TestFDStage(t *testing.T) {
	var backing [2]int
	var s FDStage
	s.Init(backing[:])

	if s.Count() != 0 || len(s.FDs()) != 0 {
		t.Fatalf("initial stage not empty: count=%d", s.Count())
	}
	if err := s.Stage(10); err != nil {
		t.Fatalf("Stage(10): %v", err)
	}
	if err := s.Stage(20); err != nil {
		t.Fatalf("Stage(20): %v", err)
	}
	if err := s.Stage(30); !errors.Is(err, ErrFDCapacityExceeded) {
		t.Fatalf("Stage(30) overflow err = %v, want ErrFDCapacityExceeded", err)
	}
	got := s.FDs()
	if len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Fatalf("FDs() = %v, want [10 20]", got)
	}

	s.Reset()
	if s.Count() != 0 || len(s.FDs()) != 0 {
		t.Fatalf("after Reset: count=%d fds=%v", s.Count(), s.FDs())
	}
}

func TestFDQueueCompactionAndOverflowClose(t *testing.T) {
	var backing [2]int
	var q FDQueue
	q.Init(backing[:])

	// Push two descriptors via RecvOOB, consume one, then push a third to
	// trigger lazy compaction.
	oob1 := unix.UnixRights(101, 102)
	if err := q.RecvOOB(oob1); err != nil {
		t.Fatalf("RecvOOB: %v", err)
	}
	if active := q.Active(); len(active) != 2 || active[0] != 101 || active[1] != 102 {
		t.Fatalf("Active() = %v, want [101 102]", active)
	}

	q.Consume(1)
	if active := q.Active(); len(active) != 1 || active[0] != 102 {
		t.Fatalf("Active() after Consume(1) = %v, want [102]", active)
	}

	// Pushing 103 must compact 102 to slot 0 and place 103 in slot 1.
	q.Push(103)
	if active := q.Active(); len(active) != 2 || active[0] != 102 || active[1] != 103 {
		t.Fatalf("Active() after compacting Push = %v, want [102 103]", active)
	}

	// Overflow when full must close the excess descriptor rather than leak it.
	var pipeFDs [2]int
	if err := unix.Pipe(pipeFDs[:]); err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer unix.Close(pipeFDs[0])

	q.Push(pipeFDs[1]) // queue is full (2/2), so pipeFDs[1] must be closed
	if _, err := unix.Write(pipeFDs[1], []byte("x")); !errors.Is(err, unix.EBADF) {
		_ = unix.Close(pipeFDs[1])
		t.Fatalf("overflow fd was not closed: Write err = %v, want EBADF", err)
	}

	q.Consume(2)
	if len(q.Active()) != 0 {
		t.Fatalf("expected empty queue after consuming all, got %v", q.Active())
	}
}

// testSpan4 decodes a 4-byte little-endian total frame length header.
func testSpan4(buf []byte) (int, error) {
	n := int(binary.LittleEndian.Uint32(buf[:4]))
	if n < 4 || n > 1024 {
		return 0, fmt.Errorf("bad frame size %d", n)
	}
	return n, nil
}

func makeFrame(payload string) []byte {
	total := 4 + len(payload)
	out := make([]byte, total)
	binary.LittleEndian.PutUint32(out[:4], uint32(total))
	copy(out[4:], payload)
	return out
}

func TestAccumulatorSplitAndCoalesced(t *testing.T) {
	var backing [256]byte
	var acc Accumulator
	acc.Init(backing[:])

	f1 := makeFrame("alpha")
	f2 := makeFrame("bravo-charlie")

	// 1. Partial header (2 bytes of 4-byte header).
	acc.Append(f1[:2])
	if acc.HasNext(4, testSpan4) {
		t.Fatal("HasNext true on partial header")
	}
	if msg, ok, err := acc.Next(4, testSpan4); err != nil || ok || msg != nil {
		t.Fatalf("Next on partial header = (%v, %v, %v)", msg, ok, err)
	}

	// 2. Remainder of header + partial body.
	acc.Append(f1[2:6])
	if msg, ok, err := acc.Next(4, testSpan4); err != nil || ok || msg != nil {
		t.Fatalf("Next on partial body = (%v, %v, %v)", msg, ok, err)
	}

	// 3. Rest of f1 coalesced with all of f2.
	acc.Append(append(f1[6:], f2...))
	if !acc.HasNext(4, testSpan4) {
		t.Fatal("HasNext false when complete frame buffered")
	}

	msg1, ok, err := acc.Next(4, testSpan4)
	if err != nil || !ok || string(msg1[4:]) != "alpha" {
		t.Fatalf("first frame = %q ok=%v err=%v", msg1, ok, err)
	}

	msg2, ok, err := acc.Next(4, testSpan4)
	if err != nil || !ok || string(msg2[4:]) != "bravo-charlie" {
		t.Fatalf("second frame = %q ok=%v err=%v", msg2, ok, err)
	}

	if acc.Len() != 0 || acc.HasNext(4, testSpan4) {
		t.Fatalf("accumulator not empty after draining: len=%d", acc.Len())
	}
}

func TestAccumulatorMalformedSpan(t *testing.T) {
	var acc Accumulator
	var bad [4]byte
	binary.LittleEndian.PutUint32(bad[:], 2) // < minHeader(4)
	acc.Append(bad[:])

	if _, ok, err := acc.Next(4, testSpan4); err == nil || ok {
		t.Fatalf("expected error on malformed span, got ok=%v err=%v", ok, err)
	}
}

func BenchmarkAccumulatorZeroAlloc(b *testing.B) {
	var backing [256]byte
	var acc Accumulator
	acc.Init(backing[:])
	frame := makeFrame("benchmark-payload")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acc.Append(frame)
		msg, ok, err := acc.Next(4, testSpan4)
		if err != nil || !ok || len(msg) != len(frame) {
			b.Fatalf("Next failed: ok=%v err=%v", ok, err)
		}
	}
}
