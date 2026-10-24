package main

import (
	"os"
	"path/filepath"
	"testing"

	"stratifi/internal/desktop"
)

// TestApplySessionEnv: the compositor advertises itself as a Wayland desktop
// session, without clobbering an outer session's identity (nested dev).
func TestApplySessionEnv(t *testing.T) {
	for _, k := range []string{"XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "XDG_SESSION_CLASS"} {
		t.Setenv(k, "")
	}
	applySessionEnv()
	for k, want := range map[string]string{
		"XDG_SESSION_TYPE":    "wayland",
		"XDG_CURRENT_DESKTOP": "stratifi",
		"XDG_SESSION_DESKTOP": "stratifi",
		"XDG_SESSION_CLASS":   "user",
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// An existing value wins (nested inside another desktop).
	t.Setenv("XDG_CURRENT_DESKTOP", "sway")
	applySessionEnv()
	if got := os.Getenv("XDG_CURRENT_DESKTOP"); got != "sway" {
		t.Errorf("XDG_CURRENT_DESKTOP = %q, want the outer value preserved", got)
	}
}

// TestSessionDesktopEntry: the shipped wayland-sessions entry is a valid,
// launchable application that runs the session script.
func TestSessionDesktopEntry(t *testing.T) {
	e, ok := desktop.ParseFile(filepath.Join("..", "..", "contrib", "stratifi.desktop"))
	if !ok {
		t.Fatal("contrib/stratifi.desktop did not parse as a launchable entry")
	}
	if e.Name != "Stratifi" {
		t.Errorf("Name = %q, want Stratifi", e.Name)
	}
	if len(e.Exec) == 0 || e.Exec[0] != "stratifi-session" {
		t.Errorf("Exec = %q, want [stratifi-session]", e.Exec)
	}
}
