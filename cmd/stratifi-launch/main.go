// Command stratifi-launch is the fzf-driven window switcher: it lists the
// compositor's windows, pipes them through fzf, and focuses the selection.
//
// Bound as `bind Super+d spawn stratifi-launch`, it has no TTY, so it opens a
// terminal running itself (guarded against recursion). Run it directly in a
// terminal to use the current one.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"stratifi/internal/ipc"
)

func main() {
	// Spawned by the compositor with no terminal: open one running us.
	if os.Getenv("STRATIFI_LAUNCH_CHILD") != "1" && !isTTY(os.Stdin) {
		if err := spawnTerminal(); err != nil {
			fmt.Fprintf(os.Stderr, "stratifi-launch: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "stratifi-launch: %v\n", err)
		os.Exit(1)
	}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// spawnTerminal opens $TERMINAL (default foot) running this binary.
func spawnTerminal() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	term := os.Getenv("TERMINAL")
	if term == "" {
		term = "foot"
	}
	cmd := exec.Command(term, "-e", exe)
	cmd.Env = append(os.Environ(), "STRATIFI_LAUNCH_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func run() error {
	wins, err := ipc.Windows("")
	if err != nil {
		return err
	}
	if len(wins) == 0 {
		return nil // nothing to switch to
	}

	// One fzf input line per window: "<id>\t<label>". The id is the first
	// field, hidden by --with-nth and parsed back from the selection.
	var in strings.Builder
	for _, w := range wins {
		label := w.Title
		switch {
		case label == "":
			label = w.AppID
		case w.AppID != "":
			label = w.Title + "  ·  " + w.AppID
		}
		fmt.Fprintf(&in, "%d\t%s\n", w.WinID, label)
	}

	fzf := exec.Command("fzf", "--delimiter=\t", "--with-nth=2..", "--no-sort", "--prompt=window> ")
	fzf.Stdin = strings.NewReader(in.String())
	fzf.Stderr = os.Stderr
	out, err := fzf.Output()
	if err != nil {
		// fzf exits 130 on Esc/Ctrl-C: a cancelled pick, not an error.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil
		}
		return err
	}
	line := strings.TrimSpace(string(out))
	idStr, _, _ := strings.Cut(line, "\t")
	id, err := strconv.ParseUint(strings.TrimSpace(idStr), 10, 32)
	if err != nil {
		return nil // no selection
	}
	return ipc.Focus("", uint32(id))
}
