package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the compositor configuration. Every field has a default; a
// missing file is not an error (defaults apply). Unknown directives and
// duplicate scalar keys are errors — a typo'd keybinding that silently does
// nothing is worse than a loud startup failure.
type Config struct {
	Gap            int32
	BorderWidth    int32
	TitlebarHeight int32
	// ColumnWidth is the scrollable column width as a fraction of the
	// output (niri model); 0 means the layout default (1/2).
	ColumnWidth float32
	// XKBLayout is the XKB keyboard layout name (e.g. "us", "gb", "gb(extd)").
	XKBLayout string
	// OutputScale is the display scale factor (e.g. 1.0, 1.25, 2.0).
	OutputScale float32
	// OutputPolicy is the multi-output layout policy: "join" (default) or "mirror".
	OutputPolicy string
	// XWayland controls XWayland support: "on_demand" (default), "enable", or "disable".
	XWayland string
	// Outputs holds per-output rules (`output <name> ...`).
	Outputs  []OutputConfig
	Bindings []Binding
	Gestures []GestureBinding
	// Exec holds startup commands (`exec <cmd>`), run once the compositor is
	// up; each is passed to `sh -c`, like sway's exec.
	Exec []string
}

// OutputConfig holds per-output layout configuration (`output <name> ...`).
type OutputConfig struct {
	Name   string
	X, Y   int32
	HasPos bool
	Scale  float32
}

// GestureBinding is one `bind_gesture` line: a recognized gesture mapped to
// a compositor action. Direction and Fingers are optional filters.
type GestureBinding struct {
	Type      string // "swipe", "pinch", "tap", "scroll"
	Direction string // "left"/"right"/"up"/"down" or "" for any
	Fingers   int    // 0 = any
	Action    string
}

// Binding maps one key combination to an action. Combo and Action are kept
// as written ("Super+Return", "spawn"); interpretation into modifier masks
// and keysyms belongs to the input layer, not the parser.
type Binding struct {
	Mode   string // "" means default mode
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
		XKBLayout:      "us",
		OutputScale:    1.0,
		OutputPolicy:   "join",
		XWayland:       "on_demand",
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
//	exec CMD...            run CMD at startup (via sh -c), repeatable
//
// A line ending in \ continues on the next line.
//
// Anything else is an error, as are duplicate scalar keys and malformed
// integers. Bindings accumulate in order.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	seen := map[string]int{} // scalar key -> first line number
	currentMode := ""
	modeStartLine := 0
	normalizeMode := func(m string) (string, bool) {
		if len(m) >= 2 && m[0] == '"' && m[len(m)-1] == '"' {
			m = m[1 : len(m)-1]
		}
		if m == "" {
			return "", false
		}
		if m == "default" {
			return "", true
		}
		return m, true
	}
	bind := func(mode string, fields []string, line int) error {
		if len(fields) < 3 {
			return parseError(line, "bind needs a combo and an action")
		}
		cfg.Bindings = append(cfg.Bindings, Binding{
			Mode:   mode,
			Combo:  fields[1],
			Action: fields[2],
			Args:   append([]string(nil), fields[3:]...),
		})
		return nil
	}

	lines := splitLines(data)
	for i := 0; i < len(lines); i++ {
		line := i + 1
		raw := string(stripComment(lines[i]))
		// Join backslash continuations (sway-style "exec ... \<newline>").
		for endsWithContinuation(raw) && i+1 < len(lines) {
			i++
			raw = dropContinuation(raw) + " " + strings.TrimSpace(string(stripComment(lines[i])))
		}
		fields := splitFields([]byte(raw))
		if len(fields) == 0 {
			continue
		}
		switch key := fields[0]; key {
		case "column_width":
			if _, dup := seen[key]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", key, seen[key])
			}
			if len(fields) != 2 {
				return Config{}, parseError(line, "column_width needs exactly one value")
			}
			f, err := parseFrac(fields[1])
			if err != nil {
				return Config{}, parseError(line, "column_width: %v", err)
			}
			seen[key] = line
			cfg.ColumnWidth = f
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
		case "exec":
			cmd := restAfterFirstField(raw)
			if cmd == "" {
				return Config{}, parseError(line, "exec needs a command")
			}
			cfg.Exec = append(cfg.Exec, cmd)
		case "bind":
			if err := bind(currentMode, fields, line); err != nil {
				return Config{}, err
			}
		case "bind_mode":
			if len(fields) < 4 {
				return Config{}, parseError(line, "bind_mode needs a mode, a combo, and an action")
			}
			mode, ok := normalizeMode(fields[1])
			if !ok {
				return Config{}, parseError(line, "bind_mode needs a non-empty mode name")
			}
			cfg.Bindings = append(cfg.Bindings, Binding{
				Mode:   mode,
				Combo:  fields[2],
				Action: fields[3],
				Args:   append([]string(nil), fields[4:]...),
			})
		case "mode":
			if modeStartLine != 0 {
				return Config{}, parseError(line, "nested mode blocks are not supported")
			}
			if len(fields) != 3 || fields[2] != "{" {
				return Config{}, parseError(line, "mode block syntax is 'mode <name> {'")
			}
			mode, ok := normalizeMode(fields[1])
			if !ok {
				return Config{}, parseError(line, "mode needs a non-empty name")
			}
			currentMode = mode
			modeStartLine = line
		case "}":
			if modeStartLine == 0 || len(fields) != 1 {
				return Config{}, parseError(line, "unexpected '}'")
			}
			currentMode = ""
			modeStartLine = 0
		case "bind_gesture":
			gb, err := parseGestureBinding(fields, line)
			if err != nil {
				return Config{}, err
			}
			cfg.Gestures = append(cfg.Gestures, gb)
		case "xkb_layout":
			if _, dup := seen["xkb_layout"]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", "xkb_layout", seen["xkb_layout"])
			}
			if len(fields) != 2 {
				return Config{}, parseError(line, "xkb_layout needs exactly one value")
			}
			layout, ok := parseXKBLayout(fields[1])
			if !ok {
				return Config{}, parseError(line, "invalid xkb_layout %q", fields[1])
			}
			seen["xkb_layout"] = line
			cfg.XKBLayout = layout
		case "input":
			// Sway compatibility: `input * xkb_layout "gb"`
			if len(fields) != 4 || fields[1] != "*" || fields[2] != "xkb_layout" {
				return Config{}, parseError(line, "unsupported input directive (expected 'input * xkb_layout <layout>')")
			}
			if _, dup := seen["xkb_layout"]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", "xkb_layout", seen["xkb_layout"])
			}
			layout, ok := parseXKBLayout(fields[3])
			if !ok {
				return Config{}, parseError(line, "invalid xkb_layout %q", fields[3])
			}
			seen["xkb_layout"] = line
			cfg.XKBLayout = layout
		case "output_policy":
			if _, dup := seen["output_policy"]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", "output_policy", seen["output_policy"])
			}
			if len(fields) != 2 || (fields[1] != "join" && fields[1] != "mirror") {
				return Config{}, parseError(line, "output_policy must be 'join' or 'mirror'")
			}
			seen["output_policy"] = line
			cfg.OutputPolicy = fields[1]
		case "xwayland":
			if _, dup := seen["xwayland"]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", "xwayland", seen["xwayland"])
			}
			if len(fields) != 2 || (fields[1] != "enable" && fields[1] != "disable" && fields[1] != "on_demand") {
				return Config{}, parseError(line, "xwayland must be 'enable', 'disable', or 'on_demand'")
			}
			seen["xwayland"] = line
			cfg.XWayland = fields[1]
		case "output_scale":
			if _, dup := seen["output_scale"]; dup {
				return Config{}, parseError(line, "duplicate key %q (first at line %d)", "output_scale", seen["output_scale"])
			}
			if len(fields) != 2 {
				return Config{}, parseError(line, "output_scale needs exactly one value")
			}
			sc, err := parseScale(fields[1])
			if err != nil {
				return Config{}, parseError(line, "output_scale: %v", err)
			}
			seen["output_scale"] = line
			seen["output_scale_explicit"] = line
			cfg.OutputScale = sc
		case "output":
			// Sway compatibility:
			//   `output <name> scale <float>`
			//   `output <name> pos <x> <y> [scale <float>]`
			if len(fields) < 4 {
				return Config{}, parseError(line, "unsupported output directive")
			}
			oc := OutputConfig{Name: fields[1], Scale: 1.0}
			idx := 2
			hasField := false
			for idx < len(fields) {
				switch fields[idx] {
				case "scale":
					if idx+1 >= len(fields) {
						return Config{}, parseError(line, "output scale needs a value")
					}
					if _, dup := seen["output_scale_explicit"]; dup {
						return Config{}, parseError(line, "duplicate key %q (first at line %d)", "output_scale", seen["output_scale_explicit"])
					}
					sc, err := parseScale(fields[idx+1])
					if err != nil {
						return Config{}, parseError(line, "output scale: %v", err)
					}
					oc.Scale = sc
					if _, set := seen["output_scale"]; !set {
						seen["output_scale"] = line
						cfg.OutputScale = sc
					}
					hasField = true
					idx += 2
				case "pos", "position":
					if idx+2 >= len(fields) {
						return Config{}, parseError(line, "output pos needs X and Y values")
					}
					x, err := parsePx(fields[idx+1])
					if err != nil {
						return Config{}, parseError(line, "output pos X: %v", err)
					}
					y, err := parsePx(fields[idx+2])
					if err != nil {
						return Config{}, parseError(line, "output pos Y: %v", err)
					}
					oc.X = x
					oc.Y = y
					oc.HasPos = true
					hasField = true
					idx += 3
				default:
					return Config{}, parseError(line, "unsupported output directive %q", fields[idx])
				}
			}
			if !hasField {
				return Config{}, parseError(line, "empty output directive")
			}
			cfg.Outputs = append(cfg.Outputs, oc)
		default:
			return Config{}, parseError(line, "unknown directive %q", key)
		}
	}
	if modeStartLine != 0 {
		return Config{}, parseError(modeStartLine, "unclosed mode block")
	}
	return cfg, nil
}

func parseError(line int, format string, args ...interface{}) error {
	return fmt.Errorf("config:%d: %s", line, fmt.Sprintf(format, args...))
}

// splitLines splits on '\n', tolerating a missing trailing newline and
// '\r\n' line endings.
// endsWithContinuation reports whether a (comment-stripped) line ends with a
// backslash continuation marker (sway-style multi-line directives).
func endsWithContinuation(s string) bool {
	s = strings.TrimRight(s, " \t")
	return strings.HasSuffix(s, "\\")
}

// dropContinuation removes a trailing backslash and any spaces around it.
func dropContinuation(s string) string {
	s = strings.TrimRight(s, " \t")
	s = strings.TrimRight(s, "\\")
	return strings.TrimRight(s, " \t")
}

// restAfterFirstField returns everything after the first whitespace-delimited
// token, trimmed — internal spacing and quotes preserved (for `exec`).
func restAfterFirstField(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}
	return strings.TrimSpace(s[i:])
}

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

// FindConfigPath locates the user configuration: $XDG_CONFIG_HOME/stratifi/
// config, else ~/.config/stratifi/config. A missing file is fine (defaults);
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
	return filepath.Join(dir, "stratifi", "config")
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

// parseFrac parses a fraction in (0, 1] (e.g. "0.5", "0.333", "1").
func parseFrac(s string) (float32, error) {
	if s == "" {
		return 0, errEmpty()
	}
	var whole, frac float64
	var fracDiv float64 = 1
	seenDot := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if seenDot {
				fracDiv *= 10
				frac = frac*10 + float64(c-'0')
			} else {
				whole = whole*10 + float64(c-'0')
			}
		case c == '.':
			if seenDot {
				return 0, errNotInt(s)
			}
			seenDot = true
		default:
			return 0, errNotInt(s)
		}
	}
	v := whole + frac/fracDiv
	if v <= 0 || v > 1 {
		return 0, &pxErr{"fraction out of range (0,1]: " + s}
	}
	return float32(v), nil
}

// parseScale parses a display scale factor in [0.5, 4.0] (e.g. "1.0", "1.25", "2").
func parseScale(s string) (float32, error) {
	if s == "" {
		return 0, errEmpty()
	}
	var whole, frac float64
	var fracDiv float64 = 1
	seenDot := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if seenDot {
				fracDiv *= 10
				frac = frac*10 + float64(c-'0')
			} else {
				whole = whole*10 + float64(c-'0')
			}
		case c == '.':
			if seenDot {
				return 0, errNotInt(s)
			}
			seenDot = true
		default:
			return 0, errNotInt(s)
		}
	}
	v := whole + frac/fracDiv
	if v < 0.5 || v > 4.0 {
		return 0, &pxErr{"scale out of range [0.5, 4.0]: " + s}
	}
	return float32(v), nil
}

// parseGestureBinding parses: bind_gesture <type> [direction] [fingers] <action>
// where direction/fingers are optional. Examples:
//
//	bind_gesture swipe left workspace_prev
//	bind_gesture swipe right workspace_next
//	bind_gesture swipe up 4 overview
//	bind_gesture pinch 0 0 overview
func parseGestureBinding(fields []string, line int) (GestureBinding, error) {
	if len(fields) < 3 {
		return GestureBinding{}, parseError(line, "bind_gesture needs a type and an action")
	}
	gb := GestureBinding{Type: fields[1]}
	rest := fields[2:]
	// Trailing token is always the action; anything before it is filters.
	gb.Action = rest[len(rest)-1]
	rest = rest[:len(rest)-1]

	switch gb.Type {
	case "swipe", "pinch", "tap", "scroll":
	default:
		return GestureBinding{}, parseError(line, "unknown gesture type %q", gb.Type)
	}
	for _, tok := range rest {
		if isDirection(tok) {
			gb.Direction = tok
			continue
		}
		if n, ok := parseSmallInt(tok); ok {
			gb.Fingers = n
			continue
		}
		return GestureBinding{}, parseError(line, "unexpected gesture filter %q", tok)
	}
	return gb, nil
}

func isDirection(s string) bool {
	switch s {
	case "left", "right", "up", "down":
		return true
	}
	return false
}

func parseSmallInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

// parseXKBLayout strips optional surrounding double-quotes and validates that
// the layout name is a safe XKB symbols identifier like "us", "gb", or "gb(extd)".
func parseXKBLayout(raw string) (string, bool) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	if !IsValidXKBLayout(raw) {
		return "", false
	}
	return raw, true
}

// IsValidXKBLayout reports whether s is a safe XKB layout identifier
// (e.g. "us", "gb", "de", "gb(extd)").
func IsValidXKBLayout(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	isAlpha := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	isIdent := func(c byte) bool {
		return isAlpha(c) || (c >= '0' && c <= '9') || c == '_' || c == '-'
	}
	if !isAlpha(s[0]) {
		return false
	}
	inParen := false
	parenStart := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '(':
			if inParen {
				return false
			}
			inParen = true
			parenStart = i
		case c == ')':
			if !inParen || i != len(s)-1 || i == parenStart+1 {
				return false
			}
			inParen = false
		default:
			if !isIdent(c) {
				return false
			}
		}
	}
	return !inParen
}
