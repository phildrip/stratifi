package gesture

import (
	"testing"
)

func TestMultiTouchTapAndScroll(t *testing.T) {
	var events []GestureEvent
	engine := NewMultiTouchEngine(func(ev GestureEvent) {
		events = append(events, ev)
	})

	// 1. Simulate 1-Finger Tap
	engine.SetSlot(0)
	engine.SetTrackingID(10, 1000)
	engine.SetPositionX(100, 1000)
	engine.SetPositionY(100, 1000)
	engine.Sync(1000)

	// Finger lifted quickly at same position
	engine.SetTrackingID(-1, 50000) // 49ms later
	engine.Sync(50000)

	if len(events) != 1 || events[0].Type != GestureTap || events[0].Fingers != 1 {
		t.Fatalf("expected 1-finger tap event, got: %+v", events)
	}

	// 2. Simulate 2-Finger Scroll
	events = events[:0]
	// Finger 1 down
	engine.SetSlot(0)
	engine.SetTrackingID(20, 100000)
	engine.SetPositionX(200, 100000)
	engine.SetPositionY(200, 100000)
	// Finger 2 down
	engine.SetSlot(1)
	engine.SetTrackingID(21, 100000)
	engine.SetPositionX(300, 100000)
	engine.SetPositionY(200, 100000)
	engine.Sync(100000)

	// Both fingers move downwards together by 30 units
	engine.SetSlot(0)
	engine.SetPositionY(230, 120000)
	engine.SetSlot(1)
	engine.SetPositionY(230, 120000)
	engine.Sync(120000)

	if len(events) < 1 || events[0].Type != GestureScroll || events[0].Dy <= 0 {
		t.Fatalf("expected 2-finger scroll event with positive Dy, got: %+v", events)
	}
}

func TestMultiTouch3FingerSwipe(t *testing.T) {
	var swipeEvent GestureEvent
	engine := NewMultiTouchEngine(func(ev GestureEvent) {
		if ev.Type == GestureSwipe {
			swipeEvent = ev
		}
	})

	// 3 fingers down
	for i := 0; i < 3; i++ {
		engine.SetSlot(i)
		engine.SetTrackingID(int32(100+i), 1000)
		engine.SetPositionX(float64(100+i*50), 1000)
		engine.SetPositionY(200, 1000)
	}
	engine.Sync(1000)

	// All 3 fingers swipe to the right by 80 units
	for i := 0; i < 3; i++ {
		engine.SetSlot(i)
		engine.SetPositionX(float64(100+i*50+80), 20000)
	}
	engine.Sync(20000)

	if swipeEvent.Type != GestureSwipe || swipeEvent.Fingers != 3 || swipeEvent.Direction != DirectionRight {
		t.Fatalf("expected 3-finger swipe right, got: %+v", swipeEvent)
	}
}

func TestMouseStrokeAndRocker(t *testing.T) {
	var defaultClicked uint32
	var strokeExecuted string
	var rockerExecuted string

	engine := NewMouseGestureEngine(0x111, func(btn uint32) {
		defaultClicked = btn
	})

	engine.RegisterStroke("R", func() { strokeExecuted = "R" })
	engine.RegisterStroke("DR", func() { strokeExecuted = "DR" })
	engine.RegisterRocker(0x111, 0x110, func() { rockerExecuted = "RMB+LMB" })

	// 1. Simple click without movement -> default click
	engine.OnButton(0x111, 1, 100, 100)
	engine.OnButton(0x111, 0, 101, 101)

	if defaultClicked != 0x111 {
		t.Fatalf("expected default click 0x111, got %d", defaultClicked)
	}

	// 2. Stroke gesture: Hold RMB, move Right 50px, release
	engine.OnButton(0x111, 1, 100, 100)
	engine.OnMotion(150, 100)
	engine.OnButton(0x111, 0, 150, 100)

	if strokeExecuted != "R" {
		t.Fatalf("expected stroke 'R', got %q", strokeExecuted)
	}

	// 3. Multi-directional stroke: Hold RMB, move Down 40px, move Right 40px, release
	strokeExecuted = ""
	engine.OnButton(0x111, 1, 100, 100)
	engine.OnMotion(100, 140) // Down
	engine.OnMotion(140, 140) // Right
	engine.OnButton(0x111, 0, 140, 140)

	if strokeExecuted != "DR" {
		t.Fatalf("expected stroke 'DR', got %q", strokeExecuted)
	}

	// 4. Rocker gesture: Hold RMB (0x111), press LMB (0x110)
	engine.OnButton(0x111, 1, 100, 100)
	engine.OnButton(0x110, 1, 100, 100)

	if rockerExecuted != "RMB+LMB" {
		t.Fatalf("expected rocker 'RMB+LMB', got %q", rockerExecuted)
	}
}

func BenchmarkMultiTouchSync(b *testing.B) {
	engine := NewMultiTouchEngine(nil)

	// Set 2 active fingers
	engine.SetSlot(0)
	engine.SetTrackingID(1, 1000)
	engine.SetPositionX(100, 1000)
	engine.SetPositionY(100, 1000)

	engine.SetSlot(1)
	engine.SetTrackingID(2, 1000)
	engine.SetPositionX(200, 1000)
	engine.SetPositionY(100, 1000)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		engine.SetSlot(0)
		engine.SetPositionX(100+float64(i%10), uint64(1000+i))
		engine.Sync(uint64(1000 + i))
	}
}
