package nested

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRunEventLoopNotifiesOnParentDisconnect: when the parent's socket ends
// without an xdg_toplevel.close, the event loop must exit AND tell the
// caller — otherwise stratifi keeps running with a dead parent.
func TestRunEventLoopNotifiesOnParentDisconnect(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	child, parent := fds[0], fds[1]

	var disconnected atomic.Bool
	b := &Backend{rawFD: child}
	b.callbacks.OnDisconnect = func() { disconnected.Store(true) }

	done := make(chan struct{})
	go func() {
		b.RunEventLoop()
		close(done)
	}()

	// The parent goes away (crash, kill, compositor exit).
	_ = unix.Close(parent)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = unix.Close(child)
		t.Fatal("event loop did not exit after parent disconnect")
	}
	if !disconnected.Load() {
		t.Fatal("OnDisconnect did not fire")
	}
	_ = unix.Close(child)
}

// TestRunEventLoopStaysQuietOnIntentionalClose: a normal Close (parent sent
// close, or shutdown) must not report a disconnect.
func TestRunEventLoopStaysQuietOnIntentionalClose(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	child, parent := fds[0], fds[1]

	var disconnected atomic.Bool
	b := &Backend{rawFD: child}
	b.callbacks.OnDisconnect = func() { disconnected.Store(true) }

	done := make(chan struct{})
	go func() {
		b.RunEventLoop()
		close(done)
	}()

	b.closed.Store(true) // intentional shutdown
	_ = unix.Close(parent)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = unix.Close(child)
		t.Fatal("event loop did not exit after Close")
	}
	if disconnected.Load() {
		t.Error("OnDisconnect fired for an intentional close")
	}
	_ = unix.Close(child)
}

// TestPointerMotionReachesCallback: the nested backend parses
// wl_pointer.motion from the parent and delivers surface-local coordinates
// to the callback that feeds the router (and thus the cursor position).
func TestPointerMotionReachesCallback(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	child, parent := fds[0], fds[1]

	var mu sync.Mutex
	var gx, gy float64
	var fired bool
	b := &Backend{rawFD: child}
	b.pointerID = 42
	b.callbacks.OnPointerMotion = func(x, y float64) {
		mu.Lock()
		gx, gy, fired = x, y, true
		mu.Unlock()
	}

	done := make(chan struct{})
	go func() { b.RunEventLoop(); close(done) }()
	defer func() {
		b.closed.Store(true)
		_ = unix.Close(child)
		<-done
	}()

	// wl_pointer.motion: obj, size<<16|opcode(2), time, surface_x, surface_y.
	msg := make([]byte, 20)
	le := binary.LittleEndian
	le.PutUint32(msg[0:], 42)
	le.PutUint32(msg[4:], uint32(20)<<16|2)
	le.PutUint32(msg[8:], 0)                // time
	le.PutUint32(msg[12:], uint32(100*256)) // surface_x = 100.0 (24.8 fixed)
	le.PutUint32(msg[16:], uint32(50*256))  // surface_y = 50.0
	if _, err := unix.Write(parent, msg); err != nil {
		t.Fatalf("write motion: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		f := fired
		mu.Unlock()
		if f {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !fired {
		t.Fatal("OnPointerMotion did not fire")
	}
	if gx != 100 || gy != 50 {
		t.Errorf("motion = (%.0f,%.0f), want (100,50)", gx, gy)
	}
	_ = unix.Close(parent)
}
