package xwayland

import (
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"
)

func TestAllocateDisplayLockAndSocket(t *testing.T) {
	dir := t.TempDir()
	// Write a stale .X0-lock with a dead PID (99999999) — AllocateDisplayInDir must reap it and claim :0.
	if err := os.WriteFile(dir+"/.X0-lock", []byte("  99999999\n"), 0444); err != nil {
		t.Fatalf("WriteFile stale lock: %v", err)
	}
	disp, err := AllocateDisplayInDir(dir)
	if err != nil {
		t.Fatalf("AllocateDisplayInDir: %v", err)
	}
	defer disp.Close()

	if disp.Name() != ":0" {
		t.Fatalf("disp.Name() = %q, want :0", disp.Name())
	}

	// Allocating a second display in the same directory must skip :0 and pick :1.
	disp2, err := AllocateDisplayInDir(dir)
	if err != nil {
		t.Fatalf("second AllocateDisplayInDir: %v", err)
	}
	defer disp2.Close()
	if disp2.Name() != ":1" {
		t.Fatalf("disp2.Name() = %q, want :1", disp2.Name())
	}
}

func TestXWMLifecycleAndSurfaceAssociation(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	type mappedEvent struct {
		xid              uint32
		wlSurfaceID      uint32
		serial           uint64
		x, y, w, h       int32
		overrideRedirect bool
		title            string
	}
	mappedCh := make(chan mappedEvent, 4)
	unmappedCh := make(chan uint32, 4)

	xwm := NewXWM(c1, XWMCallbacks{
		OnWindowMapped: func(win XWindow) {
			mappedCh <- mappedEvent{
				xid:              win.XID,
				wlSurfaceID:      win.WlSurfaceID,
				serial:           win.WlSurfaceSerial,
				x:                win.X,
				y:                win.Y,
				w:                win.Width,
				h:                win.Height,
				overrideRedirect: win.OverrideRedirect,
				title:            win.Title,
			}
		},
		OnWindowUnmapped: func(xid uint32) {
			unmappedCh <- xid
		},
	})

	// Run simulated XWayland server on c2.
	errCh := make(chan error, 1)
	deleteReceivedCh := make(chan uint32, 1)
	go func() {
		// 1. Read X11 connection setup (12 bytes).
		setupReq := make([]byte, 12)
		if _, err := c2.Read(setupReq); err != nil {
			errCh <- err
			return
		}
		// Reply with success (status=1) + root window ID = 0x100.
		reply := buildFakeX11SetupReply(0x100)
		if _, err := c2.Write(reply); err != nil {
			errCh <- err
			return
		}

		// Drain initial InternAtom + ChangeWindowAttributes requests and reply to InternAtom.
		// XWM interns 5 atoms (WL_SURFACE_ID, WL_SURFACE_SERIAL, WM_PROTOCOLS, WM_DELETE_WINDOW, _NET_WM_NAME).
		buf := make([]byte, 512)
		n, err := c2.Read(buf)
		if err != nil || n == 0 {
			errCh <- err
			return
		}
		for seq := uint16(1); seq <= 5; seq++ {
			var atomReply [32]byte
			atomReply[0] = 1 // Reply
			binary.LittleEndian.PutUint16(atomReply[2:4], seq)
			binary.LittleEndian.PutUint32(atomReply[8:12], uint32(100+seq)) // Atom ID = 101..105
			_, _ = c2.Write(atomReply[:])
		}

		// Read requests emitted by XWM (MapWindow, ConfigureWindow, SendEvent for CloseWindow) concurrently.
		go func() {
			var rbuf [512]byte
			for {
				m, rerr := c2.Read(rbuf[:])
				if rerr != nil {
					return
				}
				for off := 0; off < m; {
					op := rbuf[off]
					if off+4 > m {
						break
					}
					reqLen := int(binary.LittleEndian.Uint16(rbuf[off+2:off+4])) * 4
					if reqLen <= 0 || off+reqLen > m {
						break
					}
					if op == ReqSendEvent && reqLen >= 44 {
						// Event payload starts at off+12; window at off+12+4.
						win := binary.LittleEndian.Uint32(rbuf[off+16 : off+20])
						deleteReceivedCh <- win
					}
					off += reqLen
				}
			}
		}()

		// 2. Send CreateNotify (code 16) for XID 0x200001 (800x600 at 10,20).
		var createEv [32]byte
		createEv[0] = EventCreateNotify
		binary.LittleEndian.PutUint32(createEv[4:8], 0x100)     // parent = root
		binary.LittleEndian.PutUint32(createEv[8:12], 0x200001) // window
		binary.LittleEndian.PutUint16(createEv[12:14], 10)      // x
		binary.LittleEndian.PutUint16(createEv[14:16], 20)      // y
		binary.LittleEndian.PutUint16(createEv[16:18], 800)     // width
		binary.LittleEndian.PutUint16(createEv[18:20], 600)     // height
		createEv[22] = 0                                        // override-redirect = false
		_, _ = c2.Write(createEv[:])

		// 3. Send MapRequest (code 20) for XID 0x200001.
		var mapReq [32]byte
		mapReq[0] = EventMapRequest
		binary.LittleEndian.PutUint32(mapReq[4:8], 0x100)
		binary.LittleEndian.PutUint32(mapReq[8:12], 0x200001)
		_, _ = c2.Write(mapReq[:])

		// 4. Send ClientMessage (code 33) with WL_SURFACE_ID (atom 101) = 42.
		var cm [32]byte
		cm[0] = EventClientMessage
		cm[1] = 32                                       // format = 32-bit
		binary.LittleEndian.PutUint32(cm[4:8], 0x200001) // window
		binary.LittleEndian.PutUint32(cm[8:12], 101)     // type = WL_SURFACE_ID
		binary.LittleEndian.PutUint32(cm[12:16], 42)     // data32[0] = wl_surface ID 42
		_, _ = c2.Write(cm[:])
	}()

	if err := xwm.Handshake(); err != nil {
		t.Fatalf("xwm.Handshake: %v", err)
	}
	go func() { _ = xwm.Run() }()

	select {
	case ev := <-mappedCh:
		if ev.xid != 0x200001 || ev.wlSurfaceID != 42 || ev.w != 800 || ev.h != 600 {
			t.Fatalf("mapped event = %+v, want xid=0x200001 wlSurfaceID=42 800x600", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnWindowMapped")
	}

	if err := xwm.ConfigureWindow(0x200001, 50, 60, 1024, 768); err != nil {
		t.Fatalf("ConfigureWindow: %v", err)
	}
	if err := xwm.FocusWindow(0x200001); err != nil {
		t.Fatalf("FocusWindow: %v", err)
	}
	if err := xwm.CloseWindow(0x200001); err != nil {
		t.Fatalf("CloseWindow: %v", err)
	}

	select {
	case win := <-deleteReceivedCh:
		if win != 0x200001 {
			t.Fatalf("CloseWindow targeted %#x, want 0x200001", win)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WM_DELETE_WINDOW SendEvent")
	}
}

func BenchmarkXWMHandleEventZeroAlloc(b *testing.B) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() {
		var sink [256]byte
		for {
			if _, err := c2.Read(sink[:]); err != nil {
				return
			}
		}
	}()

	xwm := NewXWM(c1, XWMCallbacks{})
	xwm.atomWlSurfaceID = 101
	xwm.RegisterWindow(0x200001, 0, 0, 800, 600, false)

	var ev [32]byte
	ev[0] = EventClientMessage
	ev[1] = 32
	binary.LittleEndian.PutUint32(ev[4:8], 0x200001)
	binary.LittleEndian.PutUint32(ev[8:12], 101)
	binary.LittleEndian.PutUint32(ev[12:16], 42)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		xwm.HandleRawEvent(&ev)
	}
}

func buildFakeX11SetupReply(rootID uint32) []byte {
	// 8-byte header + 32-byte setup header + 40-byte screen = 80 bytes total (additional length = 18 words of 4 bytes).
	buf := make([]byte, 8+72)
	buf[0] = 1 // Success
	binary.LittleEndian.PutUint16(buf[2:4], 11)
	binary.LittleEndian.PutUint16(buf[4:6], 0)
	binary.LittleEndian.PutUint16(buf[6:8], 18) // 18 * 4 = 72 bytes following
	// resource-id-base at offset 12, resource-id-mask at 16
	binary.LittleEndian.PutUint32(buf[12:16], 0x400000)
	binary.LittleEndian.PutUint32(buf[16:20], 0x1fffff)
	// vendor-len = 0 (offset 24), num-formats = 0 (offset 29), num-screens = 1 (offset 28)
	buf[28] = 1
	buf[29] = 0
	// Screen starts at offset 40; root window is first 4 bytes of screen.
	binary.LittleEndian.PutUint32(buf[40:44], rootID)
	return buf
}

var _ = os.Getenv
