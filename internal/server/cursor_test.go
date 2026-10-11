package server

import (
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/wire"
)

type cursorEmit struct {
	clientID, surfaceID uint32
	hx, hy, w, h        int32
	active              bool
}

// TestCursorLifecycle drives wl_pointer.set_cursor end to end: a client sets
// a cursor surface with a hotspot, the compositor learns its size; clearing
// (surface 0) and destroying the surface both report inactive.
func TestCursorLifecycle(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-cursor-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	var mu sync.Mutex
	var emits []cursorEmit
	srv.SetOnCursor(func(clientID, surfaceID uint32, hx, hy, w, h int32, active bool) {
		mu.Lock()
		emits = append(emits, cursorEmit{clientID, surfaceID, hx, hy, w, h, active})
		mu.Unlock()
	})
	srv.SetOnToplevel(func(c *Client, _ *XdgToplevel, xdgSurfaceID uint32) {})

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
	var sb [1024]byte
	var sfds [4]int
	w.Init(sb[:], sfds[:])
	send := func(oob []byte, msgs ...func()) {
		w.Reset()
		for _, m := range msgs {
			m()
		}
		if err := unix.Sendmsg(fd, w.Bytes(), oob, nil, 0); err != nil {
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

	const compID, shmID, seatID, ptrID = 10, 11, 12, 13
	const cursorSurf, poolID, bufID = 20, 21, 22

	memFD, err := unix.MemfdCreate("cur-shm", 0)
	if err != nil {
		t.Fatalf("MemfdCreate: %v", err)
	}
	defer unix.Close(memFD)
	const sz = 24 * 24 * 4
	if err := unix.Ftruncate(memFD, sz); err != nil {
		t.Fatalf("Ftruncate: %v", err)
	}

	send(nil,
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalShm)
			_ = w.WriteString("wl_shm")
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(shmID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalSeat)
			_ = w.WriteString("wl_seat")
			_ = w.WriteUint32(5)
			_ = w.WriteUint32(seatID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(cursorSurf) }),
		msg(seatID, 0, func() { _ = w.WriteUint32(ptrID) }), // get_pointer
	)
	send(unix.UnixRights(memFD),
		msg(shmID, 0, func() { _ = w.WriteUint32(poolID); _ = w.WriteInt32(sz) }),
		msg(poolID, 0, func() {
			_ = w.WriteUint32(bufID)
			_ = w.WriteInt32(0)
			_ = w.WriteInt32(24)
			_ = w.WriteInt32(24)
			_ = w.WriteInt32(24 * 4)
			_ = w.WriteUint32(0)
		}),
		msg(cursorSurf, 1, func() { _ = w.WriteUint32(bufID); _ = w.WriteInt32(0); _ = w.WriteInt32(0) }),
		msg(cursorSurf, 6, func() {}), // commit: the cursor surface has a size now
	)

	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.GetClient(1) == nil {
		t.Fatal("client never registered")
	}
	time.Sleep(20 * time.Millisecond)

	// set_cursor(serial, surface, hx, hy).
	send(nil, msg(ptrID, 0, func() {
		_ = w.WriteUint32(1) // serial
		_ = w.WriteUint32(cursorSurf)
		_ = w.WriteInt32(7)
		_ = w.WriteInt32(4)
	}))
	if !waitCursor(&mu, &emits, 1, time.Second) {
		t.Fatal("set_cursor did not emit")
	}
	mu.Lock()
	e := emits[0]
	mu.Unlock()
	if e.surfaceID != cursorSurf || !e.active || e.hx != 7 || e.hy != 4 || e.w != 24 || e.h != 24 {
		t.Errorf("cursor = %+v, want surface %d active 24x24 hotspot (7,4)", e, cursorSurf)
	}

	// Re-committing the cursor surface re-emits (image/size may change).
	send(nil, msg(cursorSurf, 6, func() {}))
	if !waitCursor(&mu, &emits, 2, time.Second) {
		t.Fatal("cursor commit did not re-emit")
	}

	// Clearing: surface 0.
	send(nil, msg(ptrID, 0, func() {
		_ = w.WriteUint32(2)
		_ = w.WriteUint32(0)
		_ = w.WriteInt32(0)
		_ = w.WriteInt32(0)
	}))
	if !waitCursor(&mu, &emits, 3, time.Second) {
		t.Fatal("clear did not emit")
	}
	mu.Lock()
	e = emits[2]
	mu.Unlock()
	if e.active {
		t.Errorf("after clear: active = true, want false")
	}

	// Set again, then destroy the surface: inactive.
	send(nil,
		msg(ptrID, 0, func() { _ = w.WriteUint32(3); _ = w.WriteUint32(cursorSurf); _ = w.WriteInt32(2); _ = w.WriteInt32(3) }),
		msg(cursorSurf, 0, func() {}), // wl_surface.destroy
	)
	if !waitCursor(&mu, &emits, 5, time.Second) {
		t.Fatalf("destroy did not emit (got %d)", len(emits))
	}
	mu.Lock()
	e = emits[4]
	mu.Unlock()
	if e.active {
		t.Errorf("after surface destroy: active = true, want false")
	}
}

func waitCursor(mu *sync.Mutex, emits *[]cursorEmit, n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*emits)
		mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestCursorSurfaceBeforeBuffer: clients commonly set_cursor with a surface
// they commit afterwards. Until it has a buffer the cursor must report
// inactive (so the compositor falls back to its default), then activate on
// the surface's first commit.
func TestCursorSurfaceBeforeBuffer(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-cursor-nb-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	var mu sync.Mutex
	var emits []cursorEmit
	srv.SetOnCursor(func(clientID, surfaceID uint32, hx, hy, w, h int32, active bool) {
		mu.Lock()
		emits = append(emits, cursorEmit{clientID, surfaceID, hx, hy, w, h, active})
		mu.Unlock()
	})
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
	send := func(oob []byte, msgs ...func()) {
		w.Reset()
		for _, m := range msgs {
			m()
		}
		if err := unix.Sendmsg(fd, w.Bytes(), oob, nil, 0); err != nil {
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

	const compID, shmID, seatID, ptrID = 10, 11, 12, 13
	const curSurf, poolID, bufID = 20, 21, 22
	memFD, _ := unix.MemfdCreate("nb", 0)
	defer unix.Close(memFD)
	const sz = 24 * 24 * 4
	_ = unix.Ftruncate(memFD, sz)

	send(nil,
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalShm)
			_ = w.WriteString("wl_shm")
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(shmID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalSeat)
			_ = w.WriteString("wl_seat")
			_ = w.WriteUint32(5)
			_ = w.WriteUint32(seatID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(curSurf) }),
		msg(seatID, 0, func() { _ = w.WriteUint32(ptrID) }),
	)
	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)

	// set_cursor with NO buffer yet: inactive.
	send(nil, msg(ptrID, 0, func() { _ = w.WriteUint32(1); _ = w.WriteUint32(curSurf); _ = w.WriteInt32(3); _ = w.WriteInt32(2) }))
	if !waitCursor(&mu, &emits, 1, time.Second) {
		t.Fatal("set_cursor did not emit")
	}
	mu.Lock()
	e := emits[0]
	mu.Unlock()
	if e.active {
		t.Errorf("cursor active before its surface had a buffer")
	}

	// Now give it a buffer and commit: it activates with the right size.
	send(unix.UnixRights(memFD),
		msg(shmID, 0, func() { _ = w.WriteUint32(poolID); _ = w.WriteInt32(sz) }),
		msg(poolID, 0, func() {
			_ = w.WriteUint32(bufID)
			_ = w.WriteInt32(0)
			_ = w.WriteInt32(24)
			_ = w.WriteInt32(24)
			_ = w.WriteInt32(24 * 4)
			_ = w.WriteUint32(0)
		}),
		msg(curSurf, 1, func() { _ = w.WriteUint32(bufID); _ = w.WriteInt32(0); _ = w.WriteInt32(0) }),
		msg(curSurf, 6, func() {}),
	)
	if !waitCursor(&mu, &emits, 2, time.Second) {
		t.Fatal("cursor commit did not re-emit")
	}
	mu.Lock()
	e = emits[1]
	mu.Unlock()
	if !e.active || e.w != 24 || e.h != 24 || e.hx != 3 || e.hy != 2 {
		t.Errorf("after buffer commit: %+v, want active 24x24 hotspot (3,2)", e)
	}
}
