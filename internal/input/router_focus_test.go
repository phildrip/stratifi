package input

import (
	"testing"

	"stratifi/internal/layout"
	"stratifi/internal/server"
)

// TestRouterRequestFocusQueue pins the cross-goroutine focus command path
// (stratifi-5xj): RequestFocus queues; the next ProcessEvent applies it on
// the router's own loop — no direct cross-goroutine calls.
func TestRouterRequestFocusQueue(t *testing.T) {
	r := NewRouter(
		func(x, y float64) (WindowInfo, bool) { return WindowInfo{}, false },
		func(clientID uint32) *server.Client { return nil },
		nil,
	)

	// Queue a focus command before any event arrives.
	r.RequestFocus(7, 2, 3, 0)
	r.RequestFocus(9, 3, 4, 0) // supersedes: queued commands coalesce by arrival

	r.ProcessEvent(InputEvent{}) // drains the queue; applies the last target

	winID, clientID, surfaceID := r.Focus()
	if winID != 9 || clientID != 3 || surfaceID != 4 {
		t.Errorf("focus = %d/%d/%d, want 9/3/4 (last request wins)", winID, clientID, surfaceID)
	}
}

// TestRouterFocusQueueSaturation covers the non-blocking queue: flooding
// RequestFocus must not block; the last drain wins.
func TestRouterFocusQueueNoBlock(t *testing.T) {
	r := NewRouter(
		func(x, y float64) (WindowInfo, bool) { return WindowInfo{}, false },
		func(clientID uint32) *server.Client { return nil },
		nil,
	)
	for i := 0; i < 100; i++ {
		r.RequestFocus(layout.WindowID(i), uint32(i), uint32(i), 0) // must not block past cap 8
	}
}
