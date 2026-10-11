package evdev

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
	"stratifi/internal/input"
)

// EvdevPump monitors /dev/input/event* devices using epoll and streams
// decoded input events directly into a zero-allocation RingBuffer.
type EvdevPump struct {
	epfd      int
	devices   map[int]*Device // keyed by FD
	devMu     sync.RWMutex
	rb        *input.RingBuffer
	closed    atomic.Bool
	inotifyFD int
	// gestures receives raw MT-protocol-B events (nil disables touchpad
	// gesture recognition). Set before ScanDevices.
	gestures *input.GestureRouter
}

// SetGestureRouter attaches a multi-touch recognizer; EV_ABS MT events and
// SYN_REPORT are then routed to it instead of being ignored.
func (p *EvdevPump) SetGestureRouter(g *input.GestureRouter) { p.gestures = g }

// NewEvdevPump creates a new evdev input listener.
func NewEvdevPump(rb *input.RingBuffer) (*EvdevPump, error) {
	epfd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("epoll_create1 failed: %w", err)
	}

	p := &EvdevPump{
		epfd:    epfd,
		devices: make(map[int]*Device),
		rb:      rb,
	}

	// Setup inotify for /dev/input hotplugging
	infd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err == nil {
		_, _ = unix.InotifyAddWatch(infd, "/dev/input", unix.IN_CREATE)
		ev := unix.EpollEvent{
			Events: unix.EPOLLIN,
			Fd:     int32(infd),
		}
		_ = unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, infd, &ev)
		p.inotifyFD = infd
	}

	return p, nil
}

// ScanDevices enumerates and opens all accessible /dev/input/event* devices.
func (p *EvdevPump) ScanDevices() int {
	entries, err := os.ReadDir("/dev/input")
	if err != nil {
		return 0
	}

	opened := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "event") {
			fullPath := filepath.Join("/dev/input", name)
			if err := p.AddDevice(fullPath); err == nil {
				opened++
			}
		}
	}
	return opened
}

// AddDevice opens an evdev path, probes it, and adds it to the epoll set.
func (p *EvdevPump) AddDevice(path string) error {
	p.devMu.Lock()
	defer p.devMu.Unlock()

	// Check if already open
	for _, d := range p.devices {
		if d.Path == path {
			return nil
		}
	}

	dev, err := OpenDevice(path)
	if err != nil {
		return err
	}

	ev := unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(dev.FD),
	}
	if err := unix.EpollCtl(p.epfd, unix.EPOLL_CTL_ADD, dev.FD, &ev); err != nil {
		_ = dev.Close()
		return fmt.Errorf("epoll_ctl add failed for %s: %w", path, err)
	}

	p.devices[dev.FD] = dev
	return nil
}

// RemoveDevice unregisters and closes a device.
func (p *EvdevPump) RemoveDevice(fd int) {
	p.devMu.Lock()
	defer p.devMu.Unlock()

	if dev, ok := p.devices[fd]; ok {
		_ = unix.EpollCtl(p.epfd, unix.EPOLL_CTL_DEL, fd, nil)
		_ = dev.Close()
		delete(p.devices, fd)
	}
}

// PollAndDispatch waits for input events on all watched file descriptors
// and converts kernel input_event structs into 64-byte InputEvents (0 allocs/op).
func (p *EvdevPump) PollAndDispatch(timeoutMs int) (int, error) {
	if p.closed.Load() {
		return 0, nil
	}

	var events [16]unix.EpollEvent
	nEvents, err := unix.EpollWait(p.epfd, events[:], timeoutMs)
	if err != nil {
		if err == unix.EINTR {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	var rawBuf [1024]byte

	for i := 0; i < nEvents; i++ {
		fd := int(events[i].Fd)

		// Handle inotify hotplug
		if fd == p.inotifyFD {
			p.handleHotplug()
			continue
		}

		p.devMu.RLock()
		dev, found := p.devices[fd]
		p.devMu.RUnlock()

		if !found {
			continue
		}

		// Read pending kernel events
		for {
			n, err := unix.Read(fd, rawBuf[:])
			if err != nil || n <= 0 {
				break
			}

			numEvents := n / KernelInputEventSize
			for k := 0; k < numEvents; k++ {
				offset := k * KernelInputEventSize
				kev := (*KernelInputEvent)(unsafe.Pointer(&rawBuf[offset]))
				if p.translateAndPush(dev, kev) {
					count++
				}
			}
		}
	}

	return count, nil
}

// translateAndPush converts a 24-byte Linux kernel input_event to a 64-byte InputEvent.
func (p *EvdevPump) translateAndPush(dev *Device, kev *KernelInputEvent) bool {
	timeUsec := uint64(kev.TimeSec)*1000000 + uint64(kev.TimeUsec)
	var out input.InputEvent
	out.TimeUsec = timeUsec
	out.DeviceID = uint32(dev.FD)

	switch kev.Type {
	case EV_REL:
		switch kev.Code {
		case REL_X:
			out.Type = input.EventPointerMotion
			out.X = float64(kev.Value)
			return p.rb.Push(out)
		case REL_Y:
			out.Type = input.EventPointerMotion
			out.Y = float64(kev.Value)
			return p.rb.Push(out)
		case REL_WHEEL:
			out.Type = input.EventPointerAxis
			out.Dy = float64(kev.Value)
			return p.rb.Push(out)
		case REL_HWHEEL:
			out.Type = input.EventPointerAxis
			out.Dx = float64(kev.Value)
			return p.rb.Push(out)
		}

	case EV_ABS:
		if p.gestures != nil {
			_ = p.gestures.FeedEvent(uint16(kev.Type), int32(kev.Code), kev.Value, timeUsec)
		}
		return false

	case EV_SYN:
		if p.gestures != nil && kev.Code == input.SynReport {
			p.gestures.Sync(timeUsec)
		}
		return false

	case EV_KEY:
		// Distinguish mouse buttons vs keyboard keys
		if kev.Code >= BTN_MISC && kev.Code <= BTN_GEAR_UP {
			out.Type = input.EventPointerButton
			out.Code = uint32(kev.Code)
			out.State = uint32(kev.Value)
			return p.rb.Push(out)
		} else {
			out.Type = input.EventKey
			out.Code = uint32(kev.Code)
			out.State = uint32(kev.Value)
			return p.rb.Push(out)
		}
	}

	return false
}

// handleHotplug reads inotify events and probes new event devices.
func (p *EvdevPump) handleHotplug() {
	var buf [512]byte
	n, err := unix.Read(p.inotifyFD, buf[:])
	if err != nil || n <= 0 {
		return
	}

	offset := 0
	for offset < n {
		rawEv := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))
		nameLen := int(rawEv.Len)
		if nameLen > 0 {
			nameBytes := buf[offset+unix.SizeofInotifyEvent : offset+unix.SizeofInotifyEvent+nameLen]
			nullIdx := 0
			for nullIdx < len(nameBytes) && nameBytes[nullIdx] != 0 {
				nullIdx++
			}
			name := string(nameBytes[:nullIdx])
			if strings.HasPrefix(name, "event") {
				_ = p.AddDevice(filepath.Join("/dev/input", name))
			}
		}
		offset += unix.SizeofInotifyEvent + nameLen
	}
}

// Close shuts down the epoll and inotify listeners and closes all devices.
func (p *EvdevPump) Close() error {
	if p.closed.Swap(true) {
		return nil
	}

	p.devMu.Lock()
	for _, dev := range p.devices {
		_ = dev.Close()
	}
	p.devices = nil
	p.devMu.Unlock()

	if p.inotifyFD >= 0 {
		_ = unix.Close(p.inotifyFD)
		p.inotifyFD = -1
	}

	return unix.Close(p.epfd)
}
