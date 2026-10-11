// Package cursor loads XCursor theme images for the compositor's own
// pointer. Client-provided cursors arrive as wl_surface buffers and do not
// come through here; this is the fallback shape (arrow, text, resize, …)
// used over compositor chrome and empty space.
package cursor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Image is one decoded cursor: ARGB8888 pixels (little-endian, so bytes are
// B,G,R,A) plus the hotspot offset from the top-left.
type Image struct {
	Pixels []byte
	W, H   int32
	HotX   int32
	HotY   int32
}

// Stride is the row length in bytes.
func (im *Image) Stride() int32 { return im.W * 4 }

const (
	magic    = 0x72756358 // "Xcur"
	imgType  = 0xFFFD0002 // XCURSOR_IMAGE_TYPE
	hdrBytes = 16
	tocBytes = 12
)

// Parse decodes an XCursor file, choosing the image whose nominal size is
// closest to want (want <= 0 means any).
func Parse(data []byte, want int) (*Image, error) {
	if len(data) < hdrBytes {
		return nil, errors.New("cursor: file too short")
	}
	if binary.LittleEndian.Uint32(data[0:4]) != magic {
		return nil, errors.New("cursor: bad magic")
	}
	ntoc := int(binary.LittleEndian.Uint32(data[12:16]))

	type tocEntry struct{ size, pos uint32 }
	var images []tocEntry
	for i := 0; i < ntoc; i++ {
		off := hdrBytes + i*tocBytes
		if off+tocBytes > len(data) {
			break
		}
		typ := binary.LittleEndian.Uint32(data[off : off+4])
		if typ != imgType {
			continue // comments and other chunk types are skipped
		}
		images = append(images, tocEntry{
			size: binary.LittleEndian.Uint32(data[off+4 : off+8]),
			pos:  binary.LittleEndian.Uint32(data[off+8 : off+12]),
		})
	}
	if len(images) == 0 {
		return nil, errors.New("cursor: no images")
	}

	// Pick the nominal size closest to want (ties -> first).
	best := 0
	if want > 0 {
		bestDiff := absDiff(int(images[0].size), want)
		for i := 1; i < len(images); i++ {
			if d := absDiff(int(images[i].size), want); d < bestDiff {
				best, bestDiff = i, d
			}
		}
	}

	p := int(images[best].pos)
	if p+36 > len(data) {
		return nil, errors.New("cursor: chunk out of range")
	}
	// Chunk: header(4) type(4) subtype(4) version(4) w(4) h(4) xhot(4)
	// yhot(4) delay(4) then pixels.
	w := int(binary.LittleEndian.Uint32(data[p+16 : p+20]))
	h := int(binary.LittleEndian.Uint32(data[p+20 : p+24]))
	xhot := binary.LittleEndian.Uint32(data[p+24 : p+28])
	yhot := binary.LittleEndian.Uint32(data[p+28 : p+32])
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return nil, fmt.Errorf("cursor: bad dimensions %dx%d", w, h)
	}
	n := w * h * 4
	if p+36+n > len(data) {
		return nil, errors.New("cursor: pixel data truncated")
	}
	px := make([]byte, n)
	copy(px, data[p+36:p+36+n])
	return &Image{Pixels: px, W: int32(w), H: int32(h), HotX: int32(xhot), HotY: int32(yhot)}, nil
}

// Load reads a named cursor from the resolved theme, e.g. Load("left_ptr", 24).
func Load(name string, size int) (*Image, error) {
	dir, err := ThemeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "cursors", name))
	if err != nil {
		return nil, fmt.Errorf("cursor: read %q: %w", name, err)
	}
	return Parse(data, size)
}

// ThemeDir resolves the cursor theme directory: $XCURSOR_THEME, else the
// system default theme's Inherits, else "default"; searched across the
// standard icon directories.
func ThemeDir() (string, error) {
	name := os.Getenv("XCURSOR_THEME")
	if name == "" {
		name = defaultThemeName()
	}
	for _, root := range iconRoots() {
		cand := filepath.Join(root, name)
		if dirExists(filepath.Join(cand, "cursors")) {
			return cand, nil
		}
	}
	// Last resort: any theme with a cursors directory.
	for _, root := range iconRoots() {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			cand := filepath.Join(root, e.Name())
			if dirExists(filepath.Join(cand, "cursors")) {
				return cand, nil
			}
		}
	}
	return "", errors.New("cursor: no cursor theme found")
}

// defaultThemeName reads Inherits= from the system default theme, falling
// back to the conventional "default".
func defaultThemeName() string {
	for _, root := range iconRoots() {
		data, err := os.ReadFile(filepath.Join(root, "default", "index.theme"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Inherits="); ok {
				if v = strings.TrimSpace(v); v != "" {
					return v
				}
			}
		}
	}
	return "default"
}

func iconRoots() []string {
	roots := []string{}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		roots = append(roots, filepath.Join(d, "icons"))
	}
	if h, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(h, ".icons"))
	}
	roots = append(roots,
		"/usr/share/icons",
		"/usr/local/share/icons",
		"/usr/share/pixmaps",
	)
	return roots
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
