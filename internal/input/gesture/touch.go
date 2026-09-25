package gesture

import (
	"math"
)

// GestureType defines recognized multi-touch gestures.
type GestureType uint16

const (
	GestureNone GestureType = iota
	GestureTap
	GestureScroll
	GesturePinch
	GestureSwipe
)

// GestureDirection defines the primary direction of a swipe or directional gesture.
type GestureDirection uint8

const (
	DirectionNone GestureDirection = iota
	DirectionLeft
	DirectionRight
	DirectionUp
	DirectionDown
)

func (d GestureDirection) String() string {
	switch d {
	case DirectionLeft:
		return "left"
	case DirectionRight:
		return "right"
	case DirectionUp:
		return "up"
	case DirectionDown:
		return "down"
	default:
		return "none"
	}
}

// GestureEvent is a 64-byte POD struct emitted by the gesture recognizer.
type GestureEvent struct {
	Type      GestureType
	Direction GestureDirection
	Fingers   uint8
	_         uint8   // padding
	_         uint32  // padding
	TimeUsec  uint64  // timestamp
	X         float64 // centroid X
	Y         float64 // centroid Y
	Dx        float64 // displacement X
	Dy        float64 // displacement Y
	Scale     float64 // pinch/spread scale factor (1.0 = neutral)
	_         [8]byte // padding to 64 bytes
}

// TouchContact stores tracking state for an individual physical finger on a trackpad.
type TouchContact struct {
	TrackingID    int32   // -1 = lifted, >=0 = active
	StartX        float64 // initial touch coordinates
	StartY        float64
	CurrentX      float64
	CurrentY      float64
	PrevX         float64
	PrevY         float64
	StartTime     uint64
	UpdateTime    uint64
	Active        bool
	HasInitialPos bool
}

const (
	MaxTouchSlots = 10 // Track up to 10 simultaneous contact points
)

// MultiTouchEngine decodes Linux Multi-Touch Protocol B slots and detects
// 1-, 2-, 3-, and 4-finger gestures with zero heap allocations.
type MultiTouchEngine struct {
	slots      [MaxTouchSlots]TouchContact
	activeSlot int
	prevActive int

	// Gestures configuration thresholds
	TapTimeLimitUsec uint64  // Max duration for a tap (e.g. 250ms = 250,000 usec)
	TapDistLimit     float64 // Max movement for a tap (e.g. 15.0 units)
	SwipeThreshold   float64 // Min movement to trigger a swipe

	inPinch bool
	inSwipe bool

	// Pluggable callback
	onGesture func(ev GestureEvent)
}

// NewMultiTouchEngine constructs a gesture engine with ergonomic defaults.
func NewMultiTouchEngine(onGesture func(ev GestureEvent)) *MultiTouchEngine {
	engine := &MultiTouchEngine{
		TapTimeLimitUsec: 250000, // 250ms
		TapDistLimit:     15.0,
		SwipeThreshold:   30.0,
		onGesture:        onGesture,
	}
	for i := range engine.slots {
		engine.slots[i].TrackingID = -1
	}
	return engine
}

// SetSlot selects the active MT slot for subsequent axis updates.
func (e *MultiTouchEngine) SetSlot(slot int) {
	if slot >= 0 && slot < MaxTouchSlots {
		e.activeSlot = slot
	}
}

// SetTrackingID updates contact lifecycle for the current slot.
func (e *MultiTouchEngine) SetTrackingID(id int32, timeUsec uint64) {
	slot := &e.slots[e.activeSlot]
	if id >= 0 {
		slot.TrackingID = id
		slot.Active = true
		slot.StartTime = timeUsec
		slot.UpdateTime = timeUsec
		slot.HasInitialPos = false
	} else {
		// Finger lifted: evaluate potential tap gesture before clearing
		if slot.Active && (timeUsec-slot.StartTime) <= e.TapTimeLimitUsec {
			dist := math.Hypot(slot.CurrentX-slot.StartX, slot.CurrentY-slot.StartY)
			if dist <= e.TapDistLimit {
				e.emitTap(timeUsec, slot.CurrentX, slot.CurrentY)
			}
		}

		slot.TrackingID = -1
		slot.Active = false
		slot.HasInitialPos = false
		e.inPinch = false
		e.inSwipe = false
	}
}

// SetPositionX updates the horizontal coordinate of the active slot.
func (e *MultiTouchEngine) SetPositionX(x float64, timeUsec uint64) {
	slot := &e.slots[e.activeSlot]
	if !slot.HasInitialPos {
		slot.StartX = x
		slot.PrevX = x
		slot.CurrentX = x
	} else {
		slot.PrevX = slot.CurrentX
		slot.CurrentX = x
	}
	slot.UpdateTime = timeUsec
}

// SetPositionY updates the vertical coordinate of the active slot.
func (e *MultiTouchEngine) SetPositionY(y float64, timeUsec uint64) {
	slot := &e.slots[e.activeSlot]
	if !slot.HasInitialPos {
		slot.StartY = y
		slot.PrevY = y
		slot.CurrentY = y
	} else {
		slot.PrevY = slot.CurrentY
		slot.CurrentY = y
	}
	slot.UpdateTime = timeUsec
}

// Sync is invoked on SYN_REPORT to evaluate multi-finger motion across active slots.
func (e *MultiTouchEngine) Sync(timeUsec uint64) {
	activeCount := 0
	var sumX, sumY float64
	var sumDx, sumDy float64

	for i := range e.slots {
		if e.slots[i].Active {
			if !e.slots[i].HasInitialPos {
				e.slots[i].HasInitialPos = true
				e.slots[i].PrevX = e.slots[i].CurrentX
				e.slots[i].PrevY = e.slots[i].CurrentY
			}
			activeCount++
			sumX += e.slots[i].CurrentX
			sumY += e.slots[i].CurrentY
			sumDx += (e.slots[i].CurrentX - e.slots[i].PrevX)
			sumDy += (e.slots[i].CurrentY - e.slots[i].PrevY)
			e.slots[i].PrevX = e.slots[i].CurrentX
			e.slots[i].PrevY = e.slots[i].CurrentY
		}
	}

	if activeCount == 0 {
		e.prevActive = 0
		e.inPinch = false
		e.inSwipe = false
		return
	}

	avgX := sumX / float64(activeCount)
	avgY := sumY / float64(activeCount)
	avgDx := sumDx / float64(activeCount)
	avgDy := sumDy / float64(activeCount)

	switch activeCount {
	case 2:
		// 2-Finger scroll or pinch
		distCurr, distPrev := e.calcTwoFingerDistance()
		if distPrev > 0 && math.Abs(distCurr-distPrev) > 5.0 {
			// Pinch or spread
			scale := distCurr / distPrev
			e.inPinch = true
			if e.onGesture != nil {
				e.onGesture(GestureEvent{
					Type:     GesturePinch,
					Fingers:  2,
					TimeUsec: timeUsec,
					X:        avgX,
					Y:        avgY,
					Scale:    scale,
				})
			}
		} else if !e.inPinch && (math.Abs(avgDx) > 0.1 || math.Abs(avgDy) > 0.1) {
			// Two-finger kinetic scroll
			if e.onGesture != nil {
				e.onGesture(GestureEvent{
					Type:     GestureScroll,
					Fingers:  2,
					TimeUsec: timeUsec,
					X:        avgX,
					Y:        avgY,
					Dx:       avgDx,
					Dy:       avgDy,
					Scale:    1.0,
				})
			}
		}

	case 3, 4:
		// 3- or 4-finger directional swipe
		if math.Abs(avgDx) > e.SwipeThreshold || math.Abs(avgDy) > e.SwipeThreshold {
			var dir GestureDirection
			if math.Abs(avgDx) > math.Abs(avgDy) {
				if avgDx > 0 {
					dir = DirectionRight
				} else {
					dir = DirectionLeft
				}
			} else {
				if avgDy > 0 {
					dir = DirectionDown
				} else {
					dir = DirectionUp
				}
			}

			if !e.inSwipe {
				e.inSwipe = true
				if e.onGesture != nil {
					e.onGesture(GestureEvent{
						Type:      GestureSwipe,
						Direction: dir,
						Fingers:   uint8(activeCount),
						TimeUsec:  timeUsec,
						X:         avgX,
						Y:         avgY,
						Dx:        avgDx,
						Dy:        avgDy,
						Scale:     1.0,
					})
				}
			}
		}
	}

	e.prevActive = activeCount
}

func (e *MultiTouchEngine) emitTap(timeUsec uint64, x, y float64) {
	// Check how many fingers were active in previous packet
	fingers := uint8(1)
	if e.prevActive >= 2 {
		fingers = uint8(e.prevActive)
	}

	if e.onGesture != nil {
		e.onGesture(GestureEvent{
			Type:     GestureTap,
			Fingers:  fingers,
			TimeUsec: timeUsec,
			X:        x,
			Y:        y,
			Scale:    1.0,
		})
	}
}

func (e *MultiTouchEngine) calcTwoFingerDistance() (curr, prev float64) {
	var f1, f2 *TouchContact
	for i := range e.slots {
		if e.slots[i].Active {
			if f1 == nil {
				f1 = &e.slots[i]
			} else if f2 == nil {
				f2 = &e.slots[i]
				break
			}
		}
	}

	if f1 == nil || f2 == nil {
		return 0, 0
	}

	curr = math.Hypot(f1.CurrentX-f2.CurrentX, f1.CurrentY-f2.CurrentY)
	prev = math.Hypot(f1.PrevX-f2.PrevX, f1.PrevY-f2.PrevY)
	return curr, prev
}
