package server

import (
	"fmt"
	"sync"

	"bloxwm/internal/protocol/capture"
	"bloxwm/internal/protocol/wayland"
)

// FrameGrabber supplies compositor framebuffer pixels for capture sessions.
// dst receives tightly packed BGRA pixel data for the region.
type FrameGrabber interface {
	GrabFramebuffer(dst []byte, width, height int32, stride int32) error
}

// captureSource tracks an ext_image_capture_source_v1 object and the output
// it resolves to.
type captureSource struct {
	ID       uint32
	OutputID uint32 // wl_output object id backing this source
}

// captureSession is an ext_image_copy_capture_session_v1 bound to a source.
type captureSession struct {
	ID       uint32
	SourceID uint32
	Stopped  bool
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
	// Window-capture sources resolve through the toplevel handle registry;
	// the compositor does not yet expose ext-foreign-toplevel-list, so
	// creating a source without a resolvable handle is a protocol error.
	return fmt.Errorf("foreign toplevel capture sources not yet supported (handle %d)", toplevelHandle)
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

	c.registerObject(id, ObjCaptureSession)
	c.capture.sessions[id] = &captureSession{ID: id, SourceID: src.ID}

	w := c.conn.WriterRef()
	sW, sH := c.server.outputResolution()
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
	bufWidth, bufHeight, bufStride := buf.Width, buf.Height, buf.Stride
	// Release the client lock: the grab may take milliseconds (Vulkan
	// readback) and must not block concurrent client traffic.
	c.mu.Unlock()

	var failReason uint32
	if grabber == nil {
		failReason = uint32(uint32(capture.ExtImageCopyCaptureFrameV1FailureReasonUnknown))
	} else if err := grabber.GrabFramebuffer(pool.Data[buf.Offset:], int32(bufWidth), int32(bufHeight), int32(bufStride)); err != nil {
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
