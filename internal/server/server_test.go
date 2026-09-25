package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bloxwm/internal/protocol/wayland"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

func TestServerClientRoundTrip(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bloxwm-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sockPath := filepath.Join(tempDir, "wayland-test-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	commitCh := make(chan uint32, 1)
	srv.SetOnCommit(func(s *Surface) {
		commitCh <- s.Current.AttachedBufferID
	})

	go func() {
		_ = srv.Run()
	}()

	// Wait briefly for server listener
	time.Sleep(20 * time.Millisecond)

	// Connect a synthetic Wayland client
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		t.Fatalf("ResolveUnixAddr failed: %v", err)
	}

	netConn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatalf("DialUnix failed: %v", err)
	}
	defer netConn.Close()

	rawConn, err := netConn.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn failed: %v", err)
	}

	var rawFD int
	_ = rawConn.Control(func(fd uintptr) {
		rawFD = int(fd)
	})

	var w wire.Writer
	var sendBuf [4096]byte
	var sendFDs [4]int
	w.Init(sendBuf[:], sendFDs[:])

	// 1. Send wl_display.get_registry (registry_id = 2)
	// wl_display.get_registry has opcode 1
	start := w.BeginMessage(1, 1)
	_ = w.WriteUint32(2) // new_id = 2
	_ = w.EndMessage(start, 1, 1)

	// 2. Send wl_display.sync (callback_id = 3)
	// wl_display.sync has opcode 0
	start = w.BeginMessage(1, 0)
	_ = w.WriteUint32(3) // callback = 3
	_ = w.EndMessage(start, 1, 0)

	// Send messages to server
	if err := unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg failed: %v", err)
	}

	// 3. Read events from server (expect wl_registry.global and wl_callback.done)
	pfd := []unix.PollFd{{Fd: int32(rawFD), Events: unix.POLLIN}}
	_, err = unix.Poll(pfd, 1000)
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}

	var inBuf [4096]byte
	var oobBuf [256]byte
	n, _, _, _, err := unix.Recvmsg(rawFD, inBuf[:], oobBuf[:], 0)
	if err != nil {
		t.Fatalf("Recvmsg failed: %v", err)
	}

	var r wire.Reader
	r.Init(inBuf[:n], nil)

	var globalsReceived []string
	var syncDone bool

	for r.Remaining() >= wire.HeaderSize {
		hdr, err := r.ReadHeader()
		if err != nil {
			t.Fatalf("ReadHeader failed: %v", err)
		}

		if hdr.ObjectID == 2 && hdr.Opcode == 0 { // wl_registry.global
			_, _ = r.ReadUint32()          // name
			ifaceName, _ := r.ReadString() // interface
			_, _ = r.ReadUint32()          // version
			globalsReceived = append(globalsReceived, ifaceName)
		} else if hdr.ObjectID == 3 && hdr.Opcode == 0 { // wl_callback.done
			_, _ = r.ReadUint32() // serial
			syncDone = true
		} else {
			// Skip unknown event payload
			payloadLen := int(hdr.Size) - wire.HeaderSize
			for i := 0; i < payloadLen; i += 4 {
				_, _ = r.ReadUint32()
			}
		}
	}

	if len(globalsReceived) < 2 {
		t.Fatalf("expected at least 2 globals, got %v", globalsReceived)
	}
	if !syncDone {
		t.Fatalf("expected sync callback to complete")
	}

	// 4. Bind wl_compositor (global name 1 -> new_id 4)
	w.Reset()
	start = w.BeginMessage(2, 0) // wl_registry.bind
	_ = w.WriteUint32(GlobalCompositor)
	_ = w.WriteString("wl_compositor")
	_ = w.WriteUint32(4) // version
	_ = w.WriteUint32(4) // id
	_ = w.EndMessage(start, 2, 0)

	// 5. Create a surface (wl_compositor.create_surface, opcode 0)
	start = w.BeginMessage(4, 0)
	_ = w.WriteUint32(5) // surface_id = 5
	_ = w.EndMessage(start, 4, 0)

	// 6. Create shared memory via memfd
	memFD, err := unix.MemfdCreate("test-shm", 0)
	if err != nil {
		t.Fatalf("MemfdCreate failed: %v", err)
	}
	defer unix.Close(memFD)

	poolSize := 64 * 64 * 4
	if err := unix.Ftruncate(memFD, int64(poolSize)); err != nil {
		t.Fatalf("Ftruncate failed: %v", err)
	}

	// Bind wl_shm (global name 2 -> new_id 6)
	start = w.BeginMessage(2, 0)
	_ = w.WriteUint32(GlobalShm)
	_ = w.WriteString("wl_shm")
	_ = w.WriteUint32(1)
	_ = w.WriteUint32(6)
	_ = w.EndMessage(start, 2, 0)

	// Send bind and create_surface first
	if err := unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg failed: %v", err)
	}

	// 7. Create shm pool (wl_shm.create_pool, opcode 0) with memFD passed via SCM_RIGHTS
	w.Reset()
	start = w.BeginMessage(6, 0)
	_ = w.WriteUint32(7) // pool_id = 7
	// Note: in wire protocol, fd is not in the message payload bytes, it's in SCM_RIGHTS
	_ = w.WriteInt32(int32(poolSize))
	_ = w.EndMessage(start, 6, 0)

	// 8. Create buffer (wl_shm_pool.create_buffer, opcode 0)
	start = w.BeginMessage(7, 0)
	_ = w.WriteUint32(8)                                   // buffer_id = 8
	_ = w.WriteInt32(0)                                    // offset = 0
	_ = w.WriteInt32(64)                                   // width = 64
	_ = w.WriteInt32(64)                                   // height = 64
	_ = w.WriteInt32(64 * 4)                               // stride = 256
	_ = w.WriteUint32(uint32(wayland.WlShmFormatArgb8888)) // format
	_ = w.EndMessage(start, 7, 0)

	// 9. Attach buffer to surface (wl_surface.attach, opcode 1)
	start = w.BeginMessage(5, 1)
	_ = w.WriteUint32(8) // buffer = 8
	_ = w.WriteInt32(0)  // x = 0
	_ = w.WriteInt32(0)  // y = 0
	_ = w.EndMessage(start, 5, 1)

	// 10. Commit surface (wl_surface.commit, opcode 6)
	start = w.BeginMessage(5, 6)
	_ = w.EndMessage(start, 5, 6)

	// Send shm pool, buffer, attach, and commit with memFD
	oob := unix.UnixRights(memFD)
	if err := unix.Sendmsg(rawFD, w.Bytes(), oob, nil, 0); err != nil {
		t.Fatalf("Sendmsg with memFD failed: %v", err)
	}

	// Wait for commit callback
	select {
	case attachedBufID := <-commitCh:
		if attachedBufID != 8 {
			t.Fatalf("expected attached buffer ID 8, got %d", attachedBufID)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for surface commit")
	}
}

// TestObjectSlotRecycling verifies that recycling an object ID (such as after delete_id)
// updates the existing slot and prevents returning obsolete interface types from dispatch.
func TestObjectSlotRecycling(t *testing.T) {
	c := &Client{
		objects: make([]ObjectSlot, 0, 16),
	}

	// 1. Initial registration (e.g. temporary wl_callback)
	c.registerObject(49, ObjCallback)
	objType, found := c.findObject(49)
	if !found || objType != ObjCallback {
		t.Fatalf("expected ObjCallback, got %v (found=%v)", objType, found)
	}

	// 2. Client recycles ID 49 for a new wl_shm_pool
	c.registerObject(49, ObjShmPool)
	objType, found = c.findObject(49)
	if !found || objType != ObjShmPool {
		t.Fatalf("expected ObjShmPool after recycling, got %v (found=%v)", objType, found)
	}

	// Verify no duplicate active slots exist for ID 49
	activeCount := 0
	for _, slot := range c.objects {
		if slot.Active && slot.ID == 49 {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly 1 active slot for ID 49, found %d", activeCount)
	}

	// 3. Unregister
	c.unregisterObject(49)
	_, found = c.findObject(49)
	if found {
		t.Fatalf("expected ID 49 to be not found after unregister")
	}
}

// TestSurfaceCommitReentrancy verifies that invoking GetSurfaceBuffer inside onCommit
// does not cause a mutex deadlock.
func TestSurfaceCommitReentrancy(t *testing.T) {
	srv := &Server{
		clients: make(map[uint32]*Client),
	}

	c := &Client{
		id:       1,
		server:   srv,
		surfaces: make([]Surface, 0, 4),
		buffers:  make([]ShmBuffer, 0, 4),
		pools:    make([]ShmPool, 0, 4),
	}
	srv.clients[1] = c

	// Register surface ID 3
	c.surfaces = append(c.surfaces, Surface{
		ID:     3,
		Active: true,
		Pending: SurfaceState{
			AttachedBufferID: 10,
		},
	})

	doneCh := make(chan bool, 1)

	// onCommit callback queries GetSurfaceBuffer on client
	srv.SetOnCommit(func(s *Surface) {
		// Must not deadlock acquiring c.mu
		data, w, h, stride, ok := c.GetSurfaceBuffer(s.ID)
		_ = data
		_ = w
		_ = h
		_ = stride
		_ = ok
		doneCh <- true
	})

	// Invoke commit
	if err := c.HandleWlSurfaceCommit(3); err != nil {
		t.Fatalf("HandleWlSurfaceCommit failed: %v", err)
	}

	select {
	case <-doneCh:
		// Succeeded without deadlock
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("deadlock detected: onCommit callback did not complete")
	}
}

// TestClientLifecycleDestroy verifies that onClientDestroy is invoked when a client terminates.
func TestClientLifecycleDestroy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bloxwm-destroy-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sockPath := filepath.Join(tempDir, "wayland-destroy-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	destroyedCh := make(chan uint32, 1)
	srv.SetOnClientDestroy(func(clientID uint32) {
		destroyedCh <- clientID
	})

	go func() {
		_ = srv.Run()
	}()

	time.Sleep(20 * time.Millisecond)

	// Connect client
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		t.Fatalf("ResolveUnixAddr failed: %v", err)
	}

	netConn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatalf("DialUnix failed: %v", err)
	}

	// Close client connection immediately to trigger disconnect
	netConn.Close()

	select {
	case clientID := <-destroyedCh:
		if clientID == 0 {
			t.Fatalf("expected non-zero clientID, got %d", clientID)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for onClientDestroy callback")
	}
}
