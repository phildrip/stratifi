package server

import (
	"fmt"
	"sync"

	"stratifi/internal/protocol/capture"
	"stratifi/internal/protocol/wayland"
)

// FrameGrabber supplies compositor framebuffer pixels for capture sessions.
// dst receives tightly packed BGRA pixel data for the (x,y,width,height)
// region of the output — (0,0) for a whole-output source, or the window's
// rectangle for a toplevel source.
type FrameGrabber interface {
	GrabFramebuffer(dst []byte, x, y, width, height, stride int32) error
}

// captureSource tracks an ext_image_capture_source_v1 object and the output
// it resolves to.
type captureSource struct {
	ID          uint32
	OutputID    uint32 // wl_output object id backing this source
	ToplevelKey uint32 // non-zero: capture this window (strategy: region grab)
}

// captureSession is an ext_image_copy_capture_session_v1 bound to a source.
type captureSession struct {
	ID       uint32
	SourceID uint32
	Stopped  bool
	// region of the output this session captures (0,0 for a whole output).
	X, Y int32
}

// captureFrame is an ext_image_copy_capture_frame_v1 in flight.
type captureFrame struct {
	ID       uint32
	Session  uint32
	BufferID uint32
	Damage   []int32
}

// CaptureState holds per-client capture protocol state.
type CaptureState struct {
	sources  map[uint32]*captureSource
	sessions map[uint32]*captureSession
	frames   map[uint32]*captureFrame

	mu sync.Mutex
}

func newCaptureState() *CaptureState {
	return &CaptureState{
		sources:  make(map[uint32]*captureSource),
		sessions: make(map[uint32]*captureSession),
		frames:   make(map[uint32]*captureFrame),
	}
}

// SetFrameGrabber registers the compositor's pixel provider. Nil disables
// capture (sessions will fail with capture.FailedReasonUnknown).
func (s *Server) SetFrameGrabber(g FrameGrabber) {
	s.captureGrabber = g
}

// SetToplevelGeometry registers a reader for a window's on-screen rectangle
// (used to size and locate a toplevel capture source). Absent windows report
// ok=false.
func (s *Server) SetToplevelGeometry(fn func(key uint32) (x, y, w, h int32, ok bool)) {
	s.toplevelGeometry = fn
}

// toplevelRect resolves a window's on-screen rectangle, if known.
func (s *Server) toplevelRect(key uint32) (int32, int32, int32, int32, bool) {
	if s.toplevelGeometry == nil {
		return 0, 0, 0, 0, false
	}
	return s.toplevelGeometry(key)
}

// outputResolution reads the compositor's virtual output geometry — the
// same source wl_output advertisement uses (Server.Output()).
func (s *Server) outputResolution() (uint32, uint32) {
	o := s.Output()
	return o.Width, o.Height
}

// --- ext_output_image_capture_source_manager_v1 ---

func (c *Client) HandleExtOutputImageCaptureSourceManagerV1CreateSource(senderID uint32, id uint32, output uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.registerObject(id, ObjCaptureSource)
	c.capture.sources[id] = &captureSource{ID: id, OutputID: output}
	return nil
}

func (c *Client) HandleExtOutputImageCaptureSourceManagerV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- ext_foreign_toplevel_image_capture_source_manager_v1 ---

func (c *Client) HandleExtForeignToplevelImageCaptureSourceManagerV1CreateSource(senderID uint32, id uint32, toplevelHandle uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Resolve the client's toplevel handle to the compositor's window key.
	key, ok := c.toplevelKeys[toplevelHandle]
	if !ok {
		return fmt.Errorf("capture source for unknown toplevel handle %d", toplevelHandle)
	}
	c.registerObject(id, ObjCaptureSource)
	c.capture.sources[id] = &captureSource{ID: id, ToplevelKey: key}
	return nil
}

func (c *Client) HandleExtForeignToplevelImageCaptureSourceManagerV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- ext_image_capture_source_v1 ---

func (c *Client) HandleExtImageCaptureSourceV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	delete(c.capture.sources, senderID)
	return nil
}

// --- ext_image_copy_capture_manager_v1 ---

func (c *Client) HandleExtImageCopyCaptureManagerV1CreateSession(senderID uint32, id uint32, source uint32, options uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	src, ok := c.capture.sources[source]
	if !ok {
		return fmt.Errorf("capture session for unknown source %d", source)
	}

	sess := &captureSession{ID: id, SourceID: src.ID}
	c.registerObject(id, ObjCaptureSession)
	c.capture.sessions[id] = sess

	w := c.conn.WriterRef()
	sW, sH := c.server.outputResolution()
	if src.ToplevelKey != 0 {
		// A window source: report (and later capture) the window's rectangle.
		if x, y, ww, hh, ok := c.server.toplevelRect(src.ToplevelKey); ok {
			sess.X, sess.Y, sW, sH = x, y, uint32(ww), uint32(hh)
		} else {
			// Window is gone: tear the session down with no source.
			sW, sH = 0, 0
		}
	}
	if err := capture.SendExtImageCopyCaptureSessionV1BufferSize(w, id, sW, sH); err != nil {
		return err
	}
	// Advertise the two shm formats wl_shm already accepts.
	if err := capture.SendExtImageCopyCaptureSessionV1ShmFormat(w, id, uint32(wayland.WlShmFormatArgb8888)); err != nil {
		return err
	}
	if err := capture.SendExtImageCopyCaptureSessionV1ShmFormat(w, id, uint32(wayland.WlShmFormatXrgb8888)); err != nil {
		return err
	}
	return capture.SendExtImageCopyCaptureSessionV1Done(w, id)
}

func (c *Client) HandleExtImageCopyCaptureManagerV1CreatePointerCursorSession(senderID uint32, id uint32, source uint32, pointer uint32) error {
	// Cursor capture sessions layer on the same session; cursor image data
	// follows once the compositor tracks pointer sprites. Defer until the
	// seat gains cursor bitmap state.
	return fmt.Errorf("pointer cursor capture not yet supported (source %d)", source)
}

func (c *Client) HandleExtImageCopyCaptureManagerV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- ext_image_copy_capture_session_v1 ---

func (c *Client) HandleExtImageCopyCaptureSessionV1CreateFrame(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	session, ok := c.capture.sessions[senderID]
	if !ok {
		return fmt.Errorf("create_frame on unknown session %d", senderID)
	}
	if session.Stopped {
		return fmt.Errorf("create_frame on stopped session %d", senderID)
	}

	c.registerObject(id, ObjCaptureFrame)
	c.capture.frames[id] = &captureFrame{ID: id, Session: senderID}
	return nil
}

func (c *Client) HandleExtImageCopyCaptureSessionV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	delete(c.capture.sessions, senderID)
	return nil
}

// --- ext_image_copy_capture_frame_v1 ---

func (c *Client) HandleExtImageCopyCaptureFrameV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	delete(c.capture.frames, senderID)
	return nil
}

func (c *Client) HandleExtImageCopyCaptureFrameV1AttachBuffer(senderID uint32, buffer uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	frame, ok := c.capture.frames[senderID]
	if !ok {
		return fmt.Errorf("attach_buffer on unknown frame %d", senderID)
	}
	frame.BufferID = buffer
	return nil
}

func (c *Client) HandleExtImageCopyCaptureFrameV1DamageBuffer(senderID uint32, x, y, w, h int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	frame, ok := c.capture.frames[senderID]
	if !ok {
		return fmt.Errorf("damage_buffer on unknown frame %d", senderID)
	}
	frame.Damage = append(frame.Damage, x, y, w, h)
	return nil
}

// HandleExtImageCopyCaptureFrameV1Capture pulls compositor pixels into the
// client's wl_shm buffer and reports ready. This is the hot path: it must
// not allocate beyond the grab itself.
func (c *Client) HandleExtImageCopyCaptureFrameV1Capture(senderID uint32) error {
	c.mu.Lock()
	frame, ok := c.capture.frames[senderID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("capture on unknown frame %d", senderID)
	}
	buf, ok := c.findBuffer(frame.BufferID)
	if !ok {
		c.mu.Unlock()
		w := c.conn.WriterRef()
		_ = capture.SendExtImageCopyCaptureFrameV1Failed(w, senderID, uint32(uint32(capture.ExtImageCopyCaptureFrameV1FailureReasonUnknown)))
		return fmt.Errorf("capture on frame %d without attached buffer", senderID)
	}
	pool, ok := c.findPool(buf.PoolID)
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("capture buffer %d pool %d missing", frame.BufferID, buf.PoolID)
	}
	grabber := c.server.captureGrabber
	sess := c.capture.sessions[frame.Session]
	bufWidth, bufHeight, bufStride := buf.Width, buf.Height, buf.Stride
	var regionX, regionY int32
	if sess != nil {
		regionX, regionY = sess.X, sess.Y
	}
	// Release the client lock: the grab may take milliseconds (Vulkan
	// readback) and must not block concurrent client traffic.
	//
	// OWNERSHIP CONTRACT (stratifi-aos audit): this is safe today because
	// dispatch is strictly serial per client — the pool cannot be unmapped
	// (WlShmPoolDestroy) while the grabber writes, since both run on this
	// client's single dispatch goroutine. When the compositor core
	// (stratifi-o2x) drives grabs from the render thread, the pool MUST be
	// pinned for the grab's duration (refcount or per-grab lease) before
	// calling the grabber outside dispatch; WlShmPoolDestroy must refuse
	// to unmap pinned pools.
	c.mu.Unlock()

	var failReason uint32
	if grabber == nil {
		failReason = uint32(uint32(capture.ExtImageCopyCaptureFrameV1FailureReasonUnknown))
	} else if err := grabber.GrabFramebuffer(pool.Data[buf.Offset:], regionX, regionY, int32(bufWidth), int32(bufHeight), int32(bufStride)); err != nil {
		failReason = uint32(uint32(capture.ExtImageCopyCaptureFrameV1FailureReasonBufferConstraints))
	}

	c.mu.Lock()
	w := c.conn.WriterRef()
	c.mu.Unlock()

	if failReason != 0 {
		if err := capture.SendExtImageCopyCaptureFrameV1Failed(w, senderID, failReason); err != nil {
			return err
		}
		return fmt.Errorf("frame %d capture failed (reason %d)", senderID, failReason)
	}

	if err := capture.SendExtImageCopyCaptureFrameV1Transform(w, senderID, uint32(0 /* NORMAL (wl_output.transform) */)); err != nil {
		return err
	}
	// Full-frame damage: the compositor repaints everything per frame.
	if err := capture.SendExtImageCopyCaptureFrameV1Damage(w, senderID, 0, 0, int32(bufWidth), int32(bufHeight)); err != nil {
		return err
	}
	return capture.SendExtImageCopyCaptureFrameV1Ready(w, senderID)
}
