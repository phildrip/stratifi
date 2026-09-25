package input

// EventType identifies the category of hardware or synthetic input event.
type EventType uint16

const (
	EventNone EventType = iota
	EventPointerMotion
	EventPointerMotionAbsolute
	EventPointerButton
	EventPointerAxis
	EventPointerFrame
	EventKey
	EventTouchDown
	EventTouchUp
	EventTouchMotion
)

// Button states matching Linux evdev / Wayland pointer protocol.
const (
	ButtonStateReleased uint32 = 0
	ButtonStatePressed  uint32 = 1
)

// Key states matching Linux evdev / Wayland keyboard protocol.
const (
	KeyStateReleased uint32 = 0
	KeyStatePressed  uint32 = 1
	KeyStateRepeated uint32 = 2
)

// InputEvent is a cache-line-sized (64 byte) Plain-Old-Data (POD) struct.
// It contains zero heap pointers, allowing the Go GC to treat event streams
// as opaque memory with zero GC tracing overhead.
type InputEvent struct {
	Type      EventType // 2 bytes
	Flags     uint16    // 2 bytes
	DeviceID  uint32    // 4 bytes
	TimeUsec  uint64    // 8 bytes (timestamp in microseconds)
	Code      uint32    // 4 bytes (Linux evdev keycode or button code, e.g. KEY_A, BTN_LEFT)
	State     uint32    // 4 bytes (0=release, 1=press, 2=repeat)
	X         float64   // 8 bytes (absolute X coordinate or relative delta X)
	Y         float64   // 8 bytes (absolute Y coordinate or relative delta Y)
	Dx        float64   // 8 bytes (discrete scroll axis delta or subpixel delta)
	Dy        float64   // 8 bytes
	Modifiers uint32    // 4 bytes (depressed modifier mask)
	_         [4]byte   // 4 bytes padding to ensure exact 64-byte alignment
}
