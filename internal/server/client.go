package server

import (
	"fmt"
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

	mu           sync.Mutex
	objects      []ObjectSlot
	pools        []ShmPool
	buffers      []ShmBuffer
	surfaces     []Surface
	xdgSurfaces  []XdgSurface
	xdgToplevels []XdgToplevel

	serial atomic.Uint32
	closed bool

	pointerID  uint32
	keyboardID uint32

	capture *CaptureState
}

func newClient(id uint32, conn *wire.Conn, s *Server) *Client {
	c := &Client{
		id:           id,
		conn:         conn,
		server:       s,
		objects:      make([]ObjectSlot, 0, 64),
		pools:        make([]ShmPool, 0, 16),
		buffers:      make([]ShmBuffer, 0, 32),
		surfaces:     make([]Surface, 0, 16),
		xdgSurfaces:  make([]XdgSurface, 0, 16),
		xdgToplevels: make([]XdgToplevel, 0, 16),
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
	for i := range c.objects {
		if c.objects[i].ID == id {
			c.objects[i].Type = t
			c.objects[i].Active = true
			return
		}
	}
	for i := range c.objects {
		if !c.objects[i].Active {
			c.objects[i] = ObjectSlot{ID: id, Type: t, Active: true}
			return
		}
	}
	c.objects = append(c.objects, ObjectSlot{ID: id, Type: t, Active: true})
}

func (c *Client) unregisterObject(id uint32) {
	for i := range c.objects {
		if c.objects[i].ID == id {
			c.objects[i].Active = false
			return
		}
	}
}

func (c *Client) findBuffer(id uint32) (ShmBuffer, bool) {
	for i := range c.buffers {
		if c.buffers[i].ID == id {
			return c.buffers[i], true
		}
	}
	return ShmBuffer{}, false
}

func (c *Client) findPool(id uint32) (*ShmPool, bool) {
	for i := range c.pools {
		if c.pools[i].ID == id {
			return &c.pools[i], true
		}
	}
	return nil, false
}

func (c *Client) findObject(id uint32) (ObjectType, bool) {
	for i := range c.objects {
		if c.objects[i].Active && c.objects[i].ID == id {
			return c.objects[i].Type, true
		}
	}
	return ObjNone, false
}

// Run executes the message reading and dispatch loop for this client.
func (c *Client) Run() error {
	defer c.Close()

	for {
		fmt.Printf("[CLIENT %d] waiting for next ReadMsg...\n", c.id)
		reader, err := c.conn.ReadMsg()
		if err != nil {
			return err
		}
		fmt.Printf("[CLIENT %d] ReadMsg got %d bytes\n", c.id, reader.Remaining())

		// A single read may contain multiple back-to-back Wayland messages
		for reader.Remaining() >= wire.HeaderSize {
			hdr, err := reader.ReadHeader()
			if err != nil {
				return fmt.Errorf("error reading header: %w", err)
			}

			if err := c.dispatch(reader, hdr); err != nil {
				return fmt.Errorf("dispatch error for obj %d, op %d: %w", hdr.ObjectID, hdr.Opcode, err)
			}
		}

		// Flush all generated events back to the client
		if err := c.conn.Flush(); err != nil {
			return fmt.Errorf("flush error: %w", err)
		}
	}
}

// dispatch routes incoming messages based on the registered object type.
func (c *Client) dispatch(r *wire.Reader, hdr wire.Header) error {
	c.mu.Lock()
	objType, found := c.findObject(hdr.ObjectID)
	c.mu.Unlock()

	if !found {
		return fmt.Errorf("unknown object ID %d (opcode %d)", hdr.ObjectID, hdr.Opcode)
	}
	fmt.Printf("[SERVER DISPATCH] obj=%d op=%d (type=%d)\n", hdr.ObjectID, hdr.Opcode, objType)

	switch objType {
	case ObjDisplay:
		return wayland.DispatchWlDisplay(r, hdr, c)
	case ObjRegistry:
		return wayland.DispatchWlRegistry(r, hdr, c)
	case ObjCompositor:
		return wayland.DispatchWlCompositor(r, hdr, c)
	case ObjShm:
		return wayland.DispatchWlShm(r, hdr, c)
	case ObjShmPool:
		return wayland.DispatchWlShmPool(r, hdr, c)
	case ObjSurface:
		return wayland.DispatchWlSurface(r, hdr, c)
	case ObjBuffer:
		return wayland.DispatchWlBuffer(r, hdr, c)
	case ObjXdgWmBase:
		return xdgshell.DispatchXdgWmBase(r, hdr, c)
	case ObjXdgSurface:
		return xdgshell.DispatchXdgSurface(r, hdr, c)
	case ObjXdgToplevel:
		return xdgshell.DispatchXdgToplevel(r, hdr, c)
	case ObjOutput:
		return wayland.DispatchWlOutput(r, hdr, c)
	case ObjSeat:
		return wayland.DispatchWlSeat(r, hdr, c)
	case ObjPointer:
		return wayland.DispatchWlPointer(r, hdr, c)
	case ObjKeyboard:
		return wayland.DispatchWlKeyboard(r, hdr, c)
	case ObjSubcompositor:
		return wayland.DispatchWlSubcompositor(r, hdr, c)
	case ObjSubsurface:
		return wayland.DispatchWlSubsurface(r, hdr, c)
	case ObjDataDeviceManager:
		return wayland.DispatchWlDataDeviceManager(r, hdr, c)
	case ObjDataDevice:
		return wayland.DispatchWlDataDevice(r, hdr, c)
	case ObjDataSource:
		return wayland.DispatchWlDataSource(r, hdr, c)
	case ObjRegion:
		return wayland.DispatchWlRegion(r, hdr, c)
	case ObjCaptureSourceManagerOutput:
		return capture.DispatchExtOutputImageCaptureSourceManagerV1(r, hdr, c)
	case ObjImageCopyCaptureManager:
		return capture.DispatchExtImageCopyCaptureManagerV1(r, hdr, c)
	case ObjCaptureSourceManagerToplevel:
		return capture.DispatchExtForeignToplevelImageCaptureSourceManagerV1(r, hdr, c)
	case ObjCaptureSource:
		return capture.DispatchExtImageCaptureSourceV1(r, hdr, c)
	case ObjCaptureSession:
		return capture.DispatchExtImageCopyCaptureSessionV1(r, hdr, c)
	case ObjCaptureFrame:
		return capture.DispatchExtImageCopyCaptureFrameV1(r, hdr, c)
	default:
		return fmt.Errorf("no dispatcher for object type %d (id %d)", objType, hdr.ObjectID)
	}
}

// --- WlDisplayHandler ---

func (c *Client) HandleWlDisplaySync(senderID uint32, callback uint32) error {
	fmt.Printf("[+] HandleWlDisplaySync: callback=%d\n", callback)
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
		_ = wayland.SendWlOutputMode(w, id, 0x3, 960, 640, 60000)
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
	c.surfaces = append(c.surfaces, Surface{
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
	c.pools = append(c.pools, ShmPool{
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
	c.buffers = append(c.buffers, ShmBuffer{
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
	for i := range c.pools {
		if c.pools[i].ID == senderID {
			_ = c.pools[i].Close()
			c.pools = append(c.pools[:i], c.pools[i+1:]...)
			break
		}
	}
	return nil
}

func (c *Client) HandleWlShmPoolResize(senderID uint32, size int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.pools {
		if c.pools[i].ID == senderID {
			_ = unix.Munmap(c.pools[i].Data)
			data, err := unix.Mmap(c.pools[i].FD, 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
			if err != nil {
				return fmt.Errorf("remap failed: %w", err)
			}
			c.pools[i].Size = int(size)
			c.pools[i].Data = data
			return nil
		}
	}
	return fmt.Errorf("pool %d not found for resize", senderID)
}

// --- WlBufferHandler ---

func (c *Client) HandleWlBufferDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	for i := range c.buffers {
		if c.buffers[i].ID == senderID {
			c.buffers = append(c.buffers[:i], c.buffers[i+1:]...)
			break
		}
	}
	return nil
}

// --- WlSurfaceHandler ---

func (c *Client) HandleWlSurfaceDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	for i := range c.surfaces {
		if c.surfaces[i].ID == senderID {
			c.surfaces[i].Active = false
			break
		}
	}
	return nil
}

func (c *Client) HandleWlSurfaceAttach(senderID uint32, buffer uint32, x int32, y int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.surfaces {
		if c.surfaces[i].Active && c.surfaces[i].ID == senderID {
			c.surfaces[i].Pending.AttachedBufferID = buffer
			c.surfaces[i].Pending.BufferOffsetX = x
			c.surfaces[i].Pending.BufferOffsetY = y
			return nil
		}
	}
	return fmt.Errorf("surface %d not found for attach", senderID)
}

func (c *Client) HandleWlSurfaceDamage(senderID uint32, x int32, y int32, width int32, height int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.surfaces {
		if c.surfaces[i].Active && c.surfaces[i].ID == senderID {
			c.surfaces[i].Pending.DamageX = x
			c.surfaces[i].Pending.DamageY = y
			c.surfaces[i].Pending.DamageW = width
			c.surfaces[i].Pending.DamageH = height
			c.surfaces[i].Pending.HasDamage = true
			return nil
		}
	}
	return fmt.Errorf("surface %d not found for damage", senderID)
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

	for i := range c.surfaces {
		if c.surfaces[i].Active && c.surfaces[i].ID == senderID {
			// Atomic state commit: pending -> current
			c.surfaces[i].Current = c.surfaces[i].Pending
			c.surfaces[i].Pending.HasDamage = false

			surfCopy := c.surfaces[i]
			c.mu.Unlock()

			// Notify compositor of surface update outside of c.mu
			if c.server.onCommit != nil {
				c.server.onSurfaceCommitted(&surfCopy)
			}
			return nil
		}
	}
	c.mu.Unlock()
	return fmt.Errorf("surface %d not found for commit", senderID)
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
	return nil
}

func (c *Client) HandleXdgWmBaseGetXdgSurface(senderID uint32, id uint32, surfaceID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.registerObject(id, ObjXdgSurface)
	c.xdgSurfaces = append(c.xdgSurfaces, XdgSurface{
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
	for i := range c.xdgSurfaces {
		if c.xdgSurfaces[i].ID == senderID {
			c.xdgSurfaces[i].Active = false
			break
		}
	}
	return nil
}

func (c *Client) HandleXdgSurfaceGetToplevel(senderID uint32, id uint32) error {
	c.mu.Lock()

	c.registerObject(id, ObjXdgToplevel)
	c.xdgToplevels = append(c.xdgToplevels, XdgToplevel{
		ID:           id,
		XdgSurfaceID: senderID,
		Active:       true,
	})

	for i := range c.xdgSurfaces {
		if c.xdgSurfaces[i].Active && c.xdgSurfaces[i].ID == senderID {
			c.xdgSurfaces[i].ToplevelID = id

			top := c.xdgToplevels[len(c.xdgToplevels)-1]
			if c.server.onToplevel != nil {
				c.mu.Unlock()
				c.server.onToplevelCreated(c, &top, senderID)
				return nil
			}

			// Send default initial configure sequence
			w := c.conn.WriterRef()
			_ = xdgshell.SendXdgToplevelConfigure(w, id, 0, 0, nil)

			serial := c.NextSerial()
			c.xdgSurfaces[i].LastSerial = serial
			_ = xdgshell.SendXdgSurfaceConfigure(w, senderID, serial)
			c.mu.Unlock()
			return nil
		}
	}
	c.mu.Unlock()
	return fmt.Errorf("xdg_surface %d not found for get_toplevel", senderID)
}

func (c *Client) HandleXdgSurfaceGetPopup(senderID uint32, id uint32, parent uint32, positioner uint32) error {
	return nil
}

func (c *Client) HandleXdgSurfaceSetWindowGeometry(senderID uint32, x int32, y int32, width int32, height int32) error {
	return nil
}

func (c *Client) HandleXdgSurfaceAckConfigure(senderID uint32, serial uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.xdgSurfaces {
		if c.xdgSurfaces[i].Active && c.xdgSurfaces[i].ID == senderID {
			c.xdgSurfaces[i].Configured = true
			return nil
		}
	}
	return nil
}

// --- XdgToplevelHandler ---

func (c *Client) HandleXdgToplevelDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)
	for i := range c.xdgToplevels {
		if c.xdgToplevels[i].ID == senderID {
			c.xdgToplevels[i].Active = false
			break
		}
	}
	return nil
}

func (c *Client) HandleXdgToplevelSetParent(senderID uint32, parent uint32) error {
	return nil
}

func (c *Client) HandleXdgToplevelSetTitle(senderID uint32, title string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.xdgToplevels {
		if c.xdgToplevels[i].Active && c.xdgToplevels[i].ID == senderID {
			c.xdgToplevels[i].Title = title
			return nil
		}
	}
	return nil
}

func (c *Client) HandleXdgToplevelSetAppId(senderID uint32, appId string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.xdgToplevels {
		if c.xdgToplevels[i].Active && c.xdgToplevels[i].ID == senderID {
			c.xdgToplevels[i].AppID = appId
			return nil
		}
	}
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

	for i := range c.xdgToplevels {
		if c.xdgToplevels[i].Active && c.xdgToplevels[i].ID == senderID {
			c.xdgToplevels[i].MaxWidth = width
			c.xdgToplevels[i].MaxHeight = height
			return nil
		}
	}
	return nil
}

func (c *Client) HandleXdgToplevelSetMinSize(senderID uint32, width int32, height int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.xdgToplevels {
		if c.xdgToplevels[i].Active && c.xdgToplevels[i].ID == senderID {
			c.xdgToplevels[i].MinWidth = width
			c.xdgToplevels[i].MinHeight = height
			return nil
		}
	}
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

// ConfigureToplevel sends xdg_toplevel.configure and xdg_surface.configure to client.
func (c *Client) ConfigureToplevel(xdgSurfaceID uint32, toplevelID uint32, width, height int32, states []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	w := c.conn.WriterRef()
	_ = xdgshell.SendXdgToplevelConfigure(w, toplevelID, width, height, states)
	serial := c.NextSerial()
	for i := range c.xdgSurfaces {
		if c.xdgSurfaces[i].ID == xdgSurfaceID {
			c.xdgSurfaces[i].LastSerial = serial
			break
		}
	}
	_ = xdgshell.SendXdgSurfaceConfigure(w, xdgSurfaceID, serial)
	_ = c.conn.Flush()
}

// GetSurfaceBuffer returns the raw mapped pixel slice and dimensions for the surface's current attached buffer.
func (c *Client) GetSurfaceBuffer(surfaceID uint32) (data []byte, width, height, stride int32, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var bufID uint32
	for i := range c.surfaces {
		if c.surfaces[i].Active && c.surfaces[i].ID == surfaceID {
			bufID = c.surfaces[i].Current.AttachedBufferID
			break
		}
	}
	if bufID == 0 {
		return nil, 0, 0, 0, false
	}

	var buf *ShmBuffer
	for i := range c.buffers {
		if c.buffers[i].ID == bufID {
			buf = &c.buffers[i]
			break
		}
	}
	if buf == nil {
		return nil, 0, 0, 0, false
	}

	for i := range c.pools {
		if c.pools[i].ID == buf.PoolID {
			pool := &c.pools[i]
			start := int(buf.Offset)
			end := start + int(buf.Stride*buf.Height)
			if start >= 0 && end <= len(pool.Data) {
				return pool.Data[start:end], buf.Width, buf.Height, buf.Stride, true
			}
			break
		}
	}

	return nil, 0, 0, 0, false
}

// ReleaseSurfaceBuffer sends wl_buffer.release to the currently attached buffer.
func (c *Client) ReleaseSurfaceBuffer(surfaceID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var bufID uint32
	for i := range c.surfaces {
		if c.surfaces[i].Active && c.surfaces[i].ID == surfaceID {
			bufID = c.surfaces[i].Current.AttachedBufferID
			break
		}
	}
	if bufID == 0 {
		return
	}
	w := c.conn.WriterRef()
	_ = wayland.SendWlBufferRelease(w, bufID)
}

// GetSurfaceIDForXdgSurface returns the underlying wl_surface ID for an xdg_surface ID.
func (c *Client) GetSurfaceIDForXdgSurface(xdgSurfaceID uint32) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.xdgSurfaces {
		if c.xdgSurfaces[i].Active && c.xdgSurfaces[i].ID == xdgSurfaceID {
			return c.xdgSurfaces[i].SurfaceID
		}
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
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	for i := range c.pools {
		_ = c.pools[i].Close()
	}
	c.pools = nil
	return c.conn.Close()
}
