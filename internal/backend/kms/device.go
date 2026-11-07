package kms

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ioctl request numbers, computed from the mirrored struct sizes so encoding
// stays consistent with the ABI.
var (
	iocGetCap       = iowr(reqGetCap, unsafe.Sizeof(GetCap{}))
	iocSetClientCap = iow(reqSetClientCap, unsafe.Sizeof(SetClientCap{}))
	iocSetMaster    = ioctlNR(reqSetMaster)
	iocDropMaster   = ioctlNR(reqDropMaster)
	iocGetResources = iowr(reqGetResources, unsafe.Sizeof(CardRes{}))
	iocGetCRTC      = iowr(reqGetCRTC, unsafe.Sizeof(CrtcSet{}))
	iocSetCRTC      = iowr(reqSetCRTC, unsafe.Sizeof(CrtcSet{}))
	iocGetEncoder   = iowr(reqGetEncoder, unsafe.Sizeof(GetEncoder{}))
	iocGetConnector = iowr(reqGetConnector, unsafe.Sizeof(GetConnector{}))
	iocAddFB        = iowr(reqAddFB, unsafe.Sizeof(FbCmd{}))
	iocRMFB         = iowr(reqRMFB, unsafe.Sizeof(uint32(0)))
	iocPageFlip     = iowr(reqPageFlip, unsafe.Sizeof(PageFlip{}))
	iocCreateDumb   = iowr(reqCreateDumb, unsafe.Sizeof(CreateDumb{}))
	iocDestroyDumb  = iowr(reqDestroyDumb, unsafe.Sizeof(DestroyDumb{}))
	iocMapDumb      = iowr(reqMapDumb, unsafe.Sizeof(MapDumb{}))
	iocAddFB2       = iowr(reqAddFB2, unsafe.Sizeof(FBCmd2{}))
	iocObjGetProps  = iowr(reqObjGetProps, unsafe.Sizeof(ObjProps{}))
	iocObjSetProp   = iowr(reqObjSetProp, unsafe.Sizeof(ObjSetProp{}))
	iocAtomic       = iowr(reqAtomic, unsafe.Sizeof(Atomic{}))
	iocPrimeToFD    = iowr(reqPrimeToFD, unsafe.Sizeof(PrimeHandle{}))
	iocGetPlaneRes  = iowr(reqGetPlaneRes, unsafe.Sizeof(GetPlaneRes{}))
	iocGetPlane     = iowr(reqGetPlane, unsafe.Sizeof(GetPlane{}))
	iocGetProperty  = iowr(reqGetProp, unsafe.Sizeof(GetProperty{}))
	iocCreateBlob   = iowr(reqCreateBlob, unsafe.Sizeof(CreateBlob{}))
	iocDestroyBlob  = iowr(reqDestroyBlob, unsafe.Sizeof(DestroyBlob{}))
	iocCursor2      = iowr(reqCursor2, unsafe.Sizeof(Cursor2{}))
)

// Property names required for atomic commit, resolved per object at scan time.
const (
	propCRTCID = "CRTC_ID"
	propFBID   = "FB_ID"
	propActive = "ACTIVE"
	propModeID = "MODE_ID"
	propDPMS   = "DPMS"
)

// Mode is a display mode with layout helpers.
type Mode = ModeInfo

// Resolution returns the pixel dimensions of the mode.
func (m *Mode) Resolution() (uint16, uint16) {
	return m.HDisplay, m.VDisplay
}

// ModeName returns the human-readable mode name (e.g. "1920x1080").
func (m *Mode) ModeName() string {
	end := len(m.Name)
	for i, c := range m.Name {
		if c == 0 {
			end = i
			break
		}
	}
	return string(m.Name[:end])
}

// PreferredMode returns the mode flagged DRM_MODE_TYPE_PREFERRED, or nil.
func (m *Mode) Preferred() bool {
	return m.Type&ModeTypePreferred != 0
}

// Connector is an enumerated display connector with its mode list.
type Connector struct {
	ID       uint32
	Type     uint32
	TypeID   uint32
	Status   uint32
	Encoder  uint32
	CRTC     uint32 // bound CRTC object id (0 if none)
	MMWidth  uint32
	MMHeight uint32
	Modes    []Mode
	Encoders []uint32

	// props caches the connector's atomic property ids by name.
	props map[string]uint32
}

// IsConnected reports whether a display sink is attached.
func (c *Connector) IsConnected() bool { return c.Status == StatusConnected }

// DisplayName is the UAPI display name, e.g. "DP-1".
func (c *Connector) DisplayName() string {
	return fmt.Sprintf("%s-%d", ConnectorTypeName(c.Type), c.TypeID)
}

// PreferredMode returns the preferred mode, falling back to the highest
// resolution / refresh mode.
func (c *Connector) PreferredMode() *Mode {
	if len(c.Modes) == 0 {
		return nil
	}
	for i := range c.Modes {
		if c.Modes[i].Type&ModeTypePreferred != 0 {
			return &c.Modes[i]
		}
	}
	best := 0
	for i := 1; i < len(c.Modes); i++ {
		a, b := &c.Modes[i], &c.Modes[best]
		areaA := uint32(a.HDisplay) * uint32(a.VDisplay)
		areaB := uint32(b.HDisplay) * uint32(b.VDisplay)
		if areaA > areaB ||
			(areaA == areaB && a.VRefresh > b.VRefresh) {
			best = i
		}
	}
	return &c.Modes[best]
}

// CRTC is a scanout engine instance.
type CRTC struct {
	ID        uint32
	Index     uint32
	FBID      uint32
	BufferID  uint32
	X, Y      uint32
	GammaSize uint32
	ModeValid uint32
	Mode      ModeInfo

	// props caches the CRTC's atomic property ids by name.
	props map[string]uint32
}

// Card is an open DRM card device with KMS capabilities.
type Card struct {
	fd   int
	path string
	dev  *os.File

	master bool

	Caps struct {
		DumbBuffer   bool
		CursorWidth  uint32
		CursorHeight uint32
	}

	Connectors []*Connector
	CRTCs      []*CRTC
	Encoders   []uint32
	Planes     []*Plane

	MinWidth, MaxWidth, MinHeight, MaxHeight uint32

	// atomicReq is the pre-allocated scratch request buffer reused across
	// frames so CommitAtomic never allocates on the hot path.
	atomicReq AtomicReq
}

// OpenCard opens the first usable DRM card path.
func OpenCard() (*Card, error) {
	paths := []string{"/dev/dri/card0", "/dev/dri/card1", "/dev/dri/card2"}
	for _, p := range paths {
		c, err := OpenCardPath(p)
		if err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("kms: no accessible DRM card device")
}

// OpenCardPath opens a specific DRM card device.
func OpenCardPath(path string) (*Card, error) {
	// O_NONBLOCK: DRM event reads return EAGAIN when the queue is empty
	// instead of blocking the compositor frame loop.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("kms: open %s: %w", path, err)
	}

	c := &Card{
		fd:   fd,
		path: path,
		dev:  os.NewFile(uintptr(fd), path),
	}

	if err := c.initCaps(); err != nil {
		unix.Close(fd)
		return nil, err
	}

	if err := c.enumerate(); err != nil {
		unix.Close(fd)
		return nil, err
	}

	return c, nil
}

// FD returns the raw file descriptor for poll/read integration.
func (c *Card) FD() int { return c.fd }

// Path returns the device path this card was opened from.
func (c *Card) Path() string { return c.path }

// IsMaster reports whether this card file is the current DRM master.
func (c *Card) IsMaster() bool { return c.master }

func (c *Card) initCaps() error {
	// Dumb buffer support.
	cap := GetCap{Capability: CapDumbBuffer}
	if err := ioctl(c.fd, iocGetCap, unsafe.Pointer(&cap)); err != nil {
		return fmt.Errorf("kms: GET_CAP(DUMB_BUFFER) on %s: %w", c.path, err)
	}
	c.Caps.DumbBuffer = cap.Value != 0

	cw := GetCap{Capability: CapCursorWidth}
	if err := ioctl(c.fd, iocGetCap, unsafe.Pointer(&cw)); err == nil && cw.Value > 0 {
		c.Caps.CursorWidth = uint32(cw.Value)
	} else {
		c.Caps.CursorWidth = 64
	}
	ch := GetCap{Capability: CapCursorHeight}
	if err := ioctl(c.fd, iocGetCap, unsafe.Pointer(&ch)); err == nil && ch.Value > 0 {
		c.Caps.CursorHeight = uint32(ch.Value)
	} else {
		c.Caps.CursorHeight = 64
	}
	return nil
}

// SetMaster acquires DRM master (required for modesetting ioctls).
func (c *Card) SetMaster() error {
	if err := ioctl(c.fd, iocSetMaster, nil); err != nil {
		return fmt.Errorf("kms: SET_MASTER on %s: %w", c.path, err)
	}
	c.master = true
	return nil
}

// DropMaster releases DRM master.
func (c *Card) DropMaster() error {
	if err := ioctl(c.fd, iocDropMaster, nil); err != nil {
		return fmt.Errorf("kms: DROP_MASTER on %s: %w", c.path, err)
	}
	c.master = false
	return nil
}

// SetUniversalPlanes enables the universal planes client capability.
func (c *Card) SetUniversalPlanes() error {
	sc := SetClientCap{Capability: ClientCapUniversalPlanes, Value: 1}
	if err := ioctl(c.fd, iocSetClientCap, unsafe.Pointer(&sc)); err != nil {
		return fmt.Errorf("kms: SET_CLIENT_CAP(UNIVERSAL_PLANES) on %s: %w", c.path, err)
	}
	return nil
}

// SetAtomicCap enables the atomic modesetting client capability.
func (c *Card) SetAtomicCap() error {
	sc := SetClientCap{Capability: ClientCapAtomic, Value: 1}
	if err := ioctl(c.fd, iocSetClientCap, unsafe.Pointer(&sc)); err != nil {
		return fmt.Errorf("kms: SET_CLIENT_CAP(ATOMIC) on %s: %w", c.path, err)
	}
	return nil
}

// ioctl issues a DRM ioctl and maps errno to error.
func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		if errno == syscall.EAGAIN {
			// EAGAIN is used by DRM to indicate a transient retryable error.
			return syscall.EAGAIN
		}
		return errno
	}
	return nil
}

func (c *Card) ioctl(req uintptr, arg unsafe.Pointer) error {
	return ioctl(c.fd, req, arg)
}

// enumerate walks resources: connectors (with modes), CRTCs, encoders.
func (c *Card) enumerate() error {
	// GETRESOURCES with null pointers returns counts.
	res := CardRes{}
	if err := c.ioctl(iocGetResources, unsafe.Pointer(&res)); err != nil {
		return fmt.Errorf("kms: GETRESOURCES on %s: %w", c.path, err)
	}

	crtcIDs := make([]uint32, res.CountCrtcs)
	connectorIDs := make([]uint32, res.CountConnectors)
	encoderIDs := make([]uint32, res.CountEncoders)

	res2 := CardRes{
		CountCrtcs:      res.CountCrtcs,
		CountConnectors: res.CountConnectors,
		CountEncoders:   res.CountEncoders,
	}
	if res.CountCrtcs > 0 {
		res2.CrtcIDPtr = uint64(uintptr(unsafe.Pointer(&crtcIDs[0])))
	}
	if res.CountConnectors > 0 {
		res2.ConnectorIDPtr = uint64(uintptr(unsafe.Pointer(&connectorIDs[0])))
	}
	if res.CountEncoders > 0 {
		res2.EncoderIDPtr = uint64(uintptr(unsafe.Pointer(&encoderIDs[0])))
	}
	if err := c.ioctl(iocGetResources, unsafe.Pointer(&res2)); err != nil {
		return fmt.Errorf("kms: GETRESOURCES(ids) on %s: %w", c.path, err)
	}

	c.MinWidth, c.MaxWidth = res2.MinWidth, res2.MaxWidth
	c.MinHeight, c.MaxHeight = res2.MinHeight, res2.MaxHeight

	c.CRTCs = make([]*CRTC, res2.CountCrtcs)
	for i, id := range crtcIDs {
		c.CRTCs[i] = &CRTC{ID: id, Index: uint32(i)}
		if err := c.getCRTC(c.CRTCs[i]); err != nil {
			return fmt.Errorf("kms: GETCRTC on %s: %w", c.path, err)
		}
	}
	c.Encoders = encoderIDs

	for _, id := range connectorIDs {
		conn, err := c.getConnector(id)
		if err != nil {
			return fmt.Errorf("kms: GETCONNECTOR on %s: %w", c.path, err)
		}
		c.Connectors = append(c.Connectors, conn)
	}

	// Stable order: connectors by ID.
	sort.Slice(c.Connectors, func(i, j int) bool {
		return c.Connectors[i].ID < c.Connectors[j].ID
	})

	return nil
}

func (c *Card) getCRTC(crtc *CRTC) error {
	cs := CrtcSet{CrtcID: crtc.ID}
	if err := c.ioctl(iocGetCRTC, unsafe.Pointer(&cs)); err != nil {
		return err
	}
	crtc.FBID = cs.FbID
	crtc.BufferID = cs.CrtcID
	crtc.X, crtc.Y = cs.X, cs.Y
	crtc.GammaSize = cs.GammaSize
	crtc.ModeValid = cs.ModeValid
	crtc.Mode = cs.Mode
	return nil
}

func (c *Card) getConnector(id uint32) (*Connector, error) {
	// Pass 1: query counts. The kernel requires count_modes == 1 with a
	// one-element scratch mode buffer for the counting pass.
	scratch := Mode{}
	gc := GetConnector{
		ConnectorID: id,
		ModesPtr:    uint64(uintptr(unsafe.Pointer(&scratch))),
		CountModes:  1,
	}
	if err := c.ioctl(iocGetConnector, unsafe.Pointer(&gc)); err != nil {
		return nil, err
	}

	modes := make([]Mode, gc.CountModes)
	encoders := make([]uint32, gc.CountEncoders)

	// Pass 2: fill in.
	gc2 := GetConnector{
		ConnectorID: id,
		CountModes:  gc.CountModes,
	}
	if gc.CountModes > 0 {
		gc2.ModesPtr = uint64(uintptr(unsafe.Pointer(&modes[0])))
	}
	if gc.CountEncoders > 0 {
		gc2.EncodersPtr = uint64(uintptr(unsafe.Pointer(&encoders[0])))
	}
	if err := c.ioctl(iocGetConnector, unsafe.Pointer(&gc2)); err != nil {
		return nil, err
	}

	conn := &Connector{
		ID:       id,
		Type:     gc2.Type,
		TypeID:   gc2.TypeID,
		Status:   gc2.Connection,
		Encoder:  gc2.EncoderID,
		MMWidth:  gc2.MMWidth,
		MMHeight: gc2.MMHeight,
		Modes:    modes,
		Encoders: encoders,
	}

	// Bound CRTC (resolved through the current encoder).
	if conn.Encoder != 0 {
		enc := GetEncoder{EncoderID: conn.Encoder}
		if err := c.ioctl(iocGetEncoder, unsafe.Pointer(&enc)); err == nil && enc.CrtcID != 0 {
			conn.CRTC = enc.CrtcID
		}
	}

	return conn, nil
}

// FindConnectorByID returns the connector with the given object id.
func (c *Card) FindConnectorByID(id uint32) *Connector {
	for _, conn := range c.Connectors {
		if conn.ID == id {
			return conn
		}
	}
	return nil
}

// FindCRTCByID returns the CRTC with the given object id.
func (c *Card) FindCRTCByID(id uint32) *CRTC {
	for _, crtc := range c.CRTCs {
		if crtc.ID == id {
			return crtc
		}
	}
	return nil
}

// mu guards scanout lifetime against concurrent flips.
var mu sync.Mutex

// ReadFlipEvents drains flip-complete events into dst, returning how many
// were matched. Non-blocking: the card fd is O_NONBLOCK, so an empty queue
// returns (0, nil) immediately. One read may carry multiple packed events;
// the drm_event length header is honored when walking the buffer.
func (c *Card) ReadFlipEvents(dst []EventVBlank) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	var buf [256]byte
	n := 0
	m, err := unix.Read(c.fd, buf[:])
	if err != nil {
		switch {
		case err == syscall.EAGAIN, err == syscall.EWOULDBLOCK, err == unix.EINTR:
			return 0, nil
		default:
			return 0, err
		}
	}

	off := 0
	for off+8 <= m {
		eType := *(*uint32)(unsafe.Pointer(&buf[off]))
		eLen := *(*uint32)(unsafe.Pointer(&buf[off+4]))
		if eLen < 8 || off+int(eLen) > m {
			return n, fmt.Errorf("kms: malformed DRM event header (type=%d len=%d at %d/%d)", eType, eLen, off, m)
		}
		if eType == EventTypeFlipComplete && eLen >= uint32(unsafe.Sizeof(EventVBlank{})) {
			dst[n] = *(*EventVBlank)(unsafe.Pointer(&buf[off]))
			n++
			if n == len(dst) {
				break
			}
		}
		off += int(eLen)
	}
	return n, nil
}

// Scanout is a KMS scanout framebuffer backed by a dumb buffer. The same
// dumb buffer can be handed to Vulkan (PrimeExport + ImportDmaBuf) so the
// compositor renders straight into scanout memory: no CPU round trip.
type Scanout struct {
	mu     sync.Mutex
	Card   *Card
	FBID   uint32
	Handle uint32
	Pitch  uint32
	Size   uint64
	Width  uint32
	Height uint32
	Data   []byte // mmap'd CPU-visible pixels
}

// CreateScanout allocates a dumb buffer and registers it as a KMS framebuffer.
func (c *Card) CreateScanout(width, height uint32) (*Scanout, error) {
	cd := CreateDumb{Width: width, Height: height, Bpp: 32}
	if err := c.ioctl(iocCreateDumb, unsafe.Pointer(&cd)); err != nil {
		return nil, fmt.Errorf("kms: CREATE_DUMB %dx%d: %w", width, height, err)
	}

	fb := FbCmd{
		Width:  width,
		Height: height,
		Pitch:  cd.Pitch,
		Bpp:    32,
		Depth:  24,
		Handle: cd.Handle,
	}
	if err := c.ioctl(iocAddFB, unsafe.Pointer(&fb)); err != nil {
		// Roll back the dumb buffer.
		dd := DestroyDumb{Handle: cd.Handle}
		_ = c.ioctl(iocDestroyDumb, unsafe.Pointer(&dd))
		return nil, fmt.Errorf("kms: ADDFB %dx%d: %w", width, height, err)
	}

	md := MapDumb{Handle: cd.Handle}
	if err := c.ioctl(iocMapDumb, unsafe.Pointer(&md)); err != nil {
		_ = c.destroyDumb(fb.FbID, cd.Handle)
		return nil, fmt.Errorf("kms: MAP_DUMB: %w", err)
	}

	data, err := unix.Mmap(c.fd, int64(md.Offset), int(cd.Size),
		unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = c.destroyDumb(fb.FbID, cd.Handle)
		return nil, fmt.Errorf("kms: mmap dumb buffer: %w", err)
	}

	return &Scanout{
		Card:   c,
		FBID:   fb.FbID,
		Handle: cd.Handle,
		Pitch:  cd.Pitch,
		Size:   cd.Size,
		Width:  width,
		Height: height,
		Data:   data,
	}, nil
}

// PrimeHandlenis the PRIME_HANDLE_TO_FD payload (handle in, fd out).
type PrimeHandle struct {
	Handle uint32
	Flags  uint32
	FD     int32
}

// PrimeExport exports a dumb-buffer GEM handle as a dma-buf fd (O_RDWR).
// The fd is handed to Vulkan's ImportDmaBuf; closing the scanout closes it.
func (c *Card) PrimeExport(handle uint32) (int, error) {
	pr := PrimeHandle{Handle: handle, Flags: uint32(unix.O_RDWR | unix.O_CLOEXEC), FD: -1}
	if err := c.ioctl(iocPrimeToFD, unsafe.Pointer(&pr)); err != nil {
		return -1, fmt.Errorf("kms: PRIME export handle %d: %w", handle, err)
	}
	return int(pr.FD), nil
}

func (c *Card) destroyDumb(fbID, handle uint32) error {
	if fbID != 0 {
		fb := FbCmd{FbID: fbID}
		if err := c.ioctl(iocRMFB, unsafe.Pointer(&fb.FbID)); err != nil {
			return fmt.Errorf("kms: RMFB %d: %w", fbID, err)
		}
	}
	if handle != 0 {
		dd := DestroyDumb{Handle: handle}
		if err := c.ioctl(iocDestroyDumb, unsafe.Pointer(&dd)); err != nil {
			return fmt.Errorf("kms: DESTROY_DUMB %d: %w", handle, err)
		}
	}
	return nil
}

// Close releases the dumb buffer, framebuffer, and CPU mapping.
func (s *Scanout) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Data != nil {
		if err := unix.Munmap(s.Data); err != nil {
			return fmt.Errorf("kms: munmap: %w", err)
		}
		s.Data = nil
	}
	if s.Card != nil {
		if err := s.Card.destroyDumb(s.FBID, s.Handle); err != nil {
			return err
		}
		s.Card = nil
		s.FBID = 0
		s.Handle = 0
	}
	return nil
}

// Fill clears the scanout to a BGRA color (used for diagnostics and tests).
func (s *Scanout) Fill(r, g, b uint8) {
	row := s.Data
	for y := uint32(0); y < s.Height; y++ {
		for x := uint32(0); x < s.Width; x++ {
			o := int(x) * 4
			row[o+0] = b
			row[o+1] = g
			row[o+2] = r
			row[o+3] = 0xff
		}
		row = row[int(s.Pitch):]
	}
}

// WriteCursorARGB copies an ARGB8888 cursor image of size (srcW x srcH) into
// the top-left of the cursor dumb buffer and zeroes all remaining pixels to
// transparent (0x00000000). Zero heap allocations.
func (s *Scanout) WriteCursorARGB(src []byte, srcW, srcH, srcStride int32) {
	if s == nil || len(s.Data) == 0 {
		return
	}
	clear(s.Data)
	if len(src) == 0 || srcW <= 0 || srcH <= 0 || srcStride <= 0 {
		return
	}
	copyW := int(srcW)
	if copyW > int(s.Width) {
		copyW = int(s.Width)
	}
	copyH := int(srcH)
	if copyH > int(s.Height) {
		copyH = int(s.Height)
	}
	rowBytes := copyW * 4
	dstPitch := int(s.Pitch)
	for y := 0; y < copyH; y++ {
		srcOff := y * int(srcStride)
		dstOff := y * dstPitch
		if srcOff+rowBytes > len(src) || dstOff+rowBytes > len(s.Data) {
			break
		}
		copy(s.Data[dstOff:dstOff+rowBytes], src[srcOff:srcOff+rowBytes])
	}
}

// CreateCursorBuffer allocates a hardware-cursor-sized dumb buffer using the
// card's reported DRM_CAP_CURSOR_WIDTH x DRM_CAP_CURSOR_HEIGHT (default 64x64).
func (c *Card) CreateCursorBuffer() (*Scanout, error) {
	w, h := c.Caps.CursorWidth, c.Caps.CursorHeight
	if w == 0 {
		w = 64
	}
	if h == 0 {
		h = 64
	}
	return c.CreateScanout(w, h)
}

// SetCursorImage uploads the given GEM handle and hotspot to the CRTC's
// hardware cursor plane via DRM_IOCTL_MODE_CURSOR2 (DRM_MODE_CURSOR_BO).
func (c *Card) SetCursorImage(crtcID, handle, width, height uint32, hotX, hotY int32) error {
	cur := Cursor2{
		Flags:  CursorFlagBO,
		CrtcID: crtcID,
		Width:  width,
		Height: height,
		Handle: handle,
		HotX:   hotX,
		HotY:   hotY,
	}
	return c.ioctl(iocCursor2, unsafe.Pointer(&cur))
}

// MoveCursor updates the hardware cursor plane position on the given CRTC via
// DRM_IOCTL_MODE_CURSOR2 (DRM_MODE_CURSOR_MOVE). Zero heap allocations.
func (c *Card) MoveCursor(crtcID uint32, x, y int32) error {
	cur := Cursor2{
		Flags:  CursorFlagMove,
		CrtcID: crtcID,
		X:      x,
		Y:      y,
	}
	return c.ioctl(iocCursor2, unsafe.Pointer(&cur))
}

// HideCursor disables the hardware cursor plane on the given CRTC.
func (c *Card) HideCursor(crtcID uint32) error {
	cur := Cursor2{
		Flags:  CursorFlagBO,
		CrtcID: crtcID,
		Handle: 0,
	}
	return c.ioctl(iocCursor2, unsafe.Pointer(&cur))
}

// SetCRTC performs legacy modesetting: binds the CRTC to a connector at a
// given framebuffer and mode. Requires DRM master.
func (c *Card) SetCRTC(crtcID, fbID, connectorID uint32, mode *ModeInfo) error {
	connectors := [1]uint32{connectorID}
	cs := CrtcSet{
		SetConnectorsPtr: uint64(uintptr(unsafe.Pointer(&connectors[0]))),
		CountConnectors:  1,
		CrtcID:           crtcID,
		FbID:             fbID,
		ModeValid:        1,
	}
	if mode != nil {
		cs.Mode = *mode
	}
	return c.ioctl(iocSetCRTC, unsafe.Pointer(&cs))
}

// PageFlip requests a vblank-synchronized flip of the CRTC to the given FB.
// Returns the kernel-assigned user_data used to match the flip-complete event.
func (c *Card) PageFlip(crtcID, fbID uint32, userData uint64) error {
	pf := PageFlip{
		CrtcID:   crtcID,
		FbID:     fbID,
		Flags:    PageFlipEvent,
		UserData: userData,
	}
	return c.ioctl(iocPageFlip, unsafe.Pointer(&pf))
}

// Close releases the DRM card.
func (c *Card) Close() error {
	if c.master {
		_ = c.DropMaster()
	}
	if c.dev != nil {
		err := c.dev.Close()
		c.dev = nil
		c.fd = -1
		return err
	}
	return nil
}

// PollFD returns a poll descriptor for event-loop integration.
func (c *Card) PollFD() unix.PollFd {
	return unix.PollFd{Fd: int32(c.fd), Events: unix.POLLIN | unix.POLLERR}
}
