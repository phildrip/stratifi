package server

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// clipboardPair spins up the server + two real clients (writer, reader),
// each with wl_data_device bound. Returns the raw fds and object ids.
func clipboardPair(t *testing.T) (*Server, int, int) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "wayland-clip-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go func() { _ = srv.Run() }()
	time.Sleep(30 * time.Millisecond)

	dial := func() int {
		netConn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { netConn.Close() })
		raw, err := netConn.(*net.UnixConn).SyscallConn()
		if err != nil {
			t.Fatalf("SyscallConn: %v", err)
		}
		var fd int
		_ = raw.Control(func(f uintptr) { fd = int(f) })
		return fd
	}
	return srv, dial(), dial()
}

func sendMsg(t *testing.T, fd int, w *wire.Writer, objectID uint32, opcode uint16, fill func()) {
	t.Helper()
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

func waitClient(t *testing.T, srv *Server) *Client {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c := srv.GetClient(uint32(i)); c != nil {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("client never registered")
	return nil
}

// TestClipboardCopyPaste drives the whole path with two clients over the
// real wire: writer offers text/plain + set_selection; reader binds a data
// device, gets the offer, and receives into a pipe; assert the bytes.
func TestClipboardCopyPaste(t *testing.T) {
	srv, fdW, fdR := clipboardPair(t)

	var writer, reader *Client
	for i := 0; i < 300 && (writer == nil || reader == nil); i++ {
		if writer == nil {
			writer = srv.GetClient(1)
		}
		if reader == nil {
			reader = srv.GetClient(2)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if writer == nil || reader == nil {
		t.Fatalf("clients missing: writer=%v reader=%v", writer != nil, reader != nil)
	}

	var w wire.Writer
	sbuf := [1024]byte{}
	sfds := [8]int{}
	w.Init(sbuf[:], sfds[:])

	// Writer: bind manager + data device, create source, offer, set_selection.
	sendMsg(t, fdW, &w, 1, 1, func() { _ = w.WriteUint32(2) }) // registry
	sendMsg(t, fdW, &w, 2, 0, func() {
		_ = w.WriteUint32(GlobalDataDeviceManager)
		_ = w.WriteString("wl_data_device_manager")
		_ = w.WriteUint32(2)
		_ = w.WriteUint32(3) // manager id
	})
	sendMsg(t, fdW, &w, 3, 1, func() { _ = w.WriteUint32(4); _ = w.WriteUint32(5) }) // get_data_device(4, seat 5=stub)
	sendMsg(t, fdW, &w, 3, 0, func() { _ = w.WriteUint32(6) })                       // create_data_source(6)
	sendMsg(t, fdW, &w, 6, 0, func() { _ = w.WriteString("text/plain;charset=utf-8") })
	sendMsg(t, fdW, &w, 6, 0, func() { _ = w.WriteString("text/plain") })
	sendMsg(t, fdW, &w, 4, 1, func() { _ = w.WriteUint32(6); _ = w.WriteUint32(0) }) // set_selection

	var sel Selection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sel = srv.GetSelection()
		if sel.OwnerID == 1 && len(sel.MimeTypes) == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if sel.OwnerID != 1 || len(sel.MimeTypes) != 2 {
		t.Fatalf("selection = %+v, want owner 1 with 2 types", sel)
	}

	// Reader: registry + bind data device (fresh connection, same dance as
	// the writer).
	sendMsg(t, fdR, &w, 1, 1, func() { _ = w.WriteUint32(2) })
	sendMsg(t, fdR, &w, 2, 0, func() {
		_ = w.WriteUint32(GlobalDataDeviceManager)
		_ = w.WriteString("wl_data_device_manager")
		_ = w.WriteUint32(2)
		_ = w.WriteUint32(3)
	})
	sendMsg(t, fdR, &w, 3, 1, func() { _ = w.WriteUint32(4); _ = w.WriteUint32(5) })
	// The server processes messages on its own goroutine: wait until the
	// data device registration lands before the focus handshake.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && reader.dataDeviceID == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if reader.dataDeviceID == 0 {
		t.Fatal("reader data device never registered")
	}
	// Simulate focus handshake: the compositor hands the reader an offer.
	offer := reader.GiveClipboardOffer()
	if offer == 0 {
		t.Fatal("no clipboard offer given to the pasting client")
	}

	// Reader declares acceptance then RECEIVEs with a pipe fd.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pr.Close()
	sendMsg(t, fdR, &w, offer, 0, func() { _ = w.WriteUint32(0); _ = w.WriteString("text/plain;charset=utf-8") }) // accept
	start := w.BeginMessage(offer, 1)                                                                             // receive (fd rides as SCM_RIGHTS)
	_ = w.WriteString("text/plain;charset=utf-8")
	if err := w.EndMessage(start, offer, 1); err != nil {
		t.Fatalf("EndMessage(receive): %v", err)
	}
	oob := unix.UnixRights(int(pw.Fd()))
	if err := unix.Sendmsg(fdR, w.Bytes(), oob, nil, 0); err != nil {
		t.Fatalf("Sendmsg(receive): %v", err)
	}
	w.Reset()
	_ = pw.Close() // the writer side has our fd now

	// The writer (clipboard owner) must receive wl_data_source.send with a
	// duplicated fd. Read its wire to grab that fd, then simulate the owner
	// writing the payload.
	deadline = time.Now().Add(2 * time.Second)
	var pending []byte
	var inBuf [4096]byte
	var oobBuf [256]byte
	gotFD := -1
	// Actual send fd arrives as ancillary; we read both.
	for time.Now().Before(deadline) && gotFD < 0 {
		pfd := []unix.PollFd{{Fd: int32(fdW), Events: unix.POLLIN}}
		if n, _ := unix.Poll(pfd, 50); n == 0 {
			continue
		}
		nr, oobn, _, _, rerr := unix.Recvmsg(fdW, inBuf[:], oobBuf[:], 0)
		if nr <= 0 || rerr != nil {
			break
		}
		pending = append(pending, inBuf[:nr]...)
		if cmsgs, perr := unix.ParseSocketControlMessage(oobBuf[:oobn]); perr == nil {
			for _, cm := range cmsgs {
				if f, ferr := unix.ParseUnixRights(&cm); ferr == nil && len(f) > 0 {
					gotFD = f[0]
				}
			}
		}
		for len(pending) >= 8 {
			obj := binary.LittleEndian.Uint32(pending[0:4])
			word := binary.LittleEndian.Uint32(pending[4:8])
			size := int(word >> 16)
			op := word & 0xFFFF
			if size < 8 || size > len(pending) {
				break
			}
			if obj == 6 && op == 1 { // wl_data_source.send on the writer's source
				t.Logf("source.send event seen for source 6")
			}
			pending = pending[size:]
		}
	}
	if gotFD < 0 {
		t.Fatal("clipboard owner never received the send fd")
	}
	payload := []byte("hello clipboard")
	if _, err := unix.Write(gotFD, payload); err != nil {
		t.Fatalf("owner write: %v", err)
	}
	unix.Close(gotFD)

	// Read the pasted bytes.
	done := make(chan []byte, 1)
	go func() {
		out := make([]byte, len(payload))
		_ = unix.SetNonblock(int(pr.Fd()), false)
		n, _ := pr.Read(out)
		done <- out[:n]
	}()
	select {
	case got := <-done:
		if string(got) != "hello clipboard" {
			t.Errorf("pasted = %q, want %q", got, "hello clipboard")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("paste never arrived")
	}
}
