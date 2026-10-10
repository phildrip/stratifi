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

// TestXdgPopupLifecycle drives the popup protocol end to end with a real
// client: positioner state, get_popup, popup surface commit. Asserts the
// client receives xdg_popup.configure with geometry derived from the
// positioner and that the popup-mapped callback fires.
func TestXdgPopupLifecycle(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-popup-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	var mapped struct {
		x, y, w, h                   int32
		clientID, popupID, surfaceID uint32
		fired                        bool
	}
	srv.SetOnPopupMapped(func(clientID, popupID, surfaceID, parentXdg uint32, x, y, w, h int32) {
		mapped.fired = true
		mapped.clientID, mapped.popupID, mapped.surfaceID = clientID, popupID, surfaceID
		mapped.x, mapped.y, mapped.w, mapped.h = x, y, w, h
	})
	srv.SetOnToplevel(func(c *Client, _ *XdgToplevel, xdgSurfaceID uint32) {}) // suppress default configure

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
	var sendBuf [1024]byte
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

	const compID, wmID = 3, 4
	const parentSurf, parentXdg, parentTop = 10, 20, 30
	const popupSurf, popupXdg, popupObj, posObj = 11, 21, 22, 23

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
	send(compID, 0, func() { _ = w.WriteUint32(parentSurf) })
	send(compID, 0, func() { _ = w.WriteUint32(popupSurf) })
	send(wmID, 2, func() { _ = w.WriteUint32(parentXdg); _ = w.WriteUint32(parentSurf) })
	send(parentXdg, 1, func() { _ = w.WriteUint32(parentTop) })
	// The popup's xdg_surface must exist before get_popup (as real clients do).
	send(wmID, 2, func() { _ = w.WriteUint32(popupXdg); _ = w.WriteUint32(popupSurf) })

	// Positioner: 100x32 popup anchored to the bottom edge of a rect at
	// (80, 8, 40, 20), gravity below-right, allow flip.
	send(wmID, 1, func() { _ = w.WriteUint32(posObj) })
	send(posObj, 1, func() { _ = w.WriteInt32(100); _ = w.WriteInt32(32) })                                           // set_size
	send(posObj, 2, func() { _ = w.WriteInt32(80); _ = w.WriteInt32(8); _ = w.WriteInt32(40); _ = w.WriteInt32(20) }) // anchor_rect
	send(posObj, 3, func() { _ = w.WriteUint32(1 << 1) /*bottom*/ })
	send(posObj, 4, func() { _ = w.WriteUint32((1 << 1) | (1 << 3)) /*bottom|right*/ })
	send(posObj, 5, func() { _ = w.WriteUint32((1 << 2) | (1 << 3) | (1 << 0) | (1 << 1)) })
	send(popupXdg, 2, func() { _ = w.WriteUint32(popupObj); _ = w.WriteUint32(parentXdg); _ = w.WriteUint32(posObj) })

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

	// Commit the popup surface: the placement responds now.
	send(popupSurf, 6, func() {}) // commit: maps the popup (no buffer needed)

	// Give the server a moment to process, then read the wire for
	// xdg_popup.configure (opcode 0) on popupObj.
	deadline := time.Now().Add(2 * time.Second)
	var pending []byte
	var inBuf [4096]byte
	var confX, confY, confW, confH int32
	found := false
	for time.Now().Before(deadline) && !found {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLERR}}
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
			body := pending[8:size]
			if obj == popupObj && op == 0 { // configure
				confX = int32(binary.LittleEndian.Uint32(body[0:4]))
				confY = int32(binary.LittleEndian.Uint32(body[4:8]))
				confW = int32(binary.LittleEndian.Uint32(body[8:12]))
				confH = int32(binary.LittleEndian.Uint32(body[12:16]))
				found = true
			}
			pending = pending[size:]
		}
	}
	if !found {
		t.Fatal("no xdg_popup.configure received")
	}
	// Anchor bottom edge = (80, 8+20) = (80, 28); gravity below-right.
	if confX != 80 || confY != 28 || confW != 100 || confH != 32 {
		t.Errorf("configure = (%d,%d %dx%d), want (80,28 100x32)", confX, confY, confW, confH)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !mapped.fired {
		time.Sleep(5 * time.Millisecond)
	}
	if !mapped.fired {
		t.Fatal("onPopupMapped did not fire")
	}
	if mapped.popupID != popupObj || mapped.surfaceID != popupSurf {
		t.Errorf("mapped ids = %d/%d, want %d/%d", mapped.popupID, mapped.surfaceID, popupObj, popupSurf)
	}
	if mapped.x != 80 || mapped.y != 28 || mapped.w != 100 || mapped.h != 32 {
		t.Errorf("mapped = (%d,%d %dx%d), want (80,28 100x32)", mapped.x, mapped.y, mapped.w, mapped.h)
	}
}
