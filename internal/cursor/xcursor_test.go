package cursor

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildXCursor synthesises a minimal two-image XCursor file so parsing can
// be tested without a theme on disk.
func buildXCursor(t *testing.T, sizes []int) []byte {
	t.Helper()
	const hdr = 16
	toc := len(sizes) * 12
	chunks := make([][]byte, len(sizes))
	pos := hdr + toc
	positions := make([]int, len(sizes))
	for i, s := range sizes {
		positions[i] = pos
		chunk := make([]byte, 36+s*s*4)
		binary.LittleEndian.PutUint32(chunk[0:], 36)
		binary.LittleEndian.PutUint32(chunk[4:], imgType)
		binary.LittleEndian.PutUint32(chunk[8:], uint32(s))
		binary.LittleEndian.PutUint32(chunk[12:], 1)
		binary.LittleEndian.PutUint32(chunk[16:], uint32(s))
		binary.LittleEndian.PutUint32(chunk[20:], uint32(s))
		binary.LittleEndian.PutUint32(chunk[24:], uint32(s/4)) // xhot
		binary.LittleEndian.PutUint32(chunk[28:], uint32(s/8)) // yhot
		for p := 36; p < len(chunk); p += 4 {
			binary.LittleEndian.PutUint32(chunk[p:], 0xFF00FF00) // opaque green
		}
		chunks[i] = chunk
		pos += len(chunk)
	}
	out := make([]byte, hdr+toc)
	binary.LittleEndian.PutUint32(out[0:], magic)
	binary.LittleEndian.PutUint32(out[4:], hdr)
	binary.LittleEndian.PutUint32(out[8:], 1)
	binary.LittleEndian.PutUint32(out[12:], uint32(len(sizes)))
	for i := range sizes {
		off := hdr + i*12
		binary.LittleEndian.PutUint32(out[off:], imgType)
		binary.LittleEndian.PutUint32(out[off+4:], uint32(sizes[i]))
		binary.LittleEndian.PutUint32(out[off+8:], uint32(positions[i]))
	}
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func TestParsePicksClosestSize(t *testing.T) {
	data := buildXCursor(t, []int{16, 24, 48})
	im, err := Parse(data, 24)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if im.W != 24 || im.H != 24 {
		t.Errorf("size = %dx%d, want 24x24", im.W, im.H)
	}
	if im.HotX != 6 || im.HotY != 3 {
		t.Errorf("hotspot = (%d,%d), want (6,3)", im.HotX, im.HotY)
	}
	if len(im.Pixels) != 24*24*4 {
		t.Errorf("pixels = %d bytes, want %d", len(im.Pixels), 24*24*4)
	}
	if im.Stride() != 96 {
		t.Errorf("stride = %d, want 96", im.Stride())
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("nope"), 24); err == nil {
		t.Error("short input accepted")
	}
	bad := buildXCursor(t, []int{24})
	binary.LittleEndian.PutUint32(bad[0:], 0xDEADBEEF)
	if _, err := Parse(bad, 24); err == nil {
		t.Error("bad magic accepted")
	}
}

func TestParseTruncatedPixels(t *testing.T) {
	data := buildXCursor(t, []int{24})
	data = data[:len(data)-16] // clip pixel data
	if _, err := Parse(data, 24); err == nil {
		t.Error("truncated pixel data accepted")
	}
}

// TestLoadRealTheme loads left_ptr from the system theme when present.
func TestLoadRealTheme(t *testing.T) {
	dir, err := ThemeDir()
	if err != nil {
		t.Skipf("no cursor theme: %v", err)
	}
	im, err := Load("left_ptr", 24)
	if err != nil {
		t.Skipf("left_ptr unavailable: %v", err)
	}
	if im.W <= 0 || im.H <= 0 || len(im.Pixels) != int(im.W*im.H*4) {
		t.Fatalf("bad image %dx%d (%d px)", im.W, im.H, len(im.Pixels))
	}
	t.Logf("loaded %s left_ptr %dx%d hotspot (%d,%d)", dir, im.W, im.H, im.HotX, im.HotY)
}

func TestThemeDirRespectsEnv(t *testing.T) {
	root := t.TempDir()
	mk := filepath.Join(root, "icons", "MyTheme", "cursors")
	if err := os.MkdirAll(mk, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("XCURSOR_THEME", "MyTheme")
	got, err := ThemeDir()
	if err != nil {
		t.Fatalf("ThemeDir: %v", err)
	}
	if want := filepath.Join(root, "icons", "MyTheme"); got != want {
		t.Errorf("ThemeDir = %q, want %q", got, want)
	}
}

func TestDefaultSize(t *testing.T) {
	t.Setenv("XCURSOR_SIZE", "32")
	if got := DefaultSize(); got != 32 {
		t.Errorf("DefaultSize = %d, want 32", got)
	}
	for _, bad := range []string{"", "0", "-5", "abc", "9999"} {
		t.Setenv("XCURSOR_SIZE", bad)
		if got := DefaultSize(); got != 24 {
			t.Errorf("DefaultSize(%q) = %d, want 24 fallback", bad, got)
		}
	}
}
