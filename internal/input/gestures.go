package input

// gestures.go — bridges the multi-touch recognizer to compositor actions.
//
// The recognizer (internal/input/gesture) decodes Linux MT-protocol-B slots
// into tap/scroll/pinch/swipe events. This layer owns the raw MT feed (so
// MT-code knowledge lives in exactly one place) and a binding table that
// maps a recognized gesture to a named action. Actions are delivered via a
// callback on the input-consumer goroutine, like keybindings, so the
// compositor can route them to the actor with a channel send.

import "bloxwm/internal/input/gesture"

// Linux MT event codes (linux/input-event-codes.h).
const (
	EvSyn        = 0x00
	EvKey        = 0x01
	EvRel        = 0x02
	EvAbs        = 0x03
	SynReport    = 0x00
	AbsMtSlot    = 0x2f
	AbsMtPosX    = 0x35
	AbsMtPosY    = 0x36
	AbsMtTrackID = 0x39
)

// GestureAction names a compositor action a gesture can trigger. Kept as
// strings so they travel through config and channel events unchanged.
type GestureAction string

const (
	GestureWorkspacePrev GestureAction = "workspace_prev"
	GestureWorkspaceNext GestureAction = "workspace_next"
	GestureColumnPrev    GestureAction = "column_prev"
	GestureColumnNext    GestureAction = "column_next"
	GestureOverview      GestureAction = "overview"
	GestureFullscreen    GestureAction = "fullscreen"
)

// GestureBinding maps a recognized gesture to an action. Direction and
// Fingers are optional filters: DirectionNone means "any direction",
// Fingers 0 means "any finger count". First match wins.
type GestureBinding struct {
	Type      gesture.GestureType
	Direction gesture.GestureDirection
	Fingers   uint8
	Action    GestureAction
}

// GestureRouter owns the recognizer, feeds it raw MT events, and fires the
// bound action for each recognized gesture.
type GestureRouter struct {
	engine   *gesture.MultiTouchEngine
	bindings []GestureBinding
	onAction func(GestureAction)
}

// NewGestureRouter builds the router and its recognizer. onAction runs on
// the caller's (input) goroutine; keep it to channel sends.
func NewGestureRouter(bindings []GestureBinding, onAction func(GestureAction)) *GestureRouter {
	r := &GestureRouter{bindings: bindings, onAction: onAction}
	r.engine = gesture.NewMultiTouchEngine(r.dispatch)
	return r
}

// dispatch matches a recognized gesture against the table and fires it.
func (r *GestureRouter) dispatch(ev gesture.GestureEvent) {
	for _, b := range r.bindings {
		if b.Type != ev.Type {
			continue
		}
		if b.Direction != gesture.DirectionNone && b.Direction != ev.Direction {
			continue
		}
		if b.Fingers != 0 && b.Fingers != ev.Fingers {
			continue
		}
		if r.onAction != nil {
			r.onAction(b.Action)
		}
		return
	}
}

// FeedAbs interprets one EV_ABS event and drives the recognizer.
func (r *GestureRouter) FeedAbs(code int32, value int32, timeUsec uint64) {
	switch code {
	case AbsMtSlot:
		r.engine.SetSlot(int(value))
	case AbsMtTrackID:
		r.engine.SetTrackingID(value, timeUsec)
	case AbsMtPosX:
		r.engine.SetPositionX(float64(value), timeUsec)
	case AbsMtPosY:
		r.engine.SetPositionY(float64(value), timeUsec)
	}
}

// Sync marks the end of an MT packet (SYN_REPORT), running recognition.
func (r *GestureRouter) Sync(timeUsec uint64) { r.engine.Sync(timeUsec) }

// FeedEvent routes a raw evdev (type, code, value) into the router. Returns
// true when the event was consumed (MT/ SYN), so callers can keep pointer
// and keyboard handling separate.
func (r *GestureRouter) FeedEvent(evType uint16, code int32, value int32, timeUsec uint64) bool {
	switch evType {
	case EvAbs:
		switch code {
		case AbsMtSlot, AbsMtTrackID, AbsMtPosX, AbsMtPosY:
			r.FeedAbs(code, value, timeUsec)
			return true
		}
	case EvSyn:
		if code == SynReport {
			r.Sync(timeUsec)
			return true
		}
	}
	return false
}

// ParseGestureBinding converts config strings into a compiled binding.
func ParseGestureBinding(typ, direction string, fingers int, action string) (GestureBinding, error) {
	var b GestureBinding
	switch typ {
	case "swipe":
		b.Type = gesture.GestureSwipe
	case "pinch":
		b.Type = gesture.GesturePinch
	case "tap":
		b.Type = gesture.GestureTap
	case "scroll":
		b.Type = gesture.GestureScroll
	default:
		return b, errGesture("unknown gesture type " + typ)
	}
	switch direction {
	case "":
		b.Direction = gesture.DirectionNone
	case "left":
		b.Direction = gesture.DirectionLeft
	case "right":
		b.Direction = gesture.DirectionRight
	case "up":
		b.Direction = gesture.DirectionUp
	case "down":
		b.Direction = gesture.DirectionDown
	default:
		return b, errGesture("unknown direction " + direction)
	}
	if fingers < 0 || fingers > 10 {
		return b, errGesture("finger count out of range")
	}
	b.Fingers = uint8(fingers)
	b.Action = GestureAction(action)
	return b, nil
}

type gestureErr string

func (e gestureErr) Error() string { return "gesture: " + string(e) }
func errGesture(s string) error    { return gestureErr(s) }
