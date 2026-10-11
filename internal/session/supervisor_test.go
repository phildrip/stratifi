package session

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// live reports whether a pid is still running.
func live(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// waitFor polls until pred is true or the timeout elapses.
func waitFor(t *testing.T, d time.Duration, pred func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestCompositorRestartKeepsChildAlive: a client spawned by the supervisor
// must survive the compositor exiting and restarting.
func TestCompositorRestartKeepsChildAlive(t *testing.T) {
	// "Compositor": a process that exits after 150ms (simulating a crash).
	s := New([]string{"/bin/sh", "-c", "sleep 0.15"}, os.Environ())
	s.RestartDelay = 20 * time.Millisecond
	s.SettleDelay = 20 * time.Millisecond
	s.Logf = nil
	s.Stdin, s.Stdout, s.Stderr = nil, nil, nil

	go func() { _ = s.Run() }()

	// A long-lived client (the "kid").
	pid, err := s.Spawn([]string{"/bin/sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)

	// Wait for at least one compositor restart.
	if !waitFor(t, 3*time.Second, func() bool { return s.RestartCount() >= 1 }) {
		t.Fatalf("compositor never restarted (restarts=%d)", s.RestartCount())
	}
	// The kid must still be alive and not duplicated.
	if !live(pid) {
		t.Fatal("client died with the compositor")
	}
	if n := s.ChildCount(); n != 1 {
		t.Fatalf("child count = %d, want 1 (no duplicate respawn)", n)
	}
	s.Stop()
}

// TestRespawnRelauchesDeadClient: a client that died (like a Wayland app
// losing its connection) is relaunched after the next compositor restart.
func TestRespawnRelauchesDeadClient(t *testing.T) {
	s := New([]string{"/bin/sh", "-c", "sleep 0.15"}, os.Environ())
	s.RestartDelay = 20 * time.Millisecond
	s.SettleDelay = 20 * time.Millisecond
	s.Logf = nil
	s.Stdin, s.Stdout, s.Stderr = nil, nil, nil
	go func() { _ = s.Run() }()

	// A short-lived client that will be gone before the restart.
	pid, err := s.Spawn([]string{"/bin/sh", "-c", "sleep 0.03"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool { return !live(pid) }) {
		t.Fatal("short client did not exit")
	}
	// After the compositor restarts, respawn-lite must bring it back.
	if !waitFor(t, 3*time.Second, func() bool { return s.ChildCount() >= 1 }) {
		t.Fatalf("dead client not respawned (children=%d)", s.ChildCount())
	}
	s.Stop()
}

// TestSpawnOverControlSocket: the compositor's spawn path (SendSpawn).
func TestSpawnOverControlSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "session.sock")
	s := New([]string{"/bin/sh", "-c", "sleep 5"}, os.Environ())
	s.SettleDelay = 10 * time.Millisecond
	s.Logf = nil
	s.Stdin, s.Stdout, s.Stderr = nil, nil, nil
	go func() { _ = s.Run() }()
	go func() { _ = s.Serve(sock) }()

	if !waitFor(t, time.Second, func() bool {
		_, err := os.Stat(sock)
		return err == nil
	}) {
		t.Fatal("control socket never appeared")
	}

	pid, err := SendSpawn(sock, []string{"/bin/sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatalf("SendSpawn: %v", err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if !live(pid) {
		t.Fatal("spawned child not alive")
	}
	if n := s.ChildCount(); n != 1 {
		t.Fatalf("child count = %d, want 1", n)
	}
	s.Stop()
}

// TestStopTearsDown: Stop signals the compositor and all children.
func TestStopTearsDown(t *testing.T) {
	s := New([]string{"/bin/sh", "-c", "sleep 30"}, os.Environ())
	s.SettleDelay = 10 * time.Millisecond
	s.Logf = nil
	s.Stdin, s.Stdout, s.Stderr = nil, nil, nil
	go func() { _ = s.Run() }()

	pid, err := s.Spawn([]string{"/bin/sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	s.Stop()
	if !waitFor(t, 2*time.Second, func() bool { return !live(pid) }) {
		t.Fatal("child survived Stop")
	}
	s.Wait() // Run returned
}

// TestCrashLoopGuard: a compositor that always exits hits MaxRestarts.
func TestCrashLoopGuard(t *testing.T) {
	s := New([]string{"/bin/sh", "-c", "exit 1"}, os.Environ())
	s.RestartDelay = 5 * time.Millisecond
	s.SettleDelay = 5 * time.Millisecond
	s.MaxRestarts = 3
	s.Logf = nil
	s.Stdin, s.Stdout, s.Stderr = nil, nil, nil

	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected crash-loop error")
		}
	case <-time.After(3 * time.Second):
		s.Stop()
		t.Fatal("crash-loop guard did not fire")
	}
}

// TestSendSpawnRejectsNewlines: framing safety.
func TestSendSpawnRejectsNewlines(t *testing.T) {
	if _, err := SendSpawn("/nonexistent", []string{"echo", "a\nb"}); err == nil {
		t.Fatal("newline in argv accepted")
	}
}

// TestRespawnDoesNotGrowRecords: repeated restarts must not multiply the
// record set (each respawn updates the existing record in place). A prior
// bug appended a record per respawn, doubling children every restart.
func TestRespawnDoesNotGrowRecords(t *testing.T) {
	s := New([]string{"/bin/sh", "-c", "sleep 0.08"}, os.Environ())
	s.RestartDelay = 10 * time.Millisecond
	s.SettleDelay = 10 * time.Millisecond
	s.Logf = nil
	s.Stdin, s.Stdout, s.Stderr = nil, nil, nil
	go func() { _ = s.Run() }()

	// A client that dies immediately, so it is respawned on every restart.
	if _, err := s.Spawn([]string{"/bin/sh", "-c", "true"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Let several compositor restarts happen.
	if !waitFor(t, 3*time.Second, func() bool { return s.RestartCount() >= 3 }) {
		t.Fatalf("only %d restarts", s.RestartCount())
	}
	if n := len(s.Records()); n != 1 {
		t.Fatalf("records = %d after %d restarts, want 1 (no growth)", n, s.RestartCount())
	}
	s.Stop()
}
