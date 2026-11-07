package server

import (
	"bytes"
	"fmt"
	"os/exec"

	"golang.org/x/sys/unix"
	"stratifi/internal/config"
)

// DefaultXKBKeymap compiles a standard US QWERTY keymap using xkbcomp and returns a memfd.
func DefaultXKBKeymap() (int, uint32, error) {
	return CompileXKBKeymap("us")
}

// CompileXKBKeymap compiles the requested XKB layout (e.g. "us", "gb", "gb(extd)")
// using xkbcomp and returns a NUL-terminated memfd suitable for wl_keyboard.keymap (format v1).
func CompileXKBKeymap(layout string) (int, uint32, error) {
	if layout == "" {
		layout = "us"
	}
	if !config.IsValidXKBLayout(layout) {
		return -1, 0, fmt.Errorf("invalid xkb layout %q", layout)
	}

	input := fmt.Sprintf(`xkb_keymap {
    xkb_keycodes { include "evdev+aliases(qwerty)" };
    xkb_types { include "complete" };
    xkb_compat { include "complete" };
    xkb_symbols { include "pc+%s+inet(evdev)" };
    xkb_geometry { include "pc(pc105)" };
};`, layout)

	cmd := exec.Command("xkbcomp", "-xkb", "-", "-")
	cmd.Stdin = bytes.NewReader([]byte(input))
	out, err := cmd.Output()
	if err != nil {
		return -1, 0, fmt.Errorf("xkbcomp(%s) failed: %w", layout, err)
	}

	// wl_keyboard::keymap (format=xkb_v1) requires size to include a terminating NUL byte.
	out = append(out, 0)

	size := len(out)
	fd, err := unix.MemfdCreate("stratifi-xkb-keymap", unix.MFD_CLOEXEC)
	if err != nil {
		return -1, 0, fmt.Errorf("memfd create error: %w", err)
	}

	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		_ = unix.Close(fd)
		return -1, 0, fmt.Errorf("ftruncate error: %w", err)
	}

	n, err := unix.Write(fd, out)
	if err != nil || n != size {
		_ = unix.Close(fd)
		return -1, 0, fmt.Errorf("write keymap error: %w", err)
	}

	return fd, uint32(size), nil
}
