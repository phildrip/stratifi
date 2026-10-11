package evdev

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
	"stratifi/internal/input"
)

func TestKernelInputEventSize(t *testing.T) {
	size := unsafe.Sizeof(KernelInputEvent{})
	if size != 24 {
		t.Fatalf("expected KernelInputEvent to be 24 bytes, got %d", size)
	}
}

func TestEvdevEventTranslation(t *testing.T) {
	rb := input.NewRingBuffer(16)
	pump := &EvdevPump{rb: rb}
	mockDev := &Device{FD: 99, Type: DeviceTypeMouse}

	// 1. Test relative mouse motion
	relX := KernelInputEvent{
		TimeSec:  100,
		TimeUsec: 500,
		Type:     EV_REL,
		Code:     REL_X,
		Value:    -15,
	}
	if !pump.translateAndPush(mockDev, &relX) {
		t.Fatalf("translateAndPush failed for REL_X")
	}

	ev, ok := rb.Pop()
	if !ok || ev.Type != input.EventPointerMotion || ev.X != -15 || ev.TimeUsec != 100000500 {
		t.Fatalf("unexpected event for REL_X: %+v", ev)
	}

	// 2. Test mouse button press
	btnLeft := KernelInputEvent{
		TimeSec:  101,
		TimeUsec: 0,
		Type:     EV_KEY,
		Code:     BTN_LEFT,
		Value:    1,
	}
	if !pump.translateAndPush(mockDev, &btnLeft) {
		t.Fatalf("translateAndPush failed for BTN_LEFT")
	}

	ev, ok = rb.Pop()
	if !ok || ev.Type != input.EventPointerButton || ev.Code != uint32(BTN_LEFT) || ev.State != 1 {
		t.Fatalf("unexpected event for BTN_LEFT: %+v", ev)
	}

	// 3. Test keyboard key press
	keyA := KernelInputEvent{
		TimeSec:  102,
		TimeUsec: 0,
		Type:     EV_KEY,
		Code:     KEY_A,
		Value:    1,
	}
	if !pump.translateAndPush(mockDev, &keyA) {
		t.Fatalf("translateAndPush failed for KEY_A")
	}

	ev, ok = rb.Pop()
	if !ok || ev.Type != input.EventKey || ev.Code != uint32(KEY_A) || ev.State != 1 {
		t.Fatalf("unexpected event for KEY_A: %+v", ev)
	}

	// 4. Test wheel scroll
	wheel := KernelInputEvent{
		TimeSec:  103,
		TimeUsec: 0,
		Type:     EV_REL,
		Code:     REL_WHEEL,
		Value:    -1,
	}
	if !pump.translateAndPush(mockDev, &wheel) {
		t.Fatalf("translateAndPush failed for REL_WHEEL")
	}

	ev, ok = rb.Pop()
	if !ok || ev.Type != input.EventPointerAxis || ev.Dy != -1 {
		t.Fatalf("unexpected event for REL_WHEEL: %+v", ev)
	}
}

func TestEvdevEpollSyntheticStream(t *testing.T) {
	// Create socketpair to simulate an evdev kernel character device
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("Socketpair failed: %v", err)
	}
	devReadFD := fds[0]
	kernelWriteFD := fds[1]
	defer unix.Close(devReadFD)
	defer unix.Close(kernelWriteFD)

	rb := input.NewRingBuffer(32)
	pump, err := NewEvdevPump(rb)
	if err != nil {
		t.Fatalf("NewEvdevPump failed: %v", err)
	}
	defer pump.Close()

	// Register simulated device
	ev := unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(devReadFD),
	}
	if err := unix.EpollCtl(pump.epfd, unix.EPOLL_CTL_ADD, devReadFD, &ev); err != nil {
		t.Fatalf("EpollCtl ADD failed: %v", err)
	}

	pump.devMu.Lock()
	pump.devices[devReadFD] = &Device{
		FD:   devReadFD,
		Path: "/dev/input/simulated0",
		Name: "Simulated Mouse",
		Type: DeviceTypeMouse,
	}
	pump.devMu.Unlock()

	// Kernel writes 2 events
	events := [2]KernelInputEvent{
		{TimeSec: 1, TimeUsec: 100, Type: EV_REL, Code: REL_X, Value: 42},
		{TimeSec: 1, TimeUsec: 100, Type: EV_REL, Code: REL_Y, Value: -24},
	}
	rawBytes := (*[48]byte)(unsafe.Pointer(&events[0]))[:]
	if _, err := unix.Write(kernelWriteFD, rawBytes); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Pump dispatches
	n, err := pump.PollAndDispatch(100)
	if err != nil {
		t.Fatalf("PollAndDispatch failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 dispatched events, got %d", n)
	}

	if rb.Len() != 2 {
		t.Fatalf("expected 2 events in ring buffer, got %d", rb.Len())
	}

	ev1, _ := rb.Pop()
	ev2, _ := rb.Pop()

	if ev1.X != 42 || ev2.Y != -24 {
		t.Fatalf("unexpected event values: ev1=%+v, ev2=%+v", ev1, ev2)
	}
}

func BenchmarkEvdevTranslateAndPush(b *testing.B) {
	rb := input.NewRingBuffer(1024)
	pump := &EvdevPump{rb: rb}
	mockDev := &Device{FD: 1, Type: DeviceTypeMouse}
	kev := KernelInputEvent{
		TimeSec:  1000,
		TimeUsec: 500,
		Type:     EV_REL,
		Code:     REL_X,
		Value:    10,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pump.translateAndPush(mockDev, &kev)
		_, _ = rb.Pop()
	}
}
