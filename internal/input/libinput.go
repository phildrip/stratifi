package input

/*
#cgo LDFLAGS: -l:libinput.so.10
#include <stdlib.h>
#include <stdint.h>
#include <fcntl.h>
#include <unistd.h>

// Minimal libinput definitions for Cgo
struct libinput;
struct libinput_event;
struct libinput_event_pointer;
struct libinput_event_keyboard;
struct udev;

struct libinput_interface {
	int (*open_restricted)(const char *path, int flags, void *user_data);
	void (*close_restricted)(int fd, void *user_data);
};

enum libinput_event_type {
	LIBINPUT_EVENT_NONE = 0,
	LIBINPUT_EVENT_DEVICE_ADDED = 1,
	LIBINPUT_EVENT_DEVICE_REMOVED = 2,
	LIBINPUT_EVENT_KEYBOARD_KEY = 300,
	LIBINPUT_EVENT_POINTER_MOTION = 400,
	LIBINPUT_EVENT_POINTER_MOTION_ABSOLUTE = 401,
	LIBINPUT_EVENT_POINTER_BUTTON = 402,
	LIBINPUT_EVENT_POINTER_AXIS = 403,
	LIBINPUT_EVENT_TOUCH_DOWN = 500,
	LIBINPUT_EVENT_TOUCH_UP = 501,
	LIBINPUT_EVENT_TOUCH_MOTION = 502,
};

// Prototypes from libinput.so.10
struct libinput *libinput_udev_create_context(const struct libinput_interface *interface, void *user_data, struct udev *udev);
int libinput_udev_assign_seat(struct libinput *libinput, const char *seat_id);
int libinput_get_fd(struct libinput *libinput);
int libinput_dispatch(struct libinput *libinput);
struct libinput_event *libinput_get_event(struct libinput *libinput);
enum libinput_event_type libinput_event_get_type(struct libinput_event *event);
void libinput_event_destroy(struct libinput_event *event);
struct libinput *libinput_unref(struct libinput *libinput);

struct libinput_event_pointer *libinput_event_get_pointer_event(struct libinput_event *event);
double libinput_event_pointer_get_dx(struct libinput_event_pointer *event);
double libinput_event_pointer_get_dy(struct libinput_event_pointer *event);
double libinput_event_pointer_get_absolute_x_transformed(struct libinput_event_pointer *event, uint32_t width);
double libinput_event_pointer_get_absolute_y_transformed(struct libinput_event_pointer *event, uint32_t height);
uint32_t libinput_event_pointer_get_button(struct libinput_event_pointer *event);
uint32_t libinput_event_pointer_get_button_state(struct libinput_event_pointer *event);
uint64_t libinput_event_pointer_get_time_usec(struct libinput_event_pointer *event);

struct libinput_event_keyboard *libinput_event_get_keyboard_event(struct libinput_event *event);
uint32_t libinput_event_keyboard_get_key(struct libinput_event_keyboard *event);
uint32_t libinput_event_keyboard_get_key_state(struct libinput_event_keyboard *event);
uint64_t libinput_event_keyboard_get_time_usec(struct libinput_event_keyboard *event);

static int open_restricted(const char *path, int flags, void *user_data) {
	int fd = open(path, flags);
	return fd < 0 ? -1 : fd;
}

static void close_restricted(int fd, void *user_data) {
	close(fd);
}

static const struct libinput_interface default_interface = {
	.open_restricted = open_restricted,
	.close_restricted = close_restricted,
};

static struct libinput *init_libinput_udev(const char *seat_id) {
	struct libinput *li = libinput_udev_create_context(&default_interface, NULL, NULL);
	if (!li) return NULL;
	if (libinput_udev_assign_seat(li, seat_id) != 0) {
		libinput_unref(li);
		return NULL;
	}
	return li;
}
*/
import "C"
import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// LibinputPump manages a libinput context and pumps events into an EventRingBuffer.
type LibinputPump struct {
	li     *C.struct_libinput
	fd     int
	rb     *RingBuffer
	closed atomic.Bool
}

// NewLibinputPump initializes a libinput context for the specified seat.
func NewLibinputPump(seatID string, rb *RingBuffer) (*LibinputPump, error) {
	cSeat := C.CString(seatID)
	defer C.free(unsafe.Pointer(cSeat))

	li := C.init_libinput_udev(cSeat)
	if li == nil {
		return nil, fmt.Errorf("failed to initialize libinput context for seat %q", seatID)
	}

	fd := int(C.libinput_get_fd(li))

	return &LibinputPump{
		li: li,
		fd: fd,
		rb: rb,
	}, nil
}

// FD returns the pollable file descriptor for libinput event readiness.
func (p *LibinputPump) FD() int {
	return p.fd
}

// Dispatch processes pending events from the kernel input subsystem
// and enqueues them into the ring buffer with zero allocations.
func (p *LibinputPump) Dispatch(screenWidth, screenHeight uint32) error {
	if p.closed.Load() {
		return nil
	}

	if res := C.libinput_dispatch(p.li); res != 0 {
		return fmt.Errorf("libinput_dispatch failed: %d", res)
	}

	for {
		ev := C.libinput_get_event(p.li)
		if ev == nil {
			break
		}

		evType := C.libinput_event_get_type(ev)
		var out InputEvent

		switch evType {
		case C.LIBINPUT_EVENT_POINTER_MOTION:
			pev := C.libinput_event_get_pointer_event(ev)
			out.Type = EventPointerMotion
			out.TimeUsec = uint64(C.libinput_event_pointer_get_time_usec(pev))
			out.X = float64(C.libinput_event_pointer_get_dx(pev))
			out.Y = float64(C.libinput_event_pointer_get_dy(pev))
			p.rb.Push(out)

		case C.LIBINPUT_EVENT_POINTER_MOTION_ABSOLUTE:
			pev := C.libinput_event_get_pointer_event(ev)
			out.Type = EventPointerMotionAbsolute
			out.TimeUsec = uint64(C.libinput_event_pointer_get_time_usec(pev))
			out.X = float64(C.libinput_event_pointer_get_absolute_x_transformed(pev, C.uint32_t(screenWidth)))
			out.Y = float64(C.libinput_event_pointer_get_absolute_y_transformed(pev, C.uint32_t(screenHeight)))
			p.rb.Push(out)

		case C.LIBINPUT_EVENT_POINTER_BUTTON:
			pev := C.libinput_event_get_pointer_event(ev)
			out.Type = EventPointerButton
			out.TimeUsec = uint64(C.libinput_event_pointer_get_time_usec(pev))
			out.Code = uint32(C.libinput_event_pointer_get_button(pev))
			out.State = uint32(C.libinput_event_pointer_get_button_state(pev))
			p.rb.Push(out)

		case C.LIBINPUT_EVENT_KEYBOARD_KEY:
			kev := C.libinput_event_get_keyboard_event(ev)
			out.Type = EventKey
			out.TimeUsec = uint64(C.libinput_event_keyboard_get_time_usec(kev))
			out.Code = uint32(C.libinput_event_keyboard_get_key(kev))
			out.State = uint32(C.libinput_event_keyboard_get_key_state(kev))
			p.rb.Push(out)
		}

		C.libinput_event_destroy(ev)
	}

	return nil
}

// Close destroys the libinput context and closes all device file descriptors.
func (p *LibinputPump) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	if p.li != nil {
		C.libinput_unref(p.li)
		p.li = nil
	}
	return nil
}
