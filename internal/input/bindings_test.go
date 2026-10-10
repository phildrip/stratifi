package input

import (
	"reflect"
	"testing"
)

func TestParseBinding(t *testing.T) {
	b, err := ParseBinding("Super+Return", "spawn", []string{"foot"})
	if err != nil {
		t.Fatalf("ParseBinding: %v", err)
	}
	if b.Mods != ModSuper || b.Code != 28 || b.Action != "spawn" {
		t.Errorf("got %+v, want Super+Return/28/spawn", b)
	}
	if !reflect.DeepEqual(b.Args, []string{"foot"}) {
		t.Errorf("args = %v", b.Args)
	}
}

func TestParseBindingCaseInsensitive(t *testing.T) {
	b, err := ParseBinding("ctrl+ALT+q", "close", nil)
	if err != nil {
		t.Fatalf("ParseBinding: %v", err)
	}
	if b.Mods != ModCtrl|ModAlt || b.Code != 16 {
		t.Errorf("got mods %d code %d, want Ctrl|Alt + 16", b.Mods, b.Code)
	}
}

func TestParseBindingErrors(t *testing.T) {
	for _, combo := range []string{
		"",
		"Super+",
		"+q",
		"Super+Ctrl",  // only modifiers
		"Super+Frob",  // unknown key
		"Super+q+",    // empty component
		"Hyper+q",     // unknown modifier
		"Super+Super", // trailing modifier
	} {
		if _, err := ParseBinding(combo, "close", nil); err == nil {
			t.Errorf("ParseBinding(%q) succeeded, want error", combo)
		}
	}
}

func TestMatcherFiresAndSwallows(t *testing.T) {
	m := NewMatcher([]Binding{{Mods: ModSuper, Code: 16, Action: "close"}})

	// Plain 'q' without Super passes through.
	if _, _, c := m.HandleKey(16, 1); c {
		t.Fatal("unmodified press consumed")
	}

	// Super down (forwarded), then Super+q fires and consumes.
	if _, _, c := m.HandleKey(125, 1); c {
		t.Fatal("modifier press consumed")
	}
	action, _, consumed := m.HandleKey(16, 1)
	if !consumed || action != "close" {
		t.Fatalf("Super+q: action=%q consumed=%v, want close/true", action, consumed)
	}
	// Repeat and release of the consumed key are swallowed, not re-fired.
	if a, _, c := m.HandleKey(16, 2); c && a != "" {
		t.Error("repeat re-fired the action")
	}
	if _, _, c := m.HandleKey(16, 2); !c {
		t.Error("repeat of consumed key leaked to client")
	}
	if _, _, c := m.HandleKey(16, 0); !c {
		t.Error("release of consumed key leaked to client")
	}
	// After release the key is live again: a fresh press still fires.
	if a, _, c := m.HandleKey(16, 1); !c || a != "close" {
		t.Fatalf("re-press: action=%q consumed=%v, want close/true", a, c)
	}
	m.HandleKey(16, 0)
	// Super up; Super+q no longer matches.
	m.HandleKey(125, 0)
	if _, _, c := m.HandleKey(16, 1); c {
		t.Fatal("press without mods consumed")
	}
	m.HandleKey(16, 0)
}

func TestMatcherWrongMods(t *testing.T) {
	m := NewMatcher([]Binding{{Mods: ModSuper, Code: 16, Action: "close"}})
	m.HandleKey(29, 1) // Ctrl, not Super
	if _, _, c := m.HandleKey(16, 1); c {
		t.Fatal("Ctrl+q matched a Super binding")
	}
	m.HandleKey(29, 0)
	m.HandleKey(16, 0)
}

// TestMatcherBothShiftsHeld: releasing one of two held modifiers of the same
// type must not drop the modifier while the other is still down.
func TestMatcherBothShiftsHeld(t *testing.T) {
	m := NewMatcher([]Binding{{Mods: ModShift, Code: 30, Action: "act"}}) // Shift+a
	m.HandleKey(42, 1)                                                    // left Shift down
	m.HandleKey(54, 1)                                                    // right Shift down
	m.HandleKey(42, 0)                                                    // left Shift up
	if _, _, c := m.HandleKey(30, 1); !c {                                // 'a' must still match
		t.Fatal("Shift+a did not match with right Shift still held")
	}
	m.HandleKey(30, 0)
	m.HandleKey(54, 0) // right Shift up
	if _, _, c := m.HandleKey(30, 1); c {
		t.Fatal("Shift+a matched with no Shift held")
	}
	m.HandleKey(30, 0)
}
