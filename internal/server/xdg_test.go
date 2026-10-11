package server

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/wire"
)

func TestXdgShellToplevelLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stratifi-xdg-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sockPath := filepath.Join(tempDir, "wayland-xdg-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	committedCh := make(chan *Surface, 1)
	srv.SetOnCommit(func(s *Surface) {
		committedCh <- s
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
	defer netConn.Close()

	rawConn, err := netConn.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn failed: %v", err)
	}
	var rawFD int
	_ = rawConn.Control(func(fd uintptr) { rawFD = int(fd) })

	var w wire.Writer
	var sendBuf [4096]byte
	var sendFDs [4]int
	w.Init(sendBuf[:], sendFDs[:])

	// 1. Get registry (id 2)
	start := w.BeginMessage(1, 1)
	_ = w.WriteUint32(2)
	_ = w.EndMessage(start, 1, 1)

	// 2. Bind wl_compositor (global 1 -> id 3)
	start = w.BeginMessage(2, 0)
	_ = w.WriteUint32(GlobalCompositor)
	_ = w.WriteString("wl_compositor")
	_ = w.WriteUint32(4)
	_ = w.WriteUint32(3)
	_ = w.EndMessage(start, 2, 0)

	// 3. Bind xdg_wm_base (global 3 -> id 4)
	start = w.BeginMessage(2, 0)
	_ = w.WriteUint32(GlobalXdgWmBase)
	_ = w.WriteString("xdg_wm_base")
	_ = w.WriteUint32(3)
	_ = w.WriteUint32(4)
	_ = w.EndMessage(start, 2, 0)

	// 4. Create wl_surface (id 10)
	start = w.BeginMessage(3, 0)
	_ = w.WriteUint32(10)
	_ = w.EndMessage(start, 3, 0)

	// 5. Create xdg_surface (id 20) for wl_surface 10
	start = w.BeginMessage(4, 2) // xdg_wm_base.get_xdg_surface
	_ = w.WriteUint32(20)        // new_id = 20
	_ = w.WriteUint32(10)        // surface = 10
	_ = w.EndMessage(start, 4, 2)

	// 6. Get xdg_toplevel (id 30) for xdg_surface 20
	start = w.BeginMessage(20, 1) // xdg_surface.get_toplevel
	_ = w.WriteUint32(30)         // new_id = 30
	_ = w.EndMessage(start, 20, 1)

	// 7. Set title and app_id
	start = w.BeginMessage(30, 2) // xdg_toplevel.set_title
	_ = w.WriteString("stratifi terminal")
	_ = w.EndMessage(start, 30, 2)

	start = w.BeginMessage(30, 3) // xdg_toplevel.set_app_id
	_ = w.WriteString("org.stratifi.term")
	_ = w.EndMessage(start, 30, 3)

	if err := unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg failed: %v", err)
	}

	// 8. Expect configure event sequence from server
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

	var toplevelConfigured bool
	var surfaceConfigureSerial uint32

	for r.Remaining() >= wire.HeaderSize {
		hdr, err := r.ReadHeader()
		if err != nil {
			t.Fatalf("ReadHeader failed: %v", err)
		}

		if hdr.ObjectID == 30 && hdr.Opcode == 0 { // xdg_toplevel.configure
			width, _ := r.ReadInt32()
			height, _ := r.ReadInt32()
			states, _ := r.ReadArray()
			t.Logf("xdg_toplevel.configure received: %dx%d states=%v", width, height, states)
			toplevelConfigured = true
		} else if hdr.ObjectID == 20 && hdr.Opcode == 0 { // xdg_surface.configure
			surfaceConfigureSerial, _ = r.ReadUint32()
			t.Logf("xdg_surface.configure received: serial=%d", surfaceConfigureSerial)
		} else {
			// Skip other events (e.g. globals)
			payloadLen := int(hdr.Size) - wire.HeaderSize
			for i := 0; i < payloadLen; i += 4 {
				_, _ = r.ReadUint32()
			}
		}
	}

	if !toplevelConfigured {
		t.Fatalf("expected xdg_toplevel.configure event")
	}
	if surfaceConfigureSerial == 0 {
		t.Fatalf("expected xdg_surface.configure event with non-zero serial")
	}

	// 9. Client acknowledges configure (xdg_surface.ack_configure, opcode 4)
	w.Reset()
	start = w.BeginMessage(20, 4)
	_ = w.WriteUint32(surfaceConfigureSerial)
	_ = w.EndMessage(start, 20, 4)

	// 10. Commit surface (wl_surface.commit, opcode 6)
	start = w.BeginMessage(10, 6)
	_ = w.EndMessage(start, 10, 6)

	if err := unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg ack_configure failed: %v", err)
	}

	select {
	case s := <-committedCh:
		if s.ID != 10 {
			t.Fatalf("expected committed surface ID 10, got %d", s.ID)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for committed surface")
	}
}

// TestToplevelMetaCallback: set_title/set_app_id after mapping reach the
// compositor via onToplevelMeta (needed for window titles in the launcher).
func TestToplevelMetaCallback(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-meta-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	var mu sync.Mutex
	var titles []string
	srv.SetOnToplevelMeta(func(clientID, toplevelID uint32, title, appID string) {
		mu.Lock()
		titles = append(titles, title+"|"+appID)
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
	const compID, wmID = 5, 6
	const surf, xdgSurf, top = 10, 11, 12
	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalXdgWmBase)
			_ = w.WriteString("xdg_wm_base")
			_ = w.WriteUint32(3)
			_ = w.WriteUint32(wmID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(surf) }),
		msg(wmID, 2, func() { _ = w.WriteUint32(xdgSurf); _ = w.WriteUint32(surf) }),
		msg(xdgSurf, 1, func() { _ = w.WriteUint32(top) }),
	)
	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	// xdg_toplevel.set_title (op 2) and set_app_id (op 3).
	send(
		msg(top, 2, func() { _ = w.WriteString("~/git/stratifi") }),
		msg(top, 3, func() { _ = w.WriteString("foot") }),
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(titles)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	got := append([]string(nil), titles...)
	mu.Unlock()
	if len(got) == 0 || got[len(got)-1] != "~/git/stratifi|foot" {
		t.Errorf("meta = %v, want last = ~/git/stratifi|foot", got)
	}
}
