package server

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/protocol/xwaylandshell"
	"stratifi/internal/wire"
)

func TestXwaylandShellV1SerialAssociation(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-xwl-0")
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

	const (
		compID     uint32 = 3
		xwlShellID uint32 = 4
		surfID     uint32 = 5
		xwlSurfID  uint32 = 6
		serialLo   uint32 = 0x55667788
		serialHi   uint32 = 0x11223344
		wantSerial uint64 = uint64(serialHi)<<32 | uint64(serialLo)
	)

	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalXwaylandShell)
			_ = w.WriteString(xwaylandshell.XwaylandShellV1Interface)
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(xwlShellID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(surfID) }),
		msg(xwlShellID, xwaylandshell.XwaylandShellV1OpGetXwaylandSurface, func() {
			_ = w.WriteUint32(xwlSurfID)
			_ = w.WriteUint32(surfID)
		}),
		msg(xwlSurfID, xwaylandshell.XwaylandSurfaceV1OpSetSerial, func() {
			_ = w.WriteUint32(serialLo)
			_ = w.WriteUint32(serialHi)
		}),
		msg(surfID, 6, func() {}), // wl_surface.commit
	)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cid, sid, ok := srv.ResolveXwaylandSerial(wantSerial); ok {
			if cid == 0 || sid != surfID {
				t.Fatalf("ResolveXwaylandSerial(%#x) = (%d, %d, %v), want (non-zero, %d, true)", wantSerial, cid, sid, ok, surfID)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for ResolveXwaylandSerial(%#x)", wantSerial)
}
