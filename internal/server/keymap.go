package server

import (
	"bytes"
	"fmt"
	"os/exec"

	"golang.org/x/sys/unix"
)

// DefaultXKBKeymap compiles a standard US QWERTY keymap using xkbcomp and returns a sealed memfd.
func DefaultXKBKeymap() (int, uint32, error) {
	input := `xkb_keymap {
    xkb_keycodes { include "evdev+aliases(qwerty)" };
    xkb_types { include "complete" };
    xkb_compat { include "complete" };
    xkb_symbols { include "pc+us+inet(evdev)" };
    xkb_geometry { include "pc(pc105)" };
};`

	cmd := exec.Command("xkbcomp", "-xkb", "-", "-")
	cmd.Stdin = bytes.NewReader([]byte(input))
	out, err := cmd.Output()
	if err != nil {
		return -1, 0, fmt.Errorf("xkbcomp failed: %w", err)
	}

	size := len(out)
	fd, err := unix.MemfdCreate("bloxwm-xkb-keymap", unix.MFD_CLOEXEC)
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
