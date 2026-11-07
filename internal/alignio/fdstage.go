package alignio

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ErrFDCapacityExceeded is returned by FDStage.Stage when the caller-supplied
// descriptor backing slice is full.
var ErrFDCapacityExceeded = fmt.Errorf("fd stage capacity exceeded")

// FDStage stages outbound file descriptors into a caller-supplied fixed slice
// for SCM_RIGHTS delivery without heap allocations.
type FDStage struct {
	fds   []int
	count int
}

// Init binds the stage to backing (its full length is the capacity cap).
func (s *FDStage) Init(backing []int) {
	s.fds = backing
	s.count = 0
}

// Stage queues fd for outbound SCM_RIGHTS delivery. Returns
// ErrFDCapacityExceeded when the backing slice is full.
func (s *FDStage) Stage(fd int) error {
	if s.count >= len(s.fds) {
		return ErrFDCapacityExceeded
	}
	s.fds[s.count] = fd
	s.count++
	return nil
}

// FDs returns the currently staged file descriptors.
func (s *FDStage) FDs() []int {
	return s.fds[:s.count]
}

// Count returns the number of staged file descriptors.
func (s *FDStage) Count() int {
	return s.count
}

// Cap returns the maximum number of descriptors this stage can hold.
func (s *FDStage) Cap() int {
	return len(s.fds)
}

// Reset clears the staged count while retaining the backing slice.
func (s *FDStage) Reset() {
	s.count = 0
}

// FDQueue is a bounded FIFO queue of inbound SCM_RIGHTS file descriptors
// backed by a caller-supplied slice. Consumed slots are compacted lazily when
// the tail reaches the end of the backing slice; any descriptor that still
// exceeds capacity is closed immediately so an erratic peer cannot leak fds.
type FDQueue struct {
	fds   []int
	start int
	end   int
}

// Init binds the queue to backing (its full length is the capacity cap).
func (q *FDQueue) Init(backing []int) {
	q.fds = backing
	q.start = 0
	q.end = 0
}

// Push appends fd to the queue, compacting consumed slots if needed and
// closing fd if the queue is at full capacity.
func (q *FDQueue) Push(fd int) {
	if q.end == len(q.fds) && q.start > 0 {
		copy(q.fds, q.fds[q.start:q.end])
		q.end -= q.start
		q.start = 0
	}
	if q.end < len(q.fds) {
		q.fds[q.end] = fd
		q.end++
	} else {
		_ = unix.Close(fd)
	}
}

// RecvOOB parses socket control messages from oob and pushes any SCM_RIGHTS
// descriptors into the queue in FIFO order.
func (q *FDQueue) RecvOOB(oob []byte) error {
	if len(oob) == 0 {
		return nil
	}
	cmsgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return fmt.Errorf("failed to parse socket control message: %w", err)
	}
	for i := range cmsgs {
		cmsg := &cmsgs[i]
		if cmsg.Header.Level == unix.SOL_SOCKET && cmsg.Header.Type == unix.SCM_RIGHTS {
			fds, err := unix.ParseUnixRights(cmsg)
			if err != nil {
				return fmt.Errorf("failed to parse SCM_RIGHTS: %w", err)
			}
			for _, fd := range fds {
				q.Push(fd)
			}
		}
	}
	return nil
}

// Active returns the unconsumed descriptor slice in FIFO order.
func (q *FDQueue) Active() []int {
	return q.fds[q.start:q.end]
}

// Consume advances the queue past n consumed descriptors.
func (q *FDQueue) Consume(n int) {
	if n <= 0 {
		return
	}
	q.start += n
	if q.start >= q.end {
		q.start, q.end = 0, 0
	}
}
