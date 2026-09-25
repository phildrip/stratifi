package input

import (
	"sync"
	"testing"
	"unsafe"
)

func TestInputEventSize(t *testing.T) {
	// Verify that InputEvent is exactly 64 bytes (one standard CPU cache line)
	size := unsafe.Sizeof(InputEvent{})
	if size != 64 {
		t.Fatalf("expected InputEvent size of 64 bytes, got %d", size)
	}
}

func TestRingBufferBasicPushPop(t *testing.T) {
	rb := NewRingBuffer(16)
	if rb.Cap() != 16 {
		t.Fatalf("expected capacity 16, got %d", rb.Cap())
	}
	if !rb.IsEmpty() {
		t.Fatalf("expected empty buffer")
	}

	ev1 := InputEvent{Type: EventPointerMotion, Code: 1, X: 100.5, Y: 200.5}
	ev2 := InputEvent{Type: EventPointerButton, Code: 272, State: 1}

	if !rb.Push(ev1) {
		t.Fatalf("push ev1 failed")
	}
	if !rb.Push(ev2) {
		t.Fatalf("push ev2 failed")
	}

	if rb.Len() != 2 {
		t.Fatalf("expected Len=2, got %d", rb.Len())
	}

	out1, ok := rb.Pop()
	if !ok || out1.Type != EventPointerMotion || out1.X != 100.5 {
		t.Fatalf("unexpected pop 1: %+v", out1)
	}

	out2, ok := rb.Pop()
	if !ok || out2.Type != EventPointerButton || out2.Code != 272 {
		t.Fatalf("unexpected pop 2: %+v", out2)
	}

	if !rb.IsEmpty() {
		t.Fatalf("expected empty after popping all elements")
	}
}

func TestRingBufferOverflowAndWrapAround(t *testing.T) {
	capacity := 16
	rb := NewRingBuffer(capacity)

	// Fill buffer completely
	for i := 0; i < capacity; i++ {
		ok := rb.Push(InputEvent{Code: uint32(i)})
		if !ok {
			t.Fatalf("push failed at index %d", i)
		}
	}

	if !rb.IsFull() {
		t.Fatalf("expected buffer to be full")
	}

	// Next push should fail (overflow protection)
	if rb.Push(InputEvent{Code: 999}) {
		t.Fatalf("expected push to fail on full buffer")
	}

	// Pop 4 items
	for i := 0; i < 4; i++ {
		ev, ok := rb.Pop()
		if !ok || ev.Code != uint32(i) {
			t.Fatalf("expected code %d, got %+v", i, ev)
		}
	}

	// Push 4 new items to test circular wrap-around
	for i := 0; i < 4; i++ {
		ok := rb.Push(InputEvent{Code: uint32(100 + i)})
		if !ok {
			t.Fatalf("push after wrap failed at %d", i)
		}
	}

	// Drain remainder
	var batch [32]InputEvent
	n := rb.Drain(batch[:])
	if n != capacity {
		t.Fatalf("expected to drain %d items, got %d", capacity, n)
	}

	if !rb.IsEmpty() {
		t.Fatalf("expected empty buffer after drain")
	}
}

func TestRingBufferConcurrentProducers(t *testing.T) {
	rb := NewRingBuffer(1024)
	numGoroutines := 8
	eventsPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				_ = rb.Push(InputEvent{
					DeviceID: uint32(gid),
					Code:     uint32(i),
				})
			}
		}(g)
	}

	wg.Wait()

	if rb.Len() != numGoroutines*eventsPerGoroutine {
		t.Fatalf("expected %d events, got %d", numGoroutines*eventsPerGoroutine, rb.Len())
	}
}

func BenchmarkRingBufferPushPop(b *testing.B) {
	rb := NewRingBuffer(1024)
	ev := InputEvent{
		Type:  EventPointerMotion,
		Code:  1,
		State: 1,
		X:     320.5,
		Y:     240.5,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		rb.Push(ev)
		_, _ = rb.Pop()
	}
}

func BenchmarkRingBufferDrain(b *testing.B) {
	rb := NewRingBuffer(1024)
	ev := InputEvent{Type: EventPointerMotion, X: 1.0, Y: 2.0}

	var batch [64]InputEvent

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for j := 0; j < 64; j++ {
			rb.Push(ev)
		}
		rb.Drain(batch[:])
	}
}
