package main

import (
	"strings"
	"testing"

	"stratifi/internal/desktop"
	"stratifi/internal/ipc"
)

func TestBuildInput(t *testing.T) {
	wins := []ipc.Window{
		{WinID: 65536, AppID: "foot", Title: "~/git/stratifi"},
		{WinID: 65537, AppID: "", Title: ""},
	}
	apps := []desktop.Entry{{Name: "Firefox"}, {Name: "Zed"}}
	got := buildInput(wins, apps)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	want := []string{
		"w:65536\t~/git/stratifi  ·  foot",
		"w:65537\twindow 65537",
		"a:0\tFirefox",
		"a:1\tZed",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %d, want %d\n%s", len(lines), len(want), got)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestParseKey(t *testing.T) {
	const nApps = 3
	if win, _, ok := parseKey("w:65536", nApps); !ok || win != 65536 {
		t.Errorf("w:65536 -> win=%d ok=%v, want 65536/true", win, ok)
	}
	if win, idx, ok := parseKey("a:2", nApps); !ok || win != 0 || idx != 2 {
		t.Errorf("a:2 -> win=%d idx=%d ok=%v, want 0/2/true", win, idx, ok)
	}
	for _, bad := range []string{"", "x:1", "w:", "w:0", "w:abc", "a:", "a:9", "a:-1"} {
		if _, _, ok := parseKey(bad, nApps); ok {
			t.Errorf("parseKey(%q) accepted, want rejected", bad)
		}
	}
}
