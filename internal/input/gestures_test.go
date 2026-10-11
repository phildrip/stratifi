package input

import (
	"testing"

	"stratifi/internal/input/gesture"
)

// feedSwipe drives a 3-finger horizontal swipe through the router's raw MT
// feed. Tracking ids are sent once per contact (MT-B), positions each packet.
func feedSwipe(r *GestureRouter, dx float64) {
	var t uint64
	sync := func() { r.Sync(t); t += 1000 }
	down := func(slot, id int32, x, y float64) {
		r.FeedAbs(AbsMtSlot, slot, t)
		r.FeedAbs(AbsMtTrackID, id, t)
		r.FeedAbs(AbsMtPosX, int32(x), t)
		r.FeedAbs(AbsMtPosY, int32(y), t)
	}
	move := func(slot int32, x, y float64) {
		r.FeedAbs(AbsMtSlot, slot, t)
		r.FeedAbs(AbsMtPosX, int32(x), t)
		r.FeedAbs(AbsMtPosY, int32(y), t)
	}
	// Three fingers down (tracking id once).
	down(0, 1, 100, 100)
	down(1, 2, 150, 100)
	down(2, 3, 200, 100)
	sync()
	// Move all three right by dx (well past the 30-unit threshold).
	move(0, 100+dx, 100)
	move(1, 150+dx, 100)
	move(2, 200+dx, 100)
	sync()
	// Lift: tracking id -1 per slot ends the gesture (clears the latch).
	for slot := int32(0); slot < 3; slot++ {
		r.FeedAbs(AbsMtSlot, slot, t)
		r.FeedAbs(AbsMtTrackID, -1, t)
	}
	sync()
}

func TestGestureRouterSwipeRightFiresAction(t *testing.T) {
	var fired []GestureAction
	r := NewGestureRouter([]GestureBinding{
		{Type: gesture.GestureSwipe, Direction: gesture.DirectionRight, Fingers: 3, Action: GestureWorkspaceNext},
		{Type: gesture.GestureSwipe, Direction: gesture.DirectionLeft, Fingers: 3, Action: GestureWorkspacePrev},
	}, func(a GestureAction) { fired = append(fired, a) })

	feedSwipe(r, 80) // rightward
	if len(fired) != 1 || fired[0] != GestureWorkspaceNext {
		t.Fatalf("fired = %v, want [workspace_next]", fired)
	}
}

func TestGestureRouterSwipeLeftFiresAction(t *testing.T) {
	var fired []GestureAction
	r := NewGestureRouter([]GestureBinding{
		{Type: gesture.GestureSwipe, Direction: gesture.DirectionLeft, Fingers: 3, Action: GestureWorkspacePrev},
	}, func(a GestureAction) { fired = append(fired, a) })

	feedSwipe(r, -80) // leftward
	if len(fired) != 1 || fired[0] != GestureWorkspacePrev {
		t.Fatalf("fired = %v, want [workspace_prev]", fired)
	}
}

func TestGestureRouterDirectionAny(t *testing.T) {
	var fired []GestureAction
	// No direction filter: any swipe maps to column_next.
	r := NewGestureRouter([]GestureBinding{
		{Type: gesture.GestureSwipe, Action: GestureColumnNext},
	}, func(a GestureAction) { fired = append(fired, a) })

	feedSwipe(r, 80)
	feedSwipe(r, -80)
	if len(fired) != 2 {
		t.Fatalf("fired = %v, want two actions (direction-agnostic)", fired)
	}
}

func TestGestureRouterFingersFilter(t *testing.T) {
	var fired []GestureAction
	// Bind only 4-finger swipes; a 3-finger swipe must not fire.
	r := NewGestureRouter([]GestureBinding{
		{Type: gesture.GestureSwipe, Fingers: 4, Action: GestureOverview},
	}, func(a GestureAction) { fired = append(fired, a) })

	feedSwipe(r, 80) // 3 fingers
	if len(fired) != 0 {
		t.Fatalf("3-finger swipe fired a 4-finger binding: %v", fired)
	}
}

func TestGestureRouterUnboundIsSilent(t *testing.T) {
	var fired []GestureAction
	r := NewGestureRouter(nil, func(a GestureAction) { fired = append(fired, a) })
	feedSwipe(r, 80)
	if len(fired) != 0 {
		t.Fatalf("unbound gesture fired %v", fired)
	}
}

func TestGestureRouterFeedEventConsumesMT(t *testing.T) {
	r := NewGestureRouter(nil, nil)
	if !r.FeedEvent(EvAbs, AbsMtPosX, 100, 0) {
		t.Error("ABS_MT event not consumed")
	}
	if !r.FeedEvent(EvSyn, SynReport, 0, 0) {
		t.Error("SYN_REPORT not consumed")
	}
	// Non-MT events pass through.
	if r.FeedEvent(EvRel, 0, 1, 0) {
		t.Error("EV_REL consumed by the gesture router")
	}
	if r.FeedEvent(EvAbs, 0x00 /*ABS_X*/, 1, 0) {
		t.Error("non-MT ABS consumed")
	}
}
