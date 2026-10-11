package server

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
	"stratifi/internal/protocol/capture"
	"stratifi/internal/protocol/layershell"
	"stratifi/internal/protocol/wayland"
	"stratifi/internal/protocol/xdgoutput"
	"stratifi/internal/protocol/xdgshell"
	"stratifi/internal/wire"
)

// Global numeric IDs advertised to clients via wl_registry.
const (
	GlobalCompositor        uint32 = 1
	GlobalShm               uint32 = 2
	GlobalXdgWmBase         uint32 = 3
	GlobalOutput            uint32 = 4
	GlobalSeat              uint32 = 5
	GlobalSubcompositor     uint32 = 6
	GlobalDataDeviceManager uint32 = 7
	GlobalLayerShell        uint32 = 10
	GlobalXdgOutputManager  uint32 = 11

	// Screen capture protocol globals (Firefox/WebRTC portal path).
	GlobalOutputImageCaptureSourceManager uint32 = 8
	GlobalImageCopyCaptureManager         uint32 = 9
)

// Client represents a connected Wayland client and its object hierarchy.
type Client struct {
	id     uint32
	conn   *wire.Conn
	server *Server

	mu             sync.Mutex
	objects        objectTable
	pools          poolStore
	buffers        bufferStore
	surfaces       surfaceStore
	xdgSurfaces    xdgStore[XdgSurface]
	xdgToplevels   xdgStore[XdgToplevel]
	xdgPositioners xdgStore[XdgPositioner]
	xdgPopups      xdgStore[XdgPopup]
	subsurfaces    xdgStore[Subsurface]
	layerSurfaces  xdgStore[LayerSurface]
	dataSources    xdgStore[DataSource]

	// subsurfaceOrder maps a parent wl_surface id to the ordered list of
	// its subsurface object ids (bottom to top). place_above/below reorder
	// this list; the actor renders in this order.
	subsurfaceOrder map[uint32][]uint32

	serial atomic.Uint32

	pointerID uint32

	// Cursor: the surface the client set via wl_pointer.set_cursor (0 =
	// none, use the compositor default) and its hotspot.
	cursorSurface uint32
	cursorHX      int32
	cursorHY      int32
	hasCursor     bool
	closed        atomic.Bool
	keyboardID    uint32
	dataDeviceID  uint32

	capture *CaptureState
}

func newClient(id uint32, conn *wire.Conn, s *Server) *Client {
	c := &Client{
		id:     id,
		conn:   conn,
		server: s,
	}
	// Object ID 1 is always the wl_display singleton
	c.registerObject(1, ObjDisplay)
	c.capture = newCaptureState()
	return c
}

// ID returns the unique numeric identifier for this client.
func (c *Client) ID() uint32 {
	return c.id
}

// NextSerial returns a monotonically increasing serial number for events.
func (c *Client) NextSerial() uint32 {
	return c.serial.Add(1)
}

func (c *Client) registerObject(id uint32, t ObjectType) {
	c.objects.register(id, t)
}

func (c *Client) unregisterObject(id uint32) {
	c.objects.unregister(id)
}

func (c *Client) findBuffer(id uint32) (ShmBuffer, bool) {
	return c.buffers.get(id)
}

func (c *Client) findPool(id uint32) (*ShmPool, bool) {
	return c.pools.get(id)
}

func (c *Client) findObject(id uint32) (ObjectType, bool) {
	return c.objects.lookup(id)
}

// Run executes the message reading and dispatch loop for this client.
func (c *Client) Run() error {
	defer c.Close()

	for {
		reader, err := c.conn.ReadMsg() // blocks for one complete message
		if err != nil {
			return err
		}

		// Drain every request already buffered before replying, so a batch
		// costs one flush (framing guarantees the bytes are whole messages).
		for {
			hdr, err := reader.ReadHeader()
			if err != nil {
				return fmt.Errorf("error reading header: %w", err)
			}
			if err := c.dispatch(reader, hdr); err != nil {
				return fmt.Errorf("dispatch error for obj %d, op %d: %w", hdr.ObjectID, hdr.Opcode, err)
			}
			if !c.conn.HasPendingMessage() {
				break
			}
			if reader, err = c.conn.ReadMsg(); err != nil {
				return err
			}
		}

		// Flush all generated events back to the client. The writer is
		// shared with other goroutines that send to this client (focus-time
		// clipboard offers, compositor-driven configures), all of which use
		// it under c.mu; take the same lock so this batch flush cannot race
		// their writes.
		c.mu.Lock()
		flushErr := c.conn.Flush()
		c.mu.Unlock()
		if flushErr != nil {
			return fmt.Errorf("flush error: %w", flushErr)
		}
	}
}

// dispatch routes incoming messages based on the registered object type.
// dispatchTable routes object types to their protocol decoders. Table-driven
// so a missing entry is caught by TestDispatchTableComplete at test time
// instead of surfacing as a runtime "no dispatcher" error mid-session.
var dispatchTable = map[ObjectType]func(*Client, *wire.Reader, wire.Header) error{
	ObjDisplay:     func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlDisplay(r, h, c) },
	ObjRegistry:    func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlRegistry(r, h, c) },
	ObjCompositor:  func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlCompositor(r, h, c) },
	ObjShm:         func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlShm(r, h, c) },
	ObjShmPool:     func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlShmPool(r, h, c) },
	ObjSurface:     func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlSurface(r, h, c) },
	ObjBuffer:      func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlBuffer(r, h, c) },
	ObjXdgWmBase:   func(c *Client, r *wire.Reader, h wire.Header) error { return xdgshell.DispatchXdgWmBase(r, h, c) },
	ObjXdgSurface:  func(c *Client, r *wire.Reader, h wire.Header) error { return xdgshell.DispatchXdgSurface(r, h, c) },
	ObjXdgToplevel: func(c *Client, r *wire.Reader, h wire.Header) error { return xdgshell.DispatchXdgToplevel(r, h, c) },
	ObjXdgPopup: func(c *Client, r *wire.Reader, h wire.Header) error {
		return xdgshell.DispatchXdgPopup(r, h, c)
	},
	ObjXdgPositioner: func(c *Client, r *wire.Reader, h wire.Header) error {
		return xdgshell.DispatchXdgPositioner(r, h, c)
	},
	ObjOutput:        func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlOutput(r, h, c) },
	ObjSeat:          func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlSeat(r, h, c) },
	ObjPointer:       func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlPointer(r, h, c) },
	ObjKeyboard:      func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlKeyboard(r, h, c) },
	ObjSubcompositor: func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlSubcompositor(r, h, c) },
	ObjLayerShell: func(c *Client, r *wire.Reader, h wire.Header) error {
		return layershell.DispatchZwlrLayerShellV1(r, h, c)
	},
	ObjLayerSurface: func(c *Client, r *wire.Reader, h wire.Header) error {
		return layershell.DispatchZwlrLayerSurfaceV1(r, h, c)
	},
	ObjXdgOutputManager: func(c *Client, r *wire.Reader, h wire.Header) error {
		return xdgoutput.DispatchZxdgOutputManagerV1(r, h, c)
	},
	ObjXdgOutput: func(c *Client, r *wire.Reader, h wire.Header) error {
		return xdgoutput.DispatchZxdgOutputV1(r, h, c)
	},
	ObjSubsurface: func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlSubsurface(r, h, c) },
	ObjDataDeviceManager: func(c *Client, r *wire.Reader, h wire.Header) error {
		return wayland.DispatchWlDataDeviceManager(r, h, c)
	},
	ObjDataDevice: func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlDataDevice(r, h, c) },
	ObjDataOffer:  func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlDataOffer(r, h, c) },
	ObjDataSource: func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlDataSource(r, h, c) },
	ObjRegion:     func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlRegion(r, h, c) },
	ObjCaptureSourceManagerOutput: func(c *Client, r *wire.Reader, h wire.Header) error {
		return capture.DispatchExtOutputImageCaptureSourceManagerV1(r, h, c)
	},
	ObjImageCopyCaptureManager: func(c *Client, r *wire.Reader, h wire.Header) error {
		return capture.DispatchExtImageCopyCaptureManagerV1(r, h, c)
	},
	ObjCaptureSourceManagerToplevel: func(c *Client, r *wire.Reader, h wire.Header) error {
		return capture.DispatchExtForeignToplevelImageCaptureSourceManagerV1(r, h, c)
	},
	ObjCaptureSource: func(c *Client, r *wire.Reader, h wire.Header) error {
		return capture.DispatchExtImageCaptureSourceV1(r, h, c)
	},
	ObjCaptureSession: func(c *Client, r *wire.Reader, h wire.Header) error {
		return capture.DispatchExtImageCopyCaptureSessionV1(r, h, c)
	},
	ObjCaptureFrame: func(c *Client, r *wire.Reader, h wire.Header) error {
		return capture.DispatchExtImageCopyCaptureFrameV1(r, h, c)
	},
	ObjCallback: func(c *Client, r *wire.Reader, h wire.Header) error {
		return fmt.Errorf("callback objects receive events only")
	},
	ObjCaptureCursorSession: func(c *Client, r *wire.Reader, h wire.Header) error {
		return fmt.Errorf("pointer cursor capture not supported")
	},
}

func (c *Client) dispatch(r *wire.Reader, hdr wire.Header) error {
	c.mu.Lock()
	objType, found := c.findObject(hdr.ObjectID)
	c.mu.Unlock()

	if !found {
		return fmt.Errorf("unknown object ID %d (opcode %d)", hdr.ObjectID, hdr.Opcode)
	}
	if objType == ObjCallback {
		return nil // callbacks receive events, never requests
	}

	dispatcher, ok := dispatchTable[objType]
	if !ok {
		return fmt.Errorf("no dispatcher for object type %d (id %d)", objType, hdr.ObjectID)
	}
	return dispatcher(c, r, hdr)
}

// --- WlDisplayHandler ---

func (c *Client) HandleWlDisplaySync(senderID uint32, callback uint32) error {
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	if err := wayland.SendWlCallbackDone(w, callback, serial); err != nil {
		return err
	}
	return wayland.SendWlDisplayDeleteId(w, 1, callback)
}

func (c *Client) HandleWlDisplayGetRegistry(senderID uint32, registryID uint32) error {
	c.mu.Lock()
	c.registerObject(registryID, ObjRegistry)
	c.mu.Unlock()

	w := c.conn.WriterRef()
	// Advertise available globals
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalCompositor, "wl_compositor", 4); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalShm, "wl_shm", 1); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalXdgWmBase, "xdg_wm_base", 3); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalOutput, "wl_output", 4); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalSeat, "wl_seat", 5); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalSubcompositor, "wl_subcompositor", 1); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalLayerShell, "zwlr_layer_shell_v1", 5); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalXdgOutputManager, "zxdg_output_manager_v1", 3); err != nil {
		return err
	}
	return wayland.SendWlRegistryGlobal(w, registryID, GlobalDataDeviceManager, "wl_data_device_manager", 3)
}

// --- WlRegistryHandler ---

func (c *Client) HandleWlRegistryBind(senderID uint32, name uint32, ifaceName string, version uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch name {
	case GlobalCompositor:
		c.registerObject(id, ObjCompositor)
	case GlobalShm:
		c.registerObject(id, ObjShm)
		// Immediately notify client of supported pixel formats
		w := c.conn.WriterRef()
		_ = wayland.SendWlShmFormat(w, id, uint32(wayland.WlShmFormatArgb8888))
		_ = wayland.SendWlShmFormat(w, id, uint32(wayland.WlShmFormatXrgb8888))
	case GlobalXdgWmBase:
		c.registerObject(id, ObjXdgWmBase)
	case GlobalOutput:
		c.registerObject(id, ObjOutput)
		w := c.conn.WriterRef()
		_ = wayland.SendWlOutputGeometry(w, id, 0, 0, 320, 240, 0, "stratifi", "stratifi-display", 0)
		_ = wayland.SendWlOutputScale(w, id, 1)
		out := c.server.Output()
		_ = wayland.SendWlOutputMode(w, id, 0x3,
			int32(out.Width), int32(out.Height), int32(out.RefreshHz*1000))
		// v4: clients (waybar et al.) match bars to outputs by name.
		_ = wayland.SendWlOutputName(w, id, "STRATIFI-1")
		_ = wayland.SendWlOutputDescription(w, id, "stratifi output 1")
		_ = wayland.SendWlOutputDone(w, id)
	case GlobalSeat:
		c.registerObject(id, ObjSeat)
		w := c.conn.WriterRef()
		_ = wayland.SendWlSeatCapabilities(w, id, 3) // Pointer | Keyboard
		_ = wayland.SendWlSeatName(w, id, "seat0")
	case GlobalSubcompositor:
		c.registerObject(id, ObjSubcompositor)
	case GlobalLayerShell:
		c.registerObject(id, ObjLayerShell)
	case GlobalXdgOutputManager:
		c.registerObject(id, ObjXdgOutputManager)
	case GlobalDataDeviceManager:
		c.registerObject(id, ObjDataDeviceManager)
	case GlobalOutputImageCaptureSourceManager:
		c.registerObject(id, ObjCaptureSourceManagerOutput)
	case GlobalImageCopyCaptureManager:
		c.registerObject(id, ObjImageCopyCaptureManager)
	default:
		return fmt.Errorf("unknown global name %d (iface %s)", name, ifaceName)
	}
	return nil
}

// --- WlCompositorHandler ---

func (c *Client) HandleWlCompositorCreateSurface(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.registerObject(id, ObjSurface)
	c.surfaces.add(Surface{
		ID:       id,
		ClientID: c.id,
		Active:   true,
	})
	return nil
}

func (c *Client) HandleWlCompositorCreateRegion(senderID uint32, id uint32) error {
	c.mu.Lock()
	c.registerObject(id, ObjRegion)
	c.mu.Unlock()
	return nil
}

func (c *Client) HandleWlCompositorRelease(senderID uint32) error {
	c.mu.Lock()
	c.unregisterObject(senderID)
	c.mu.Unlock()
	return nil
}

// --- WlRegionHandler ---

func (c *Client) HandleWlRegionDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleWlRegionAdd(senderID uint32, x int32, y int32, width int32, height int32) error {
	return nil
}

func (c *Client) HandleWlRegionSubtract(senderID uint32, x int32, y int32, width int32, height int32) error {
	return nil
}

// --- WlShmHandler ---

func (c *Client) HandleWlShmCreatePool(senderID uint32, id uint32, fd int, size int32) error {
	if size <= 0 {
		_ = unix.Close(fd)
		return fmt.Errorf("invalid pool size: %d", size)
	}

	// PROT_WRITE required: the compositor writes into client pools both for
	// surface rendering and for capture (ext-image-copy-capture) buffers.
	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("mmap failed for pool %d: %w", id, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.registerObject(id, ObjShmPool)
	c.pools.add(ShmPool{
		ID:   id,
		FD:   fd,
		Size: int(size),
		Data: data,
	})
	return nil
}

func (c *Client) HandleWlShmRelease(senderID uint32) error {
	c.mu.Lock()
	c.unregisterObject(senderID)
	c.mu.Unlock()
	return nil
}

// --- WlShmPoolHandler ---

func (c *Client) HandleWlShmPoolCreateBuffer(senderID uint32, id uint32, offset int32, width int32, height int32, stride int32, format uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.registerObject(id, ObjBuffer)
	c.buffers.add(ShmBuffer{
		ID:       id,
		PoolID:   senderID,
		Offset:   offset,
		Width:    width,
		Height:   height,
		Stride:   stride,
		Format:   format,
		Released: false,
	})
	return nil
}

func (c *Client) HandleWlShmPoolDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	if pool, ok := c.pools.get(senderID); ok {
		_ = pool.Close()
		c.pools.remove(senderID)
	}
	return nil
}

func (c *Client) HandleWlShmPoolResize(senderID uint32, size int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	pool, ok := c.pools.get(senderID)
	if !ok {
		return fmt.Errorf("pool %d not found for resize", senderID)
	}
	_ = unix.Munmap(pool.Data)
	data, err := unix.Mmap(pool.FD, 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("remap failed: %w", err)
	}
	pool.Size = int(size)
	pool.Data = data
	return nil
}

// --- WlBufferHandler ---

func (c *Client) HandleWlBufferDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	c.buffers.remove(senderID)
	return nil
}

// --- WlSurfaceHandler ---

func (c *Client) HandleWlSurfaceDestroy(senderID uint32) error {
	c.mu.Lock()
	c.unregisterObject(senderID)
	c.surfaces.mutate(senderID, func(sf *Surface) { sf.Active = false })
	wasCursor := c.hasCursor && c.cursorSurface == senderID
	if wasCursor {
		c.hasCursor = false
		c.cursorSurface = 0
	}
	c.mu.Unlock()

	if wasCursor {
		c.emitCursor()
	}
	return nil
}

func (c *Client) HandleWlSurfaceAttach(senderID uint32, buffer uint32, x int32, y int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	applied := c.surfaces.mutate(senderID, func(sf *Surface) {
		if !sf.Active {
			return
		}
		sf.Pending.AttachedBufferID = buffer
		sf.Pending.BufferOffsetX = x
		sf.Pending.BufferOffsetY = y
	})
	if !applied {
		return fmt.Errorf("surface %d not found for attach", senderID)
	}
	return nil
}

func (c *Client) HandleWlSurfaceDamage(senderID uint32, x int32, y int32, width int32, height int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	applied := c.surfaces.mutate(senderID, func(sf *Surface) {
		if !sf.Active {
			return
		}
		sf.Pending.DamageX = x
		sf.Pending.DamageY = y
		sf.Pending.DamageW = width
		sf.Pending.DamageH = height
		sf.Pending.HasDamage = true
	})
	if !applied {
		return fmt.Errorf("surface %d not found for damage", senderID)
	}
	return nil
}

func (c *Client) HandleWlSurfaceDamageBuffer(senderID uint32, x int32, y int32, width int32, height int32) error {
	return c.HandleWlSurfaceDamage(senderID, x, y, width, height)
}

func (c *Client) HandleWlSurfaceFrame(senderID uint32, callback uint32) error {
	w := c.conn.WriterRef()
	timeMs := uint32(c.NextSerial() * 16)
	_ = wayland.SendWlCallbackDone(w, callback, timeMs)
	return wayland.SendWlDisplayDeleteId(w, 1, callback)
}

func (c *Client) HandleWlSurfaceSetOpaqueRegion(senderID uint32, region uint32) error {
	return nil
}

func (c *Client) HandleWlSurfaceSetInputRegion(senderID uint32, region uint32) error {
	return nil
}

func (c *Client) HandleWlSurfaceCommit(senderID uint32) error {
	c.mu.Lock()

	var surfCopy Surface
	committed := c.surfaces.mutate(senderID, func(sf *Surface) {
		if !sf.Active {
			return
		}
		// Atomic state commit: pending -> current
		sf.Current = sf.Pending
		sf.Pending.HasDamage = false
		surfCopy = *sf
	})
	if !committed {
		c.mu.Unlock()
		return fmt.Errorf("surface %d not found for commit", senderID)
	}
	c.mu.Unlock()

	// Popups are placed as soon as the client commits: configure geometry
	// from the positioner, then ack-configure. The commit's senderID is the
	// wl_surface; find the popup role registered for that surface.
	c.configurePendingPopups(senderID)

	// Subsurfaces: a desync child applies on its own commit; a sync child
	// applies when its parent commits.
	c.applySubsurfaceCommit(senderID)

	// Layer surfaces configure on their first commit and re-emit each commit.
	c.applyLayerCommit(senderID)

	// A cursor surface's commit may have changed its image or size.
	c.mu.Lock()
	isCursor := c.hasCursor && c.cursorSurface == senderID
	c.mu.Unlock()
	if isCursor {
		c.emitCursor()
	}

	// Notify compositor of surface update outside of c.mu
	if c.server.onCommit != nil {
		c.server.onSurfaceCommitted(&surfCopy)
	}
	return nil
}

// applySubsurfaceCommit propagates a wl_surface.commit to any subsurface
// role: the committing surface may itself be a desync child (apply now) or
// a parent (flush its sync children). Emits happen after the lock drops.
func (c *Client) applySubsurfaceCommit(surfaceID uint32) {
	type emit struct {
		surfaceID, parentID uint32
		x, y, w, h, order   int32
	}
	var emits []emit

	c.mu.Lock()
	for i := range c.subsurfaces.items {
		ss := &c.subsurfaces.items[i].rec
		if !ss.Active {
			continue
		}
		child := ss.SurfaceID == surfaceID && !ss.Sync
		parent := ss.ParentID == surfaceID && ss.Sync
		if !child && !parent {
			continue
		}
		if ss.HasPending {
			ss.X, ss.Y = ss.PendingX, ss.PendingY
			ss.HasPending = false
		}
		w, h, mapped := c.surfaceSize(ss.SurfaceID)
		order := c.subsurfaceOrderOf(ss.ID, ss.ParentID)
		if ss.emitted && ss.lastX == ss.X && ss.lastY == ss.Y &&
			ss.lastW == w && ss.lastH == h && ss.lastMapped == mapped && ss.lastOrder == order {
			continue // nothing changed; do not spam the actor
		}
		ss.lastX, ss.lastY, ss.lastW, ss.lastH = ss.X, ss.Y, w, h
		ss.lastMapped, ss.lastOrder, ss.emitted = mapped, order, true
		emits = append(emits, emit{ss.SurfaceID, ss.ParentID, ss.X, ss.Y, w, h, order})
	}
	c.mu.Unlock()

	if c.server.onSubsurface == nil {
		return
	}
	for _, e := range emits {
		c.server.onSubsurface(c.ID(), e.surfaceID, e.parentID, e.x, e.y, e.w, e.h, e.order)
	}
}

// surfaceSize returns the current buffer dimensions of a wl_surface.
func (c *Client) surfaceSize(surfaceID uint32) (int32, int32, bool) {
	sf, ok := c.surfaces.get(surfaceID)
	if !ok || sf.Current.AttachedBufferID == 0 {
		return 0, 0, false
	}
	buf, ok := c.buffers.get(sf.Current.AttachedBufferID)
	if !ok {
		return 0, 0, false
	}
	return buf.Width, buf.Height, true
}

// (The popup's own xdg_surface id was recorded in XdgPopup.XdgSurfaceID at
// get_popup time.)
// configurePendingPopups places a popup whose wl_surface just committed:
// configure geometry from its positioner, then ack-configure. senderID is
// the committing wl_surface id; no-op for non-popup surfaces. Locking is
// explicit (not defer) because the mapped callback must run unlocked.
func (c *Client) configurePendingPopups(surfaceID uint32) {
	c.mu.Lock()

	popupID := uint32(0)
	var popup *XdgPopup
	for i := range c.xdgPopups.items {
		p := &c.xdgPopups.items[i].rec
		if !p.Active || p.SurfaceID != surfaceID {
			continue
		}
		if p.Configured {
			continue
		}
		popupID = c.xdgPopups.items[i].id
		popup = p
		break
	}
	if popupID == 0 {
		c.mu.Unlock()
		return
	}

	// Parent geometry: the parent xdg_surface's current buffer size (0 while
	// the parent has no buffer; the popup then uses no flip constraints).
	var parentW, parentH int32 = 100000, 100000 // unconstrained until the parent maps
	if x, ok := c.xdgSurfaces.get(popup.Parent); ok {
		if sf, found := c.surfaces.get(x.SurfaceID); found && sf.Current.AttachedBufferID != 0 {
			if buf, found := c.buffers.get(sf.Current.AttachedBufferID); found {
				parentW = buf.Width
				parentH = buf.Height
			}
		}
	}

	// Positioner snapshot: last one registered with the popup (there is
	// exactly one per get_popup call in practice; the latest wins).
	var pos XdgPositioner
	foundPos := false
	for i := range c.xdgPositioners.items {
		if p := &c.xdgPositioners.items[i].rec; p.Active {
			pos = *p
			foundPos = true
		}
	}
	if !foundPos {
		pos = XdgPositioner{Width: 100, Height: 32}
	}

	// Output bounds in parent-relative coordinates: the compositor places
	// popups over the parent's window body, so the output equals the parent
	// body size. The parent size is the whole usable area here.
	placement := PlacePopup(pos, parentW, parentH, parentW, parentH)

	// Snapshot what the callback needs before dropping the lock: holding
	// mutexes across callbacks re-enters dispatch paths that lock again.
	mapped := struct {
		popupID, surfaceID, parent uint32
		x, y, w, h                 int32
	}{popupID, popup.SurfaceID, popup.Parent, placement.X, placement.Y, placement.Width, placement.Height}

	w := c.conn.WriterRef()
	_ = xdgshell.SendXdgPopupConfigure(w, popupID, placement.X, placement.Y, placement.Width, placement.Height)
	serial := c.NextSerial()
	c.xdgSurfaces.mutate(popup.XdgSurfaceID, func(x *XdgSurface) { x.LastSerial = serial })
	_ = xdgshell.SendXdgSurfaceConfigure(w, popup.XdgSurfaceID, serial)
	_ = c.conn.Flush()

	popup.Configured = true
	popup.ConfiguredX, popup.ConfiguredY = placement.X, placement.Y
	popup.ConfiguredW, popup.ConfiguredH = placement.Width, placement.Height
	c.mu.Unlock()

	if c.server.onPopupMapped != nil {
		c.server.onPopupMapped(c.ID(), mapped.popupID, mapped.surfaceID, mapped.parent,
			mapped.x, mapped.y, mapped.w, mapped.h)
	}
}

func (c *Client) HandleWlSurfaceSetBufferTransform(senderID uint32, transform int32) error {
	return nil
}

func (c *Client) HandleWlSurfaceSetBufferScale(senderID uint32, scale int32) error {
	return nil
}

func (c *Client) HandleWlSurfaceOffset(senderID uint32, x int32, y int32) error {
	return nil
}

func (c *Client) HandleWlSurfaceGetRelease(senderID uint32, callback uint32) error {
	return nil
}

// --- XdgWmBaseHandler ---

func (c *Client) HandleXdgWmBaseDestroy(senderID uint32) error {
	c.mu.Lock()
	c.unregisterObject(senderID)
	c.mu.Unlock()
	return nil
}

func (c *Client) HandleXdgWmBaseCreatePositioner(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjXdgPositioner)
	c.xdgPositioners.add(id, XdgPositioner{ID: id, Active: true})
	return nil
}

// --- XdgPositionerHandler ---

func (c *Client) HandleXdgPositionerDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.Active = false })
	return nil
}

func (c *Client) HandleXdgPositionerSetSize(senderID uint32, width, height int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.Width, p.Height = width, height })
	return nil
}

func (c *Client) HandleXdgPositionerSetAnchorRect(senderID uint32, x, y, w, h int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) {
		p.AnchorRect = [4]int32{x, y, w, h}
	})
	return nil
}

func (c *Client) HandleXdgPositionerSetAnchor(senderID uint32, anchor uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.AnchorMask = anchor })
	return nil
}

func (c *Client) HandleXdgPositionerSetGravity(senderID uint32, gravity uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.GravityMask = gravity })
	return nil
}

func (c *Client) HandleXdgPositionerSetConstraintAdjustment(senderID uint32, adj uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.ConstraintAdjustment = adj })
	return nil
}

func (c *Client) HandleXdgPositionerSetOffset(senderID uint32, x, y int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.OffsetX, p.OffsetY = x, y })
	return nil
}

func (c *Client) HandleXdgPositionerSetReactive(senderID uint32) error { return nil }

func (c *Client) HandleXdgPositionerSetParentSize(senderID uint32, w, h int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPositioners.mutate(senderID, func(p *XdgPositioner) { p.ParentWidth, p.ParentHeight = w, h })
	return nil
}

func (c *Client) HandleXdgPositionerSetParentConfigure(senderID uint32, serial uint32) error {
	return nil
}

func (c *Client) HandleXdgWmBaseGetXdgSurface(senderID uint32, id uint32, surfaceID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.registerObject(id, ObjXdgSurface)
	c.xdgSurfaces.add(id, XdgSurface{
		ID:        id,
		SurfaceID: surfaceID,
		Active:    true,
	})
	return nil
}

func (c *Client) HandleXdgWmBasePong(senderID uint32, serial uint32) error {
	return nil
}

// --- XdgSurfaceHandler ---

func (c *Client) HandleXdgSurfaceDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	c.xdgSurfaces.mutate(senderID, func(x *XdgSurface) { x.Active = false })
	return nil
}

func (c *Client) HandleXdgSurfaceGetToplevel(senderID uint32, id uint32) error {
	c.mu.Lock()

	c.registerObject(id, ObjXdgToplevel)
	top := c.xdgToplevels.add(id, XdgToplevel{
		ID:           id,
		XdgSurfaceID: senderID,
		Active:       true,
	})

	surf, found := c.xdgSurfaces.get(senderID)
	if found && surf.Active {
		surf.ToplevelID = id

		if c.server.onToplevel != nil {
			c.mu.Unlock()
			c.server.onToplevelCreated(c, top, senderID)
			return nil
		}

		// Send default initial configure sequence
		w := c.conn.WriterRef()
		_ = xdgshell.SendXdgToplevelConfigure(w, id, 0, 0, nil)

		serial := c.NextSerial()
		surf.LastSerial = serial
		_ = xdgshell.SendXdgSurfaceConfigure(w, senderID, serial)
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return fmt.Errorf("xdg_surface %d not found for get_toplevel", senderID)
}

// HandleXdgSurfaceGetPopup registers a popup role for a surface. Placement
// happens on the surface's first commit, once the client has set its size.
func (c *Client) HandleXdgSurfaceGetPopup(senderID uint32, id uint32, parent uint32, positioner uint32) error {
	c.mu.Lock()

	if os.Getenv("STRATIFI_TRACE") != "" {
		fmt.Printf("[popup] get_popup popup=%d parent=%d positioner=%d surface=%d\n", id, parent, positioner, c.xdgSurfaceSurfaceID(senderID))
	}
	c.registerObject(id, ObjXdgPopup)
	c.xdgPopups.add(id, XdgPopup{
		ID:           id,
		SurfaceID:    c.xdgSurfaceSurfaceID(senderID),
		XdgSurfaceID: senderID,
		Parent:       parent,
		Active:       true,
	})
	surfaceID := c.xdgSurfaceSurfaceID(senderID)
	c.mu.Unlock()
	c.server.onPopupCreated(c, id, surfaceID, senderID)
	return nil
}

// --- XdgPopupHandler ---

func (c *Client) HandleXdgPopupDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	c.xdgPopups.mutate(senderID, func(p *XdgPopup) { p.Active = false })
	return nil
}

// HandleXdgPopupGrab takes a modal seat grab: input outside the popup
// dismisses it (compositor policy) — recorded so the scene can trigger
// dismissal on outside clicks.
func (c *Client) HandleXdgPopupGrab(senderID uint32, seat uint32, serial uint32) error {
	if os.Getenv("STRATIFI_TRACE") != "" {
		fmt.Printf("[popup] grab popup=%d seat=%d serial=%d\n", senderID, seat, serial)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xdgPopups.mutate(senderID, func(p *XdgPopup) { p.Grabs++ })
	return nil
}

func (c *Client) HandleXdgPopupReposition(senderID uint32, positioner uint32, token uint32) error {
	// Reposition re-runs placement at the next commit with the new
	// positioner; nothing to record yet.
	return nil
}

// xdgSurfaceSurfaceID maps an xdg_surface object id to its wl_surface id.
func (c *Client) xdgSurfaceSurfaceID(xdgID uint32) uint32 {
	if x, ok := c.xdgSurfaces.get(xdgID); ok {
		return x.SurfaceID
	}
	return 0
}

func (c *Client) HandleXdgSurfaceSetWindowGeometry(senderID uint32, x int32, y int32, width int32, height int32) error {
	return nil
}

func (c *Client) HandleXdgSurfaceAckConfigure(senderID uint32, serial uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.xdgSurfaces.mutate(senderID, func(x *XdgSurface) {
		if x.Active {
			x.Configured = true
		}
	})
	return nil
}

// --- XdgToplevelHandler ---

func (c *Client) HandleXdgToplevelDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	c.xdgToplevels.mutate(senderID, func(x *XdgToplevel) { x.Active = false })
	return nil
}

func (c *Client) HandleXdgToplevelSetParent(senderID uint32, parent uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelSetTitle(senderID uint32, title string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.xdgToplevels.mutate(senderID, func(x *XdgToplevel) {
		if x.Active {
			x.Title = title
		}
	})
	return nil
}

func (c *Client) HandleXdgToplevelSetAppId(senderID uint32, appId string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.xdgToplevels.mutate(senderID, func(x *XdgToplevel) {
		if x.Active {
			x.AppID = appId
		}
	})
	return nil
}

func (c *Client) HandleXdgToplevelShowWindowMenu(senderID uint32, seat uint32, serial uint32, x int32, y int32) error {
	return nil
}

func (c *Client) HandleXdgToplevelMove(senderID uint32, seat uint32, serial uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelResize(senderID uint32, seat uint32, serial uint32, edges uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelSetMaxSize(senderID uint32, width int32, height int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.xdgToplevels.mutate(senderID, func(x *XdgToplevel) {
		if x.Active {
			x.MaxWidth = width
			x.MaxHeight = height
		}
	})
	return nil
}

func (c *Client) HandleXdgToplevelSetMinSize(senderID uint32, width int32, height int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.xdgToplevels.mutate(senderID, func(x *XdgToplevel) {
		if x.Active {
			x.MinWidth = width
			x.MinHeight = height
		}
	})
	return nil
}

func (c *Client) HandleXdgToplevelSetMaximized(senderID uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelUnsetMaximized(senderID uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelSetFullscreen(senderID uint32, output uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelUnsetFullscreen(senderID uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelSetMinimized(senderID uint32) error {
	return nil
}

// --- WlOutputHandler ---

func (c *Client) HandleWlOutputRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- WlSeatHandler ---

func (c *Client) HandleWlSeatGetPointer(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjPointer)
	c.pointerID = id
	return nil
}

func (c *Client) HandleWlSeatGetKeyboard(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjKeyboard)
	c.keyboardID = id

	if c.server.keymapFD >= 0 && c.server.keymapSize > 0 {
		w := c.conn.WriterRef()
		_ = wayland.SendWlKeyboardKeymap(w, id, 1, c.server.keymapFD, c.server.keymapSize)
		_ = wayland.SendWlKeyboardRepeatInfo(w, id, 25, 400)
	}
	return nil
}

func (c *Client) HandleWlSeatGetTouch(senderID uint32, id uint32) error {
	return nil
}

func (c *Client) HandleWlSeatRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- WlPointerHandler ---

func (c *Client) HandleWlPointerSetCursor(senderID uint32, serial uint32, surface uint32, hotspotX int32, hotspotY int32) error {
	c.mu.Lock()
	if surface == 0 {
		c.hasCursor = false
		c.cursorSurface = 0
	} else {
		c.hasCursor = true
		c.cursorSurface = surface
		c.cursorHX, c.cursorHY = hotspotX, hotspotY
	}
	c.mu.Unlock()
	c.emitCursor()
	return nil
}

// emitCursor reports the client's current cursor to the compositor. Fired on
// set_cursor and whenever the cursor surface commits (its image or size may
// have changed). Reads state under the lock, calls out unlocked.
func (c *Client) emitCursor() {
	c.mu.Lock()
	active := c.hasCursor
	surface := c.cursorSurface
	hx, hy := c.cursorHX, c.cursorHY
	var w, h int32
	if active {
		w, h, active = c.surfaceSize(surface)
		if !active {
			// Cursor surface has no buffer yet; report the hotspot so the
			// compositor can keep it but renders nothing until mapped.
			w, h = 0, 0
		}
	}
	c.mu.Unlock()

	if c.server.onCursor != nil {
		c.server.onCursor(c.ID(), surface, hx, hy, w, h, active)
	}
}

func (c *Client) HandleWlPointerRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	if c.pointerID == senderID {
		c.pointerID = 0
	}
	return nil
}

// --- WlKeyboardHandler ---

func (c *Client) HandleWlKeyboardRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	if c.keyboardID == senderID {
		c.keyboardID = 0
	}
	return nil
}

// SendPointerEnter dispatches wl_pointer.enter to the client.
func (c *Client) SendPointerEnter(surfaceID uint32, sx, sy float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pointerID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlPointerEnter(w, c.pointerID, serial, surfaceID, wire.ToFixed(sx), wire.ToFixed(sy))
	_ = wayland.SendWlPointerFrame(w, c.pointerID)
	_ = c.conn.Flush()
}

// SendPointerLeave dispatches wl_pointer.leave to the client.
func (c *Client) SendPointerLeave(surfaceID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pointerID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlPointerLeave(w, c.pointerID, serial, surfaceID)
	_ = wayland.SendWlPointerFrame(w, c.pointerID)
	_ = c.conn.Flush()
}

// SendPointerMotion dispatches wl_pointer.motion to the client.
func (c *Client) SendPointerMotion(timeMs uint32, sx, sy float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pointerID == 0 {
		return
	}
	w := c.conn.WriterRef()
	_ = wayland.SendWlPointerMotion(w, c.pointerID, timeMs, wire.ToFixed(sx), wire.ToFixed(sy))
	_ = wayland.SendWlPointerFrame(w, c.pointerID)
	_ = c.conn.Flush()
}

// SendPointerButton dispatches wl_pointer.button to the client.
func (c *Client) SendPointerButton(timeMs uint32, button uint32, state uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pointerID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlPointerButton(w, c.pointerID, serial, timeMs, button, state)
	_ = wayland.SendWlPointerFrame(w, c.pointerID)
	_ = c.conn.Flush()
}

// SendKeyboardEnter dispatches wl_keyboard.enter to the client.
func (c *Client) SendKeyboardEnter(surfaceID uint32, keys []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keyboardID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlKeyboardEnter(w, c.keyboardID, serial, surfaceID, keys)
	_ = c.conn.Flush()
}

// SendKeyboardLeave dispatches wl_keyboard.leave to the client.
func (c *Client) SendKeyboardLeave(surfaceID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keyboardID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlKeyboardLeave(w, c.keyboardID, serial, surfaceID)
	_ = c.conn.Flush()
}

// SendKeyboardKey dispatches wl_keyboard.key to the client.
func (c *Client) SendKeyboardKey(timeMs uint32, key uint32, state uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keyboardID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlKeyboardKey(w, c.keyboardID, serial, timeMs, key, state)
	_ = c.conn.Flush()
}

// SendKeyboardModifiers dispatches wl_keyboard.modifiers to the client.
func (c *Client) SendKeyboardModifiers(depressed, latched, locked, group uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keyboardID == 0 {
		return
	}
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	_ = wayland.SendWlKeyboardModifiers(w, c.keyboardID, serial, depressed, latched, locked, group)
	_ = c.conn.Flush()
}

// SendDataSourceSend forwards wl_data_source.send to the owner with the
// pasting client's fd. The owner writes selection bytes into the fd.
func (c *Client) SendDataSourceSend(sourceID uint32, mimeType string, fd int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("owner client closed")
	}
	if typ, ok := c.findObject(sourceID); !ok || typ != ObjDataSource {
		return fmt.Errorf("owner data source %d gone", sourceID)
	}
	w := c.conn.WriterRef()
	if err := wayland.SendWlDataSourceSend(w, sourceID, mimeType, fd); err != nil {
		return err
	}
	return c.conn.Flush()
}

// SendToplevelClose asks the client to close a toplevel window (the graceful
// path behind the compositor's close action). No-op unless the id is a live
// toplevel, so a stale window reference can never emit a protocol error.
func (c *Client) SendToplevelClose(toplevelID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if typ, ok := c.findObject(toplevelID); !ok || typ != ObjXdgToplevel {
		return
	}
	w := c.conn.WriterRef()
	_ = xdgshell.SendXdgToplevelClose(w, toplevelID)
	_ = c.conn.Flush()
}

// ConfigureToplevel sends xdg_toplevel.configure and xdg_surface.configure to client.
func (c *Client) ConfigureToplevel(xdgSurfaceID uint32, toplevelID uint32, width, height int32, states []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	w := c.conn.WriterRef()
	_ = xdgshell.SendXdgToplevelConfigure(w, toplevelID, width, height, states)
	serial := c.NextSerial()
	c.xdgSurfaces.mutate(xdgSurfaceID, func(x *XdgSurface) { x.LastSerial = serial })
	_ = xdgshell.SendXdgSurfaceConfigure(w, xdgSurfaceID, serial)
	_ = c.conn.Flush()
}

// GetSurfaceBuffer returns the raw mapped pixel slice and dimensions for the surface's current attached buffer.
func (c *Client) GetSurfaceBuffer(surfaceID uint32) (data []byte, width, height, stride int32, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	surf, ok := c.surfaces.get(surfaceID)
	if !ok || !surf.Active || surf.Current.AttachedBufferID == 0 {
		return nil, 0, 0, 0, false
	}
	buf, ok := c.buffers.get(surf.Current.AttachedBufferID)
	if !ok {
		return nil, 0, 0, 0, false
	}
	pool, ok := c.pools.get(buf.PoolID)
	if !ok {
		return nil, 0, 0, 0, false
	}
	start := int(buf.Offset)
	end := start + int(buf.Stride*buf.Height)
	if start < 0 || end > len(pool.Data) {
		return nil, 0, 0, 0, false
	}
	return pool.Data[start:end], buf.Width, buf.Height, buf.Stride, true
}

// ReleaseSurfaceBuffer sends wl_buffer.release to the currently attached buffer.
func (c *Client) ReleaseSurfaceBuffer(surfaceID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	surf, ok := c.surfaces.get(surfaceID)
	if !ok || !surf.Active || surf.Current.AttachedBufferID == 0 {
		return
	}
	w := c.conn.WriterRef()
	_ = wayland.SendWlBufferRelease(w, surf.Current.AttachedBufferID)
}

// GetSurfaceIDForXdgSurface returns the underlying wl_surface ID for an xdg_surface ID.
func (c *Client) GetSurfaceIDForXdgSurface(xdgSurfaceID uint32) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	if x, ok := c.xdgSurfaces.get(xdgSurfaceID); ok && x.Active {
		return x.SurfaceID
	}
	return 0
}

// --- WlSubcompositorHandler ---

func (c *Client) HandleWlSubcompositorDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleWlSubcompositorGetSubsurface(senderID uint32, id uint32, surface uint32, parent uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjSubsurface)
	c.subsurfaces.add(id, Subsurface{
		ID:        id,
		SurfaceID: surface,
		ParentID:  parent,
		Sync:      true, // protocol default: synchronized
		Active:    true,
	})
	if c.subsurfaceOrder == nil {
		c.subsurfaceOrder = make(map[uint32][]uint32)
	}
	// New subsurfaces go on top of the parent's stack.
	c.subsurfaceOrder[parent] = append(c.subsurfaceOrder[parent], id)
	return nil
}

// --- WlSubsurfaceHandler ---

func (c *Client) HandleWlSubsurfaceDestroy(senderID uint32) error {
	c.mu.Lock()
	var surfaceID uint32
	var gone bool
	if ss, ok := c.subsurfaces.get(senderID); ok {
		surfaceID = ss.SurfaceID
		c.subsurfaceOrder[ss.ParentID] = removeUint32(c.subsurfaceOrder[ss.ParentID], senderID)
		gone = true
	}
	c.subsurfaces.remove(senderID)
	c.unregisterObject(senderID)
	c.mu.Unlock()

	if gone && c.server.onSubsurfaceGone != nil {
		c.server.onSubsurfaceGone(c.ID(), surfaceID)
	}
	return nil
}

func (c *Client) HandleWlSubsurfaceSetPosition(senderID uint32, x int32, y int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Takes effect on the next commit of the subsurface (synchronized
	// subsurfaces defer to the parent's commit).
	c.subsurfaces.mutate(senderID, func(ss *Subsurface) {
		ss.PendingX, ss.PendingY = x, y
		ss.HasPending = true
	})
	return nil
}

func (c *Client) HandleWlSubsurfacePlaceAbove(senderID uint32, sibling uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reorderSubsurface(senderID, sibling, true)
	return nil
}

func (c *Client) HandleWlSubsurfacePlaceBelow(senderID uint32, sibling uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reorderSubsurface(senderID, sibling, false)
	return nil
}

func (c *Client) HandleWlSubsurfaceSetSync(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subsurfaces.mutate(senderID, func(ss *Subsurface) { ss.Sync = true })
	return nil
}

func (c *Client) HandleWlSubsurfaceSetDesync(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subsurfaces.mutate(senderID, func(ss *Subsurface) { ss.Sync = false })
	return nil
}

// reorderSubsurface moves senderID next to sibling in the parent's stack.
func (c *Client) reorderSubsurface(senderID, sibling uint32, above bool) {
	ss, ok := c.subsurfaces.get(senderID)
	if !ok {
		return
	}
	list := c.subsurfaceOrder[ss.ParentID]
	// Remove the mover.
	list = removeUint32(list, senderID)
	// Find the sibling's position in the trimmed list.
	idx := indexUint32(list, sibling)
	if idx < 0 {
		// Sibling unknown: keep the mover at the end.
		c.subsurfaceOrder[ss.ParentID] = append(list, senderID)
		return
	}
	pos := idx + 1 // above
	if !above {
		pos = idx
	}
	list = append(list, 0)
	copy(list[pos+1:], list[pos:])
	list[pos] = senderID
	c.subsurfaceOrder[ss.ParentID] = list
}

func removeUint32(s []uint32, v uint32) []uint32 {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func indexUint32(s []uint32, v uint32) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// --- ZwlrLayerShellV1Handler / ZwlrLayerSurfaceV1Handler ---

func (c *Client) HandleZwlrLayerShellV1GetLayerSurface(senderID, id, surface, output, layer uint32, namespace string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjLayerSurface)
	c.layerSurfaces.add(id, LayerSurface{
		ID: id, SurfaceID: surface, OutputID: output,
		Layer: layer, Namespace: namespace, Active: true,
	})
	return nil
}

func (c *Client) HandleZwlrLayerShellV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetSize(senderID, width, height uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.Width, ls.Height = int32(width), int32(height) })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetAnchor(senderID, anchor uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.Anchor = anchor })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetExclusiveZone(senderID uint32, zone int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.ExclusiveZone = zone })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetMargin(senderID uint32, top, right, bottom, left int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.Margin = [4]int32{top, right, bottom, left} })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetKeyboardInteractivity(senderID, mode uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.KeyboardInteractivity = mode })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetLayer(senderID, layer uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.Layer = layer })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1SetExclusiveEdge(senderID, edge uint32) error {
	// Exclusive edge only matters when the exclusive zone is -1 (choose the
	// edge automatically); we always anchor, so it is a no-op here.
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1GetPopup(senderID, popup uint32) error {
	// Layer-surface popups (e.g. waybar tooltips) are not composited yet;
	// recognized so a client's request does not error.
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1AckConfigure(senderID, serial uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerSurfaces.mutate(senderID, func(ls *LayerSurface) { ls.LastSerial = serial })
	return nil
}

func (c *Client) HandleZwlrLayerSurfaceV1Destroy(senderID uint32) error {
	c.mu.Lock()
	var surfaceID uint32
	var gone bool
	if ls, ok := c.layerSurfaces.get(senderID); ok {
		surfaceID = ls.SurfaceID
		gone = true
	}
	c.layerSurfaces.remove(senderID)
	c.unregisterObject(senderID)
	c.mu.Unlock()

	if gone && c.server.onLayerSurfaceGone != nil {
		c.server.onLayerSurfaceGone(c.ID(), surfaceID)
	}
	return nil
}

// --- ZxdgOutputManagerV1Handler / ZxdgOutputV1Handler ---

func (c *Client) HandleZxdgOutputManagerV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleZxdgOutputManagerV1GetXdgOutput(senderID, id, output uint32) error {
	c.mu.Lock()
	c.registerObject(id, ObjXdgOutput)
	out := c.server.Output()
	w := c.conn.WriterRef()
	// Single logical output at the origin; name/description let clients
	// (waybar et al.) match panels to outputs.
	_ = xdgoutput.SendZxdgOutputV1LogicalPosition(w, id, 0, 0)
	_ = xdgoutput.SendZxdgOutputV1LogicalSize(w, id, int32(out.Width), int32(out.Height))
	_ = xdgoutput.SendZxdgOutputV1Name(w, id, "STRATIFI-1")
	_ = xdgoutput.SendZxdgOutputV1Description(w, id, "stratifi output 1")
	_ = xdgoutput.SendZxdgOutputV1Done(w, id)
	_ = c.conn.Flush()
	c.mu.Unlock()
	return nil
}

func (c *Client) HandleZxdgOutputV1Destroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// layerConfigureSize resolves the size to configure a layer surface with:
// a requested 0 fills the anchored dimension of the output, minus margins.
func (c *Client) layerConfigureSize(ls *LayerSurface) (int32, int32) {
	const (
		anchorTop    = 1
		anchorBottom = 2
		anchorLeft   = 4
		anchorRight  = 8
	)
	out := c.server.Output()
	w, h := ls.Width, ls.Height
	if w == 0 && ls.Anchor&(anchorLeft|anchorRight) == (anchorLeft|anchorRight) {
		w = int32(out.Width) - ls.Margin[1] - ls.Margin[3]
	}
	if h == 0 && ls.Anchor&(anchorTop|anchorBottom) == (anchorTop|anchorBottom) {
		h = int32(out.Height) - ls.Margin[0] - ls.Margin[2]
	}
	return w, h
}

// applyLayerCommit propagates a wl_surface.commit to a layer surface: the
// first commit gets a configure (so the client can map), and every commit
// re-emits the state so the actor can place it. Emits run unlocked.
func (c *Client) applyLayerCommit(surfaceID uint32) {
	c.mu.Lock()
	var ls *LayerSurface
	for i := range c.layerSurfaces.items {
		if c.layerSurfaces.items[i].rec.SurfaceID == surfaceID && c.layerSurfaces.items[i].rec.Active {
			ls = &c.layerSurfaces.items[i].rec
			break
		}
	}
	if ls == nil {
		c.mu.Unlock()
		return
	}
	needConfigure := !ls.Configured
	w, h := c.layerConfigureSize(ls)
	if needConfigure {
		ls.Configured = true
		serial := c.NextSerial()
		ls.LastSerial = serial
		lw := c.conn.WriterRef()
		_ = layershell.SendZwlrLayerSurfaceV1Configure(lw, ls.ID, serial, uint32(w), uint32(h))
		_ = c.conn.Flush()
	}
	info := LayerSurfaceInfo{
		ClientID: c.ID(), SurfaceID: ls.SurfaceID, Layer: ls.Layer,
		Namespace: ls.Namespace, Width: w, Height: h, Anchor: ls.Anchor,
		ExclusiveZone: ls.ExclusiveZone, Margin: ls.Margin,
		KeyboardInteractivity: ls.KeyboardInteractivity,
	}
	c.mu.Unlock()

	if c.server.onLayerSurface != nil {
		c.server.onLayerSurface(info)
	}
}

// subsurfaceOrderOf returns the stack index of a subsurface object id.
func (c *Client) subsurfaceOrderOf(id, parent uint32) int32 {
	for i, x := range c.subsurfaceOrder[parent] {
		if x == id {
			return int32(i)
		}
	}
	return 0
}

// --- WlDataDeviceManagerHandler ---

func (c *Client) HandleWlDataDeviceManagerCreateDataSource(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjDataSource)
	c.dataSources.add(id, DataSource{ID: id, Active: true})
	return nil
}

func (c *Client) HandleWlDataDeviceManagerGetDataDevice(senderID uint32, id uint32, seat uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjDataDevice)
	c.dataDeviceID = id
	return nil
}

func (c *Client) HandleWlDataDeviceManagerRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- WlDataDeviceHandler ---

func (c *Client) HandleWlDataDeviceStartDrag(senderID uint32, source uint32, origin uint32, icon uint32, serial uint32) error {
	return nil
}

// HandleWlDataDeviceSetSelection makes this client the clipboard owner:
// remember which data source cross-references pasting clients' offers.
func (c *Client) HandleWlDataDeviceSetSelection(senderID uint32, source uint32, serial uint32) error {
	c.mu.Lock()
	mimes := []string(nil)
	if src, ok := c.dataSources.get(source); ok {
		mimes = append([]string(nil), src.MimeTypes...)
	}
	c.mu.Unlock()

	if mimes == nil {
		return fmt.Errorf("set_selection: unknown data source %d in client %d", source, c.id)
	}
	c.server.setSelection(Selection{
		OwnerID:   c.id,
		SourceID:  source,
		MimeTypes: mimes,
	})
	return nil
}

func (c *Client) HandleWlDataDeviceRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- WlDataSourceHandler ---

// HandleWlDataSourceOffer records a mime type offered by the source; the
// selection machinery broadcasts it to pasting clients.
func (c *Client) HandleWlDataSourceOffer(senderID uint32, mimeType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if src, ok := c.dataSources.get(senderID); ok {
		if !containsString(src.MimeTypes, mimeType) {
			src.MimeTypes = append(src.MimeTypes, mimeType)
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (c *Client) HandleWlDataSourceDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	c.dataSources.mutate(senderID, func(src *DataSource) { src.Active = false })
	c.server.clearSelectionIfOwner(c.id, senderID)
	return nil
}

func (c *Client) HandleWlDataSourceSetActions(senderID uint32, dndActions uint32) error {
	return nil
}

// Close terminates client connection and cleans up all mapped SHM pools.
// Uses its own closeMu: Close is called from BOTH the server's teardown
// and the connection's deferred cleanup, and must never tangle with
// handlers holding c.mu. The closed flag is the idempotence gate; Set's
// second-value return makes double Close cheap and safe.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	for _, p := range c.pools.items {
		_ = p.Close()
	}
	c.pools = poolStore{}
	return c.conn.Close()
}

// --- WlDataOfferHandler ---

// HandleWlDataOfferAccept records the paste client's chosen type (or the
// empty string to decline). No-where needed yet; ACCEPT MUST NOT
// unregister the offer — that bug made paste offers vanish.
func (c *Client) HandleWlDataOfferAccept(senderID uint32, serial uint32, mimeType string) error {
	return nil
}

// HandleWlDataOfferReceive routes a paste: hand the fd to the clipboard
// OWNER client via wl_data_source.send; the owner writes the bytes into
// the fd directly. The compositor stores nothing.
func (c *Client) HandleWlDataOfferReceive(senderID uint32, mimeType string, fd int) error {
	// On error paths the fd must not leak; on success the OWNER closes it
	// after writing (Wayland contract for data_source.send).
	owner := c.server.clipboardOwner(mimeType)
	if owner == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("receive: no source offers %q", mimeType)
	}
	if err := owner.SendDataSourceSend(c.server.selection.SourceID, mimeType, fd); err != nil {
		_ = unix.Close(fd)
		return err
	}
	return nil
}

func (c *Client) HandleWlDataOfferDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleWlDataOfferFinish(senderID uint32) error {
	return nil
}

func (c *Client) HandleWlDataOfferSetActions(senderID uint32, dndActions uint32, preferredAction uint32) error {
	return nil
}

// SnapshotSurfaceBuffer copies the surface's currently attached shm buffer
// contents into dst under the client mutex, returning the buffer geometry.
//
// The copy happens under c.mu so the source mapping cannot be unmapped
// (pool destroy / client teardown) mid-copy: goroutines outside the client's
// dispatch loop — the compositor blit path — must use this instead of the
// raw GetSurfaceBuffer slice, whose backing map is only valid while the
// caller holds c.mu.
func (c *Client) SnapshotSurfaceBuffer(surfaceID uint32, dst []byte) (copied int, width, height, stride int32, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	surf, found := c.surfaces.get(surfaceID)
	if !found || !surf.Active || surf.Current.AttachedBufferID == 0 {
		return 0, 0, 0, 0, false
	}
	buf, found := c.buffers.get(surf.Current.AttachedBufferID)
	if !found {
		return 0, 0, 0, 0, false
	}
	pool, found := c.pools.get(buf.PoolID)
	if !found {
		return 0, 0, 0, 0, false
	}
	start := int(buf.Offset)
	end := start + int(buf.Stride*buf.Height)
	if start < 0 || end > len(pool.Data) {
		return 0, 0, 0, 0, false
	}
	src := pool.Data[start:end]
	n := copy(dst, src)
	return n, buf.Width, buf.Height, buf.Stride, true
}

// SurfaceBufferInfo reports the geometry of the surface's attached shm
// buffer without touching pixel data (for sizing a snapshot scratch buffer).
func (c *Client) SurfaceBufferInfo(surfaceID uint32) (width, height, stride int32, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	surf, found := c.surfaces.get(surfaceID)
	if !found || !surf.Active || surf.Current.AttachedBufferID == 0 {
		return 0, 0, 0, false
	}
	buf, found := c.buffers.get(surf.Current.AttachedBufferID)
	if !found {
		return 0, 0, 0, false
	}
	if _, found := c.pools.get(buf.PoolID); !found {
		return 0, 0, 0, false
	}
	return buf.Width, buf.Height, buf.Stride, true
}

// GiveClipboardOffer creates a wl_data_offer for this client advertising the
// current selection's mime types, then sends wl_data_device.selection. Call
// when the client receives keyboard focus (the standard paste handshake).
// No-op when the clipboard is empty. Returns the offer id (0 if none).
func (c *Client) GiveClipboardOffer() uint32 {
	sel := c.server.GetSelection()
	if sel.OwnerID == 0 || sel.OwnerID == c.id {
		return 0 // no content, or pasting from itself
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() || c.dataDeviceID == 0 {
		return 0
	}

	// Compositor-chosen id: one past the highest the client ever used.
	// Wayland lets the compositor pick new ids for data_offer events; going
	// above the client's high-water mark avoids ever colliding with
	// client-chosen ids (which may reuse destroyed slots).
	offerID := c.objects.high
	c.registerObject(offerID, ObjDataOffer)

	w := c.conn.WriterRef()
	_ = wayland.SendWlDataDeviceDataOffer(w, c.dataDeviceID, offerID)
	for _, mime := range sel.MimeTypes {
		_ = wayland.SendWlDataOfferOffer(w, offerID, mime)
	}
	_ = wayland.SendWlDataDeviceSelection(w, c.dataDeviceID, offerID)
	_ = c.conn.Flush()
	return offerID
}
