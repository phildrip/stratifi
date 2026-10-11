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

// TestSplitRequestIsDispatched: a request arriving in two chunks must still be
// handled. Before framing was fixed, the partial message either errored the
// client out or was dropped, so no reply ever came back.
func TestSplitRequestIsDispatched(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-split-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	go func() { _ = srv.Run() }()
	time.Sleep(30 * time.Millisecond)

	netConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer netConn.Close()
	rawConn, err := netConn.(*net.UnixConn).SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var fd int
	_ = rawConn.Control(func(f uintptr) { fd = int(f) })

	// wl_display.sync: object 1, opcode 0, new callback id 2.
	var buf [64]byte
	var w wire.Writer
	w.Init(buf[:], nil)
	start := w.BeginMessage(1, 0)
	_ = w.WriteUint32(2)
	if err := w.EndMessage(start, 1, 0); err != nil {
		t.Fatalf("EndMessage: %v", err)
	}
	msg := make([]byte, len(w.Bytes()))
	copy(msg, w.Bytes())

	// Deliver it in two pieces: the reply must still arrive.
	if err := unix.Sendmsg(fd, msg[:5], nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg chunk 1: %v", err)
	}
	if err := unix.Sendmsg(fd, msg[5:], nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg chunk 2: %v", err)
	}

	// Expect wl_callback.done on object 2 (opcode 0).
	deadline := time.Now().Add(2 * time.Second)
	var pending []byte
	var inBuf [4096]byte
	for time.Now().Before(deadline) {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, _ := unix.Poll(pfd, 50); n == 0 {
			continue
		}
		nr, err := unix.Read(fd, inBuf[:])
		if nr <= 0 || err != nil {
			break
		}
		pending = append(pending, inBuf[:nr]...)
		for len(pending) >= 8 {
			obj := binary.LittleEndian.Uint32(pending[0:4])
			word := binary.LittleEndian.Uint32(pending[4:8])
			size := int(word >> 16)
			op := word & 0xFFFF
			if size < 8 || size > len(pending) {
				break
			}
			if obj == 2 && op == 0 {
				return // reply received: the split request was dispatched
			}
			pending = pending[size:]
		}
	}
	t.Fatal("no wl_callback.done reply: split request was not dispatched")
}
