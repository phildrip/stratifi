package session

// supervisor.go — a long-lived parent of the compositor and the clients it
// spawns. The point: restarting the compositor must not take down the
// session. Clients are children of THIS process, so a compositor crash
// signals nothing; the supervisor restarts the compositor and (respawn-lite)
// relaunches the clients it remembers.
//
// Honest scope: this preserves *processes*, not *connections*. An on-screen
// Wayland client still dies when the compositor's socket closes; it is
// respawned, not resumed. Connection survival would need protocol-state
// handoff and is deliberately out of scope.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// SpawnRecord remembers one spawned client so it can be relaunched after a
// compositor restart (respawn-lite). pid is the live child (0 when it has
// exited) — respawn relaunches only records whose process is gone, so a
// child that survived the restart is never duplicated.
type SpawnRecord struct {
	Argv []string
	Dir  string
	pid  int
}

// Supervisor owns the compositor child and the client children.
type Supervisor struct {
	compositorArgv []string
	env            []string // environment for the compositor (parent display preserved)
	clientEnv      []string // environment for spawned clients (points at OUR display)
	dir            string

	// RestartDelay waits before restarting a compositor that exited.
	RestartDelay time.Duration
	// SettleDelay waits after (re)starting the compositor before respawning
	// remembered clients, so its socket is up. A future READY handshake from
	// the compositor removes this guess.
	SettleDelay time.Duration
	// MaxRestarts caps rapid restarts; 0 means unlimited. Exceeding it makes
	// Run return an error (crash-loop guard).
	MaxRestarts int
	// Logf receives lifecycle lines (defaults to stderr).
	Logf func(format string, args ...any)

	// Stdio for children. Nil means the child gets /dev/null — useful in
	// tests, where inheriting the harness's stdout keeps its pipe open.
	// Defaults to the process's own stdio (a real session).
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File

	mu        sync.Mutex
	comp      *exec.Cmd
	children  map[int]*exec.Cmd // live client pids
	records   []SpawnRecord
	restarts  int
	stopped   bool
	stopCh    chan struct{}
	stoppedCh chan struct{}
}

// New creates a supervisor for the given compositor command and environment.
// ClientEnv (children) defaults to the same environment; set it with
// SetClientEnv when clients must point at a different WAYLAND_DISPLAY than
// the compositor (nested: compositor keeps the parent's, clients get ours).
func New(compositorArgv []string, env []string) *Supervisor {
	return &Supervisor{
		compositorArgv: compositorArgv,
		env:            env,
		clientEnv:      env,
		RestartDelay:   500 * time.Millisecond,
		SettleDelay:    time.Second,
		children:       make(map[int]*exec.Cmd),
		Stdin:          os.Stdin,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		stopCh:         make(chan struct{}),
		stoppedCh:      make(chan struct{}),
		Logf:           func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
	}
}

// SetClientEnv overrides the environment used for spawned clients.
func (s *Supervisor) SetClientEnv(env []string) { s.clientEnv = env }

func (s *Supervisor) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

// startClient launches a child in its own process group and reaps it,
// marking any matching record dead on exit. It does not touch records
// itself — Spawn appends one, respawn updates one in place.
func (s *Supervisor) startClient(argv []string) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("spawn: empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = s.clientEnv
	cmd.Dir = s.dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.Stdin, s.Stdout, s.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid

	s.mu.Lock()
	s.children[pid] = cmd
	s.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		delete(s.children, pid)
		for i := range s.records {
			if s.records[i].pid == pid {
				s.records[i].pid = 0
			}
		}
		s.mu.Unlock()
	}()
	s.logf("[session] spawned %v (pid %d)", argv, pid)
	return pid, nil
}

// Spawn starts a client as a child of the supervisor and records it for
// respawn. The child is put in its own process group so signalling the
// supervisor's group never reaches it, but it is reaped here.
func (s *Supervisor) Spawn(argv []string) (int, error) {
	pid, err := s.startClient(argv)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.records = append(s.records, SpawnRecord{Argv: append([]string(nil), argv...), Dir: s.dir, pid: pid})
	s.mu.Unlock()
	return pid, nil
}

// Records returns a copy of the spawn history.
func (s *Supervisor) Records() []SpawnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SpawnRecord, len(s.records))
	copy(out, s.records)
	return out
}

// ChildCount reports live client children.
func (s *Supervisor) ChildCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.children)
}

// CompositorPID reports the current compositor pid (0 when not running).
func (s *Supervisor) CompositorPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.comp == nil || s.comp.Process == nil {
		return 0
	}
	return s.comp.Process.Pid
}

// RestartCount reports how many times the compositor has been restarted.
func (s *Supervisor) RestartCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restarts
}

// startCompositor launches the compositor once.
func (s *Supervisor) startCompositor() error {
	cmd := exec.Command(s.compositorArgv[0], s.compositorArgv[1:]...)
	cmd.Env = s.env
	cmd.Dir = s.dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.Stdin, s.Stdout, s.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	s.comp = cmd
	s.mu.Unlock()
	s.logf("[session] compositor started (pid %d)", cmd.Process.Pid)
	return nil
}

// respawn relaunches remembered clients that are no longer running (those
// that died with the previous compositor). Children that survived are left
// alone — respawn never duplicates a live process.
func (s *Supervisor) respawn() {
	s.mu.Lock()
	dead := make([]int, 0, len(s.records)) // record indices to relaunch
	for i := range s.records {
		if s.records[i].pid == 0 {
			dead = append(dead, i)
		}
	}
	s.mu.Unlock()

	for _, i := range dead {
		s.mu.Lock()
		argv := s.records[i].Argv
		s.mu.Unlock()
		pid, err := s.startClient(argv)
		if err != nil {
			s.logf("[session] respawn %v failed: %v", argv, err)
			continue
		}
		s.mu.Lock()
		// Update the SAME record (if it is still dead): appending would
		// grow the record set on every restart.
		if s.records[i].pid == 0 {
			s.records[i].pid = pid
		}
		s.mu.Unlock()
	}
}

// Run starts the compositor and supervises it until Stop. On compositor exit
// it restarts (rate-limited) and respawns remembered clients.
func (s *Supervisor) Run() error {
	defer close(s.stoppedCh)
	if err := s.startCompositor(); err != nil {
		return fmt.Errorf("session: compositor start: %w", err)
	}

	for {
		s.mu.Lock()
		comp := s.comp
		s.mu.Unlock()

		// Settle, then bring remembered clients back for the new compositor.
		select {
		case <-time.After(s.SettleDelay):
			if s.RestartCount() > 0 {
				s.respawn()
			}
		case <-s.stopCh:
			return nil
		}

		err := comp.Wait()
		if s.isStopped() {
			return nil
		}
		s.logf("[session] compositor exited (%v)", err)

		s.mu.Lock()
		s.restarts++
		n := s.restarts
		s.mu.Unlock()
		if s.MaxRestarts > 0 && n > s.MaxRestarts {
			return fmt.Errorf("session: compositor restarted %d times; giving up", n)
		}
		select {
		case <-time.After(s.RestartDelay):
		case <-s.stopCh:
			return nil
		}
		if err := s.startCompositor(); err != nil {
			return fmt.Errorf("session: compositor restart: %w", err)
		}
	}
}

func (s *Supervisor) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// Stop shuts the compositor and all children down and unblocks Run.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	comp := s.comp
	children := make([]*exec.Cmd, 0, len(s.children))
	for _, c := range s.children {
		children = append(children, c)
	}
	s.mu.Unlock()

	close(s.stopCh)
	for _, c := range children {
		if c.Process != nil {
			_ = c.Process.Signal(syscall.SIGTERM)
		}
	}
	if comp != nil && comp.Process != nil {
		_ = comp.Process.Signal(syscall.SIGTERM)
	}
}

// Wait blocks until Run has returned (after Stop or a fatal condition).
func (s *Supervisor) Wait() { <-s.stoppedCh }
