package session

// protocol.go — the supervisor's control socket. A tiny line protocol so the
// compositor (and tooling) can ask the supervisor to spawn clients, restart,
// stop, or report status. Line-based and human-debuggable with socat/nc.
//
// Client -> server, one command per connection:
//
//	SPAWN <count>\n<arg1>\n<arg2>\n...   (count following lines, one arg each)
//	RESTART
//	STOP
//	STATUS
//
// Server -> client, one reply line:
//
//	OK <detail>
//	ERR <message>

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Serve accepts control connections on socketPath until Stop. It runs the
// supervisor's command loop on a goroutine.
func (s *Supervisor) Serve(socketPath string) error {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("session: listen %s: %w", socketPath, err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleControl(conn)
		}
	}()

	s.Wait()
	return nil
}

// handleControl processes one control connection.
func (s *Supervisor) handleControl(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimRight(line, "\r\n")
	verb, rest, _ := strings.Cut(line, " ")

	switch verb {
	case "SPAWN":
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil || n <= 0 || n > 1024 {
			fmt.Fprintln(conn, "ERR bad argv count")
			return
		}
		argv := make([]string, 0, n)
		for i := 0; i < n; i++ {
			arg, err := r.ReadString('\n')
			if err != nil {
				fmt.Fprintln(conn, "ERR short argv")
				return
			}
			argv = append(argv, strings.TrimRight(arg, "\r\n"))
		}
		pid, err := s.Spawn(argv)
		if err != nil {
			fmt.Fprintf(conn, "ERR %v\n", err)
			return
		}
		fmt.Fprintf(conn, "OK %d\n", pid)

	case "STATUS":
		fmt.Fprintf(conn, "OK compositor=%d children=%d restarts=%d\n",
			s.CompositorPID(), s.ChildCount(), s.RestartCount())

	case "RESTART":
		// Signal the compositor to exit; Run restarts it.
		s.mu.Lock()
		comp := s.comp
		s.mu.Unlock()
		if comp != nil && comp.Process != nil {
			_ = comp.Process.Signal(os.Interrupt)
		}
		fmt.Fprintln(conn, "OK restarting")

	case "STOP":
		fmt.Fprintln(conn, "OK stopping")
		go s.Stop()

	default:
		fmt.Fprintln(conn, "ERR unknown command")
	}
}

// SendSpawn asks a running supervisor to spawn argv. Returns the child pid.
// Used by the compositor's spawn action when BLOXWM_SESSION_SOCKET is set.
func SendSpawn(socketPath string, argv []string) (int, error) {
	if len(argv) == 0 {
		return 0, fmt.Errorf("spawn: empty argv")
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	var b strings.Builder
	fmt.Fprintf(&b, "SPAWN %d\n", len(argv))
	for _, a := range argv {
		// Newlines would break the line framing; reject rather than mangle.
		if strings.ContainsAny(a, "\n\r") {
			return 0, fmt.Errorf("spawn: argument contains a newline")
		}
		b.WriteString(a)
		b.WriteByte('\n')
	}
	if _, err := conn.Write([]byte(b.String())); err != nil {
		return 0, err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0, err
	}
	reply = strings.TrimRight(reply, "\r\n")
	if strings.HasPrefix(reply, "OK ") {
		return strconv.Atoi(strings.TrimSpace(reply[3:]))
	}
	return 0, fmt.Errorf("session: %s", strings.TrimPrefix(reply, "ERR "))
}
