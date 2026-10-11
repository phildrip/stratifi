package server

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/wire"
)

// TestIdleNotification: a client registers an idle notification and gets
// idled after the timeout with no input, then resumed when input arrives.
func TestIdleNotification(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-idle-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.SetOnToplevel(func(c *Client, _ *XdgToplevel, xdgSurfaceID uint32) {})
	go func() { _ = srv.Run() }()
	time.Sleep(30 * time.Millisecond)

	netConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer netConn.Close()
	rawConn, _ := netConn.(*net.UnixConn).SyscallConn()
	var fd int
	_ = rawConn.Control(func(f uintptr) { fd = int(f) })

	var w wire.Writer
	var sb [1024]byte
	var sfds [4]int
	w.Init(sb[:], sfds[:])
	send := func(msgs ...func()) {
		w.Reset()
		for _, m := range msgs {
			m()
		}
		if err := unix.Sendmsg(fd, w.Bytes(), nil, nil, 0); err != nil {
			t.Fatalf("Sendmsg: %v", err)
		}
	}
	msg := func(obj uint32, op uint16, fill func()) func() {
		return func() {
			s := w.BeginMessage(obj, op)
			fill()
			if err := w.EndMessage(s, obj, op); err != nil {
				t.Fatalf("EndMessage: %v", err)
			}
		}
	}
	const notifierID, notifID = 5, 6
	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalIdleNotifier)
			_ = w.WriteString("ext_idle_notifier_v1")
			_ = w.WriteUint32(2)
			_ = w.WriteUint32(notifierID)
		}),
	)
	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	// Drain the initial globals burst so the reader sees a clean stream.
	{
		var dbuf [8192]byte
		for {
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 100); n == 0 {
				break
			}
			if nr, _ := unix.Read(fd, dbuf[:]); nr <= 0 {
				break
			}
		}
	}
	// get_idle_notification(id, timeout=150ms, seat=0).
	send(msg(notifierID, 1, func() { _ = w.WriteUint32(notifID); _ = w.WriteUint32(150); _ = w.WriteUint32(0) }))

	// Collect notification events (idled op 0, resumed op 1) on notifID.
	le := binary.LittleEndian
	events := make(chan uint32, 8)
	stop := make(chan struct{})
	go func() {
		var buf [4096]byte
		var pending []byte
		for {
			select {
			case <-stop:
				return
			default:
			}
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 50); n == 0 {
				continue
			}
			nr, _ := unix.Read(fd, buf[:])
			if nr <= 0 {
				continue
			}
			pending = append(pending, buf[:nr]...)
			for len(pending) >= 8 {
				obj := le.Uint32(pending[0:4])
				word := le.Uint32(pending[4:8])
				size := int(word >> 16)
				op := word & 0xFFFF
				if size < 8 || size > len(pending) {
					break
				}
				if obj == notifID {
					events <- op
				}
				pending = pending[size:]
			}
		}
	}()
	defer close(stop)

	// idled (op 0) after the timeout.
	if op := waitEvent(t, events, 3*time.Second); op != 0 {
		t.Fatalf("first event op = %d, want idled (0)", op)
	}

	// Input activity -> resumed (op 1).
	srv.NotifyInputActivity()
	if op := waitEvent(t, events, 3*time.Second); op != 1 {
		t.Fatalf("event after activity op = %d, want resumed (1)", op)
	}
}

func waitEvent(t *testing.T, ch chan uint32, d time.Duration) uint32 {
	t.Helper()
	select {
	case op := <-ch:
		return op
	case <-time.After(d):
		t.Fatal("timed out waiting for an idle event")
		return 0xFFFF
	}
}
