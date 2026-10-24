package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"stratifi/internal/protocol/wayland"
	"stratifi/internal/wire"
)

// regionGrabber records the region it was asked for. The grab runs on the
// server goroutine; the test reads it, so it is mutex-guarded.
type regionGrabber struct {
	mu         sync.Mutex
	x, y, w, h int32
}

func (g *regionGrabber) GrabFramebuffer(_ []byte, x, y, w, h, _ int32) error {
	g.mu.Lock()
	g.x, g.y, g.w, g.h = x, y, w, h
	g.mu.Unlock()
	return nil
}

func (g *regionGrabber) region() (int32, int32, int32, int32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.x, g.y, g.w, g.h
}

// TestToplevelCaptureSource exercises the window-share path: a client lists
// toplevels, makes a capture source from one, and the session is sized to the
// window and grabs the window's region of the output.
func TestToplevelCaptureSource(t *testing.T) {
	dir, err := os.MkdirTemp("", "stratifi-tlcap-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	srv, err := NewServer(filepath.Join(dir, "wayland-tlcap-0"))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.SetToplevelProvider(func() []Toplevel {
		return []Toplevel{{Key: 7, Title: "Editor", AppID: "zed", Identifier: "stratifi-7"}}
	})
	srv.SetToplevelGeometry(func(key uint32) (int32, int32, int32, int32, bool) {
		if key == 7 {
			return 100, 50, 400, 300, true
		}
		return 0, 0, 0, 0, false
	})
	grab := &regionGrabber{}
	srv.SetFrameGrabber(grab)
	srv.SetOnToplevel(func(c *Client, _ *XdgToplevel, xdgSurfaceID uint32) {})
	go func() { _ = srv.Run() }()
	time.Sleep(30 * time.Millisecond)

	cc := newCaptureTestClient(t, srv.SocketPath())

	// registry + binds: wl_shm(3), output src mgr(4), copy-capture mgr(5),
	// foreign-toplevel-list(6), foreign-toplevel src mgr(7).
	start := cc.w.BeginMessage(1, 1)
	_ = cc.w.WriteUint32(2)
	_ = cc.w.EndMessage(start, 1, 1)
	bind := func(global uint32, iface string, id uint32) {
		start = cc.w.BeginMessage(2, 0)
		_ = cc.w.WriteUint32(global)
		_ = cc.w.WriteString(iface)
		_ = cc.w.WriteUint32(1)
		_ = cc.w.WriteUint32(id)
		_ = cc.w.EndMessage(start, 2, 0)
	}
	bind(GlobalShm, "wl_shm", 3)
	bind(GlobalOutputImageCaptureSourceManager, "ext_output_image_capture_source_manager_v1", 4)
	bind(GlobalImageCopyCaptureManager, "ext_image_copy_capture_manager_v1", 5)
	bind(GlobalForeignToplevelList, "ext_foreign_toplevel_list_v1", 6)
	bind(GlobalForeignToplevelImageCaptureSourceManager, "ext_foreign_toplevel_image_capture_source_manager_v1", 7)
	cc.send()

	// The list emits a handle for the existing window.
	cc.recvAll(500)
	var handleID uint32
	for cc.r.Remaining() >= wire.HeaderSize {
		hdr, _ := cc.r.ReadHeader()
		if hdr.ObjectID == 6 && hdr.Opcode == 0 {
			handleID, _ = cc.r.ReadUint32()
		} else {
			skipPayload(&cc.r, hdr)
		}
	}
	if handleID == 0 {
		t.Fatal("no toplevel handle from the list")
	}

	// Source from the toplevel handle, then a session.
	start = cc.w.BeginMessage(7, 0)
	_ = cc.w.WriteUint32(10)
	_ = cc.w.WriteUint32(handleID)
	_ = cc.w.EndMessage(start, 7, 0)
	start = cc.w.BeginMessage(5, 0)
	_ = cc.w.WriteUint32(11)
	_ = cc.w.WriteUint32(10)
	_ = cc.w.WriteUint32(0)
	_ = cc.w.EndMessage(start, 5, 0)
	cc.send()
	cc.recvAll(500)

	var bufW, bufH uint32
	for cc.r.Remaining() >= wire.HeaderSize {
		hdr, _ := cc.r.ReadHeader()
		if hdr.ObjectID == 11 && hdr.Opcode == 0 {
			bufW, _ = cc.r.ReadUint32()
			bufH, _ = cc.r.ReadUint32()
		} else {
			skipPayload(&cc.r, hdr)
		}
	}
	if bufW != 400 || bufH != 300 {
		t.Fatalf("session buffer_size = %dx%d, want the window's 400x300", bufW, bufH)
	}

	// Frame + a 400x300 buffer + capture.
	start = cc.w.BeginMessage(11, 0)
	_ = cc.w.WriteUint32(12)
	_ = cc.w.EndMessage(start, 11, 0)
	cc.send()
	cc.sendPool(3, 13, 400*300*4)
	start = cc.w.BeginMessage(13, 0)
	_ = cc.w.WriteUint32(14)
	_ = cc.w.WriteInt32(0)
	_ = cc.w.WriteInt32(400)
	_ = cc.w.WriteInt32(300)
	_ = cc.w.WriteInt32(400 * 4)
	_ = cc.w.WriteUint32(uint32(wayland.WlShmFormatArgb8888))
	_ = cc.w.EndMessage(start, 13, 0)
	start = cc.w.BeginMessage(12, 1)
	_ = cc.w.WriteUint32(14)
	_ = cc.w.EndMessage(start, 12, 1)
	start = cc.w.BeginMessage(12, 2)
	_ = cc.w.WriteInt32(0)
	_ = cc.w.WriteInt32(0)
	_ = cc.w.WriteInt32(400)
	_ = cc.w.WriteInt32(300)
	_ = cc.w.EndMessage(start, 12, 2)
	start = cc.w.BeginMessage(12, 3)
	_ = cc.w.EndMessage(start, 12, 3)
	cc.send()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, w, _ := grab.region(); w != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if x, y, w, h := grab.region(); x != 100 || y != 50 || w != 400 || h != 300 {
		t.Errorf("grab region = (%d,%d %dx%d), want the window's (100,50 400x300)", x, y, w, h)
	}
}

// skipPayload advances the reader past a message's payload.
func skipPayload(r *wire.Reader, hdr wire.Header) {
	for n := int(hdr.Size) - wire.HeaderSize; n >= 4; n -= 4 {
		_, _ = r.ReadUint32()
	}
}
