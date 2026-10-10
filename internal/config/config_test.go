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
