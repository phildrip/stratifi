package server

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"stratifi/internal/protocol/textinput"
	"stratifi/internal/wire"
)

func TestTextInputV3LifecycleAndIMECommit(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wayland-textinput-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	go func() { _ = srv.Run() }()
	time.Sleep(30 * time.Millisecond)

	netConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer netConn.Close()
	rawConn, _ := netConn.(*net.UnixConn).SyscallConn()
	var fd int
	_ = rawConn.Control(func(f uintptr) { fd = int(f) })

	var w wire.Writer
	var sb [1024]byte
	var sfds [4]int
	w.Init(sb[:], sfds[:])
	send := func(msgs ...func()) {
		w.Reset()
		for _, m := range msgs {
			m()
		}
		if err := unix.Sendmsg(fd, w.Bytes(), nil, nil, 0); err != nil {
			t.Fatalf("Sendmsg: %v", err)
		}
	}
	msg := func(obj uint32, op uint16, fill func()) func() {
		return func() {
			s := w.BeginMessage(obj, op)
			fill()
			if err := w.EndMessage(s, obj, op); err != nil {
				t.Fatalf("EndMessage: %v", err)
			}
		}
	}

	const (
		compID  = 3
		surfID  = 4
		seatID  = 5
		kbdID   = 6
		tiMgrID = 7
		tiID    = 8
	)
	send(
		msg(1, 1, func() { _ = w.WriteUint32(2) }),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalCompositor)
			_ = w.WriteString("wl_compositor")
			_ = w.WriteUint32(4)
			_ = w.WriteUint32(compID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalSeat)
			_ = w.WriteString("wl_seat")
			_ = w.WriteUint32(5)
			_ = w.WriteUint32(seatID)
		}),
		msg(2, 0, func() {
			_ = w.WriteUint32(GlobalTextInputManager)
			_ = w.WriteString(textinput.ZwpTextInputManagerV3Interface)
			_ = w.WriteUint32(1)
			_ = w.WriteUint32(tiMgrID)
		}),
		msg(compID, 0, func() { _ = w.WriteUint32(surfID) }),
		msg(seatID, 1, func() { _ = w.WriteUint32(kbdID) }),
		msg(tiMgrID, textinput.ZwpTextInputManagerV3OpGetTextInput, func() {
			_ = w.WriteUint32(tiID)
			_ = w.WriteUint32(seatID)
		}),
	)

	var c *Client
	for i := 0; i < 200; i++ {
		if c = srv.GetClient(1); c != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if c == nil {
		t.Fatal("client did not connect")
	}

	// Drain initial registry/seat events.
	{
		var dbuf [8192]byte
		for {
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 100); n == 0 {
				break
			}
			if nr, _ := unix.Read(fd, dbuf[:]); nr <= 0 {
				break
			}
		}
	}

	type tiEv struct {
		op      uint16
		payload []byte
	}
	events := make(chan tiEv, 16)
	stop := make(chan struct{})
	le := binary.LittleEndian
	go func() {
		var buf [4096]byte
		var pending []byte
		for {
			select {
			case <-stop:
				return
			default:
			}
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, _ := unix.Poll(pfd, 30); n == 0 {
				continue
			}
			nr, _ := unix.Read(fd, buf[:])
			if nr <= 0 {
				continue
			}
			pending = append(pending, buf[:nr]...)
			for len(pending) >= 8 {
				obj := le.Uint32(pending[0:4])
				word := le.Uint32(pending[4:8])
				size := int(word >> 16)
				op := uint16(word & 0xFFFF)
				if size < 8 || size > len(pending) {
					break
				}
				if obj == tiID {
					cp := make([]byte, size-8)
					copy(cp, pending[8:size])
					events <- tiEv{op: op, payload: cp}
				}
				pending = pending[size:]
			}
		}
	}()
	defer close(stop)

	// 1. Focus surface -> must emit zwp_text_input_v3.enter(surfID).
	c.SendKeyboardEnter(surfID, nil)
	select {
	case ev := <-events:
		if ev.op != textinput.ZwpTextInputV3EventOpEnter {
			t.Fatalf("expected enter (op 0), got op %d", ev.op)
		}
		if gotSurf := le.Uint32(ev.payload[:4]); gotSurf != surfID {
			t.Fatalf("enter surface = %d, want %d", gotSurf, surfID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for zwp_text_input_v3.enter")
	}

	// 2. Enable text input, set content type, cursor rectangle, surrounding text, and commit.
	send(
		msg(tiID, textinput.ZwpTextInputV3OpEnable, func() {}),
		msg(tiID, textinput.ZwpTextInputV3OpSetSurroundingText, func() {
			_ = w.WriteString("hello ")
			_ = w.WriteInt32(6)
			_ = w.WriteInt32(6)
		}),
		msg(tiID, textinput.ZwpTextInputV3OpSetTextChangeCause, func() {
			_ = w.WriteUint32(uint32(textinput.ZwpTextInputV3ChangeCauseOther))
		}),
		msg(tiID, textinput.ZwpTextInputV3OpSetContentType, func() {
			_ = w.WriteUint32(uint32(textinput.ZwpTextInputV3ContentHintCompletion))
			_ = w.WriteUint32(uint32(textinput.ZwpTextInputV3ContentPurposeTerminal))
		}),
		msg(tiID, textinput.ZwpTextInputV3OpSetCursorRectangle, func() {
			_ = w.WriteInt32(10)
			_ = w.WriteInt32(20)
			_ = w.WriteInt32(8)
			_ = w.WriteInt32(16)
		}),
		msg(tiID, textinput.ZwpTextInputV3OpCommit, func() {}),
	)

	// Wait for the commit to be processed by polling ActiveTextInputState.
	var state TextInputState
	var active bool
	for i := 0; i < 100; i++ {
		if state, active = c.ActiveTextInputState(); active && state.Serial == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !active || !state.Enabled || state.Serial != 1 {
		t.Fatalf("expected enabled TextInputState with Serial=1, got active=%v state=%+v", active, state)
	}
	if state.CursorRect != [4]int32{10, 20, 8, 16} {
		t.Fatalf("CursorRect = %v, want [10 20 8 16]", state.CursorRect)
	}
	if state.ContentPurpose != uint32(textinput.ZwpTextInputV3ContentPurposeTerminal) {
		t.Fatalf("ContentPurpose = %d, want terminal (%d)", state.ContentPurpose, textinput.ZwpTextInputV3ContentPurposeTerminal)
	}

	// 3. Server/IME sends preedit + commit string; client receives preedit_string, commit_string, and done(serial=1).
	c.SendTextInputPreedit("nihao", 0, 5)
	c.SendTextInputCommit("你好")

	ev1 := <-events
	if ev1.op != textinput.ZwpTextInputV3EventOpPreeditString {
		t.Fatalf("expected preedit_string (op 2), got op %d", ev1.op)
	}
	ev2 := <-events
	if ev2.op != textinput.ZwpTextInputV3EventOpDone {
		t.Fatalf("expected done (op 5) after preedit, got op %d", ev2.op)
	}
	if gotSerial := le.Uint32(ev2.payload[:4]); gotSerial != 1 {
		t.Fatalf("done serial = %d, want 1", gotSerial)
	}

	ev3 := <-events
	if ev3.op != textinput.ZwpTextInputV3EventOpCommitString {
		t.Fatalf("expected commit_string (op 3), got op %d", ev3.op)
	}
	ev4 := <-events
	if ev4.op != textinput.ZwpTextInputV3EventOpDone {
		t.Fatalf("expected done (op 5) after commit_string, got op %d", ev4.op)
	}
	if gotSerial := le.Uint32(ev4.payload[:4]); gotSerial != 1 {
		t.Fatalf("done serial = %d, want 1", gotSerial)
	}

	// 4. Unfocus surface -> must emit zwp_text_input_v3.leave(surfID) and disable text input.
	c.SendKeyboardLeave(surfID)
	select {
	case ev := <-events:
		if ev.op != textinput.ZwpTextInputV3EventOpLeave {
			t.Fatalf("expected leave (op 1), got op %d", ev.op)
		}
		if gotSurf := le.Uint32(ev.payload[:4]); gotSurf != surfID {
			t.Fatalf("leave surface = %d, want %d", gotSurf, surfID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for zwp_text_input_v3.leave")
	}
	if _, active = c.ActiveTextInputState(); active {
		t.Fatal("expected text input to be inactive after keyboard leave")
	}

	// 5. Re-enter, enable+commit, then destroy the wl_surface directly -> text input must deactivate.
	c.SendKeyboardEnter(surfID, nil)
	<-events // enter
	send(
		msg(tiID, textinput.ZwpTextInputV3OpEnable, func() {}),
		msg(tiID, textinput.ZwpTextInputV3OpCommit, func() {}),
	)
	for i := 0; i < 100; i++ {
		if state, active = c.ActiveTextInputState(); active && state.Serial == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !active || state.Serial != 2 {
		t.Fatalf("expected active text input with Serial=2 before surface destroy, got active=%v state=%+v", active, state)
	}
	send(msg(surfID, 0, func() {})) // wl_surface.destroy
	for i := 0; i < 100; i++ {
		if _, active = c.ActiveTextInputState(); !active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if active {
		t.Fatal("expected text input to deactivate when focused wl_surface is destroyed")
	}
}
