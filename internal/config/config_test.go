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

func TestParseXKBLayout(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"default is us", "# empty\n", "us"},
		{"native directive", "xkb_layout gb\n", "gb"},
		{"quoted layout", "xkb_layout \"gb\"\n", "gb"},
		{"sway input syntax", "input * xkb_layout \"gb\"\n", "gb"},
		{"variant syntax", "xkb_layout gb(extd)\n", "gb(extd)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.doc))
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.doc, err)
			}
			if cfg.XKBLayout != tc.want {
				t.Errorf("XKBLayout = %q, want %q", cfg.XKBLayout, tc.want)
			}
		})
	}

	bad := []string{
		"xkb_layout\n",
		"xkb_layout gb us\n",
		"xkb_layout \"\"\n",
		"xkb_layout gb\";\n",
		"xkb_layout ../gb\n",
		"xkb_layout gb(\n",
		"xkb_layout gb)\n",
		"xkb_layout gb\nxkb_layout us\n",
		"xkb_layout gb\ninput * xkb_layout us\n",
		"input * xkb_options caps:escape\n",
	}
	for _, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", doc)
		}
	}
}

func TestParseModes(t *testing.T) {
	doc := `
bind Super+r mode resize
bind Super+Pause mode system
bind_mode resize j resize_col_width -10
bind_mode resize semicolon resize_col_width 10
mode "resize" {
    bind k resize_window_height 10
    bind l resize_window_height -10
    bind Escape mode default
}
mode system {
    bind l spawn swaylock -f
    bind Escape mode default
}
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse modes: %v", err)
	}
	if len(cfg.Bindings) != 9 {
		t.Fatalf("len(Bindings) = %d, want 9: %+v", len(cfg.Bindings), cfg.Bindings)
	}
	if cfg.Bindings[0].Mode != "" || cfg.Bindings[0].Action != "mode" || cfg.Bindings[0].Args[0] != "resize" {
		t.Errorf("binding[0] = %+v, want default-mode 'mode resize'", cfg.Bindings[0])
	}
	if cfg.Bindings[2].Mode != "resize" || cfg.Bindings[2].Combo != "j" || cfg.Bindings[2].Action != "resize_col_width" {
		t.Errorf("binding[2] = %+v, want resize-mode 'j resize_col_width -10'", cfg.Bindings[2])
	}
	if cfg.Bindings[4].Mode != "resize" || cfg.Bindings[4].Combo != "k" || cfg.Bindings[4].Action != "resize_window_height" {
		t.Errorf("binding[4] = %+v, want resize-mode 'k resize_window_height 10'", cfg.Bindings[4])
	}
	if cfg.Bindings[7].Mode != "system" || cfg.Bindings[7].Combo != "l" || cfg.Bindings[7].Action != "spawn" {
		t.Errorf("binding[7] = %+v, want system-mode 'l spawn swaylock -f'", cfg.Bindings[7])
	}

	for _, bad := range []string{
		"bind_mode resize j\n",
		"mode resize\n",
		"mode \"\" {\n}\n",
		"mode resize {\nbind j close\n", // unclosed mode block
		"mode default {\nmode resize {\n}\n}\n",
		"}\n", // stray closing brace
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestParseOutputScale(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want float32
	}{
		{"default is 1.0", "# empty\n", 1.0},
		{"output_scale directive", "output_scale 1.25\n", 1.25},
		{"sway output scale syntax", "output HDMI-A-1 scale 1.25\n", 1.25},
		{"integer scale", "output * scale 2\n", 2.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.doc))
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.doc, err)
			}
			if cfg.OutputScale != tc.want {
				t.Errorf("OutputScale = %v, want %v", cfg.OutputScale, tc.want)
			}
		})
	}

	for _, bad := range []string{
		"output_scale\n",
		"output_scale 0\n",
		"output_scale 0.25\n",
		"output_scale 5\n",
		"output_scale abc\n",
		"output_scale 1.25\noutput_scale 1.5\n",
		"output_scale 1.25\noutput HDMI-A-1 scale 1.5\n",
		"output HDMI-A-1 mode 1920x1080\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestParseMultiOutputPolicyAndPosition(t *testing.T) {
	doc := `
output_policy mirror
output DP-1 pos 0 0 scale 1.0
output HDMI-A-1 position 1920 0 scale 2.0
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse multi-output: %v", err)
	}
	if cfg.OutputPolicy != "mirror" {
		t.Fatalf("OutputPolicy = %q, want mirror", cfg.OutputPolicy)
	}
	if len(cfg.Outputs) != 2 {
		t.Fatalf("len(Outputs) = %d, want 2", len(cfg.Outputs))
	}
	if cfg.Outputs[1].Name != "HDMI-A-1" || !cfg.Outputs[1].HasPos || cfg.Outputs[1].X != 1920 || cfg.Outputs[1].Scale != 2.0 {
		t.Fatalf("Outputs[1] = %+v", cfg.Outputs[1])
	}

	for _, bad := range []string{
		"output_policy clone\n",
		"output_policy join\noutput_policy mirror\n",
		"output DP-1 pos 100\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}
