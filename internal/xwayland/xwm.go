package xwayland

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

// X11 core protocol request opcodes (<X11/Xproto.h>).
const (
	ReqChangeWindowAttributes byte = 2
	ReqMapWindow              byte = 8
	ReqConfigureWindow        byte = 12
	ReqInternAtom             byte = 16
	ReqGetProperty            byte = 20
	ReqSendEvent              byte = 25
	ReqSetInputFocus          byte = 42
)

// X11 core protocol event codes (<X11/X.h>).
const (
	EventCreateNotify     byte = 16
	EventDestroyNotify    byte = 17
	EventUnmapNotify      byte = 18
	EventMapNotify        byte = 19
	EventMapRequest       byte = 20
	EventConfigureNotify  byte = 22
	EventConfigureRequest byte = 23
	EventPropertyNotify   byte = 28
	EventClientMessage    byte = 33
)

// X11 event masks & configure masks.
const (
	EventMaskSubstructureNotify   uint32 = 1 << 19
	EventMaskSubstructureRedirect uint32 = 1 << 20
	EventMaskPropertyChange       uint32 = 1 << 22

	CWEventMask uint32 = 1 << 11

	ConfigWindowX         uint16 = 1 << 0
	ConfigWindowY         uint16 = 1 << 1
	ConfigWindowWidth     uint16 = 1 << 2
	ConfigWindowHeight    uint16 = 1 << 3
	ConfigWindowBorder    uint16 = 1 << 4
	ConfigWindowSibling   uint16 = 1 << 5
	ConfigWindowStackMode uint16 = 1 << 6
	InputFocusPointerRoot byte   = 1
	CurrentTime           uint32 = 0
)

// XWindow is a flat value record representing an X11 window managed by XWM.
type XWindow struct {
	XID              uint32
	WlSurfaceID      uint32
	WlSurfaceSerial  uint64
	X, Y             int32
	Width, Height    int32
	OverrideRedirect bool
	Mapped           bool
	NotifiedMapped   bool
	Active           bool
	Title            string
}

// XWMCallbacks hooks XWM window lifecycle into the compositor actor.
type XWMCallbacks struct {
	OnWindowMapped   func(win XWindow)
	OnWindowUnmapped func(xid uint32)
}

// XWM is a pure-Go, zero-allocation X11 Window Manager for XWayland.
// Windows are stored in a flat contiguous slice (DOD) so event decoding and
// state updates allocate 0 bytes on the hot path.
type XWM struct {
	conn net.Conn
	cb   XWMCallbacks

	mu      sync.Mutex
	rootID  uint32
	windows []XWindow

	atomWlSurfaceID     uint32
	atomWlSurfaceSerial uint32
	atomWMProtocols     uint32
	atomWMDeleteWindow  uint32
	atomNetWMName       uint32

	writeBuf [64]byte
}

// NewXWM constructs an XWM bound to an X11 connection.
func NewXWM(conn net.Conn, cb XWMCallbacks) *XWM {
	return &XWM{
		conn:    conn,
		cb:      cb,
		windows: make([]XWindow, 0, 32),
	}
}

// Handshake performs the X11 connection setup, selects SubstructureRedirect on
// the root window, and interns the core XWayland/ICCCM atoms.
func (x *XWM) Handshake() error {
	// 1. Send 12-byte little-endian X11 connection setup request (protocol 11.0).
	var req [12]byte
	req[0] = 'l' // little-endian
	binary.LittleEndian.PutUint16(req[2:4], 11)
	binary.LittleEndian.PutUint16(req[4:6], 0)
	if _, err := x.conn.Write(req[:]); err != nil {
		return fmt.Errorf("xwm: write setup: %w", err)
	}

	// 2. Read 8-byte setup response header + payload.
	var hdr [8]byte
	if _, err := io.ReadFull(x.conn, hdr[:]); err != nil {
		return fmt.Errorf("xwm: read setup header: %w", err)
	}
	if hdr[0] != 1 {
		return fmt.Errorf("xwm: setup rejected (status=%d)", hdr[0])
	}
	addWords := int(binary.LittleEndian.Uint16(hdr[6:8]))
	body := make([]byte, addWords*4)
	if _, err := io.ReadFull(x.conn, body); err != nil {
		return fmt.Errorf("xwm: read setup body: %w", err)
	}
	if len(body) >= 36 {
		vendorLen := int(binary.LittleEndian.Uint16(body[16:18]))
		numFormats := int(body[21])
		vendorPad := (vendorLen + 3) & ^3
		screenOff := 32 + vendorPad + numFormats*8
		if screenOff+4 <= len(body) {
			x.rootID = binary.LittleEndian.Uint32(body[screenOff : screenOff+4])
		}
	}

	// 3. Intern core atoms + select SubstructureRedirectMask on the root window.
	atoms := []string{
		"WL_SURFACE_ID",
		"WL_SURFACE_SERIAL",
		"WM_PROTOCOLS",
		"WM_DELETE_WINDOW",
		"_NET_WM_NAME",
	}
	var batch []byte
	for _, name := range atoms {
		batch = appendInternAtom(batch, name)
	}
	// ChangeWindowAttributes on rootID: CWEventMask = SubstructureRedirect | SubstructureNotify | PropertyChange
	var cwa [16]byte
	cwa[0] = ReqChangeWindowAttributes
	binary.LittleEndian.PutUint16(cwa[2:4], 4)
	binary.LittleEndian.PutUint32(cwa[4:8], x.rootID)
	binary.LittleEndian.PutUint32(cwa[8:12], CWEventMask)
	binary.LittleEndian.PutUint32(cwa[12:16], EventMaskSubstructureRedirect|EventMaskSubstructureNotify|EventMaskPropertyChange)
	batch = append(batch, cwa[:]...)

	if _, err := x.conn.Write(batch); err != nil {
		return fmt.Errorf("xwm: write init batch: %w", err)
	}

	// Read the 5 InternAtom replies (32 bytes each).
	var reply [32]byte
	resolved := [5]*uint32{
		&x.atomWlSurfaceID,
		&x.atomWlSurfaceSerial,
		&x.atomWMProtocols,
		&x.atomWMDeleteWindow,
		&x.atomNetWMName,
	}
	for i := 0; i < len(resolved); i++ {
		if _, err := io.ReadFull(x.conn, reply[:]); err != nil {
			return fmt.Errorf("xwm: read atom reply %d: %w", i, err)
		}
		if reply[0] == 1 {
			*resolved[i] = binary.LittleEndian.Uint32(reply[8:12])
		}
	}
	return nil
}

func appendInternAtom(dst []byte, name string) []byte {
	n := len(name)
	pad := (4 - (n & 3)) & 3
	words := uint16((8 + n + pad) / 4)
	var hdr [8]byte
	hdr[0] = ReqInternAtom
	hdr[1] = 0 // only-if-exists = false
	binary.LittleEndian.PutUint16(hdr[2:4], words)
	binary.LittleEndian.PutUint16(hdr[4:6], uint16(n))
	dst = append(dst, hdr[:]...)
	dst = append(dst, name...)
	for i := 0; i < pad; i++ {
		dst = append(dst, 0)
	}
	return dst
}

// Run reads 32-byte X11 packets from the XWM connection and dispatches events
// until the connection closes.
func (x *XWM) Run() error {
	var pkt [32]byte
	for {
		if _, err := io.ReadFull(x.conn, pkt[:]); err != nil {
			return err
		}
		if (pkt[0] & 0x7f) == 1 {
			// X11 Reply packet: drain any trailing 4-byte words to keep the
			// packet stream aligned.
			addWords := binary.LittleEndian.Uint32(pkt[4:8])
			if addWords > 0 && addWords <= 1<<20 {
				_, _ = io.CopyN(io.Discard, x.conn, int64(addWords)*4)
			}
			continue
		}
		x.HandleRawEvent(&pkt)
	}
}

// RegisterWindow inserts or updates a tracked X11 window in the flat table.
func (x *XWM) RegisterWindow(xid uint32, px, py, w, h int32, overrideRedirect bool) {
	x.mu.Lock()
	wPtr := x.getOrCreateLocked(xid)
	wPtr.X = px
	wPtr.Y = py
	wPtr.Width = w
	wPtr.Height = h
	wPtr.OverrideRedirect = overrideRedirect
	wPtr.Active = true
	x.mu.Unlock()
}

func (x *XWM) getOrCreateLocked(xid uint32) *XWindow {
	for i := range x.windows {
		if x.windows[i].Active && x.windows[i].XID == xid {
			return &x.windows[i]
		}
	}
	for i := range x.windows {
		if !x.windows[i].Active {
			x.windows[i] = XWindow{XID: xid, Active: true, Width: 640, Height: 480}
			return &x.windows[i]
		}
	}
	x.windows = append(x.windows, XWindow{XID: xid, Active: true, Width: 640, Height: 480})
	return &x.windows[len(x.windows)-1]
}

func (x *XWM) findLocked(xid uint32) *XWindow {
	for i := range x.windows {
		if x.windows[i].Active && x.windows[i].XID == xid {
			return &x.windows[i]
		}
	}
	return nil
}

// HandleRawEvent processes one 32-byte X11 event packet with 0 heap allocations.
func (x *XWM) HandleRawEvent(pkt *[32]byte) {
	code := pkt[0] & 0x7f
	switch code {
	case EventCreateNotify:
		xid := binary.LittleEndian.Uint32(pkt[8:12])
		px := int32(int16(binary.LittleEndian.Uint16(pkt[12:14])))
		py := int32(int16(binary.LittleEndian.Uint16(pkt[14:16])))
		w := int32(binary.LittleEndian.Uint16(pkt[16:18]))
		h := int32(binary.LittleEndian.Uint16(pkt[18:20]))
		or := pkt[22] != 0
		x.RegisterWindow(xid, px, py, w, h, or)

	case EventConfigureRequest:
		xid := binary.LittleEndian.Uint32(pkt[8:12])
		px := int32(int16(binary.LittleEndian.Uint16(pkt[16:18])))
		py := int32(int16(binary.LittleEndian.Uint16(pkt[18:20])))
		w := int32(binary.LittleEndian.Uint16(pkt[20:22]))
		h := int32(binary.LittleEndian.Uint16(pkt[22:24]))
		x.mu.Lock()
		win := x.getOrCreateLocked(xid)
		if w > 0 {
			win.Width = w
		}
		if h > 0 {
			win.Height = h
		}
		win.X = px
		win.Y = py
		x.mu.Unlock()
		_ = x.ConfigureWindow(xid, px, py, w, h)

	case EventMapRequest:
		xid := binary.LittleEndian.Uint32(pkt[8:12])
		var notifyWin XWindow
		shouldNotify := false
		x.mu.Lock()
		win := x.getOrCreateLocked(xid)
		win.Mapped = true
		// Send X11 MapWindow request (8 bytes).
		x.writeBuf[0] = ReqMapWindow
		x.writeBuf[1] = 0
		binary.LittleEndian.PutUint16(x.writeBuf[2:4], 2)
		binary.LittleEndian.PutUint32(x.writeBuf[4:8], xid)
		_, _ = x.conn.Write(x.writeBuf[:8])
		if (win.WlSurfaceID != 0 || win.WlSurfaceSerial != 0) && !win.NotifiedMapped {
			win.NotifiedMapped = true
			notifyWin = *win
			shouldNotify = true
		}
		x.mu.Unlock()
		if shouldNotify && x.cb.OnWindowMapped != nil {
			x.cb.OnWindowMapped(notifyWin)
		}

	case EventClientMessage:
		xid := binary.LittleEndian.Uint32(pkt[4:8])
		msgType := binary.LittleEndian.Uint32(pkt[8:12])
		var notifyWin XWindow
		shouldNotify := false
		x.mu.Lock()
		win := x.getOrCreateLocked(xid)
		switch msgType {
		case x.atomWlSurfaceID:
			win.WlSurfaceID = binary.LittleEndian.Uint32(pkt[12:16])
		case x.atomWlSurfaceSerial:
			lo := binary.LittleEndian.Uint32(pkt[12:16])
			hi := binary.LittleEndian.Uint32(pkt[16:20])
			win.WlSurfaceSerial = uint64(hi)<<32 | uint64(lo)
		}
		if win.Mapped && (win.WlSurfaceID != 0 || win.WlSurfaceSerial != 0) && !win.NotifiedMapped {
			win.NotifiedMapped = true
			notifyWin = *win
			shouldNotify = true
		}
		x.mu.Unlock()
		if shouldNotify && x.cb.OnWindowMapped != nil {
			x.cb.OnWindowMapped(notifyWin)
		}

	case EventUnmapNotify:
		xid := binary.LittleEndian.Uint32(pkt[8:12])
		wasMapped := false
		x.mu.Lock()
		if win := x.findLocked(xid); win != nil {
			wasMapped = win.Mapped
			win.Mapped = false
			win.NotifiedMapped = false
		}
		x.mu.Unlock()
		if wasMapped && x.cb.OnWindowUnmapped != nil {
			x.cb.OnWindowUnmapped(xid)
		}

	case EventDestroyNotify:
		xid := binary.LittleEndian.Uint32(pkt[8:12])
		wasActive := false
		x.mu.Lock()
		if win := x.findLocked(xid); win != nil {
			wasActive = win.Mapped
			win.Active = false
			win.Mapped = false
			win.NotifiedMapped = false
		}
		x.mu.Unlock()
		if wasActive && x.cb.OnWindowUnmapped != nil {
			x.cb.OnWindowUnmapped(xid)
		}
	}
}

// ConfigureWindow sends an X11 ConfigureWindow request (x, y, width, height, border=0).
func (x *XWM) ConfigureWindow(xid uint32, px, py, w, h int32) error {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	mask := ConfigWindowX | ConfigWindowY | ConfigWindowWidth | ConfigWindowHeight | ConfigWindowBorder
	x.writeBuf[0] = ReqConfigureWindow
	x.writeBuf[1] = 0
	binary.LittleEndian.PutUint16(x.writeBuf[2:4], 8) // 32 bytes (8 words)
	binary.LittleEndian.PutUint32(x.writeBuf[4:8], xid)
	binary.LittleEndian.PutUint16(x.writeBuf[8:10], mask)
	binary.LittleEndian.PutUint16(x.writeBuf[10:12], 0)
	binary.LittleEndian.PutUint32(x.writeBuf[12:16], uint32(px))
	binary.LittleEndian.PutUint32(x.writeBuf[16:20], uint32(py))
	binary.LittleEndian.PutUint32(x.writeBuf[20:24], uint32(w))
	binary.LittleEndian.PutUint32(x.writeBuf[24:28], uint32(h))
	binary.LittleEndian.PutUint32(x.writeBuf[28:32], 0) // border_width = 0
	_, err := x.conn.Write(x.writeBuf[:32])
	return err
}

// FocusWindow sends an X11 SetInputFocus request to the given XID.
func (x *XWM) FocusWindow(xid uint32) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.writeBuf[0] = ReqSetInputFocus
	x.writeBuf[1] = InputFocusPointerRoot
	binary.LittleEndian.PutUint16(x.writeBuf[2:4], 3) // 12 bytes
	binary.LittleEndian.PutUint32(x.writeBuf[4:8], xid)
	binary.LittleEndian.PutUint32(x.writeBuf[8:12], CurrentTime)
	_, err := x.conn.Write(x.writeBuf[:12])
	return err
}

// CloseWindow sends an ICCCM WM_PROTOCOLS / WM_DELETE_WINDOW ClientMessage to
// gracefully close an X11 window.
func (x *XWM) CloseWindow(xid uint32) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	for i := 0; i < 44; i++ {
		x.writeBuf[i] = 0
	}
	x.writeBuf[0] = ReqSendEvent
	x.writeBuf[1] = 0                                  // propagate = false
	binary.LittleEndian.PutUint16(x.writeBuf[2:4], 11) // 44 bytes
	binary.LittleEndian.PutUint32(x.writeBuf[4:8], xid)
	binary.LittleEndian.PutUint32(x.writeBuf[8:12], 0) // event-mask = NoEventMask

	// 32-byte ClientMessage payload at offset 12:
	x.writeBuf[12] = EventClientMessage
	x.writeBuf[13] = 32 // format = 32
	binary.LittleEndian.PutUint32(x.writeBuf[16:20], xid)
	binary.LittleEndian.PutUint32(x.writeBuf[20:24], x.atomWMProtocols)
	binary.LittleEndian.PutUint32(x.writeBuf[24:28], x.atomWMDeleteWindow)
	binary.LittleEndian.PutUint32(x.writeBuf[28:32], CurrentTime)
	_, err := x.conn.Write(x.writeBuf[:44])
	return err
}
