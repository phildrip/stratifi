package pipewire

import (
	"testing"
	"time"
)

// TestHandshakeReal performs the version handshake against the real
// PipeWire daemon and parses the core_info event.
func TestHandshakeReal(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	c, err := ConnectDefault()
	if err != nil {
		t.Skipf("no daemon: %v", err)
	}
	defer c.nc.Close()

	if err := c.Hello(); err != nil {
		t.Fatalf("Hello: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h, body, _, err := c.recvMsg()
		if err != nil {
			continue
		}
		if h.ID == 0 && h.Opcode == CoreEventInfo {
			info, err := parseCoreInfo(body)
			if err != nil {
				t.Fatalf("parse core_info: %v", err)
			}
			t.Logf("core: id=%d cookie=%d version=%q name=%q user=%q host=%q",
				info.ID, info.Cookie, info.Version, info.Name, info.UserName, info.HostName)
			if info.Version == "" || info.Name == "" {
				t.Error("core_info missing version/name")
			}
			if info.ID != 0 {
				t.Errorf("core id = %d, want 0", info.ID)
			}
			return
		}
	}
	t.Error("no core_info event within deadline")
}
