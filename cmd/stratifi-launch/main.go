// Command stratifi-launch is the fzf-driven combi launcher: it lists the
// compositor's windows and the installed desktop applications, pipes them
// through fzf, then focuses the picked window, launches the picked app, or
// runs whatever was typed.
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

	"stratifi/internal/desktop"
	"stratifi/internal/ipc"
)

func main() {
	// --list prints the combi list (windows + apps) and exits: a debug aid
	// and a way to verify without a TTY.
	if len(os.Args) > 1 && os.Args[1] == "--list" {
		wins, _ := ipc.Windows("")
		fmt.Print(buildInput(wins, desktop.Scan()))
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "--dpms" {
		var on bool
		switch strings.ToLower(os.Args[2]) {
		case "on", "1", "true":
			on = true
		case "off", "0", "false":
			on = false
		default:
			fmt.Fprintf(os.Stderr, "stratifi-launch: --dpms expects on|off\n")
			os.Exit(1)
		}
		if err := ipc.SetDPMS("", on); err != nil {
			fmt.Fprintf(os.Stderr, "stratifi-launch: %v\n", err)
			os.Exit(1)
		}
		return
	}
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

// run builds the combi list and drives fzf. Each entry's first (hidden) field
// is a key: "w:<id>" for a window, "a:<index>" for an app.
func run() error {
	apps := desktop.Scan()

	wins, _ := ipc.Windows("") // no compositor is not fatal: apps still list
	input := buildInput(wins, apps)

	fzf := exec.Command("fzf",
		"--delimiter=\t", "--with-nth=2..", "--no-sort",
		"--print-query", "--prompt=❯ ", "--height=100%")
	fzf.Stdin = strings.NewReader(input)
	fzf.Stderr = os.Stderr
	out, err := fzf.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil // Esc / Ctrl-C: cancelled
		}
		// fzf exit 1 = no match: fall through with just the query.
	}

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	query := strings.TrimSpace(lines[0])
	selection := ""
	if len(lines) > 1 {
		selection = lines[1]
	}

	if selection != "" {
		key, _, _ := strings.Cut(selection, "\t")
		if win, idx, ok := parseKey(key, len(apps)); ok {
			if win != 0 {
				return ipc.Focus("", win)
			}
			return launch(apps[idx])
		}
		return nil
	}

	// No selection but a query: run it (rofi's "run" mode).
	if query != "" {
		return ipc.Spawn("", []string{"sh", "-c", query})
	}
	return nil
}

// buildInput renders the combi list: one fzf line per window then per app,
// the first (hidden) field a key — "w:<id>" or "a:<index>".
func buildInput(wins []ipc.Window, apps []desktop.Entry) string {
	var b strings.Builder
	for _, w := range wins {
		label := w.Title
		switch {
		case label == "":
			label = w.AppID
		case w.AppID != "":
			label = w.Title + "  ·  " + w.AppID
		}
		if label == "" {
			label = fmt.Sprintf("window %d", w.WinID)
		}
		fmt.Fprintf(&b, "w:%d\t%s\n", w.WinID, label)
	}
	for i, a := range apps {
		fmt.Fprintf(&b, "a:%d\t%s\n", i, a.Name)
	}
	return b.String()
}

// parseKey decodes a selection key into either a window to focus (win != 0)
// or an app index to launch.
func parseKey(key string, nApps int) (win uint32, appIdx int, ok bool) {
	if rest, found := strings.CutPrefix(key, "w:"); found {
		id, err := strconv.ParseUint(rest, 10, 32)
		if err != nil || id == 0 {
			return 0, 0, false
		}
		return uint32(id), 0, true
	}
	if rest, found := strings.CutPrefix(key, "a:"); found {
		idx, err := strconv.Atoi(rest)
		if err != nil || idx < 0 || idx >= nApps {
			return 0, 0, false
		}
		return 0, idx, true
	}
	return 0, 0, false
}

// launch runs an app entry, wrapping in $TERMINAL when it asks for one.
func launch(a desktop.Entry) error {
	argv := a.Exec
	if a.Terminal {
		term := os.Getenv("TERMINAL")
		if term == "" {
			term = "foot"
		}
		argv = append([]string{term, "-e"}, argv...)
	}
	return ipc.Spawn("", argv)
}
