package server

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/wire"
)

// TestForeignToplevelList: a client binds the list, receives the windows that
// already exist, then live opened/changed/closed events — the window picker
// the screen-share portal drives.
func TestForeignToplevelList(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-ftl-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.SetToplevelProvider(func() []Toplevel {
		return []Toplevel{{Key: 7, Title: "~/git/stratifi", AppID: "foot", Identifier: "stratifi-7"}}
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
	const listID = 3
	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }), // get_registry
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalForeignToplevelList)
			_ = w.WriteString("ext_foreign_toplevel_list_v1")
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(listID)
		}),
	)
	for i := 0; i < 200 && srv.GetClient(1) == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}

	type ev struct {
		obj  uint32
		op   uint32
		body []byte
	}
	read := func(timeout time.Duration) []ev {
		var out []ev
		deadline := time.Now().Add(timeout)
		var pending []byte
		var buf [4096]byte
		le := binary.LittleEndian
		for time.Now().Before(deadline) {
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 30); n == 0 {
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
				if size < 8 || size > len(pending) {
					break
				}
				out = append(out, ev{obj, word & 0xFFFF, append([]byte(nil), pending[8:size]...)})
				pending = pending[size:]
			}
		}
		return out
	}
	str := func(body []byte) string {
		n := int(binary.LittleEndian.Uint32(body[0:4]))
		return string(body[4 : 4+n-1])
	}

	// Initial list: one handle for the existing window + finished.
	evs := read(500 * time.Millisecond)
	var handleID uint32
	var title, appID, ident string
	done, finished := false, false
	for _, e := range evs {
		switch {
		case e.obj == listID && e.op == 0: // toplevel(new_id)
			handleID = binary.LittleEndian.Uint32(e.body[0:4])
		case e.obj == listID && e.op == 1:
			finished = true
		}
	}
	for _, e := range evs {
		if e.obj != handleID || handleID == 0 {
			continue
		}
		switch e.op {
		case 1:
			done = true
		case 2:
			title = str(e.body)
		case 3:
			appID = str(e.body)
		case 4:
			ident = str(e.body)
		}
	}
	if handleID == 0 || !finished || !done {
		t.Fatalf("initial list incomplete: handle=%d finished=%v done=%v", handleID, finished, done)
	}
	if title != "~/git/stratifi" || appID != "foot" || ident != "stratifi-7" {
		t.Errorf("metadata = %q/%q/%q", title, appID, ident)
	}

	// A new window opens live.
	var mu sync.Mutex
	mu.Lock()
	srv.NotifyToplevelOpened(Toplevel{Key: 9, Title: "Editor", AppID: "zed", Identifier: "stratifi-9"})
	mu.Unlock()
	opened := false
	for _, e := range read(500 * time.Millisecond) {
		if e.obj == listID && e.op == 0 {
			opened = true
		}
	}
	if !opened {
		t.Error("no toplevel event for a live window")
	}

	// Title change.
	srv.NotifyToplevelChanged(Toplevel{Key: 7, Title: "renamed", AppID: "foot", Identifier: "stratifi-7"})
	renamed := false
	for _, e := range read(500 * time.Millisecond) {
		if e.obj == handleID && e.op == 2 && str(e.body) == "renamed" {
			renamed = true
		}
	}
	if !renamed {
		t.Error("title change not delivered")
	}

	// Close.
	srv.NotifyToplevelClosed(7)
	closed := false
	for _, e := range read(500 * time.Millisecond) {
		if e.obj == handleID && e.op == 0 {
			closed = true
		}
	}
	if !closed {
		t.Error("close not delivered")
	}
}
