package dbus

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Conn is an authenticated D-Bus connection to a bus (session or system).
type Conn struct {
	nc     net.Conn
	serial atomic.Uint32
	unique string
}

// ConnectSessionBus connects to the session bus via DBUS_SESSION_BUS_ADDRESS,
// falling back to $XDG_RUNTIME_DIR/bus.
func ConnectSessionBus() (*Conn, error) {
	addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if addr == "" {
		runtime := os.Getenv("XDG_RUNTIME_DIR")
		if runtime == "" {
			return nil, fmt.Errorf("dbus: no DBUS_SESSION_BUS_ADDRESS or XDG_RUNTIME_DIR")
		}
		addr = "unix:path=" + runtime + "/bus"
	}
	return Connect(addr)
}

// Connect dials an address of the form "unix:path=/run/user/1000/bus".
func Connect(addr string) (*Conn, error) {
	path, ok := strings.CutPrefix(addr, "unix:path=")
	if !ok {
		return nil, fmt.Errorf("dbus: unsupported address %q", addr)
	}
	nc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("dbus: dial %s: %w", path, err)
	}
	c := &Conn{nc: nc}
	if err := c.auth(); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return c, nil
}

// auth performs the EXTERNAL handshake and bus Hello.
func (c *Conn) auth() error {
	uid := os.Getuid()
	line := fmt.Sprintf("\x00AUTH EXTERNAL %x\r\n", []byte(fmt.Sprintf("%d", uid)))
	if _, err := c.nc.Write([]byte(line)); err != nil {
		return fmt.Errorf("dbus: auth write: %w", err)
	}

	resp := make([]byte, 128)
	n, err := c.nc.Read(resp)
	if err != nil {
		return fmt.Errorf("dbus: auth read: %w", err)
	}
	if !strings.Contains(string(resp[:n]), "OK") {
		return fmt.Errorf("dbus: auth rejected: %q", string(resp[:n]))
	}

	if _, err := c.nc.Write([]byte("BEGIN\r\n")); err != nil {
		return fmt.Errorf("dbus: begin write: %w", err)
	}

	// Hello returns our unique name; parse the reply synchronously.
	unique, err := c.Hello()
	if err != nil {
		return err
	}
	c.unique = unique
	return nil
}

// UniqueName returns the bus-assigned unique name (":1.42").
func (c *Conn) UniqueName() string { return c.unique }

// Hello sends org.freedesktop.DBus.Hello and returns the assigned unique name.
func (c *Conn) Hello() (string, error) {
	msg := &Msg{
		Type:        TypeMethodCall,
		Serial:      c.nextSerial(),
		Interface:   "org.freedesktop.DBus",
		Member:      "Hello",
		Destination: "org.freedesktop.DBus",
		Path:        "/org/freedesktop/DBus",
	}
	if err := c.send(msg); err != nil {
		return "", err
	}
	reply, err := c.recvReply(msg.Serial)
	if err != nil {
		return "", err
	}
	r := &Reader{buf: reply.Body}
	name, err := r.ReadString()
	if err != nil {
		return "", fmt.Errorf("dbus: Hello reply decode: %w", err)
	}
	c.unique = name
	return name, nil
}

func (c *Conn) nextSerial() uint32 { return c.serial.Add(1) }

func (c *Conn) send(m *Msg) error {
	var w Writer
	w.Build(m)
	if _, err := c.nc.Write(w.Bytes()); err != nil {
		return fmt.Errorf("dbus: send %s.%s: %w", m.Interface, m.Member, err)
	}
	return nil
}

// recvReply reads messages until the method return for the given serial
// arrives; error returns are surfaced as errors.
func (c *Conn) recvReply(serial uint32) (*Msg, error) {
	var acc [4096]byte
	accBuf := make([]byte, 0, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := c.nc.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		n, err := c.nc.Read(acc[:])
		if err != nil {
			return nil, fmt.Errorf("dbus: recv: %w", err)
		}
		accBuf = append(accBuf, acc[:n]...)

		// Parse greedily; message boundaries are self-describing enough for
		// the single pending reply this minimal client tracks.
		m, derr := DecodeHeader(accBuf)
		if derr == nil && m.ReplySerial == serial {
			if m.Type == TypeError {
				return nil, fmt.Errorf("dbus: remote error %s", m.ErrorName)
			}
			if m.Type == TypeMethodReturn {
				return m, nil
			}
		}
	}
}

// Call sends a fully prepared message and waits for its reply.
func (c *Conn) Call(m *Msg) (*Msg, error) {
	if m.Serial == 0 {
		m.Serial = c.nextSerial()
	}
	if err := c.send(m); err != nil {
		return nil, err
	}
	return c.recvReply(m.Serial)
}

// MethodCall issues one method call and blocks for its reply.
// encodeBody (if non-nil) receives a fresh body writer and must leave the
// encoded bytes in w.Bytes(); the resulting signature is bodySig.
func (c *Conn) MethodCall(dest, path, iface, member, bodySig string, encodeBody func(w *Writer) error) (*Msg, error) {
	var bw Writer
	var body []byte
	if encodeBody != nil {
		if err := encodeBody(&bw); err != nil {
			return nil, err
		}
		body = bw.Bytes()
	}

	msg := &Msg{
		Type:        TypeMethodCall,
		Serial:      c.nextSerial(),
		Interface:   iface,
		Member:      member,
		Path:        path,
		Destination: dest,
		BodySig:     bodySig,
		Body:        body,
	}

	if err := c.send(msg); err != nil {
		return nil, err
	}
	return c.recvReply(msg.Serial)
}
