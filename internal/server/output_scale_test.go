package server

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/protocol/wayland"
	"stratifi/internal/protocol/xdgoutput"
	"stratifi/internal/wire"
)

func TestOutputScaleAndLogicalSize(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-scale-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	srv.SetOutput(Output{Width: 2560, Height: 1440, RefreshHz: 60, Scale: 1.25})
	out := srv.Output()
	if got := out.IntScale(); got != 2 {
		t.Fatalf("IntScale() = %d, want 2", got)
	}
	if lw, lh := out.LogicalSize(); lw != 2048 || lh != 1152 {
		t.Fatalf("LogicalSize() = (%d, %d), want (2048, 1152)", lw, lh)
	}

	go func() { _ = srv.Run() }()
	time.Sleep(20 * time.Millisecond)

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
	msg := func(obj uint32, op uint16, fill func()) {
		s := w.BeginMessage(obj, op)
		fill()
		if err := w.EndMessage(s, obj, op); err != nil {
			t.Fatalf("EndMessage: %v", err)
		}
	}

	const outputID, xdgOutMgrID, xdgOutID = 10, 11, 12
	w.Reset()
	msg(1, 1, func() { _ = w.WriteUint32(2) }) // wl_display.get_registry(2)
	msg(2, 0, func() {
		_ = w.WriteUint32(GlobalOutput)
		_ = w.WriteString("wl_output")
		_ = w.WriteUint32(4)
		_ = w.WriteUint32(outputID)
	})
	msg(2, 0, func() {
		_ = w.WriteUint32(GlobalXdgOutputManager)
		_ = w.WriteString("zxdg_output_manager_v1")
		_ = w.WriteUint32(3)
		_ = w.WriteUint32(xdgOutMgrID)
	})
	msg(xdgOutMgrID, 1, func() {
		_ = w.WriteUint32(xdgOutID)
		_ = w.WriteUint32(outputID)
	})
	if err := unix.Sendmsg(fd, w.Bytes(), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg: %v", err)
	}

	readScaleAndLogical := func() (int32, int32, int32) {
		t.Helper()
		var buf [8192]byte
		var gotScale int32 = -1
		var gotLogW, gotLogH int32 = -1, -1
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && (gotScale < 0 || gotLogW < 0) {
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 50); n == 0 {
				continue
			}
			n, err := unix.Read(fd, buf[:])
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			off := 0
			for off+8 <= n {
				obj := binary.LittleEndian.Uint32(buf[off : off+4])
				word := binary.LittleEndian.Uint32(buf[off+4 : off+8])
				op := uint16(word & 0xffff)
				sz := int(word >> 16)
				if sz < 8 || off+sz > n {
					break
				}
				if obj == outputID && op == wayland.WlOutputEventOpScale {
					gotScale = int32(binary.LittleEndian.Uint32(buf[off+8 : off+12]))
				}
				if obj == xdgOutID && op == xdgoutput.ZxdgOutputV1EventOpLogicalSize {
					gotLogW = int32(binary.LittleEndian.Uint32(buf[off+8 : off+12]))
					gotLogH = int32(binary.LittleEndian.Uint32(buf[off+12 : off+16]))
				}
				off += sz
			}
		}
		return gotScale, gotLogW, gotLogH
	}

	scale, logW, logH := readScaleAndLogical()
	if scale != 2 || logW != 2048 || logH != 1152 {
		t.Errorf("initial scale=%d logical=(%d,%d), want scale=2 logical=(2048,1152)", scale, logW, logH)
	}

	// Hot-update output scale to 1.0 and verify live broadcast to bound objects.
	srv.SetOutput(Output{Width: 2560, Height: 1440, RefreshHz: 60, Scale: 1.0})
	scale2, logW2, logH2 := readScaleAndLogical()
	if scale2 != 1 || logW2 != 2560 || logH2 != 1440 {
		t.Errorf("reloaded scale=%d logical=(%d,%d), want scale=1 logical=(2560,1440)", scale2, logW2, logH2)
	}
}
