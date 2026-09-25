package input

import (
	"sync"
	"sync/atomic"
)

// RingBuffer is a high-throughput, cache-conscious contiguous circular buffer
// storing fixed-size 64-byte InputEvent structs.
// It uses power-of-two capacity for fast bitwise indexing and operates with
// strictly zero heap allocations on all push, pop, and drain paths.
type RingBuffer struct {
	events []InputEvent
	mask   uint32
	head   atomic.Uint32 // written by producer(s)
	tail   atomic.Uint32 // written by consumer(s)
	mu     sync.Mutex    // guards concurrent multi-producer pushes
}

// NewRingBuffer allocates an event ring buffer of the requested capacity.
// Capacity is automatically rounded up to the nearest power of two.
func NewRingBuffer(minCap int) *RingBuffer {
	if minCap < 16 {
		minCap = 16
	}

	// Round up to nearest power of 2
	capacity := 1
	for capacity < minCap {
		capacity <<= 1
	}

	return &RingBuffer{
		events: make([]InputEvent, capacity),
		mask:   uint32(capacity - 1),
	}
}

// Cap returns the total event capacity of the ring buffer.
func (rb *RingBuffer) Cap() int {
	return len(rb.events)
}

// Len returns the current number of queued unconsumed events.
func (rb *RingBuffer) Len() int {
	h := rb.head.Load()
	t := rb.tail.Load()
	return int(h - t)
}

// IsFull returns true if no additional events can be accepted without dropping.
func (rb *RingBuffer) IsFull() bool {
	return rb.Len() >= len(rb.events)
}

// IsEmpty returns true if there are no pending events.
func (rb *RingBuffer) IsEmpty() bool {
	return rb.head.Load() == rb.tail.Load()
}

// Push appends an event to the ring buffer.
// If the buffer is full, the event is rejected and Push returns false (non-blocking).
// Push is zero-allocating (0 B/op, 0 allocs/op).
func (rb *RingBuffer) Push(ev InputEvent) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	h := rb.head.Load()
	t := rb.tail.Load()

	if h-t >= uint32(len(rb.events)) {
		return false // Buffer full
	}

	rb.events[h&rb.mask] = ev
	rb.head.Store(h + 1)
	return true
}

// Pop extracts the oldest unconsumed event.
// Returns false if the buffer is empty.
// Pop is zero-allocating (0 B/op, 0 allocs/op).
func (rb *RingBuffer) Pop() (InputEvent, bool) {
	t := rb.tail.Load()
	h := rb.head.Load()

	if t == h {
		return InputEvent{}, false // Empty
	}

	ev := rb.events[t&rb.mask]
	rb.tail.Store(t + 1)
	return ev, true
}

// Drain copies up to len(dst) queued events into dst in a single contiguous batch.
// Returns the number of events drained.
// Drain is zero-allocating (0 B/op, 0 allocs/op).
func (rb *RingBuffer) Drain(dst []InputEvent) int {
	t := rb.tail.Load()
	h := rb.head.Load()

	avail := int(h - t)
	if avail == 0 {
		return 0
	}

	n := len(dst)
	if n > avail {
		n = avail
	}

	for i := 0; i < n; i++ {
		dst[i] = rb.events[(t+uint32(i))&rb.mask]
	}

	rb.tail.Store(t + uint32(n))
	return n
}

// Reset clears all queued events instantaneously without deallocating memory.
func (rb *RingBuffer) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head.Store(0)
	rb.tail.Store(0)
}
