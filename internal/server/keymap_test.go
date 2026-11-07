package server

import (
	"bytes"
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/protocol/wayland"
	"stratifi/internal/wire"
)

func TestCompileXKBKeymap(t *testing.T) {
	usFD, usSize, err := CompileXKBKeymap("us")
	if err != nil {
		t.Fatalf("CompileXKBKeymap(us): %v", err)
	}
	defer unix.Close(usFD)

	gbFD, gbSize, err := CompileXKBKeymap("gb")
	if err != nil {
		t.Fatalf("CompileXKBKeymap(gb): %v", err)
	}
	defer unix.Close(gbFD)

	if usSize == 0 || gbSize == 0 {
		t.Fatalf("unexpected zero keymap size: us=%d gb=%d", usSize, gbSize)
	}

	usBuf := make([]byte, usSize)
	if _, err := unix.Pread(usFD, usBuf, 0); err != nil {
		t.Fatalf("pread us keymap: %v", err)
	}
	if usBuf[len(usBuf)-1] != 0 {
		t.Errorf("us keymap must be NUL-terminated for wl_keyboard.keymap v1; last byte = %d", usBuf[len(usBuf)-1])
	}

	gbBuf := make([]byte, gbSize)
	if _, err := unix.Pread(gbFD, gbBuf, 0); err != nil {
		t.Fatalf("pread gb keymap: %v", err)
	}
	if gbBuf[len(gbBuf)-1] != 0 {
		t.Errorf("gb keymap must be NUL-terminated for wl_keyboard.keymap v1; last byte = %d", gbBuf[len(gbBuf)-1])
	}
	if !bytes.Contains(gbBuf, []byte("sterling")) {
		t.Errorf("gb keymap missing 'sterling' (£) keysym")
	}
	if bytes.Equal(usBuf, gbBuf) {
		t.Errorf("gb keymap should differ from us keymap")
	}
}

func TestCompileXKBKeymapRejectsInvalidLayout(t *testing.T) {
	bad := []string{
		"gb\";",
		"../etc/passwd",
		"gb us",
		"gb(extd",
		"gb)extd(",
		"abcdefghijklmnopqrstuvwxyz0123456789",
	}
	for _, layout := range bad {
		if fd, _, err := CompileXKBKeymap(layout); err == nil {
			_ = unix.Close(fd)
			t.Errorf("CompileXKBKeymap(%q) succeeded, want error", layout)
		}
	}
}

func TestSetKeymapBroadcastsToConnectedKeyboards(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-keymap-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	usFD, usSize, err := CompileXKBKeymap("us")
	if err != nil {
		t.Fatalf("CompileXKBKeymap(us): %v", err)
	}
	defer unix.Close(usFD)
	srv.SetKeymap(usFD, usSize)

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

	const seatID, kbdID = 10, 11
	w.Reset()
	msg(1, 1, func() { _ = w.WriteUint32(2) }) // wl_display.get_registry(2)
	msg(2, 0, func() {
		_ = w.WriteUint32(GlobalSeat)
		_ = w.WriteString("wl_seat")
		_ = w.WriteUint32(7)
		_ = w.WriteUint32(seatID)
	})
	msg(seatID, 1, func() { _ = w.WriteUint32(kbdID) }) // wl_seat.get_keyboard(11)
	if err := unix.Sendmsg(fd, w.Bytes(), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg: %v", err)
	}

	readKeymapEvent := func() (int, uint32) {
		t.Helper()
		var buf [8192]byte
		var oob [128]byte
		var fds []int
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 50); n == 0 {
				continue
			}
			n, oobn, _, _, err := unix.Recvmsg(fd, buf[:], oob[:], 0)
			if err != nil {
				t.Fatalf("Recvmsg: %v", err)
			}
			if oobn > 0 {
				cmsgs, _ := unix.ParseSocketControlMessage(oob[:oobn])
				for _, cm := range cmsgs {
					got, _ := unix.ParseUnixRights(&cm)
					fds = append(fds, got...)
				}
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
				if obj == kbdID && op == wayland.WlKeyboardEventOpKeymap {
					size := binary.LittleEndian.Uint32(buf[off+12 : off+16])
					if len(fds) == 0 {
						t.Fatalf("wl_keyboard.keymap arrived without SCM_RIGHTS fd")
					}
					kmFD := fds[0]
					for _, extra := range fds[1:] {
						_ = unix.Close(extra)
					}
					return kmFD, size
				}
				off += sz
			}
		}
		t.Fatalf("timed out waiting for wl_keyboard.keymap event")
		return -1, 0
	}

	initFD, initSize := readKeymapEvent()
	_ = unix.Close(initFD)
	if initSize != usSize {
		t.Errorf("initial keymap size = %d, want %d", initSize, usSize)
	}

	// Hot-swap keymap to "gb" and verify connected client receives updated keymap.
	gbFD, gbSize, err := CompileXKBKeymap("gb")
	if err != nil {
		t.Fatalf("CompileXKBKeymap(gb): %v", err)
	}
	defer unix.Close(gbFD)
	srv.SetKeymap(gbFD, gbSize)

	reloadFD, reloadSize := readKeymapEvent()
	defer unix.Close(reloadFD)
	if reloadSize != gbSize {
		t.Errorf("reloaded keymap size = %d, want %d", reloadSize, gbSize)
	}
	reloadBuf := make([]byte, reloadSize)
	if _, err := unix.Pread(reloadFD, reloadBuf, 0); err != nil {
		t.Fatalf("pread reloaded keymap: %v", err)
	}
	if !bytes.Contains(reloadBuf, []byte("sterling")) {
		t.Errorf("reloaded keymap did not contain 'sterling'")
	}
}
