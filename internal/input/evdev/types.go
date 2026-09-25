package evdev

import (
	"unsafe"
)

// KernelInputEvent represents the standard 24-byte Linux kernel input_event struct (64-bit architecture).
type KernelInputEvent struct {
	TimeSec  int64  // 8 bytes (seconds since epoch)
	TimeUsec int64  // 8 bytes (microseconds)
	Type     uint16 // 2 bytes (EV_SYN, EV_KEY, EV_REL, EV_ABS)
	Code     uint16 // 2 bytes (event code, e.g. KEY_A, REL_X)
	Value    int32  // 4 bytes (key state: 0/1/2, or axis delta/position)
}

const (
	KernelInputEventSize = int(unsafe.Sizeof(KernelInputEvent{})) // 24 bytes
)

// Linux input event types (EV_*)
const (
	EV_SYN       uint16 = 0x00
	EV_KEY       uint16 = 0x01
	EV_REL       uint16 = 0x02
	EV_ABS       uint16 = 0x03
	EV_MSC       uint16 = 0x04
	EV_SW        uint16 = 0x05
	EV_LED       uint16 = 0x11
	EV_SND       uint16 = 0x12
	EV_REP       uint16 = 0x14
	EV_FF        uint16 = 0x15
	EV_PWR       uint16 = 0x16
	EV_FF_STATUS uint16 = 0x17
	EV_MAX       uint16 = 0x1f
)

// Synchronization events (SYN_*)
const (
	SYN_REPORT    uint16 = 0
	SYN_CONFIG    uint16 = 1
	SYN_MT_REPORT uint16 = 2
	SYN_DROPPED   uint16 = 3
)

// Relative axes (REL_*)
const (
	REL_X             uint16 = 0x00
	REL_Y             uint16 = 0x01
	REL_Z             uint16 = 0x02
	REL_RX            uint16 = 0x03
	REL_RY            uint16 = 0x04
	REL_RZ            uint16 = 0x05
	REL_HWHEEL        uint16 = 0x06
	REL_DIAL          uint16 = 0x07
	REL_WHEEL         uint16 = 0x08
	REL_MISC          uint16 = 0x09
	REL_RESERVED      uint16 = 0x0a
	REL_WHEEL_HI_RES  uint16 = 0x0b
	REL_HWHEEL_HI_RES uint16 = 0x0c
	REL_MAX           uint16 = 0x0f
)

// Absolute axes (ABS_*)
const (
	ABS_X              uint16 = 0x00
	ABS_Y              uint16 = 0x01
	ABS_Z              uint16 = 0x02
	ABS_PRESSURE       uint16 = 0x18
	ABS_MT_SLOT        uint16 = 0x2f // MT slot being modified
	ABS_MT_TOUCH_MAJOR uint16 = 0x30
	ABS_MT_TOUCH_MINOR uint16 = 0x31
	ABS_MT_WIDTH_MAJOR uint16 = 0x32
	ABS_MT_WIDTH_MINOR uint16 = 0x33
	ABS_MT_ORIENTATION uint16 = 0x34
	ABS_MT_POSITION_X  uint16 = 0x35 // Center X touch position
	ABS_MT_POSITION_Y  uint16 = 0x36 // Center Y touch position
	ABS_MT_TOOL_TYPE   uint16 = 0x37
	ABS_MT_BLOB_ID     uint16 = 0x38
	ABS_MT_TRACKING_ID uint16 = 0x39 // Unique ID of initiated contact (-1 = lifted)
	ABS_MT_PRESSURE    uint16 = 0x3a
	ABS_MT_DISTANCE    uint16 = 0x3b
	ABS_MT_TOOL_X      uint16 = 0x3c
	ABS_MT_TOOL_Y      uint16 = 0x3d
	ABS_MAX            uint16 = 0x3f
)

// Buttons (BTN_*)
const (
	BTN_MISC           uint16 = 0x100
	BTN_0              uint16 = 0x100
	BTN_1              uint16 = 0x101
	BTN_2              uint16 = 0x102
	BTN_MOUSE          uint16 = 0x110
	BTN_LEFT           uint16 = 0x110
	BTN_RIGHT          uint16 = 0x111
	BTN_MIDDLE         uint16 = 0x112
	BTN_SIDE           uint16 = 0x113
	BTN_EXTRA          uint16 = 0x114
	BTN_FORWARD        uint16 = 0x115
	BTN_BACK           uint16 = 0x116
	BTN_TASK           uint16 = 0x117
	BTN_JOYSTICK       uint16 = 0x120
	BTN_GAMEPAD        uint16 = 0x130
	BTN_DIGI           uint16 = 0x140
	BTN_TOOL_PEN       uint16 = 0x140
	BTN_TOOL_RUBBER    uint16 = 0x141
	BTN_TOOL_BRUSH     uint16 = 0x142
	BTN_TOOL_PENCIL    uint16 = 0x143
	BTN_TOOL_AIRBRUSH  uint16 = 0x144
	BTN_TOOL_FINGER    uint16 = 0x145
	BTN_TOOL_MOUSE     uint16 = 0x146
	BTN_TOOL_LENS      uint16 = 0x147
	BTN_TOUCH          uint16 = 0x14a
	BTN_STYLUS         uint16 = 0x14b
	BTN_STYLUS2        uint16 = 0x14c
	BTN_TOOL_DOUBLETAP uint16 = 0x14d
	BTN_TOOL_TRIPLETAP uint16 = 0x14e
	BTN_TOOL_QUADTAP   uint16 = 0x14f
	BTN_WHEEL          uint16 = 0x150
	BTN_GEAR_DOWN      uint16 = 0x150
	BTN_GEAR_UP        uint16 = 0x151
)

// Common Keyboard Keys (KEY_*)
const (
	KEY_RESERVED   uint16 = 0
	KEY_ESC        uint16 = 1
	KEY_1          uint16 = 2
	KEY_2          uint16 = 3
	KEY_TAB        uint16 = 15
	KEY_Q          uint16 = 16
	KEY_W          uint16 = 17
	KEY_E          uint16 = 18
	KEY_R          uint16 = 19
	KEY_ENTER      uint16 = 28
	KEY_LEFTCTRL   uint16 = 29
	KEY_A          uint16 = 30
	KEY_S          uint16 = 31
	KEY_D          uint16 = 32
	KEY_LEFTSHIFT  uint16 = 42
	KEY_SPACE      uint16 = 57
	KEY_CAPSLOCK   uint16 = 58
	KEY_LEFTALT    uint16 = 56
	KEY_LEFTMETA   uint16 = 125
	KEY_RIGHTMETA  uint16 = 126
)

// Linux evdev ioctl constants (64-bit Linux x86_64)
const (
	IoctlEVIOCGVERSION = 0x80044501
	IoctlEVIOCGID      = 0x80084502
	IoctlEVIOCGNAME256 = 0x81004506
	IoctlEVIOCGPROP8   = 0x80084509
	IoctlEVIOCGBIT0    = 0x80044520
	IoctlEVIOCGBITKEY  = 0x80404521
	IoctlEVIOCGBITREL  = 0x80044522
	IoctlEVIOCGBITABS  = 0x80084523
	IoctlEVIOCGABS0    = 0x80184540
)

// DeviceType classifies the physical hardware role of an input device.
type DeviceType uint8

const (
	DeviceTypeUnknown DeviceType = iota
	DeviceTypeKeyboard
	DeviceTypeMouse
	DeviceTypeTouchpad
	DeviceTypeTouchscreen
)

func (dt DeviceType) String() string {
	switch dt {
	case DeviceTypeKeyboard:
		return "keyboard"
	case DeviceTypeMouse:
		return "mouse"
	case DeviceTypeTouchpad:
		return "touchpad"
	case DeviceTypeTouchscreen:
		return "touchscreen"
	default:
		return "unknown"
	}
}

// AbsInfo stores limits and resolution for an absolute axis.
type AbsInfo struct {
	Value      int32
	Minimum    int32
	Maximum    int32
	Fuzz       int32
	Flat       int32
	Resolution int32
}
