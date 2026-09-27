package nested

import (
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRunEventLoopNotifiesOnParentDisconnect: when the parent's socket ends
// without an xdg_toplevel.close, the event loop must exit AND tell the
// caller — otherwise bloxwm keeps running with a dead parent.
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
