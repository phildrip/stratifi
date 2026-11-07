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

func TestLayoutOutputsJoinAndMirror(t *testing.T) {
	in := []Output{
		{Name: "eDP-1", Width: 1920, Height: 1080, RefreshHz: 60, Scale: 1.0},
		{Name: "HDMI-A-1", Width: 2560, Height: 1440, RefreshHz: 60, Scale: 2.0},
	}

	joined, totalW, totalH := LayoutOutputs(in, "join")
	if len(joined) != 2 {
		t.Fatalf("len(joined) = %d, want 2", len(joined))
	}
	if joined[0].X != 0 || joined[0].Y != 0 {
		t.Fatalf("joined[0] pos = (%d,%d), want (0,0)", joined[0].X, joined[0].Y)
	}
	// HDMI-A-1 is placed at eDP-1's logical width (1920), and its logical size is 1280x720.
	if joined[1].X != 1920 || joined[1].Y != 0 {
		t.Fatalf("joined[1] pos = (%d,%d), want (1920,0)", joined[1].X, joined[1].Y)
	}
	if totalW != 1920+1280 || totalH != 1080 {
		t.Fatalf("join total = %dx%d, want 3200x1080", totalW, totalH)
	}

	mirrored, mw, mh := LayoutOutputs(in, "mirror")
	if mirrored[0].X != 0 || mirrored[1].X != 0 || mirrored[1].Y != 0 {
		t.Fatalf("mirror positions = (%d,%d) and (%d,%d), want both (0,0)",
			mirrored[0].X, mirrored[0].Y, mirrored[1].X, mirrored[1].Y)
	}
	if mw != 1920 || mh != 1080 {
		t.Fatalf("mirror total = %dx%d, want primary 1920x1080", mw, mh)
	}
}

func TestMultiOutputHotplugAdvertisesAndRemovesGlobals(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-multiout-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.SetOutputs([]Output{
		{Name: "eDP-1", Description: "Internal Panel", Width: 1920, Height: 1080, RefreshHz: 60, Scale: 1.0},
	})
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

	const regID = 2
	send(msg(1, 1, func() { _ = w.WriteUint32(regID) }))

	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}

	// Drain initial registry globals.
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

	type regEv struct {
		op   uint16
		name uint32
	}
	events := make(chan regEv, 16)
	stop := make(chan struct{})
	le := binary.LittleEndian
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
			if n, _ := unix.Poll(pfd, 30); n == 0 {
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
				op := uint16(word & 0xFFFF)
				if size < 8 || size > len(pending) {
					break
				}
				if obj == regID && size >= 12 {
					events <- regEv{op: op, name: le.Uint32(pending[8:12])}
				}
				pending = pending[size:]
			}
		}
	}()
	defer close(stop)

	// Hotplug-add a second output ("HDMI-A-1") -> must emit wl_registry.global (op 0).
	hotGlobalID := srv.AddOutput(Output{
		Name:        "HDMI-A-1",
		Description: "External Monitor",
		X:           1920,
		Y:           0,
		Width:       2560,
		Height:      1440,
		RefreshHz:   60,
		Scale:       1.0,
	})
	if hotGlobalID == 0 {
		t.Fatal("AddOutput returned 0 globalID")
	}

	select {
	case ev := <-events:
		if ev.op != 0 || ev.name != hotGlobalID {
			t.Fatalf("expected wl_registry.global (op 0) name=%d, got %+v", hotGlobalID, ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hotplug wl_registry.global")
	}

	// Hotplug-remove "HDMI-A-1" -> must emit wl_registry.global_remove (op 1).
	if !srv.RemoveOutput("HDMI-A-1") {
		t.Fatal("RemoveOutput(HDMI-A-1) returned false")
	}
	select {
	case ev := <-events:
		if ev.op != 1 || ev.name != hotGlobalID {
			t.Fatalf("expected wl_registry.global_remove (op 1) name=%d, got %+v", hotGlobalID, ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hotplug wl_registry.global_remove")
	}
}
