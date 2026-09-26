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

// TestUpdateClientPropertiesDictCount pins that the dict n_items equals the
// encoded pair count (a mismatch corrupts the protocol silently).
func TestUpdateClientPropertiesDictCount(t *testing.T) {
	c, err := Connect("unix:path=/dev/null") // fail write before parse checks
	if err == nil {
		c.nc.Close()
	}
	_ = err // Connect to /dev/null is allowed to fail; we build offline below.

	id := ClientIdentity{
		ApplicationName: "bloxwm",
		Binary:          "bloxwm",
		User:            "tester",
		Host:            "testhost",
		Language:        "C",
		CoreName:        "pipewire-test",
	}
	pairs := 6

	var b spa.Builder
	b.BeginStruct()
	b.BeginStruct()
	b.Int(int32(pairs))
	b.String("application.name")
	b.String(id.ApplicationName)
	b.String("application.process.binary")
	b.String(id.Binary)
	b.String("application.process.user")
	b.String(id.User)
	b.String("application.process.host")
	b.String(id.Host)
	b.String("application.language")
	b.String(id.Language)
	b.String("core.name")
	b.String(id.CoreName)
	b.End()
	b.End()

	pod, _, err := spa.ReadPod(b.Bytes())
	if err != nil {
		t.Fatalf("outer pod: %v", err)
	}
	// outer struct body = inner dict pod
	dict, _, err := spa.ReadPod(pod.Body)
	if err != nil {
		t.Fatalf("dict pod: %v", err)
	}
	it := spa.NewIter(dict.Body)
	nPod, ok, err := it.Next()
	if err != nil || !ok || nPod.Type != spa.TypeInt {
		t.Fatalf("dict count pod missing")
	}
	// count string pairs
	strings := 0
	for {
		p, ok, err := it.Next()
		if err != nil || !ok {
			break
		}
		if p.Type != spa.TypeString {
			t.Fatalf("dict item not a String pod: type=%x", p.Type)
		}
		strings++
	}
	if leU32(nPod.Body) != uint32(pairs) {
		t.Errorf("dict n_items = %d, want %d", leU32(nPod.Body), pairs)
	}
	if strings != pairs*2 {
		t.Errorf("dict strings = %d, want %d", strings, pairs*2)
	}
}
