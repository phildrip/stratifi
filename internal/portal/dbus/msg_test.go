package dbus

import (
	"testing"
)

// TestDictEncodingRoundTrip drives the a{sv} portal argument encoding used by
// ScreenCast.CreateSession(options a{sv}).
func TestDictEncodingRoundTrip(t *testing.T) {
	var w Writer
	start := w.BeginDict()
	w.DictEntry("handle_token", "stratifi_token_1")
	w.DictEntry("session_handle_token", "stratifi_session_1")
	w.FinishDict(start)

	dict := w.Bytes()
	if len(dict) == 0 {
		t.Fatal("empty dict encoding")
	}

	// Trailing alignment is not required for the final dict entry; D-Bus
	// only aligns element starts. Verify via a decode loop below.

	// Decode the string keys by walking the buffer with a Reader: skip the
	// 8-aligned u32 length word, then walk entries 8-aligned.
	r := &Reader{buf: dict}
	r.off = 4 // length word
	var keys []string
	for r.Remaining() > 0 {
		for r.off%8 != 0 {
			r.off++
		}
		if r.Remaining() < 4 {
			break
		}
		key, err := r.ReadString()
		if err != nil {
			t.Fatalf("dict key decode failed: %v", err)
		}
		keys = append(keys, key)
		// skip variant: 4-align, sig (u8 len + bytes + NUL), then string
		for r.off%4 != 0 {
			r.off++
		}
		sigLen := int(r.buf[r.off])
		r.off += 1 + sigLen + 1
		_, err = r.ReadString()
		if err != nil {
			t.Fatalf("dict value decode failed: %v", err)
		}
	}
	if len(keys) != 2 || keys[0] != "handle_token" || keys[1] != "session_handle_token" {
		t.Fatalf("dict keys = %v", keys)
	}
}

// TestValidObjectPath is a table-driven path validation check.
func TestValidObjectPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/org/freedesktop/portal/desktop", true},
		{"/org/freedesktop/portal/desktop_2", true},
		{"", false},
		{"no-slash", false},
		{"/double//slash", false},
		{"/trailing/", false},
		{"/bad@char", false},
		{"/dollar$", false},
	}
	for _, tc := range cases {
		if got := ValidObjectPath(tc.path); got != tc.want {
			t.Errorf("ValidObjectPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestDecodeHeaderRejectsShort validates the truncated-message error path.
func TestDecodeHeaderRejectsShort(t *testing.T) {
	for i := 0; i < 15; i++ {
		if _, err := DecodeHeader(make([]byte, i)); err == nil {
			t.Errorf("DecodeHeader(%d bytes) unexpectedly succeeded", i)
		}
	}
}

// TestDecodeHeaderRejectsEndian covers the endianness guard.
func TestDecodeHeaderRejectsEndian(t *testing.T) {
	buf := make([]byte, 64)
	buf[0] = 'B' // big-endian unsupported
	if _, err := DecodeHeader(buf); err == nil {
		t.Fatal("expected error for big-endian message")
	}
	buf[0] = 'l'
	buf[3] = 2 // unsupported version
	if _, err := DecodeHeader(buf); err == nil {
		t.Fatal("expected error for unsupported protocol version")
	}
}
