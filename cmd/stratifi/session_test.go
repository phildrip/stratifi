package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stratifi/internal/config"
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

// TestReadmeListsAllActions keeps the docs honest: every action the config
// validator accepts must appear in the README's action table.
func TestReadmeListsAllActions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readme := string(data)
	for _, a := range knownActions {
		if !strings.Contains(readme, a) {
			t.Errorf("README does not mention action %q", a)
		}
	}
}

func TestSwayParityConfigCompiles(t *testing.T) {
	doc := `
output HDMI-A-1 scale 1.25
input * xkb_layout "gb"
gap 6
border_width 2
titlebar_height 28
column_width 0.5
bind Super+Shift+space floating_toggle
bind Super+space focus_mode_toggle
bind Super+minus scratchpad_show
bind Super+Shift+minus move_scratchpad
bind Super+r mode resize
mode "resize" {
    bind j resize_col_width -10
    bind k resize_window_height 10
    bind l resize_window_height -10
    bind semicolon resize_col_width 10
    bind Return mode default
    bind Escape mode default
}
bind Super+Pause mode system
mode "system" {
    bind l spawn swaylock -f -c 2E3440
    bind e quit
    bind Shift+s spawn systemctl poweroff -i
    bind Return mode default
    bind Escape mode default
}
`
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse sway parity config: %v", err)
	}
	if cfg.XKBLayout != "gb" || cfg.OutputScale != 1.25 {
		t.Errorf("XKBLayout=%q OutputScale=%v, want gb/1.25", cfg.XKBLayout, cfg.OutputScale)
	}
	bs, err := compileBindings(cfg)
	if err != nil {
		t.Fatalf("compileBindings: %v", err)
	}
	if len(bs) != len(cfg.Bindings) {
		t.Errorf("compiled %d bindings, want %d", len(bs), len(cfg.Bindings))
	}

	if p := config.FindConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			userCfg, err := config.LoadFile(p)
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", p, err)
			}
			if _, err := compileBindings(userCfg); err != nil {
				t.Fatalf("compileBindings(%s): %v", p, err)
			}
		}
	}
}
