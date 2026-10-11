package wire

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Server handles accepting Wayland client connections over a UNIX domain socket.
type Server struct {
	listener *net.UnixListener
	path     string
	mu       sync.Mutex
	closed   bool
}

// Listen creates and binds a new Wayland UNIX domain socket.
// If socketName is relative (e.g. "wayland-stratifi-0"), it is placed in $XDG_RUNTIME_DIR.
func Listen(socketName string) (*Server, error) {
	var sockPath string
	if filepath.IsAbs(socketName) {
		sockPath = socketName
	} else {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			return nil, fmt.Errorf("XDG_RUNTIME_DIR environment variable is not set")
		}
		sockPath = filepath.Join(runtimeDir, socketName)
	}

	// Remove stale socket if present
	_ = os.Remove(sockPath)

	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve unix addr %s: %w", sockPath, err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", sockPath, err)
	}

	return &Server{
		listener: listener,
		path:     sockPath,
	}, nil
}

// Path returns the filesystem path of the active socket.
func (s *Server) Path() string {
	return s.path
}

// Close unlinks the socket and closes the listener.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.listener.Close()
	_ = os.Remove(s.path)
	return err
}

// Accept awaits the next client connection.
func (s *Server) Accept() (*Conn, error) {
	unixConn, err := s.listener.AcceptUnix()
	if err != nil {
		return nil, err
	}

	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		_ = unixConn.Close()
		return nil, fmt.Errorf("failed to get syscall conn: %w", err)
	}

	var rawFD int
	err = rawConn.Control(func(fd uintptr) {
		rawFD = int(fd)
	})
	if err != nil {
		_ = unixConn.Close()
		return nil, fmt.Errorf("failed to extract raw fd: %w", err)
	}

	conn := &Conn{
		unixConn: unixConn,
		rawConn:  rawConn,
		fd:       rawFD,
		pending:  make([]byte, 0, 8192),
	}
	conn.Writer.Init(conn.sendBuf[:], conn.sendFdBuf[:])
	return conn, nil
}

// Conn represents an active Wayland client connection with zero-allocation I/O buffers.
type Conn struct {
	unixConn *net.UnixConn
	rawConn  syscall.RawConn
	fd       int

	// Pre-allocated I/O buffers (avoiding heap allocations)
	recvBuf [8192]byte
	oobBuf  [256]byte

	// Framing across reads. A stream socket preserves no message
	// boundaries: one recv may deliver a partial message or several
	// messages at once. pending carries unparsed bytes between calls, and
	// inFDs carries received descriptors that keep FIFO order with them.
	pending []byte
	inFDs   [8]int
	fdStart int // first unconsumed descriptor
	fdEnd   int // one past the last received descriptor

	sendBuf    [8192]byte
	sendOobBuf [256]byte
	sendFdBuf  [8]int

	Reader Reader
	Writer Writer
}

// recvMore performs one recvmsg, appending payload bytes and any descriptors
// to the framing buffers.
func (c *Conn) recvMore() error {
	var (
		n, oobn int
		recvErr error
	)

	err := c.rawConn.Read(func(fd uintptr) bool {
		n, oobn, _, _, recvErr = unix.Recvmsg(int(fd), c.recvBuf[:], c.oobBuf[:], 0)
		if recvErr == unix.EAGAIN || recvErr == unix.EWOULDBLOCK {
			return false // Yield to Go netpoller
		}
		return true // Completed
	})
	if err != nil {
		return err
	}
	if recvErr != nil {
		return recvErr
	}
	if n == 0 {
		return fmt.Errorf("client closed connection")
	}

	if oobn > 0 {
		cmsgs, err := unix.ParseSocketControlMessage(c.oobBuf[:oobn])
		if err != nil {
			return fmt.Errorf("failed to parse socket control message: %w", err)
		}
		for _, cmsg := range cmsgs {
			if cmsg.Header.Level == unix.SOL_SOCKET && cmsg.Header.Type == unix.SCM_RIGHTS {
				fds, err := unix.ParseUnixRights(&cmsg)
				if err != nil {
					return fmt.Errorf("failed to parse SCM_RIGHTS: %w", err)
				}
				for _, fd := range fds {
					if c.fdEnd == len(c.inFDs) {
						// Reclaim consumed slots before dropping any fd.
						copy(c.inFDs[:], c.inFDs[c.fdStart:c.fdEnd])
						c.fdEnd -= c.fdStart
						c.fdStart = 0
					}
					if c.fdEnd < len(c.inFDs) {
						c.inFDs[c.fdEnd] = fd
						c.fdEnd++
					} else {
						_ = unix.Close(fd) // overflow: close rather than leak
					}
				}
			}
		}
	}

	c.pending = append(c.pending, c.recvBuf[:n]...)
	return nil
}

// ReadMsg returns a Reader positioned over exactly one complete Wayland
// message, carrying bytes and descriptors across reads as needed. The caller
// must finish decoding before the next call (the Reader aliases c.pending).
func (c *Conn) ReadMsg() (*Reader, error) {
	// Account for descriptors taken while the caller decoded the previous
	// message (Reader.fdIdx is reset per message by Init).
	c.fdStart += c.Reader.fdIdx
	if c.fdStart >= c.fdEnd {
		c.fdStart, c.fdEnd = 0, 0
	}

	for len(c.pending) < HeaderSize {
		if err := c.recvMore(); err != nil {
			return nil, err
		}
	}
	// size covers the whole message including the header, multiple of 4.
	size := int(binary.LittleEndian.Uint32(c.pending[4:8]) >> 16)
	if size < HeaderSize || size%4 != 0 {
		return nil, fmt.Errorf("invalid Wayland message size %d", size)
	}
	for len(c.pending) < size {
		if err := c.recvMore(); err != nil {
			return nil, err
		}
	}

	msg := c.pending[:size]
	c.Reader.Init(msg, c.inFDs[c.fdStart:c.fdEnd])
	// Advance past the message without moving bytes: msg stays valid until
	// the next ReadMsg overwrites the region.
	c.pending = c.pending[size:]
	return &c.Reader, nil
}

// HasPendingMessage reports whether another complete message is already
// buffered, so a caller can drain a batch of requests before flushing.
// It never blocks.
func (c *Conn) HasPendingMessage() bool {
	if len(c.pending) < HeaderSize {
		return false
	}
	size := int(binary.LittleEndian.Uint32(c.pending[4:8]) >> 16)
	return size >= HeaderSize && size%4 == 0 && len(c.pending) >= size
}

// WriterRef returns the active Writer for appending events to this client.
func (c *Conn) WriterRef() *Writer {
	return &c.Writer
}

// Flush sends all buffered messages and attached FDs to the client via SCM_RIGHTS.
func (c *Conn) Flush() error {
	data := c.Writer.Bytes()
	if len(data) == 0 {
		return nil
	}

	fds := c.Writer.FDs()
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}

	err := unix.Sendmsg(c.fd, data, oob, nil, 0)
	if err != nil {
		return fmt.Errorf("sendmsg failed: %w", err)
	}

	c.Writer.Reset()
	return nil
}

// PeerPID returns the credentials (PID, UID, GID) of the connected client.
func (c *Conn) PeerCredentials() (*unix.Ucred, error) {
	ucred, err := unix.GetsockoptUcred(c.fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return nil, fmt.Errorf("failed to get SO_PEERCRED: %w", err)
	}
	return ucred, nil
}

// Close terminates the client connection.
func (c *Conn) Close() error {
	return c.unixConn.Close()
}
