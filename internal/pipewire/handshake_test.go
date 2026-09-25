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

// TestRegistryReal performs Hello + GetRegistry against the live daemon and
// lists the advertised globals, locating the Stream factory.
func TestRegistryReal(t *testing.T) {
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
	if err := c.UpdateClientProperties(); err != nil {
		t.Fatalf("UpdateClientProperties: %v", err)
	}
	registryID, err := c.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	c.registryID = registryID
	if err := c.Sync(0, 1); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	foundStream := false
	for time.Now().Before(deadline) {
		h, body, _, err := c.recvMsg()
		if err != nil {
			continue
		}
		if h.ID != registryID || h.Opcode != RegistryEventGlobal {
			continue
		}
		g, err := parseGlobal(body)
		if err != nil {
			t.Logf("global parse err: %v", err)
			continue
		}
		t.Logf("global id=%d type=%s/%d perms=%#x", g.ID, g.Type, g.Version, g.Permissions)
		if g.Type == TypeFactory || g.Type == TypeNode || g.Type == TypeClient {
			foundStream = true
		}
	}
	if !foundStream {
		t.Error("expected at least one factory/node global")
	}
}
