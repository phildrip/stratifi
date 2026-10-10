package server

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"bloxwm/internal/protocol/capture"
	"bloxwm/internal/protocol/wayland"
	"bloxwm/internal/protocol/xdgshell"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
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

	serial atomic.Uint32

	pointerID  uint32
	closed     atomic.Bool
	keyboardID uint32

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

		// Flush all generated events back to the client
		if err := c.conn.Flush(); err != nil {
			return fmt.Errorf("flush error: %w", err)
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
	ObjSubsurface:    func(c *Client, r *wire.Reader, h wire.Header) error { return wayland.DispatchWlSubsurface(r, h, c) },
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
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalOutput, "wl_output", 2); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalSeat, "wl_seat", 5); err != nil {
		return err
	}
	if err := wayland.SendWlRegistryGlobal(w, registryID, GlobalSubcompositor, "wl_subcompositor", 1); err != nil {
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
		_ = wayland.SendWlOutputGeometry(w, id, 0, 0, 320, 240, 0, "bloxwm", "bloxwm-display", 0)
		_ = wayland.SendWlOutputScale(w, id, 1)
		out := c.server.Output()
		_ = wayland.SendWlOutputMode(w, id, 0x3,
			int32(out.Width), int32(out.Height), int32(out.RefreshHz*1000))
		_ = wayland.SendWlOutputDone(w, id)
	case GlobalSeat:
		c.registerObject(id, ObjSeat)
		w := c.conn.WriterRef()
		_ = wayland.SendWlSeatCapabilities(w, id, 3) // Pointer | Keyboard
		_ = wayland.SendWlSeatName(w, id, "seat0")
	case GlobalSubcompositor:
		c.registerObject(id, ObjSubcompositor)
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
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	c.surfaces.mutate(senderID, func(sf *Surface) { sf.Active = false })
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

	// Notify compositor of surface update outside of c.mu
	if c.server.onCommit != nil {
		c.server.onSurfaceCommitted(&surfCopy)
	}
	return nil
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

	if os.Getenv("BLOXWM_TRACE") != "" {
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
	if os.Getenv("BLOXWM_TRACE") != "" {
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
	return nil
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
	return nil
}

// --- WlSubsurfaceHandler ---

func (c *Client) HandleWlSubsurfaceDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleWlSubsurfaceSetPosition(senderID uint32, x int32, y int32) error {
	return nil
}

func (c *Client) HandleWlSubsurfacePlaceAbove(senderID uint32, sibling uint32) error {
	return nil
}

func (c *Client) HandleWlSubsurfacePlaceBelow(senderID uint32, sibling uint32) error {
	return nil
}

func (c *Client) HandleWlSubsurfaceSetSync(senderID uint32) error {
	return nil
}

func (c *Client) HandleWlSubsurfaceSetDesync(senderID uint32) error {
	return nil
}

// --- WlDataDeviceManagerHandler ---

func (c *Client) HandleWlDataDeviceManagerCreateDataSource(senderID uint32, id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjDataSource)
	return nil
}

func (c *Client) HandleWlDataDeviceManagerGetDataDevice(senderID uint32, id uint32, seat uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerObject(id, ObjDataDevice)
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

func (c *Client) HandleWlDataDeviceSetSelection(senderID uint32, source uint32, serial uint32) error {
	return nil
}

func (c *Client) HandleWlDataDeviceRelease(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

// --- WlDataSourceHandler ---

func (c *Client) HandleWlDataSourceOffer(senderID uint32, mimeType string) error {
	return nil
}

func (c *Client) HandleWlDataSourceDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
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

func (c *Client) HandleWlDataOfferAccept(senderID uint32, serial uint32, mimeType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregisterObject(senderID)
	return nil
}

func (c *Client) HandleWlDataOfferReceive(senderID uint32, mimeType string, fd int) error {
	// The compositor does not source selection data yet; close the offered
	// fd so the client is not left hanging on a receive.
	_ = unix.Close(fd)
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
