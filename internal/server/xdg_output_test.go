package server

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// TestXdgOutputInfo: a client that binds zxdg_output_manager_v1 and requests
// an xdg_output receives the logical geometry, name and description waybar
// needs to match a bar to the output.
func TestXdgOutputInfo(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-xdgout-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.SetOutput(Output{Width: 1280, Height: 720, RefreshHz: 60})
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

	const outID, mgrID, xdgOut = 5, 6, 7
	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalOutput)
			_ = w.WriteString("wl_output")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(outID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalXdgOutputManager)
			_ = w.WriteString("zxdg_output_manager_v1")
			_ = w.WriteUint32(3)
			_ = w.WriteUint32(mgrID)
		}),
	)
	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	// get_xdg_output(new id, wl_output).
	send(msg(mgrID, 1, func() { _ = w.WriteUint32(xdgOut); _ = w.WriteUint32(outID) }))

	// Read events on xdgOut: expect logical_size(1280,720), name, description.
	deadline := time.Now().Add(2 * time.Second)
	var pending []byte
	var buf [4096]byte
	var lw, lh int32
	var name string
	gotSize, gotName := false, false
	le := binary.LittleEndian
	for time.Now().Before(deadline) && !(gotSize && gotName) {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, _ := unix.Poll(pfd, 50); n == 0 {
			continue
		}
		nr, _ := unix.Read(fd, buf[:])
		if nr <= 0 {
			break
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
			body := pending[8:size]
			if obj == xdgOut {
				switch op {
				case 1: // logical_size
					lw = int32(le.Uint32(body[0:4]))
					lh = int32(le.Uint32(body[4:8]))
					gotSize = true
				case 3: // name (string: len incl. NUL, bytes, pad)
					n := int(le.Uint32(body[0:4]))
					name = string(body[4 : 4+n-1]) // drop the NUL terminator
					gotName = true
				}
			}
			pending = pending[size:]
		}
	}
	if !gotSize || lw != 1280 || lh != 720 {
		t.Errorf("logical_size = %dx%d (got=%v), want 1280x720", lw, lh, gotSize)
	}
	if !gotName || name != "BLOXWM-1" {
		t.Errorf("name = %q (got=%v), want BLOXWM-1", name, gotName)
	}
}
