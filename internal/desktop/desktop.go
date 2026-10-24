// Package desktop discovers installed applications from the freedesktop
// .desktop entries under the XDG data directories, for the launcher's app
// mode.
package desktop

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one launchable application.
type Entry struct {
	ID       string   // desktop-file id (relative path, '/' -> '-')
	Name     string   // display name
	Exec     []string // argv with field codes resolved
	Icon     string   // icon name (may be empty)
	Terminal bool     // run inside a terminal
}

// Scan returns applications from the XDG data directories, deduplicated by
// desktop-file id (an earlier directory wins, so a user entry overrides a
// system one) and sorted by name.
func Scan() []Entry {
	seen := make(map[string]bool)
	var out []Entry
	for _, dir := range dataDirs() {
		root := filepath.Join(dir, "applications")
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".desktop") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			id := strings.ReplaceAll(rel, string(filepath.Separator), "-")
			if seen[id] {
				return nil // a higher-priority directory already provided it
			}
			seen[id] = true
			if e, ok := load(path, id); ok {
				out = append(out, e)
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// load parses one .desktop file, keeping only visible applications.
func load(path, id string) (Entry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer f.Close()
	e, ok := parse(f, id)
	return e, ok
}

// parse reads the [Desktop Entry] group. Type must be Application, and
// NoDisplay/Hidden entries are skipped (they are not meant to be launched).
func parse(r io.Reader, id string) (Entry, bool) {
	e := Entry{ID: id}
	inGroup := false
	typ, name, exec, icon := "", "", "", ""
	noDisplay, hidden, terminal := false, false, false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inGroup = line == "[Desktop Entry]"
			continue
		}
		if !inGroup {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Type":
			typ = strings.TrimSpace(val)
		case "Name":
			if name == "" { // prefer the untranslated Name
				name = strings.TrimSpace(val)
			}
		case "Exec":
			exec = strings.TrimSpace(val)
		case "Icon":
			icon = strings.TrimSpace(val)
		case "NoDisplay":
			noDisplay = strings.EqualFold(strings.TrimSpace(val), "true")
		case "Hidden":
			hidden = strings.EqualFold(strings.TrimSpace(val), "true")
		case "Terminal":
			terminal = strings.EqualFold(strings.TrimSpace(val), "true")
		}
	}
	if typ != "Application" || noDisplay || hidden || name == "" || exec == "" {
		return Entry{}, false
	}
	argv := ParseExec(exec)
	if len(argv) == 0 {
		return Entry{}, false
	}
	e.Name, e.Exec, e.Icon, e.Terminal = name, argv, icon, terminal
	return e, true
}

// ParseExec tokenizes an Exec line: space-separated, honoring single/double
// quotes and backslash escapes, with desktop field codes removed.
func ParseExec(s string) []string {
	var tokens []string
	var cur strings.Builder
	var quote byte
	started := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(s) {
				if n := s[i+1]; n == '"' || n == '\\' || n == '$' || n == '`' {
					cur.WriteByte(n)
					i++
					continue
				}
			}
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case ' ', '\t':
			if started {
				tokens = append(tokens, cur.String())
				cur.Reset()
				started = false
			}
		case '"', '\'':
			quote = c
			started = true
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		tokens = append(tokens, cur.String())
	}
	out := tokens[:0]
	for _, t := range tokens {
		if t = stripFieldCodes(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// stripFieldCodes removes %f/%u/%i and friends (we launch with no file
// arguments) and collapses %% to %.
func stripFieldCodes(a string) string {
	var b strings.Builder
	for i := 0; i < len(a); i++ {
		if a[i] == '%' && i+1 < len(a) {
			switch a[i+1] {
			case '%':
				b.WriteByte('%')
			case 'f', 'F', 'u', 'U', 'i', 'c', 'k', 'd', 'D', 'n', 'N', 'v', 'm':
				// dropped: no file arguments / deprecated
			default:
				b.WriteByte('%')
				b.WriteByte(a[i+1])
			}
			i++
			continue
		}
		b.WriteByte(a[i])
	}
	return b.String()
}

// dataDirs returns the XDG data directories, highest priority first.
func dataDirs() []string {
	var dirs []string
	if h := os.Getenv("XDG_DATA_HOME"); h != "" {
		dirs = append(dirs, h)
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share"))
	}
	list := os.Getenv("XDG_DATA_DIRS")
	if list == "" {
		list = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(list, ":") {
		if d = strings.TrimRight(strings.TrimSpace(d), "/"); d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
