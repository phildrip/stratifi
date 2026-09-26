package gesture

import (
	"math"
)

// MouseAction represents a triggered mouse gesture action callback.
type MouseAction func()

// MouseGestureEngine recognizes mouse strokes, rocker gestures, and button chords.
type MouseGestureEngine struct {
	TriggerButton uint32  // Button that starts gesture recording (default: BTN_RIGHT = 0x111)
	MinStrokeDist float64 // Minimum displacement to register a stroke direction (default: 25.0)

	isRecording bool
	startX      float64
	startY      float64
	lastX       float64
	lastY       float64
	strokeSeq   []byte // sequence of 'L', 'R', 'U', 'D'
	buttonMask  uint32 // currently held buttons
	rockerFired bool

	strokeBindings map[string]MouseAction
	rockerBindings map[uint64]MouseAction // key = (uint64(btn1) << 32) | uint64(btn2)
	onDefaultClick func(button uint32)
}

// NewMouseGestureEngine constructs a mouse gesture recognizer.
func NewMouseGestureEngine(triggerButton uint32, onDefaultClick func(button uint32)) *MouseGestureEngine {
	if triggerButton == 0 {
		triggerButton = 0x111 // Default BTN_RIGHT
	}

	return &MouseGestureEngine{
		TriggerButton:  triggerButton,
		MinStrokeDist:  25.0,
		strokeSeq:      make([]byte, 0, 8),
		strokeBindings: make(map[string]MouseAction),
		rockerBindings: make(map[uint64]MouseAction),
		onDefaultClick: onDefaultClick,
	}
}

// RegisterStroke binds a cardinal direction sequence (e.g. "L", "R", "UD", "DR") to an action.
func (m *MouseGestureEngine) RegisterStroke(sequence string, action MouseAction) {
	m.strokeBindings[sequence] = action
}

// RegisterRocker binds a rocker chord (e.g. hold btn1 then press btn2) to an action.
func (m *MouseGestureEngine) RegisterRocker(firstButton, secondButton uint32, action MouseAction) {
	key := (uint64(firstButton) << 32) | uint64(secondButton)
	m.rockerBindings[key] = action
}

// OnButton processes mouse button press and release events.
// Returns true if the event was consumed by a gesture.
func (m *MouseGestureEngine) OnButton(button uint32, state uint32, cursorX, cursorY float64) bool {
	if state == 1 { // Button Pressed
		// Check for rocker gestures with already-held buttons
		for held := uint32(0x100); held <= 0x117; held++ {
			if (m.buttonMask&(1<<(held&0x1f))) != 0 && held != button {
				key := (uint64(held) << 32) | uint64(button)
				if action, found := m.rockerBindings[key]; found {
					m.rockerFired = true
					action()
					return true
				}
			}
		}

		m.buttonMask |= (1 << (button & 0x1f))

		if button == m.TriggerButton {
			m.isRecording = true
			m.startX = cursorX
			m.startY = cursorY
			m.lastX = cursorX
			m.lastY = cursorY
			m.strokeSeq = m.strokeSeq[:0]
			m.rockerFired = false
			return true // Intercept for gesture tracking
		}
	} else { // Button Released
		m.buttonMask &^= (1 << (button & 0x1f))

		if button == m.TriggerButton && m.isRecording {
			m.isRecording = false

			if m.rockerFired {
				return true
			}

			// If a gesture stroke sequence was drawn, execute it
			if len(m.strokeSeq) > 0 {
				strokeStr := string(m.strokeSeq)
				if action, ok := m.strokeBindings[strokeStr]; ok {
					action()
					return true
				}
			}

			// If minimal or no movement occurred, emit regular default click
			totalDist := math.Hypot(cursorX-m.startX, cursorY-m.startY)
			if totalDist < m.MinStrokeDist {
				if m.onDefaultClick != nil {
					m.onDefaultClick(button)
				}
			}
			return true
		}
	}

	return false
}

// OnMotion tracks cursor movement during gesture recording.
func (m *MouseGestureEngine) OnMotion(cursorX, cursorY float64) {
	if !m.isRecording {
		return
	}

	dx := cursorX - m.lastX
	dy := cursorY - m.lastY
	dist := math.Hypot(dx, dy)

	if dist >= m.MinStrokeDist {
		var dir byte
		if math.Abs(dx) > math.Abs(dy) {
			if dx > 0 {
				dir = 'R'
			} else {
				dir = 'L'
			}
		} else {
			if dy > 0 {
				dir = 'D'
			} else {
				dir = 'U'
			}
		}

		// Don't record repeated consecutive directions
		if len(m.strokeSeq) == 0 || m.strokeSeq[len(m.strokeSeq)-1] != dir {
			if len(m.strokeSeq) < 8 {
				m.strokeSeq = append(m.strokeSeq, dir)
			}
		}

		m.lastX = cursorX
		m.lastY = cursorY
	}
}
