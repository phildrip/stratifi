// Package ipc is the compositor's small control channel for out-of-process
// helpers (the launcher today, config reload and the app launcher later).
// It is a line protocol over a unix stream socket in $XDG_RUNTIME_DIR; the
// compositor serves it, helper binaries dial it.
package ipc

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// DefaultSocket is the socket file name under $XDG_RUNTIME_DIR.
const DefaultSocket = "stratifi-ipc.sock"

// Window is one window's identity for a launcher/switcher.
type Window struct {
	WinID uint32
	AppID string
	Title string
}

// Handler is implemented by the compositor: Windows is a lock-free snapshot,
// Focus queues a focus transfer onto the owner goroutine.
type Handler interface {
	Windows() []Window
	Focus(winID uint32) bool
}

// SocketPath resolves the default control socket under $XDG_RUNTIME_DIR.
func SocketPath() string {
	return socketPath()
}

func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return dir + "/" + DefaultSocket
}

// Serve listens on path and dispatches client commands until the returned
// stop function is called. A stale socket file is removed first.
func Serve(path string, h Handler) (func(), error) {
	if path == "" {
		path = socketPath()
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen %s: %w", path, err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(conn, h)
		}
	}()
	return func() { _ = ln.Close(); _ = os.Remove(path) }, nil
}

func serveConn(conn net.Conn, h Handler) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	w := bufio.NewWriter(conn)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "LIST":
			wins := h.Windows()
			fmt.Fprintf(w, "OK %d\n", len(wins))
			for _, win := range wins {
				fmt.Fprintf(w, "%d\t%s\t%s\n", win.WinID, sanitize(win.AppID), sanitize(win.Title))
			}
		case strings.HasPrefix(line, "FOCUS "):
			id, err := strconv.ParseUint(strings.TrimSpace(line[len("FOCUS "):]), 10, 32)
			if err != nil || !h.Focus(uint32(id)) {
				fmt.Fprintln(w, "ERR focus")
			} else {
				fmt.Fprintln(w, "OK")
			}
		default:
			fmt.Fprintln(w, "ERR unknown command")
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

// sanitize keeps one record on one line: tabs and newlines become spaces.
func sanitize(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

// --- client helpers (used by the launcher binary) ---

func dial(path string) (net.Conn, error) {
	if path == "" {
		path = socketPath()
	}
	return net.Dial("unix", path)
}

// Windows asks the compositor for its current window list.
func Windows(path string) ([]Window, error) {
	conn, err := dial(path)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, "LIST"); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	if !sc.Scan() {
		return nil, fmt.Errorf("ipc: no reply")
	}
	head := strings.Fields(sc.Text())
	if len(head) != 2 || head[0] != "OK" {
		return nil, fmt.Errorf("ipc: bad reply %q", sc.Text())
	}
	n, err := strconv.Atoi(head[1])
	if err != nil {
		return nil, fmt.Errorf("ipc: bad count %q", head[1])
	}
	out := make([]Window, 0, n)
	for i := 0; i < n && sc.Scan(); i++ {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		id, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil {
			continue
		}
		out = append(out, Window{WinID: uint32(id), AppID: parts[1], Title: parts[2]})
	}
	return out, nil
}

// Focus asks the compositor to focus a window by id.
func Focus(path string, winID uint32) error {
	conn, err := dial(path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "FOCUS %d\n", winID); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	if !sc.Scan() || sc.Text() != "OK" {
		return fmt.Errorf("ipc: focus failed")
	}
	return nil
}
