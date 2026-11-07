package wire

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	"stratifi/internal/alignio"
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
	}
	conn.acc.Init(conn.pendingBuf[:])
	conn.inFDs.Init(conn.inFDStorage[:])
	conn.Writer.Init(conn.sendBuf[:], conn.sendFdBuf[:])
	return conn, nil
}

// Conn represents an active Wayland client connection with zero-allocation I/O buffers.
type Conn struct {
	unixConn *net.UnixConn
	rawConn  syscall.RawConn
	fd       int

	// Pre-allocated I/O buffers (avoiding heap allocations)
	recvBuf    [8192]byte
	oobBuf     [256]byte
	pendingBuf [8192]byte

	// Framing across reads. A stream socket preserves no message
	// boundaries: one recv may deliver a partial message or several
	// messages at once. acc carries unparsed bytes between calls, and
	// inFDs carries received descriptors that keep FIFO order with them.
	acc         alignio.Accumulator
	inFDStorage [8]int
	inFDs       alignio.FDQueue

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
		if err := c.inFDs.RecvOOB(c.oobBuf[:oobn]); err != nil {
			return err
		}
	}

	c.acc.Append(c.recvBuf[:n])
	return nil
}

// ReadMsg returns a Reader positioned over exactly one complete Wayland
// message, carrying bytes and descriptors across reads as needed. The caller
// must finish decoding before the next call (the Reader aliases c.acc).
func (c *Conn) ReadMsg() (*Reader, error) {
	// Account for descriptors taken while the caller decoded the previous
	// message (Reader.fdIdx is reset per message by Init).
	c.inFDs.Consume(c.Reader.fdIdx)

	for {
		msg, ok, err := c.acc.Next(HeaderSize, MessageTotalSpan)
		if err != nil {
			return nil, err
		}
		if ok {
			c.Reader.Init(msg, c.inFDs.Active())
			return &c.Reader, nil
		}
		if err := c.recvMore(); err != nil {
			return nil, err
		}
	}
}

// HasPendingMessage reports whether another complete message is already
// buffered, so a caller can drain a batch of requests before flushing.
// It never blocks.
func (c *Conn) HasPendingMessage() bool {
	return c.acc.HasNext(HeaderSize, MessageTotalSpan)
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
