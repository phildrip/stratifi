// Package xwayland implements the XWayland display allocator, on-demand
// Xwayland process launcher, and pure-Go zero-allocation X11 Window Manager
// (XWM) for running X11 applications inside stratifi (stratifi-yfh).
package xwayland

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"
)

// Display represents an allocated X11 display number (:N), its lock file
// (/tmp/.X<N>-lock), and its listening Unix domain socket (/tmp/.X11-unix/X<N>).
type Display struct {
	number   int
	name     string
	lockPath string
	sockPath string
	listener *net.UnixListener

	mu      sync.Mutex
	cmd     *exec.Cmd
	wmConn  net.Conn
	started bool
}

// AllocateDisplay finds the lowest unused X11 display number in /tmp and
// claims its lock file and Unix socket.
func AllocateDisplay() (*Display, error) {
	return AllocateDisplayInDir("/tmp")
}

// AllocateDisplayInDir allocates an X11 display in the given root directory
// (e.g. "/tmp" in production or t.TempDir() in tests).
func AllocateDisplayInDir(rootDir string) (*Display, error) {
	sockDir := filepath.Join(rootDir, ".X11-unix")
	if err := os.MkdirAll(sockDir, 0777); err != nil {
		return nil, fmt.Errorf("xwayland: mkdir %s: %w", sockDir, err)
	}

	for n := 0; n < 64; n++ {
		lockPath := filepath.Join(rootDir, fmt.Sprintf(".X%d-lock", n))
		sockPath := filepath.Join(sockDir, fmt.Sprintf("X%d", n))

		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
		if err != nil {
			if reapStaleLock(lockPath) {
				f, err = os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
			}
			if err != nil {
				continue
			}
		}
		// Xorg lock format: 10-character space-padded PID followed by newline.
		_, _ = fmt.Fprintf(f, "%10d\n", os.Getpid())
		_ = f.Close()

		_ = os.Remove(sockPath)
		ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
		if err != nil {
			_ = os.Remove(lockPath)
			continue
		}
		return &Display{
			number:   n,
			name:     ":" + strconv.Itoa(n),
			lockPath: lockPath,
			sockPath: sockPath,
			listener: ln,
		}, nil
	}
	return nil, fmt.Errorf("xwayland: no free X11 display found in %s", rootDir)
}

func reapStaleLock(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	s := string(data)
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	pid, err := strconv.Atoi(s)
	if err != nil || pid <= 0 {
		return false
	}
	if unix.Kill(pid, 0) == unix.ESRCH {
		return os.Remove(lockPath) == nil
	}
	return false
}

// Name returns the X11 DISPLAY string (e.g. ":0").
func (d *Display) Name() string { return d.name }

// Number returns the numeric X11 display index.
func (d *Display) Number() int { return d.number }

// SocketPath returns the bound Unix socket path.
func (d *Display) SocketPath() string { return d.sockPath }

// Spawn launches the Xwayland binary wired to this display's listening socket
// and returns the XWM net.Conn for window management.
func (d *Display) Spawn(waylandSocket string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started && d.wmConn != nil {
		return d.wmConn, nil
	}

	binPath, err := exec.LookPath("Xwayland")
	if err != nil {
		return nil, fmt.Errorf("xwayland: Xwayland binary not found: %w", err)
	}

	lnFile, err := d.listener.File()
	if err != nil {
		return nil, fmt.Errorf("xwayland: listener file: %w", err)
	}
	defer lnFile.Close()

	// Create socketpair for the X11 Window Manager connection (-wm fd).
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("xwayland: socketpair: %w", err)
	}
	wmHostFile := os.NewFile(uintptr(fds[0]), "xwayland-wm-host")
	wmChildFile := os.NewFile(uintptr(fds[1]), "xwayland-wm-child")
	defer wmChildFile.Close()

	wmConn, err := net.FileConn(wmHostFile)
	_ = wmHostFile.Close()
	if err != nil {
		return nil, fmt.Errorf("xwayland: FileConn: %w", err)
	}

	// ExtraFDs: fd 3 = listenfd, fd 4 = wmfd.
	cmd := exec.Command(binPath,
		d.name,
		"-rootless",
		"-core",
		"-listenfd", "3",
		"-wm", "4",
	)
	cmd.ExtraFiles = []*os.File{lnFile, wmChildFile}
	cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+waylandSocket)
	if err := cmd.Start(); err != nil {
		_ = wmConn.Close()
		return nil, fmt.Errorf("xwayland: start Xwayland: %w", err)
	}

	d.cmd = cmd
	d.wmConn = wmConn
	d.started = true
	return wmConn, nil
}

// Close stops Xwayland (if running) and removes the socket and lock file.
func (d *Display) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.wmConn != nil {
		_ = d.wmConn.Close()
		d.wmConn = nil
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
		_, _ = d.cmd.Process.Wait()
		d.cmd = nil
	}
	if d.listener != nil {
		_ = d.listener.Close()
	}
	if d.sockPath != "" {
		_ = os.Remove(d.sockPath)
	}
	if d.lockPath != "" {
		_ = os.Remove(d.lockPath)
	}
	return nil
}
