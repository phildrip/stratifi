package config

import (
	"reflect"
	"testing"
)

func TestParseScalars(t *testing.T) {
	cfg, err := Parse([]byte("gap 12\nborder_width 0\ntitlebar_height 40\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Gap != 12 || cfg.BorderWidth != 0 || cfg.TitlebarHeight != 40 {
		t.Errorf("got %+v, want gap 12 border 0 title 40", cfg)
	}
}

func TestParseDefaultsUntouched(t *testing.T) {
	cfg, err := Parse([]byte("# only a comment\n\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("got %+v, want defaults %+v", cfg, Default())
	}
}

func TestParseBindings(t *testing.T) {
	cfg, err := Parse([]byte("bind Super+Return spawn foot\nbind Super+q close\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Binding{
		{Combo: "Super+Return", Action: "spawn", Args: []string{"foot"}},
		{Combo: "Super+q", Action: "close"},
	}
	if !reflect.DeepEqual(cfg.Bindings, want) {
		t.Errorf("bindings = %+v, want %+v", cfg.Bindings, want)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"unknown directive", "frobnicate 1\n"},
		{"duplicate scalar", "gap 8\ngap 9\n"},
		{"missing value", "gap\n"},
		{"extra value", "gap 8 9\n"},
		{"not an integer", "gap eight\n"},
		{"negative", "gap -1\n"},
		{"zero titlebar", "titlebar_height 0\n"},
		{"bind without action", "bind Super+q\n"},
		{"bind bare", "bind\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.doc)); err == nil {
				t.Errorf("Parse(%q) succeeded, want error", tc.doc)
			}
		})
	}
}

func TestParseToleratesWhitespace(t *testing.T) {
	cfg, err := Parse([]byte("  gap\t12  # trailing comment\r\n\tbind  Super+q\tclose  \r\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Gap != 12 {
		t.Errorf("gap = %d, want 12", cfg.Gap)
	}
	if len(cfg.Bindings) != 1 || cfg.Bindings[0].Action != "close" {
		t.Errorf("bindings = %+v, want one close", cfg.Bindings)
	}
}

func TestLoadFileMissingIsDefault(t *testing.T) {
	cfg, err := LoadFile(t.TempDir() + "/does-not-exist")
	if err != nil {
		t.Fatalf("LoadFile missing: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("got %+v, want defaults", cfg)
	}
}

func TestParseColumnWidth(t *testing.T) {
	cfg, err := Parse([]byte("column_width 0.5\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.ColumnWidth != 0.5 {
		t.Errorf("ColumnWidth = %v, want 0.5", cfg.ColumnWidth)
	}
	for _, bad := range []string{"column_width 0\n", "column_width 2\n", "column_width x\n", "column_width\n"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestParseGestureBindings(t *testing.T) {
	cfg, err := Parse([]byte("bind_gesture swipe left workspace_prev\nbind_gesture swipe right workspace_next\nbind_gesture swipe up 4 overview\nbind_gesture pinch 0 0 overview\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []GestureBinding{
		{Type: "swipe", Direction: "left", Action: "workspace_prev"},
		{Type: "swipe", Direction: "right", Action: "workspace_next"},
		{Type: "swipe", Direction: "up", Fingers: 4, Action: "overview"},
		{Type: "pinch", Fingers: 0, Action: "overview"},
	}
	if len(cfg.Gestures) != len(want) {
		t.Fatalf("gestures = %d, want %d", len(cfg.Gestures), len(want))
	}
	for i := range want {
		if cfg.Gestures[i] != want[i] {
			t.Errorf("gesture[%d] = %+v, want %+v", i, cfg.Gestures[i], want[i])
		}
	}
}

func TestParseGestureBindingErrors(t *testing.T) {
	for _, bad := range []string{
		"bind_gesture swipe\n",
		"bind_gesture wobble left workspace_prev\n",
		"bind_gesture swipe sideways workspace_prev\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestParseExecAndContinuation(t *testing.T) {
	cfg, err := Parse([]byte(`
gap 6
exec xsettingsd
exec swayidle -w \
     timeout 300 'swaylock -f' \
     before-sleep 'swaylock -f'
bind Super+Return spawn foot
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Exec) != 2 {
		t.Fatalf("exec = %d, want 2 (%v)", len(cfg.Exec), cfg.Exec)
	}
	if cfg.Exec[0] != "xsettingsd" {
		t.Errorf("exec[0] = %q, want xsettingsd", cfg.Exec[0])
	}
	// Continuations join; internal spacing/quotes preserved.
	want := "swayidle -w timeout 300 'swaylock -f' before-sleep 'swaylock -f'"
	if cfg.Exec[1] != want {
		t.Errorf("exec[1] = %q\n         want %q", cfg.Exec[1], want)
	}
	if cfg.Gap != 6 || len(cfg.Bindings) != 1 {
		t.Errorf("gap/bindings = %d/%d, want 6/1", cfg.Gap, len(cfg.Bindings))
	}
}

func TestParseExecNeedsCommand(t *testing.T) {
	if _, err := Parse([]byte("exec\n")); err == nil {
		t.Error("bare exec accepted, want error")
	}
}
