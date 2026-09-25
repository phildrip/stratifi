package dbus

import (
	"net"
	"path/filepath"
	"testing"
)

// stubBus is a minimal D-Bus server socket: it speaks the EXTERNAL
// handshake and replies to every method call with a fixed MethodReturn.
type stubBus struct {
	ln       *net.UnixListener
	reply    []byte // pre-encoded method return message
	gotCalls chan []byte
}

func startStubBus(t *testing.T) *stubBus {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "bus")

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	sb := &stubBus{ln: ln, gotCalls: make(chan []byte, 8)}
	t.Cleanup(func() { close(sb.gotCalls) })
	go sb.serve()
	return sb
}

func (sb *stubBus) serve() {
	for {
		conn, err := sb.ln.AcceptUnix()
		if err != nil {
			return
		}
		go func(c *net.UnixConn) {
			defer c.Close()
			buf := make([]byte, 4096)
			// EXTERNAL handshake
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			if !containsBytes(buf[:n], []byte("AUTH EXTERNAL")) {
				return
			}
			if _, err := c.Write([]byte("OK 1234deadbeef\r\n")); err != nil {
				return
			}

			// Read until one complete message (past the BEGIN line).
			var acc []byte
			for {
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				acc = append(acc, buf[:n]...)
				// Strip the leading "BEGIN\r\n" control line.
				if i := indexOf(acc, []byte("\r\n")); i >= 0 {
					acc = acc[i+2:]
				}
				if _, err := DecodeHeader(acc); err == nil {
					break
				}
			}
			if _, err := c.Write(sb.reply); err != nil {
				return
			}
			select {
			case sb.gotCalls <- append([]byte(nil), acc...):
			default:
			}
		}(conn)
	}
}

func indexOf(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// buildStubReply encodes a MethodReturn (serial 1, reply_serial 1) with a
// single string body "org.freedesktop.DBus.UniqueName:1-42".
func buildStubReply() []byte {
	var bw Writer
	bw.WriteString("org.freedesktop.DBus.UniqueName:1-42")
	body := bw.Bytes()

	var w Writer
	w.Build(&Msg{
		Type:        TypeMethodReturn,
		Serial:      1,
		ReplySerial: 1,
		Body:        body,
		BodySig:     "s",
	})
	return w.Bytes()
}

// TestConnAuthAndHello drives the full client handshake against the stub.
func TestConnAuthAndHello(t *testing.T) {
	sb := startStubBus(t)
	sb.reply = buildStubReply()

	c, err := Connect("unix:path=" + sb.ln.Addr().String())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.nc.Close()

	if !ValidObjectPath("/org/freedesktop/DBus") {
		t.Fatal("sanity path check failed")
	}
	if c.unique != "org.freedesktop.DBus.UniqueName:1-42" {
		t.Fatalf("unique name = %q", c.unique)
	}
}
