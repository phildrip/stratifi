package input

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"bloxwm/internal/layout"
	"bloxwm/internal/server"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// readWireFrames reads available frames from fd within timeout, returning
// (objectID, opcode, body) triples. File descriptors carried as ancillary data
// (e.g. wl_keyboard.keymap) are intentionally dropped.
func readWireFrames(fd int, timeout time.Duration) [][3]interface{} {
	deadline := time.Now().Add(timeout)
	var frames [][3]interface{}
	var buf [8192]byte
	var pending []byte
	for time.Now().Before(deadline) {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, _ := unix.Poll(pfd, 20)
		if n == 0 {
			continue
		}
		nr, err := unix.Read(fd, buf[:])
		if nr <= 0 || err != nil {
			break
		}
		pending = append(pending, buf[:nr]...)
		for len(pending) >= 8 {
			obj := binary.LittleEndian.Uint32(pending[0:4])
			word := binary.LittleEndian.Uint32(pending[4:8])
			size := int(word >> 16)
			op := word & 0xFFFF
			if size < 8 || size > len(pending) {
				break
			}
			body := make([]byte, size-8)
			copy(body, pending[8:size])
			frames = append(frames, [3]interface{}{obj, op, body})
			pending = pending[size:]
		}
	}
	return frames
}

// keyboardClient brings up a real server + client with a bound keyboard,
// returning the server, client, client socket fd and keyboard object id.
// Frame reading helpers (readWireFrames) filter protocol noise.
func keyboardClient(t *testing.T) (*server.Server, *server.Client, int, uint32) {
	t.Helper()
	sockPath := t.TempDir() + "/wayland-rk-0"
	srv, err := server.NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go func() { _ = srv.Run() }()
	time.Sleep(30 * time.Millisecond)

	netConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { netConn.Close() })
	rawConn, err := netConn.(*net.UnixConn).SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var fd int
	_ = rawConn.Control(func(f uintptr) { fd = int(f) })

	var w wire.Writer
	var sendBuf [512]byte
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

	const seatID, keyboardID uint32 = 3, 4
	send(1, 1, func() { _ = w.WriteUint32(2) })
	send(2, 0, func() {
		_ = w.WriteUint32(server.GlobalSeat)
		_ = w.WriteString("wl_seat")
		_ = w.WriteUint32(5)
		_ = w.WriteUint32(seatID)
	})
	send(seatID, 1, func() { _ = w.WriteUint32(keyboardID) })

	var client *server.Client
	for i := 0; i < 200 && client == nil; i++ {
		client = srv.GetClient(1)
		if client == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if client == nil {
		t.Fatal("client never registered")
	}
	// Drain the keymap burst so it cannot be mistaken for a key.
	readWireFrames(fd, 200*time.Millisecond)
	return srv, client, fd, keyboardID
}

// TestRouterRoutesKeysToFocusedClient: a key is dropped when nothing has
// focus, and delivered as wl_keyboard.key once a window is focused. Pins the
// router half of the "typing does nothing" bug (bloxwm-o2x): the router only
// forwards keys to its focused client, so the compositor must focus a window
// when it appears.
func TestRouterRoutesKeysToFocusedClient(t *testing.T) {
	sockPath := t.TempDir() + "/wayland-rk-0"
	srv, err := server.NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
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
	var sendBuf [512]byte
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

	const seatID, keyboardID uint32 = 3, 4
	// wl_display.get_registry(id 2)
	send(1, 1, func() { _ = w.WriteUint32(2) })
	// wl_registry.bind(GlobalSeat, "wl_seat", 5, seatID)
	send(2, 0, func() {
		_ = w.WriteUint32(server.GlobalSeat)
		_ = w.WriteString("wl_seat")
		_ = w.WriteUint32(5)
		_ = w.WriteUint32(seatID)
	})
	// wl_seat.get_keyboard(keyboardID)
	send(seatID, 1, func() { _ = w.WriteUint32(keyboardID) })

	var client *server.Client
	for i := 0; i < 200 && client == nil; i++ {
		client = srv.GetClient(1)
		if client == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if client == nil {
		t.Fatal("client never registered")
	}
	// Drain the keymap/enter burst so it cannot be mistaken for a key.
	readWireFrames(fd, 200*time.Millisecond)

	keysFrom := func(frames [][3]interface{}) []uint32 {
		var keys []uint32
		for _, f := range frames {
			if f[0].(uint32) == keyboardID && f[1].(uint32) == 3 { // wl_keyboard.key
				body := f[2].([]byte)
				if len(body) >= 12 {
					keys = append(keys, binary.LittleEndian.Uint32(body[8:12]))
				}
			}
		}
		return keys
	}

	r := NewRouter(
		func(x, y float64) (WindowInfo, bool) { return WindowInfo{}, false },
		func(clientID uint32) *server.Client {
			if clientID == 1 {
				return client
			}
			return nil
		},
		nil,
	)

	const keyA = 30
	// No focus yet: the key must be dropped, never reaching the client.
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 1000, Code: keyA, State: 1})
	if got := keysFrom(readWireFrames(fd, 200*time.Millisecond)); len(got) != 0 {
		t.Fatalf("key leaked to unfocused client: %v", got)
	}

	// Focus the window, then a key must arrive on the wire.
	r.RequestFocus(layout.WindowID(7), 1, 12, 0)
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 2000, Code: keyA, State: 1})

	got := keysFrom(readWireFrames(fd, 500*time.Millisecond))
	if len(got) == 0 {
		t.Fatal("focused client received no key")
	}
	if got[0] != keyA {
		t.Errorf("key = %d, want %d", got[0], keyA)
	}
}

// TestRouterBindingsConsumeBeforeForward: with a Super+q binding installed,
// the bound press fires the action and never reaches the client as
// wl_keyboard.key — while an unbound key still does. The release of the
// consumed press is swallowed too.
func TestRouterBindingsConsumeBeforeForward(t *testing.T) {
	_, client, fd, keyboardID := keyboardClient(t)

	var actions []string
	r := NewRouter(
		func(x, y float64) (WindowInfo, bool) { return WindowInfo{}, false },
		func(clientID uint32) *server.Client {
			if clientID == 1 {
				return client
			}
			return nil
		},
		nil,
	)
	b, err := ParseBinding("Super+q", "close", nil)
	if err != nil {
		t.Fatalf("ParseBinding: %v", err)
	}
	r.SetBindings(NewMatcher([]Binding{b}))
	r.SetActionHandler(func(action string, args []string) {
		actions = append(actions, action)
	})

	keysFrom := func(frames [][3]interface{}) []uint32 {
		var keys []uint32
		for _, f := range frames {
			if f[0].(uint32) == keyboardID && f[1].(uint32) == 3 {
				body := f[2].([]byte)
				if len(body) >= 12 {
					keys = append(keys, binary.LittleEndian.Uint32(body[8:12]))
				}
			}
		}
		return keys
	}

	r.RequestFocus(layout.WindowID(7), 1, 12, 33)
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 100, Code: 125, State: 1}) // Super
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 200, Code: 16, State: 1})  // q: bound
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 300, Code: 16, State: 0})  // release: swallowed
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 400, Code: 30, State: 1})  // 'a': unbound
	r.ProcessEvent(InputEvent{Type: EventKey, TimeUsec: 500, Code: 125, State: 0}) // Super up

	if len(actions) != 1 || actions[0] != "close" {
		t.Fatalf("actions = %v, want [close]", actions)
	}
	if got := r.FocusToplevel(); got != 33 {
		t.Errorf("FocusToplevel = %d, want 33", got)
	}
	got := keysFrom(readWireFrames(fd, 500*time.Millisecond))
	has := func(code uint32) bool {
		for _, k := range got {
			if k == code {
				return true
			}
		}
		return false
	}
	// Modifiers are never consumed (clients still see them); the bound 'q'
	// (16) must be absent while the unbound 'a' (30) arrives.
	if has(16) {
		t.Errorf("bound key 16 reached the client: %v", got)
	}
	if !has(30) {
		t.Errorf("unbound key 30 missing from client keys: %v", got)
	}
}
