package wire

import (
	"net"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// connPair returns the raw client fd and the accepted server Conn.
func connPair(t *testing.T) (int, *Conn) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "framing.sock")
	server, err := Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		t.Fatalf("ResolveUnixAddr: %v", err)
	}
	client, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var clientFD int
	_ = raw.Control(func(fd uintptr) { clientFD = int(fd) })

	conn, err := server.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return clientFD, conn
}

// encode builds a single Wayland message: objID/opcode plus a uint32 payload.
func encode(t *testing.T, objectID uint32, opcode uint16, payload uint32) []byte {
	t.Helper()
	var buf [64]byte
	var w Writer
	w.Init(buf[:], nil)
	start := w.BeginMessage(objectID, opcode)
	_ = w.WriteUint32(payload)
	if err := w.EndMessage(start, objectID, opcode); err != nil {
		t.Fatalf("EndMessage: %v", err)
	}
	out := make([]byte, len(w.Bytes()))
	copy(out, w.Bytes())
	return out
}

func readMessage(t *testing.T, conn *Conn) (Header, uint32) {
	t.Helper()
	reader, err := conn.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	hdr, err := reader.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	val, err := reader.ReadUint32()
	if err != nil {
		t.Fatalf("ReadUint32: %v", err)
	}
	return hdr, val
}

// TestReadMsgReassemblesSplitMessage: a message delivered across two reads
// must be returned whole, not dropped or mis-parsed.
func TestReadMsgReassemblesSplitMessage(t *testing.T) {
	clientFD, conn := connPair(t)
	msg := encode(t, 7, 3, 0xDEADBEEF)

	// Split mid-message, with a pause so the server sees two reads.
	if err := unix.Sendmsg(clientFD, msg[:6], nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg chunk 1: %v", err)
	}
	if err := unix.Sendmsg(clientFD, msg[6:], nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg chunk 2: %v", err)
	}

	hdr, val := readMessage(t, conn)
	if hdr.ObjectID != 7 || hdr.Opcode != 3 || val != 0xDEADBEEF {
		t.Fatalf("got obj %d op %d val %#x, want obj 7 op 3 val 0xdeadbeef", hdr.ObjectID, hdr.Opcode, val)
	}
}

// TestReadMsgSeparatesCoalescedMessages: several messages in one read must
// come back one at a time, in order.
func TestReadMsgSeparatesCoalescedMessages(t *testing.T) {
	clientFD, conn := connPair(t)
	batch := append(encode(t, 2, 0, 111), encode(t, 2, 1, 222)...)
	if err := unix.Sendmsg(clientFD, batch, nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg: %v", err)
	}

	if hdr, val := readMessage(t, conn); hdr.Opcode != 0 || val != 111 {
		t.Fatalf("first = op %d val %d, want op 0 val 111", hdr.Opcode, val)
	}
	if hdr, val := readMessage(t, conn); hdr.Opcode != 1 || val != 222 {
		t.Fatalf("second = op %d val %d, want op 1 val 222", hdr.Opcode, val)
	}
}

// TestReadMsgRejectsMalformedSize: a size that cannot be a message (below the
// header, or not a multiple of 4) is a protocol violation, not a payload to
// accumulate forever.
func TestReadMsgRejectsMalformedSize(t *testing.T) {
	for _, size := range []uint32{0, 4, 6} {
		clientFD, conn := connPair(t)
		var hdr [8]byte
		// object 1, size in the high 16 bits, opcode 0
		hdr[4] = byte(size)
		hdr[5] = byte(size >> 8)
		if err := unix.Sendmsg(clientFD, hdr[:], nil, nil, 0); err != nil {
			t.Fatalf("Sendmsg: %v", err)
		}
		if _, err := conn.ReadMsg(); err == nil {
			t.Errorf("size %d: ReadMsg succeeded, want error", size)
		}
	}
}

// TestReadMsgKeepsFDWithSplitMessage: descriptors arrive attached to the first
// byte of their message, so a message split across reads must still deliver
// its fd to that message — and a following message must not inherit it.
func TestReadMsgKeepsFDWithSplitMessage(t *testing.T) {
	clientFD, conn := connPair(t)

	pipeFds := make([]int, 2)
	if err := unix.Pipe(pipeFds); err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer unix.Close(pipeFds[0])
	defer unix.Close(pipeFds[1])

	msg := encode(t, 5, 0, 42)
	oob := unix.UnixRights(pipeFds[1])
	if err := unix.Sendmsg(clientFD, msg[:6], oob, nil, 0); err != nil {
		t.Fatalf("Sendmsg chunk 1: %v", err)
	}
	if err := unix.Sendmsg(clientFD, msg[6:], nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg chunk 2: %v", err)
	}
	if err := unix.Sendmsg(clientFD, encode(t, 6, 0, 7), nil, nil, 0); err != nil {
		t.Fatalf("Sendmsg follow-up: %v", err)
	}

	reader, err := conn.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if _, err := reader.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if val, _ := reader.ReadUint32(); val != 42 {
		t.Fatalf("payload = %d, want 42", val)
	}
	fd, err := reader.ReadFD()
	if err != nil {
		t.Fatalf("ReadFD on the split message: %v", err)
	}
	_ = unix.Close(fd)

	// The next message carries no descriptor of its own.
	next, err := conn.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg follow-up: %v", err)
	}
	hdr, err := next.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader follow-up: %v", err)
	}
	if hdr.ObjectID != 6 {
		t.Fatalf("follow-up object = %d, want 6", hdr.ObjectID)
	}
	if _, err := next.ReadFD(); err == nil {
		t.Error("follow-up message inherited the previous message's fd")
	}
}
