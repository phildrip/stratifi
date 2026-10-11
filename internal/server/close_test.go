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

// TestSendToplevelClose: a graceful close request must arrive as
// xdg_toplevel.close on the wire, and unknown ids must send nothing.
func TestSendToplevelClose(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-close-0")
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

	var w wire.Writer
	var sendBuf [512]byte
	var sendFDs [4]int
	w.Init(sendBuf[:], sendFDs[:])
	send := func(objectID uint32, opcode uint16, fill func()) {
		start := w.BeginMessage(objectID, opcode)
		fill()
		if err := w.EndMessage(start, objectID, opcode); err != nil {
			t.Fatalf("EndMessage: %v", err)
		}
		if err := unix.Sendmsg(fd, w.Bytes(), nil, nil, 0); err != nil {
			t.Fatalf("Sendmsg: %v", err)
		}
		w.Reset()
	}

	const compID, wmID, surfID, xdgID, topID uint32 = 3, 4, 10, 20, 30
	send(1, 1, func() { _ = w.WriteUint32(2) })
	send(2, 0, func() {
		_ = w.WriteUint32(GlobalCompositor)
		_ = w.WriteString("wl_compositor")
		_ = w.WriteUint32(4)
		_ = w.WriteUint32(compID)
	})
	send(2, 0, func() {
		_ = w.WriteUint32(GlobalXdgWmBase)
		_ = w.WriteString("xdg_wm_base")
		_ = w.WriteUint32(3)
		_ = w.WriteUint32(wmID)
	})
	send(compID, 0, func() { _ = w.WriteUint32(surfID) })
	send(wmID, 2, func() {
		_ = w.WriteUint32(xdgID)
		_ = w.WriteUint32(surfID)
	})
	send(xdgID, 1, func() { _ = w.WriteUint32(topID) })

	var client *Client
	for i := 0; i < 200 && client == nil; i++ {
		client = srv.GetClient(1)
		if client == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if client == nil {
		t.Fatal("client never registered")
	}
	// Drain the registry/configure burst.
	drainWire(fd, 300*time.Millisecond)

	client.SendToplevelClose(topID)
	if !sawClose(fd, topID, 500*time.Millisecond) {
		t.Fatal("no xdg_toplevel.close on the wire")
	}

	// Unknown ids are silent (no protocol error possible).
	drainWire(fd, 100*time.Millisecond)
	client.SendToplevelClose(9999)
	if sawClose(fd, 9999, 200*time.Millisecond) {
		t.Fatal("close sent for unknown object")
	}
}

func drainWire(fd int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	var buf [4096]byte
	for time.Now().Before(deadline) {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, _ := unix.Poll(pfd, 20)
		if n == 0 {
			continue
		}
		if nr, _ := unix.Read(fd, buf[:]); nr <= 0 {
			return
		}
	}
}

// sawClose reports whether an xdg_toplevel.close (opcode 1) for id arrives.
func sawClose(fd int, id uint32, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	var buf [4096]byte
	var pending []byte
	for time.Now().Before(deadline) {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, _ := unix.Poll(pfd, 20)
		if n == 0 {
			continue
		}
		nr, err := unix.Read(fd, buf[:])
		if nr <= 0 || err != nil {
			return false
		}
		pending = append(pending, buf[:nr]...)
		for len(pending) >= 8 {
			obj := binary.LittleEndian.Uint32(pending[0:4])
			word := binary.LittleEndian.Uint32(pending[4:8])
			size := int(word >> 16)
			op := word & 0xFFFF
			if size < 8 || size > len(pending) {
				break
			}
			if obj == id && op == 1 {
				return true
			}
			pending = pending[size:]
		}
	}
	return false
}
