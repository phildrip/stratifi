package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Config is the compositor configuration. Every field has a default; a
// missing file is not an error (defaults apply). Unknown directives and
// duplicate scalar keys are errors — a typo'd keybinding that silently does
// nothing is worse than a loud startup failure.
type Config struct {
	Gap            int32
	BorderWidth    int32
	TitlebarHeight int32
	Bindings       []Binding
}

// Binding maps one key combination to an action. Combo and Action are kept
// as written ("Super+Return", "spawn"); interpretation into modifier masks
// and keysyms belongs to the input layer, not the parser.
type Binding struct {
	Combo  string
	Action string
	Args   []string
}

// Default returns the built-in configuration (matches the hardcoded values
// the compositor ran with before config existed).
func Default() Config {
	return Config{
		Gap:            8,
		BorderWidth:    3,
		TitlebarHeight: 28,
	}
}

// Parse reads one configuration document. Format: line-oriented, '#' starts
// a comment anywhere on a line, blank lines ignored. There is no quoting or
// escaping: values cannot contain whitespace or '#'. Directives:
//
//	gap N                  window gaps in px (>= 0)
//	border_width N         border width in px (>= 0)
//	titlebar_height N      titlebar height in px (> 0)
//	bind COMBO ACTION...   keybinding, e.g. "bind Super+Return spawn foot"
//
// Anything else is an error, as are duplicate scalar keys and malformed
// integers. Bindings accumulate in order.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	seen := map[string]int{} // scalar key -> first line number
	bind := func(fields []string, line int) error {
		if len(fields) < 3 {
			return parseError(line, "bind needs a combo and an action")
		}
		cfg.Bindings = append(cfg.Bindings, Binding{
			Combo:  fields[1],
			Action: fields[2],
			Args:   append([]string(nil), fields[3:]...),
		})
		return nil
	}

	for i, raw := range splitLines(data) {
		line := i + 1
		fields := splitFields(stripComment(raw))
		if len(fields) == 0 {
			continue
		}
		switch key := fields[0]; key {
		case "gap", "border_width", "titlebar_height":
			if _, dup := seen[key]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", key, seen[key])
			}
			if len(fields) != 2 {
				return Config{}, parseError(line, "%s needs exactly one value", key)
			}
			v, err := parsePx(fields[1])
			if err != nil {
				return Config{}, parseError(line, "%s: %v", key, err)
			}
			seen[key] = line
			switch key {
			case "gap":
				cfg.Gap = v
			case "border_width":
				cfg.BorderWidth = v
			case "titlebar_height":
				if v <= 0 {
					return Config{}, parseError(line, "titlebar_height must be positive")
				}
				cfg.TitlebarHeight = v
			}
		case "bind":
			if err := bind(fields, line); err != nil {
				return Config{}, err
			}
		default:
			return Config{}, parseError(line, "unknown directive %q", key)
		}
	}
	return cfg, nil
}

func parseError(line int, format string, args ...interface{}) error {
	return fmt.Errorf("config:%d: %s", line, fmt.Sprintf(format, args...))
}

// splitLines splits on '\n', tolerating a missing trailing newline and
// '\r\n' line endings.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range data {
		if c == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// stripComment cuts a '#' comment, honouring that '#' starts a comment
// anywhere (values never contain '#').
func stripComment(line []byte) []byte {
	for i, c := range line {
		if c == '#' {
			return line[:i]
		}
		if c == '\r' {
			return line[:i]
		}
	}
	// Trailing \r without following content.
	if n := len(line); n > 0 && line[n-1] == '\r' {
		return line[:n-1]
	}
	return line
}

// splitFields splits on ASCII whitespace (space, tab).
func splitFields(line []byte) []string {
	var fields []string
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != '\t' {
			j++
		}
		fields = append(fields, string(line[i:j]))
		i = j
	}
	return fields
}

// parsePx parses a non-negative pixel count.
func parsePx(s string) (int32, error) {
	if s == "" {
		return 0, errEmpty()
	}
	var v int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errNotInt(s)
		}
		v = v*10 + int64(c-'0')
		if v > 1<<30 {
			return 0, errRange(s)
		}
	}
	return int32(v), nil
}

type pxErr struct{ msg string }

func (e *pxErr) Error() string { return e.msg }
func errEmpty() error          { return &pxErr{"empty value"} }
func errNotInt(s string) error { return &pxErr{"not an integer: " + s} }
func errRange(s string) error  { return &pxErr{"out of range: " + s} }

// FindConfigPath locates the user configuration: $XDG_CONFIG_HOME/bloxwm/
// config, else ~/.config/bloxwm/config. A missing file is fine (defaults);
// the caller distinguishes by loading only if it exists.
func FindConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "bloxwm", "config")
}

// LoadFile parses path, or returns defaults (and no error) when the file
// does not exist.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, err
	}
	return Parse(data)
}
