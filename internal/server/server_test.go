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
	_ = w.WriteUint32(8)                                         // buffer_id = 8
	_ = w.WriteInt32(0)                                          // offset = 0
	_ = w.WriteInt32(64)                                         // width = 64
	_ = w.WriteInt32(64)                                         // height = 64
	_ = w.WriteInt32(64 * 4)                                     // stride = 256
	_ = w.WriteUint32(uint32(wayland.WlShmFormatArgb8888))       // format
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
