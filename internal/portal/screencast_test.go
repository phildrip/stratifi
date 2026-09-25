package portal

import (
	"testing"

	dbus "bloxwm/internal/portal/dbus"
)

// buildStartResponse encodes (u code, a{sv} results) with one stream,
// mirroring the exact layout ParseStartResults must decode.
func buildStartResponse(code uint32, nodeID uint32) []byte {
	var w dbus.Writer
	w.WriteUint32(code)

	// results a{sv}: length word 8-aligned (element alignment), entry aligned 8.
	w.Align(8)
	w.WriteString("streams")

	// variant: sig bytes then boxed a(ua{sv})
	w.Align(4)
	w.PutByte(byte(len("a(ua{sv})")))
	for _, c := range "a(ua{sv})" {
		w.PutByte(byte(c))
	}
	w.PutByte(0)

	// boxed value: array header aligned to element alignment (8), then
	// one struct (u node_id, a{sv} props) whose dict length lands 8-aligned.
	w.Align(8)
	w.WriteUint32(8) // array content: 4 (node) + 4 (dict length)
	w.WriteUint32(nodeID)
	w.WriteUint32(0) // empty props dict length

	return w.Bytes()
}

func TestParseStartResults(t *testing.T) {
	body := buildStartResponse(0, 42)
	code, streams, err := ParseStartResults(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if len(streams) != 1 || streams[0].NodeID != 42 {
		t.Fatalf("streams = %+v", streams)
	}
}

func TestParseStartResultsDenied(t *testing.T) {
	body := buildStartResponse(1, 0)
	code, _, err := ParseStartResults(body)
	if err == nil {
		t.Fatal("expected denied error")
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if _, ok := err.(*PortalError); !ok {
		t.Fatalf("error type = %T", err)
	}
}

func TestParseStartResultsTruncated(t *testing.T) {
	if _, _, err := ParseStartResults([]byte{0, 0}); err == nil {
		t.Fatal("expected truncated-body error")
	}
}
