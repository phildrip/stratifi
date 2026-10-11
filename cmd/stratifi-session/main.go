// Command stratifi-session supervises a stratifi compositor and the clients it
// spawns, so restarting the compositor does not take down the session.
//
// Usage:
//
//	stratifi-session [--socket PATH] [--display NAME] -- stratifi [args...]
//
// It sets WAYLAND_DISPLAY for children, listens on a control socket, starts
// the compositor as its child, restarts it on exit, and spawns clients on
// request (so they are ITS children, not the compositor's). Clients that
// died with a restart are relaunched; survivors are left alone.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"stratifi/internal/session"
)

func main() {
	var (
		socket  = flag.String("socket", "", "control socket path (default $XDG_RUNTIME_DIR/stratifi-session.sock)")
		display = flag.String("display", "wayland-stratifi-0", "WAYLAND_DISPLAY value exported to children")
		maxRst  = flag.Int("max-restarts", 10, "give up after this many compositor restarts (0 = unlimited)")
	)
	flag.Parse()

	comp := flag.Args()
	if len(comp) == 0 {
		fmt.Fprintln(os.Stderr, "usage: stratifi-session [flags] -- <compositor> [args...]")
		os.Exit(2)
	}

	sock := *socket
	if sock == "" {
		sock = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "stratifi-session.sock")
	}

	// The compositor keeps the inherited WAYLAND_DISPLAY (its PARENT's, in
	// nested mode) and is told where the control socket is. Spawned clients
	// instead point at OUR display (the compositor's socket).
	compEnv := setEnv(os.Environ(), "STRATIFI_SESSION_SOCKET", sock)
	clientEnv := setEnv(os.Environ(), "WAYLAND_DISPLAY", *display)
	clientEnv = setEnv(clientEnv, "STRATIFI_SESSION_SOCKET", sock)

	s := session.New(comp, compEnv)
	s.SetClientEnv(clientEnv)
	s.MaxRestarts = *maxRst

	fmt.Printf("[session] supervising %v (control socket %s, display %s)\n", comp, sock, *display)

	// Run supervises the compositor; Serve handles control connections. Run
	// must start first so the compositor is up before we accept commands.
	go func() {
		if err := s.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[-] %v\n", err)
			s.Stop()
		}
	}()
	if err := s.Serve(sock); err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		os.Exit(1)
	}
}

// setEnv replaces key in env (or appends it) — a duplicate leaves the old
// value winning, which is exactly the bug this avoids.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
