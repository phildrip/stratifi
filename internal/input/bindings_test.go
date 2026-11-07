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

func TestMatcherModesAndPauseKey(t *testing.T) {
	bPause, err := ParseBinding("Super+Pause", "mode", []string{"system"})
	if err != nil {
		t.Fatalf("ParseBinding Super+Pause: %v", err)
	}
	bResizeEnter, err := ParseBinding("Super+r", "mode", []string{"resize"})
	if err != nil {
		t.Fatalf("ParseBinding Super+r: %v", err)
	}
	bResizeJ, err := ParseBinding("j", "resize_col_width", []string{"-10"})
	if err != nil {
		t.Fatalf("ParseBinding j: %v", err)
	}
	bResizeJ.Mode = "resize"
	bResizeEsc, err := ParseBinding("Escape", "mode", []string{"default"})
	if err != nil {
		t.Fatalf("ParseBinding Escape: %v", err)
	}
	bResizeEsc.Mode = "resize"

	m := NewMatcher([]Binding{bPause, bResizeEnter, bResizeJ, bResizeEsc})
	if m.Mode() != "default" {
		t.Fatalf("initial mode = %q, want default", m.Mode())
	}

	// In default mode, plain 'j' (code 36) is NOT consumed.
	if _, _, consumed := m.HandleKey(36, 1); consumed {
		t.Fatalf("plain j consumed in default mode")
	}
	m.HandleKey(36, 0)

	// Switch to resize mode: plain 'j' now fires resize_col_width.
	m.SetMode("resize")
	if m.Mode() != "resize" {
		t.Fatalf("mode = %q, want resize", m.Mode())
	}
	act, args, consumed := m.HandleKey(36, 1)
	if !consumed || act != "resize_col_width" || len(args) != 1 || args[0] != "-10" {
		t.Fatalf("resize j: got (%q, %v, %v), want (resize_col_width, [-10], true)", act, args, consumed)
	}
	m.HandleKey(36, 0)

	// Escape in resize mode fires mode default; even if mode switches while Escape is held,
	// releasing Escape must still be swallowed so it doesn't leak to the client.
	act, args, consumed = m.HandleKey(1, 1)
	if !consumed || act != "mode" || len(args) != 1 || args[0] != "default" {
		t.Fatalf("resize Escape: got (%q, %v, %v)", act, args, consumed)
	}
	m.SetMode("default")
	if _, _, relConsumed := m.HandleKey(1, 0); !relConsumed {
		t.Errorf("Escape release after mode switch leaked to client")
	}

	// Failsafe: entering a mode that has NO explicit Escape binding still exits on bare Escape.
	m.SetMode("trapped")
	act, args, consumed = m.HandleKey(1, 1)
	if !consumed || act != "mode" || len(args) != 1 || args[0] != "default" {
		t.Errorf("failsafe Escape in trapped mode: got (%q, %v, %v)", act, args, consumed)
	}
	m.HandleKey(1, 0)
}

func TestRouterSuperDragMoveAndResize(t *testing.T) {
	r := NewRouter(
		func(x, y float64) (WindowInfo, bool) {
			return WindowInfo{WinID: 42, ClientID: 1, SurfaceID: 2, ToplevelID: 3}, true
		},
		nil,
		nil,
	)
	r.SetBindings(NewMatcher(nil))
	var gotWin uint32
	var gotDX, gotDY int32
	var gotResize bool
	r.SetWindowDragHandler(func(winID uint32, dx, dy int32, resize bool) {
		gotWin = winID
		gotDX += dx
		gotDY += dy
		gotResize = resize
	})

	// Move pointer to (100, 100), hold Super (125), left button down (272), drag to (130, 145), release.
	r.HandlePointerMotion(1, 100, 100)
	r.HandleKeyboardKey(2, 125, 1)
	r.HandlePointerButton(3, 272, ButtonStatePressed)
	r.HandlePointerMotion(4, 130, 145)
	r.HandlePointerButton(5, 272, ButtonStateReleased)
	if gotWin != 42 || gotDX != 30 || gotDY != 45 || gotResize {
		t.Errorf("Super+left drag: win=%d dx=%d dy=%d resize=%v, want 42/30/45/false",
			gotWin, gotDX, gotDY, gotResize)
	}

	// Right button down (273) with Super still held -> resize drag.
	gotDX, gotDY = 0, 0
	r.HandlePointerButton(6, 273, ButtonStatePressed)
	r.HandlePointerMotion(7, 180, 125)
	r.HandlePointerButton(8, 273, ButtonStateReleased)
	r.HandleKeyboardKey(9, 125, 0)
	if gotWin != 42 || gotDX != 50 || gotDY != -20 || !gotResize {
		t.Errorf("Super+right drag: win=%d dx=%d dy=%d resize=%v, want 42/50/-20/true",
			gotWin, gotDX, gotDY, gotResize)
	}
}
