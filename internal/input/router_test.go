package input

import (
	"testing"

	"bloxwm/internal/layout"
	"bloxwm/internal/server"
)

func TestRouterPointerMotionAndFocus(t *testing.T) {
	targetWin := layout.WindowID(42)
	winRect := layout.Rect{X: 100, Y: 100, W: 400, H: 300}

	lookup := func(x, y float64) (WindowInfo, bool) {
		if x >= 100 && x < 500 && y >= 100 && y < 400 {
			return WindowInfo{
				WinID:     targetWin,
				ClientID:  1,
				SurfaceID: 10,
				Rect:      winRect,
			}, true
		}
		return WindowInfo{}, false
	}

	var focusChanges []layout.WindowID
	onFocus := func(oldWin, newWin layout.WindowID) {
		focusChanges = append(focusChanges, newWin)
	}

	// Mock client lookup returns nil (safe for pointer/keyboard dispatch)
	getClient := func(clientID uint32) *server.Client {
		return nil
	}

	router := NewRouter(lookup, getClient, onFocus)

	// Move cursor outside window
	router.ProcessEvent(InputEvent{
		Type:     EventPointerMotionAbsolute,
		TimeUsec: 1000,
		X:        50,
		Y:        50,
	})

	if router.hoverWinID != 0 {
		t.Fatalf("expected no hover window, got %d", router.hoverWinID)
	}

	// Move cursor inside window
	router.ProcessEvent(InputEvent{
		Type:     EventPointerMotionAbsolute,
		TimeUsec: 2000,
		X:        200,
		Y:        200,
	})

	if router.hoverWinID != targetWin {
		t.Fatalf("expected hover window %d, got %d", targetWin, router.hoverWinID)
	}

	// Click button to focus
	router.ProcessEvent(InputEvent{
		Type:     EventPointerButton,
		TimeUsec: 3000,
		Code:     272, // BTN_LEFT
		State:    ButtonStatePressed,
	})

	if router.FocusWindowID() != targetWin {
		t.Fatalf("expected focused window %d, got %d", targetWin, router.FocusWindowID())
	}
	if len(focusChanges) != 1 || focusChanges[0] != targetWin {
		t.Fatalf("unexpected focus history: %v", focusChanges)
	}

	// Move cursor outside window -> hover should clear
	router.ProcessEvent(InputEvent{
		Type:     EventPointerMotionAbsolute,
		TimeUsec: 4000,
		X:        600,
		Y:        600,
	})

	if router.hoverWinID != 0 {
		t.Fatalf("expected hover to clear, got %d", router.hoverWinID)
	}
	// Focus should remain on targetWin until another window is clicked
	if router.FocusWindowID() != targetWin {
		t.Fatalf("expected focus to remain %d, got %d", targetWin, router.FocusWindowID())
	}
}

func BenchmarkRouterProcessEvent(b *testing.B) {
	lookup := func(x, y float64) (WindowInfo, bool) {
		return WindowInfo{
			WinID:     1,
			ClientID:  1,
			SurfaceID: 10,
			Rect:      layout.Rect{X: 0, Y: 0, W: 1000, H: 1000},
		}, true
	}

	router := NewRouter(lookup, func(id uint32) *server.Client { return nil }, nil)
	ev := InputEvent{
		Type:     EventPointerMotionAbsolute,
		TimeUsec: 1000,
		X:        500,
		Y:        500,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		router.ProcessEvent(ev)
	}
}

// TestRouterCursorStateIsReadable: the compositor actor reads the cursor
// position and hovered client from another goroutine, so both must be
// exposed race-free.
func TestRouterCursorStateIsReadable(t *testing.T) {
	lookup := func(x, y float64) (WindowInfo, bool) {
		if x >= 100 && x < 500 && y >= 100 && y < 400 {
			return WindowInfo{WinID: 42, ClientID: 7, SurfaceID: 10,
				Rect: layout.Rect{X: 100, Y: 100, W: 400, H: 300}}, true
		}
		return WindowInfo{}, false
	}
	router := NewRouter(lookup, func(uint32) *server.Client { return nil }, nil)

	router.HandlePointerMotion(0, 250, 260)
	if x, y := router.CursorPosition(); x != 250 || y != 260 {
		t.Errorf("cursor = (%.0f,%.0f), want (250,260)", x, y)
	}
	if got := router.HoverClient(); got != 7 {
		t.Errorf("hovered client = %d, want 7", got)
	}

	// Moving off the window clears the hovered client but keeps position.
	router.HandlePointerMotion(0, 10, 10)
	if got := router.HoverClient(); got != 0 {
		t.Errorf("hovered client = %d, want 0 after leaving", got)
	}
	if x, y := router.CursorPosition(); x != 10 || y != 10 {
		t.Errorf("cursor = (%.0f,%.0f), want (10,10)", x, y)
	}
}
