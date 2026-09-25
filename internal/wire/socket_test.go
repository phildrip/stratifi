package wire

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSocketClientServerRoundTripWithFD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bloxwm-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sockPath := filepath.Join(tempDir, "test-wayland.sock")
	server, err := Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer server.Close()

	// Create a dummy pipe to send an FD across the socket
	pipeFds := make([]int, 2)
	if err := unix.Pipe(pipeFds); err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	defer unix.Close(pipeFds[0])
	defer unix.Close(pipeFds[1])

	// Client goroutine
	clientDone := make(chan error, 1)
	go func() {
		defer close(clientDone)
		addr, err := net.ResolveUnixAddr("unix", sockPath)
		if err != nil {
			clientDone <- err
			return
		}
		conn, err := net.DialUnix("unix", nil, addr)
		if err != nil {
			clientDone <- err
			return
		}
		defer conn.Close()

		rawConn, err := conn.SyscallConn()
		if err != nil {
			clientDone <- err
			return
		}

		var rawFD int
		_ = rawConn.Control(func(fd uintptr) {
			rawFD = int(fd)
		})

		// Encode a message: objID=1, opcode=5, uint32=99, string="hello wayland"
		var buf [256]byte
		var fds [4]int
		var w Writer
		w.Init(buf[:], fds[:])
		start := w.BeginMessage(1, 5)
		_ = w.WriteUint32(99)
		_ = w.WriteString("hello wayland")
		_ = w.EndMessage(start, 1, 5)

		// Send message with pipeFds[1] attached via SCM_RIGHTS
		oob := unix.UnixRights(pipeFds[1])
		if err := unix.Sendmsg(rawFD, w.Bytes(), oob, nil, 0); err != nil {
			clientDone <- err
			return
		}
		clientDone <- nil
	}()

	// Server accepts connection
	conn, err := server.Accept()
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	defer conn.Close()

	// Wait for client to finish sending
	if err := <-clientDone; err != nil {
		t.Fatalf("client error: %v", err)
	}

	reader, err := conn.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg failed: %v", err)
	}

	hdr, err := reader.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}

	if hdr.ObjectID != 1 || hdr.Opcode != 5 {
		t.Fatalf("unexpected header: %+v", hdr)
	}

	val, err := reader.ReadUint32()
	if err != nil || val != 99 {
		t.Fatalf("expected uint32 99, got %d (err %v)", val, err)
	}

	str, err := reader.ReadString()
	if err != nil || str != "hello wayland" {
		t.Fatalf("expected string 'hello wayland', got '%s' (err %v)", str, err)
	}

	receivedFD, err := reader.ReadFD()
	if err != nil {
		t.Fatalf("ReadFD failed: %v", err)
	}
	defer unix.Close(receivedFD)

	// Verify the received FD actually points to our pipe by writing and reading bytes
	testMsg := []byte("ping from fd")
	if _, err := unix.Write(receivedFD, testMsg); err != nil {
		t.Fatalf("failed to write to received FD: %v", err)
	}

	readBack := make([]byte, len(testMsg))
	n, err := unix.Read(pipeFds[0], readBack)
	if err != nil || n != len(testMsg) || string(readBack) != string(testMsg) {
		t.Fatalf("pipe read verify failed: got '%s', err %v", string(readBack), err)
	}
}
