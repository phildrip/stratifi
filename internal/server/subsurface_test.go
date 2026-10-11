package server

import (
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

type ssEmit struct {
	clientID, surfaceID, parentID uint32
	x, y, w, h, order             int32
}

// TestSubsurfaceLifecycle drives wl_subcompositor/wl_subsurface end to end
// with a real client: sync defers until the parent commits, desync applies
// on the child's own commit, place_below reorders the stack, and destroy
// fires the gone callback.
func TestSubsurfaceLifecycle(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-ss-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	var mu sync.Mutex
	var emits []ssEmit
	var gone []uint32
	srv.SetOnSubsurface(func(clientID, surfaceID, parentID uint32, x, y, w, h, order int32) {
		mu.Lock()
		emits = append(emits, ssEmit{clientID, surfaceID, parentID, x, y, w, h, order})
		mu.Unlock()
	})
	srv.SetOnSubsurfaceGone(func(clientID, surfaceID uint32) {
		mu.Lock()
		gone = append(gone, surfaceID)
		mu.Unlock()
	})
	srv.SetOnToplevel(func(c *Client, _ *XdgToplevel, xdgSurfaceID uint32) {}) // no default configure

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
	sendOob := func(oob []byte, msgs ...func()) {
		w.Reset()
		for _, fill := range msgs {
			fill()
		}
		if err := unix.Sendmsg(fd, w.Bytes(), oob, nil, 0); err != nil {
			t.Fatalf("Sendmsg: %v", err)
		}
	}
	msg := func(objectID uint32, opcode uint16, fill func()) func() {
		return func() {
			start := w.BeginMessage(objectID, opcode)
			fill()
			if err := w.EndMessage(start, objectID, opcode); err != nil {
				t.Fatalf("EndMessage: %v", err)
			}
		}
	}

	const compID, subcompID, shmID = 5, 6, 7
	const parentSurf, childSurf, sub1, child2Surf, sub2 = 10, 11, 12, 13, 14
	const poolID, bufID = 15, 16

	memFD, err := unix.MemfdCreate("ss-shm", 0)
	if err != nil {
		t.Fatalf("MemfdCreate: %v", err)
	}
	defer unix.Close(memFD)
	const poolSize = 64 * 64 * 4
	if err := unix.Ftruncate(memFD, poolSize); err != nil {
		t.Fatalf("Ftruncate: %v", err)
	}

	// Batch 1: registry + binds + surfaces.
	sendOob(nil,
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalSubcompositor)
			_ = w.WriteString("wl_subcompositor")
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(subcompID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalShm)
			_ = w.WriteString("wl_shm")
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(shmID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(parentSurf) }),
		msg(compID, 0, func() { _ = w.WriteUint32(childSurf) }),
		msg(compID, 0, func() { _ = w.WriteUint32(child2Surf) }),
	)

	// Batch 2: shm pool + buffer + attach + commit the child (so it has a
	// size). The pool carries the memFD in SCM_RIGHTS.
	sendOob(unix.UnixRights(memFD),
		msg(shmID, 0, func() { _ = w.WriteUint32(poolID); _ = w.WriteInt32(poolSize) }),
		msg(poolID, 0, func() {
			_ = w.WriteUint32(bufID)
			_ = w.WriteInt32(0)
			_ = w.WriteInt32(64)
			_ = w.WriteInt32(64)
			_ = w.WriteInt32(64 * 4)
			_ = w.WriteUint32(0) // ARGB8888
		}),
		msg(childSurf, 1, func() { _ = w.WriteUint32(bufID); _ = w.WriteInt32(0); _ = w.WriteInt32(0) }),
		msg(childSurf, 6, func() {}),
	)

	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.GetClient(1) == nil {
		t.Fatal("client never registered")
	}
	time.Sleep(20 * time.Millisecond)

	// Create sub1 (sync by default), position it, commit the CHILD: sync
	// must NOT emit yet.
	sendOob(nil,
		msg(subcompID, 1, func() { _ = w.WriteUint32(sub1); _ = w.WriteUint32(childSurf); _ = w.WriteUint32(parentSurf) }),
		msg(sub1, 1, func() { _ = w.WriteInt32(10); _ = w.WriteInt32(20) }),
		msg(childSurf, 6, func() {}),
	)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	n := len(emits)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("sync child emitted before parent commit (%d emits)", n)
	}

	// Commit the PARENT: now the sync child applies.
	sendOob(nil, msg(parentSurf, 6, func() {}))
	if !waitEmits(&mu, &emits, 1, time.Second) {
		t.Fatal("parent commit did not flush the sync child")
	}
	mu.Lock()
	got := emits[0]
	mu.Unlock()
	if got.surfaceID != childSurf || got.parentID != parentSurf {
		t.Errorf("emit ids = child %d parent %d, want %d/%d", got.surfaceID, got.parentID, childSurf, parentSurf)
	}
	if got.x != 10 || got.y != 20 || got.w != 64 || got.h != 64 || got.order != 0 {
		t.Errorf("emit = (%d,%d %dx%d order %d), want (10,20 64x64 order 0)", got.x, got.y, got.w, got.h, got.order)
	}

	// sub2: place_below(sub1) then desync; its own commit applies at order 0
	// (below sub1, which is now order 1).
	sendOob(nil,
		msg(subcompID, 1, func() { _ = w.WriteUint32(sub2); _ = w.WriteUint32(child2Surf); _ = w.WriteUint32(parentSurf) }),
		msg(sub2, 3, func() { _ = w.WriteUint32(sub1) }), // place_below
		msg(sub2, 5, func() {}), // set_desync
		msg(child2Surf, 1, func() { _ = w.WriteUint32(bufID); _ = w.WriteInt32(0); _ = w.WriteInt32(0) }),
		msg(child2Surf, 6, func() {}),
	)
	if !waitEmits(&mu, &emits, 2, time.Second) {
		t.Fatal("desync child did not emit on its own commit")
	}
	mu.Lock()
	got2 := emits[1]
	mu.Unlock()
	if got2.surfaceID != child2Surf || got2.order != 0 {
		t.Errorf("sub2 = surface %d order %d, want %d order 0", got2.surfaceID, got2.order, child2Surf)
	}

	// Destroy sub1: gone fires for the child surface.
	sendOob(nil, msg(sub1, 0, func() {}))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		g := len(gone)
		mu.Unlock()
		if g > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	g := append([]uint32(nil), gone...)
	mu.Unlock()
	if len(g) != 1 || g[0] != childSurf {
		t.Errorf("gone = %v, want [%d]", g, childSurf)
	}
}

func waitEmits(mu *sync.Mutex, emits *[]ssEmit, n int, d time.Duration) bool {
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
