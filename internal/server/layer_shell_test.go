package server

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// TestLayerSurfaceLifecycle drives zwlr_layer_shell_v1 end to end with a
// real client: get_layer_surface, set_size/anchor/exclusive_zone/margin, and
// a commit, which must configure the surface (fill the anchored dimension)
// and report its state; destroy reports gone.
func TestLayerSurfaceLifecycle(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-layer-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.SetOutput(Output{Width: 1280, Height: 720, RefreshHz: 60})

	var mu sync.Mutex
	var infos []LayerSurfaceInfo
	var gone []uint32
	srv.SetOnLayerSurface(func(info LayerSurfaceInfo) {
		mu.Lock()
		infos = append(infos, info)
		mu.Unlock()
	})
	srv.SetOnLayerSurfaceGone(func(clientID, surfaceID uint32) {
		mu.Lock()
		gone = append(gone, surfaceID)
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

	const compID, lsID = 5, 6
	const surf, layerSurf = 10, 11

	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalLayerShell)
			_ = w.WriteString("zwlr_layer_shell_v1")
			_ = w.WriteUint32(5)
			_ = w.WriteUint32(lsID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(surf) }),
	)
	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.GetClient(1) == nil {
		t.Fatal("client never registered")
	}

	// get_layer_surface(id, surface, output=0, layer=TOP, namespace="bar").
	send(msg(lsID, 0, func() {
		_ = w.WriteUint32(layerSurf)
		_ = w.WriteUint32(surf)
		_ = w.WriteUint32(0)
		_ = w.WriteUint32(2) // top
		_ = w.WriteString("bar")
	}))
	// size (0 = fill), anchor TOP|LEFT|RIGHT, exclusive 30, margin 0.
	send(
		msg(layerSurf, 0, func() { _ = w.WriteUint32(0); _ = w.WriteUint32(30) }),
		msg(layerSurf, 1, func() { _ = w.WriteUint32(1 | 4 | 8) }),
		msg(layerSurf, 2, func() { _ = w.WriteInt32(30) }),
		msg(layerSurf, 3, func() { _ = w.WriteInt32(0); _ = w.WriteInt32(0); _ = w.WriteInt32(0); _ = w.WriteInt32(0) }),
		msg(surf, 6, func() {}), // commit
	)

	if !waitLayer(&mu, &infos, 1, time.Second) {
		t.Fatal("onLayerSurface did not fire")
	}
	mu.Lock()
	info := infos[0]
	mu.Unlock()
	if info.Layer != 2 || info.Namespace != "bar" {
		t.Errorf("layer/namespace = %d/%q, want 2/bar", info.Layer, info.Namespace)
	}
	// width 0 with left+right anchored fills the output (1280); height 30.
	if info.Width != 1280 || info.Height != 30 {
		t.Errorf("size = %dx%d, want 1280x30 (fill anchored axis)", info.Width, info.Height)
	}
	if info.Anchor != 13 || info.ExclusiveZone != 30 {
		t.Errorf("anchor/zone = %d/%d, want 13/30", info.Anchor, info.ExclusiveZone)
	}

	// The client must have received zwlr_layer_surface_v1.configure.
	le := binary.LittleEndian
	deadline := time.Now().Add(time.Second)
	var buf [4096]byte
	var pending []byte
	gotConfigure := false
	for time.Now().Before(deadline) && !gotConfigure {
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
			if obj == layerSurf && op == 0 { // configure(serial, w, h)
				cw := le.Uint32(pending[12:16])
				ch := le.Uint32(pending[16:20])
				if cw != 1280 || ch != 30 {
					t.Errorf("configure size = %dx%d, want 1280x30", cw, ch)
				}
				gotConfigure = true
			}
			pending = pending[size:]
		}
	}
	if !gotConfigure {
		t.Fatal("no zwlr_layer_surface_v1.configure received")
	}

	// Destroy reports gone.
	send(msg(layerSurf, 7, func() {}))
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(gone)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	g := append([]uint32(nil), gone...)
	mu.Unlock()
	if len(g) != 1 || g[0] != surf {
		t.Errorf("gone = %v, want [%d]", g, surf)
	}
}

func waitLayer(mu *sync.Mutex, infos *[]LayerSurfaceInfo, n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*infos)
		mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
