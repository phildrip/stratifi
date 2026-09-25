package evdev

import (
	"bytes"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Device represents an open Linux evdev kernel input device.
type Device struct {
	FD       int
	Path     string
	Name     string
	Type     DeviceType
	AbsX     AbsInfo
	AbsY     AbsInfo
	HasRelX  bool
	HasRelY  bool
	HasWheel bool
}

// OpenDevice opens and probes an evdev device at the given path (e.g. /dev/input/event0).
func OpenDevice(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s failed: %w", path, err)
	}

	dev, err := ProbeDevice(fd, path)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	return dev, nil
}

// ProbeDevice queries an open evdev file descriptor to determine its capabilities and type.
func ProbeDevice(fd int, path string) (*Device, error) {
	// 1. Query device name
	var nameBuf [256]byte
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(IoctlEVIOCGNAME256),
		uintptr(unsafe.Pointer(&nameBuf[0])),
	)
	name := "Unknown"
	if errno == 0 {
		n := bytes.IndexByte(nameBuf[:], 0)
		if n > 0 {
			name = string(nameBuf[:n])
		}
	}

	// 2. Query event type bitmask (EV_*)
	var evBits [4]byte // 32 bits
	_, _, errno = unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(IoctlEVIOCGBIT0),
		uintptr(unsafe.Pointer(&evBits[0])),
	)
	if errno != 0 {
		return nil, fmt.Errorf("EVIOCGBIT(0) failed on %s: %v", path, errno)
	}

	hasKey := (evBits[EV_KEY/8] & (1 << (EV_KEY % 8))) != 0
	hasRel := (evBits[EV_REL/8] & (1 << (EV_REL % 8))) != 0
	hasAbs := (evBits[EV_ABS/8] & (1 << (EV_ABS % 8))) != 0

	dev := &Device{
		FD:   fd,
		Path: path,
		Name: name,
		Type: DeviceTypeUnknown,
	}

	// 3. Query relative axes if supported
	if hasRel {
		var relBits [4]byte
		_, _, _ = unix.Syscall(
			unix.SYS_IOCTL,
			uintptr(fd),
			uintptr(IoctlEVIOCGBITREL),
			uintptr(unsafe.Pointer(&relBits[0])),
		)
		dev.HasRelX = (relBits[REL_X/8] & (1 << (REL_X % 8))) != 0
		dev.HasRelY = (relBits[REL_Y/8] & (1 << (REL_Y % 8))) != 0
		dev.HasWheel = (relBits[REL_WHEEL/8] & (1 << (REL_WHEEL % 8))) != 0
	}

	// 4. Query absolute axes if supported
	var isTouchpad, isTouchscreen bool
	if hasAbs {
		var absBits [8]byte
		_, _, _ = unix.Syscall(
			unix.SYS_IOCTL,
			uintptr(fd),
			uintptr(IoctlEVIOCGBITABS),
			uintptr(unsafe.Pointer(&absBits[0])),
		)
		hasMTPosX := (absBits[ABS_MT_POSITION_X/8] & (1 << (ABS_MT_POSITION_X % 8))) != 0
		hasPosX := (absBits[ABS_X/8] & (1 << (ABS_X % 8))) != 0

		if hasMTPosX || hasPosX {
			// Read axis limits for ABS_X
			var absXInfo AbsInfo
			_, _, _ = unix.Syscall(
				unix.SYS_IOCTL,
				uintptr(fd),
				uintptr(IoctlEVIOCGABS0),
				uintptr(unsafe.Pointer(&absXInfo)),
			)
			dev.AbsX = absXInfo

			// Read axis limits for ABS_Y
			var absYInfo AbsInfo
			_, _, _ = unix.Syscall(
				unix.SYS_IOCTL,
				uintptr(fd),
				uintptr(IoctlEVIOCGABS0+1),
				uintptr(unsafe.Pointer(&absYInfo)),
			)
			dev.AbsY = absYInfo
		}

		if hasMTPosX {
			isTouchpad = true
		}
	}

	// 5. Query key capabilities
	if hasKey {
		var keyBits [64]byte // 512 bits
		_, _, _ = unix.Syscall(
			unix.SYS_IOCTL,
			uintptr(fd),
			uintptr(IoctlEVIOCGBITKEY),
			uintptr(unsafe.Pointer(&keyBits[0])),
		)

		hasBtnLeft := (keyBits[BTN_LEFT/8] & (1 << (BTN_LEFT % 8))) != 0
		hasBtnTouch := (keyBits[BTN_TOUCH/8] & (1 << (BTN_TOUCH % 8))) != 0
		hasBtnToolFinger := (keyBits[BTN_TOOL_FINGER/8] & (1 << (BTN_TOOL_FINGER % 8))) != 0
		hasKeyA := (keyBits[KEY_A/8] & (1 << (KEY_A % 8))) != 0
		hasKeyEnter := (keyBits[KEY_ENTER/8] & (1 << (KEY_ENTER % 8))) != 0

		if hasBtnToolFinger || (isTouchpad && hasBtnTouch) {
			dev.Type = DeviceTypeTouchpad
		} else if isTouchscreen || (hasBtnTouch && !dev.HasRelX) {
			dev.Type = DeviceTypeTouchscreen
		} else if dev.HasRelX && (hasBtnLeft || dev.HasWheel) {
			dev.Type = DeviceTypeMouse
		} else if hasKeyA || hasKeyEnter {
			dev.Type = DeviceTypeKeyboard
		}
	} else if dev.HasRelX && dev.HasRelY {
		dev.Type = DeviceTypeMouse
	}

	return dev, nil
}

// Close closes the device file descriptor.
func (d *Device) Close() error {
	if d.FD >= 0 {
		err := unix.Close(d.FD)
		d.FD = -1
		return err
	}
	return nil
}
