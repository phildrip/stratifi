package input

import (
	"fmt"
	"strings"
)

// Modifier bits tracked from mod-key press/release (evdev codes), not from
// XKB state — identical behaviour in nested and evdev modes.
const (
	ModShift uint32 = 1 << 0
	ModCtrl  uint32 = 1 << 1
	ModAlt   uint32 = 1 << 2
	ModSuper uint32 = 1 << 3
)

// modCodes maps evdev keycodes of modifier keys to bits. Both left and
// right variants set the same bit.
var modCodes = map[uint32]uint32{
	42: ModShift, 54: ModShift, // Shift L/R
	29: ModCtrl, 97: ModCtrl, // Ctrl L/R
	56: ModAlt, 100: ModAlt, // Alt L/R
	125: ModSuper, 126: ModSuper, // Meta L/R
}

// keyNames maps lowercase key names to evdev codes (linux/input-event-codes.h).
// Modifier names are resolved as mods, never as keys: a bare modifier is
// tracked and forwarded, never consumed by a binding.
var keyNames = map[string]uint32{
	"escape": 1, "esc": 1,
	"1": 2, "2": 3, "3": 4, "4": 5, "5": 6,
	"6": 7, "7": 8, "8": 9, "9": 10, "0": 11,
	"minus": 12, "equal": 13, "backspace": 14, "tab": 15,
	"q": 16, "w": 17, "e": 18, "r": 19, "t": 20,
	"y": 21, "u": 22, "i": 23, "o": 24, "p": 25,
	"enter": 28, "return": 28,
	"a": 30, "s": 31, "d": 32, "f": 33, "g": 34,
	"h": 35, "j": 36, "k": 37, "l": 38,
	"semicolon": 39, "grave": 41,
	"z": 44, "x": 45, "c": 46, "v": 47, "b": 48,
	"n": 49, "m": 50, "comma": 51, "dot": 52, "slash": 53,
	"space": 57,
	"f1":    59, "f2": 60, "f3": 61, "f4": 62, "f5": 63,
	"f6": 64, "f7": 65, "f8": 66, "f9": 67, "f10": 68,
	"f11": 87, "f12": 88,
	"home": 102, "up": 103, "left": 105, "right": 106,
	"end": 107, "down": 108, "delete": 111,
}

// Binding is one compiled key combination and its action.
type Binding struct {
	Mods   uint32
	Code   uint32
	Action string
	Args   []string
}

// ParseBinding compiles "Super+Return" + "spawn" + ["foot"] into a Binding.
// Modifiers (case-insensitive): Super, Ctrl, Alt, Shift. The final component
// is the key name. A combo that is only modifiers, an unknown key, or an
// empty component is an error.
func ParseBinding(combo, action string, args []string) (Binding, error) {
	parts := strings.Split(combo, "+")
	if len(parts) == 0 {
		return Binding{}, fmt.Errorf("empty combo")
	}
	var b Binding
	b.Action = action
	b.Args = append([]string(nil), args...)
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return Binding{}, fmt.Errorf("combo %q has an empty component", combo)
		}
		lower := strings.ToLower(p)
		isLast := i == len(parts)-1
		if !isLast {
			switch lower {
			case "super", "mod4", "win":
				b.Mods |= ModSuper
				continue
			case "ctrl", "control":
				b.Mods |= ModCtrl
				continue
			case "alt", "mod1":
				b.Mods |= ModAlt
				continue
			case "shift":
				b.Mods |= ModShift
				continue
			}
			return Binding{}, fmt.Errorf("combo %q: %q is not a modifier", combo, p)
		}
		code, ok := keyNames[lower]
		if !ok {
			// A trailing modifier name is a likely typo ("Super+Ctrl").
			if _, isMod := modName(lower); isMod {
				return Binding{}, fmt.Errorf("combo %q is only modifiers", combo)
			}
			return Binding{}, fmt.Errorf("combo %q: unknown key %q", combo, p)
		}
		b.Code = code
	}
	return b, nil
}

func modName(lower string) (uint32, bool) {
	switch lower {
	case "super", "mod4", "win":
		return ModSuper, true
	case "ctrl", "control":
		return ModCtrl, true
	case "alt", "mod1":
		return ModAlt, true
	case "shift":
		return ModShift, true
	}
	return 0, false
}

// Matcher tracks held modifiers and matches key presses against bindings.
// It is the single place that decides consume-vs-forward, so the router
// cannot double-deliver a bound press or leak a swallowed release.
type Matcher struct {
	heldMods uint32
	modRefs  [4]int // press count per mod bit: releasing one of two held
	consumed map[uint32]bool
	bindings []Binding
}

// modIndex maps a modifier bit to its refcount slot.
func modIndex(bit uint32) int {
	switch bit {
	case ModShift:
		return 0
	case ModCtrl:
		return 1
	case ModAlt:
		return 2
	default:
		return 3 // ModSuper
	}
}

// NewMatcher builds a matcher over compiled bindings.
func NewMatcher(bindings []Binding) *Matcher {
	return &Matcher{consumed: make(map[uint32]bool), bindings: bindings}
}

// HandleKey processes one key event (state: 0 release, 1 press, 2 repeat).
// It returns the matched action (if any) and whether the event is consumed
// (must not reach the client).
//
//   - Modifier keys update held state and are never consumed (clients still
//     see Shift+a); a binding that is only modifiers never fires.
//   - A press matching (heldMods, code) fires once and marks the key
//     consumed; its repeat and release are swallowed so the client never
//     sees a half keystroke.
//   - Anything else passes through.
func (m *Matcher) HandleKey(code, state uint32) (action string, args []string, consumed bool) {
	if bit, isMod := modCodes[code]; isMod {
		idx := modIndex(bit)
		if state == 0 {
			if m.modRefs[idx] > 0 {
				m.modRefs[idx]--
			}
			if m.modRefs[idx] == 0 {
				m.heldMods &^= bit
			}
		} else {
			m.modRefs[idx]++
			m.heldMods |= bit
		}
		return "", nil, false
	}
	switch state {
	case 1: // press
		for _, b := range m.bindings {
			if b.Code == code && b.Mods == m.heldMods {
				m.consumed[code] = true
				return b.Action, b.Args, true
			}
		}
		return "", nil, false
	case 2: // repeat
		if m.consumed[code] {
			return "", nil, true
		}
		return "", nil, false
	default: // release (and anything else)
		if m.consumed[code] {
			delete(m.consumed, code)
			return "", nil, true
		}
		return "", nil, false
	}
}
