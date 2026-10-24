package ipc

import (
	"path/filepath"
	"testing"
)

type fakeHandler struct {
	wins    []Window
	focused []uint32
	spawned [][]string
}

func (f *fakeHandler) Windows() []Window { return f.wins }
func (f *fakeHandler) Spawn(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	f.spawned = append(f.spawned, argv)
	return true
}
func (f *fakeHandler) Focus(winID uint32) bool {
	if winID == 0 {
		return false
	}
	f.focused = append(f.focused, winID)
	return true
}

func TestServeListFocus(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ipc.sock")
	h := &fakeHandler{wins: []Window{
		{WinID: 65536, AppID: "foot", Title: "~/git/stratifi"},
		{WinID: 65537, AppID: "firefox", Title: "Inbox\twith a tab"},
	}}
	stop, err := Serve(sock, h)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer stop()

	wins, err := Windows(sock)
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if len(wins) != 2 {
		t.Fatalf("windows = %d, want 2", len(wins))
	}
	if wins[0].WinID != 65536 || wins[0].AppID != "foot" || wins[0].Title != "~/git/stratifi" {
		t.Errorf("win[0] = %+v", wins[0])
	}
	// Tabs in a title must not split the record.
	if wins[1].Title != "Inbox with a tab" {
		t.Errorf("win[1].Title = %q, want tab sanitized", wins[1].Title)
	}

	if err := Focus(sock, 65537); err != nil {
		t.Fatalf("Focus: %v", err)
	}
	if len(h.focused) != 1 || h.focused[0] != 65537 {
		t.Errorf("focused = %v, want [65537]", h.focused)
	}

	// An unknown id reports failure rather than a silent OK.
	if err := Focus(sock, 0); err == nil {
		t.Error("Focus(0) should fail")
	}

	// Spawn carries argv across tabs (arguments with spaces survive).
	if err := Spawn(sock, []string{"/usr/bin/foot", "-e", "sh -c hi"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(h.spawned) != 1 || h.spawned[0][2] != "sh -c hi" {
		t.Errorf("spawned = %v, want argv with the spaced arg intact", h.spawned)
	}
	if err := Spawn(sock, nil); err == nil {
		t.Error("Spawn(nil) should fail")
	}
}
