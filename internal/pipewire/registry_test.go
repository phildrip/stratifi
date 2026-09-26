package pipewire

import (
	"testing"

	"bloxwm/internal/pipewire/spa"
)

// buildGlobalEvent builds a registry global event pod the way the daemon
// marshals it: Struct{Int id, Int perms, String type, Int version, dict}.
func buildGlobal(id, perms uint32, typ string, version uint32) []byte {
	var b spa.Builder
	b.BeginStruct()
	b.Int(int32(id))
	b.Int(int32(perms))
	b.String(typ)
	b.Int(int32(version))
	b.BeginStruct() // dict
	b.Int(1)
	b.String("object.serial")
	b.String("42")
	b.End()
	b.End()
	return b.Bytes()
}

func TestParseGlobal(t *testing.T) {
	body := buildGlobal(29, 0x148, TypeNode, 3)
	g, err := parseGlobal(body)
	if err != nil {
		t.Fatalf("parseGlobal: %v", err)
	}
	if g.ID != 29 || g.Permissions != 0x148 || g.Type != TypeNode || g.Version != 3 {
		t.Errorf("global = %+v", g)
	}
}

func TestParseGlobalTruncated(t *testing.T) {
	if _, err := parseGlobal([]byte{0x10, 0, 0, 0}); err == nil {
		t.Error("expected error for truncated global body")
	}
	if _, err := parseGlobal(nil); err == nil {
		t.Error("expected error for empty body")
	}
}

// TestNextResIDSequential pins the sequential proxy id rule: ids must start
// at 2 (PW_ID_CLIENT=1 is reserved) and never skip — the daemon's object map
// rejects gaps with ENOSPC.
func TestNextResIDSequential(t *testing.T) {
	var c Conn
	if id := c.nextResID.next(); id != 2 {
		t.Errorf("first proxy id = %d, want 2", id)
	}
	if id := c.nextResID.next(); id != 3 {
		t.Errorf("second proxy id = %d, want 3", id)
	}
}

// TestParseCoreInfo builds the daemon's core_info shape and parses it.
func TestParseCoreInfo(t *testing.T) {
	var b spa.Builder
	b.BeginStruct()
	b.Int(0)          // id
	b.Int(1304448578) // cookie
	b.String("phil")
	b.String("phil-desktop")
	b.String("1.6.2")
	b.String("pipewire-0")
	b.Long(3)       // change mask
	b.BeginStruct() // dict
	b.Int(0)
	b.End()
	b.End()

	info, err := parseCoreInfo(b.Bytes())
	if err != nil {
		t.Fatalf("parseCoreInfo: %v", err)
	}
	if info.ID != 0 || info.Cookie != 1304448578 || info.Version != "1.6.2" ||
		info.Name != "pipewire-0" || info.UserName != "phil" || info.HostName != "phil-desktop" {
		t.Errorf("core info = %+v", info)
	}
}
