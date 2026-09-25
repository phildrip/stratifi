package server

import (
	"fmt"
	"sync"
	"sync/atomic"

	"bloxwm/internal/protocol/wayland"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// Global numeric IDs advertised to clients via wl_registry.
const (
	GlobalCompositor uint32 = 1
	GlobalShm        uint32 = 2
	GlobalXdgWmBase   uint32 = 3
)

// Client represents a connected Wayland client and its object hierarchy.
type Client struct {
	id     uint32
	conn   *wire.Conn
	server *Server

	mu       sync.Mutex
	objects  []ObjectSlot
	pools    []ShmPool
	buffers  []ShmBuffer
	surfaces []Surface

	serial atomic.Uint32
	closed bool
}

func newClient(id uint32, conn *wire.Conn, s *Server) *Client {
	c := &Client{
		id:       id,
		conn:     conn,
		server:   s,
		objects:  make([]ObjectSlot, 0, 64),
		pools:    make([]ShmPool, 0, 16),
		buffers:  make([]ShmBuffer, 0, 32),
		surfaces: make([]Surface, 0, 16),
	}
	// Object ID 1 is always the wl_display singleton
	c.registerObject(1, ObjDisplay)
	return c
}

// NextSerial returns a monotonically increasing serial number for events.
func (c *Client) NextSerial() uint32 {
	return c.serial.Add(1)
}

func (c *Client) registerObject(id uint32, t ObjectType) {
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
		reader, err := c.conn.ReadMsg()
		if err != nil {
			return err
		}

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
	default:
		return fmt.Errorf("no dispatcher for object type %d (id %d)", objType, hdr.ObjectID)
	}
}

// --- WlDisplayHandler ---

func (c *Client) HandleSync(senderID uint32, callback uint32) error {
	w := c.conn.WriterRef()
	serial := c.NextSerial()
	if err := wayland.SendWlCallbackDone(w, callback, serial); err != nil {
		return err
	}
	return wayland.SendWlDisplayDeleteId(w, 1, callback)
}

func (c *Client) HandleGetRegistry(senderID uint32, registryID uint32) error {
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
	return wayland.SendWlRegistryGlobal(w, registryID, GlobalXdgWmBase, "xdg_wm_base", 3)
}

// --- WlRegistryHandler ---

func (c *Client) HandleBind(senderID uint32, name uint32, ifaceName string, version uint32, id uint32) error {
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
	default:
		return fmt.Errorf("unknown global name %d (iface %s)", name, ifaceName)
	}
	return nil
}

// --- WlCompositorHandler ---

func (c *Client) HandleCreateSurface(senderID uint32, id uint32) error {
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

func (c *Client) HandleCreateRegion(senderID uint32, id uint32) error {
	c.mu.Lock()
	c.registerObject(id, ObjRegion)
	c.mu.Unlock()
	return nil
}

func (c *Client) HandleRelease(senderID uint32) error {
	c.mu.Lock()
	c.unregisterObject(senderID)
	c.mu.Unlock()
	return nil
}

// --- WlShmHandler ---

func (c *Client) HandleCreatePool(senderID uint32, id uint32, fd int, size int32) error {
	if size <= 0 {
		_ = unix.Close(fd)
		return fmt.Errorf("invalid pool size: %d", size)
	}

	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
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

// --- WlShmPoolHandler & WlBufferHandler & WlSurfaceHandler ---

func (c *Client) HandleCreateBuffer(senderID uint32, id uint32, offset int32, width int32, height int32, stride int32, format uint32) error {
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

func (c *Client) HandleDestroy(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.unregisterObject(senderID)

	// Clean up if it was a pool
	for i := range c.pools {
		if c.pools[i].ID == senderID {
			_ = c.pools[i].Close()
			c.pools = append(c.pools[:i], c.pools[i+1:]...)
			return nil
		}
	}

	// Clean up if it was a buffer
	for i := range c.buffers {
		if c.buffers[i].ID == senderID {
			c.buffers = append(c.buffers[:i], c.buffers[i+1:]...)
			return nil
		}
	}

	// Clean up if it was a surface
	for i := range c.surfaces {
		if c.surfaces[i].ID == senderID {
			c.surfaces[i].Active = false
			return nil
		}
	}

	return nil
}

func (c *Client) HandleResize(senderID uint32, size int32) error {
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

// --- WlSurfaceHandler ---

func (c *Client) HandleAttach(senderID uint32, buffer uint32, x int32, y int32) error {
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

func (c *Client) HandleDamage(senderID uint32, x int32, y int32, width int32, height int32) error {
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

func (c *Client) HandleDamageBuffer(senderID uint32, x int32, y int32, width int32, height int32) error {
	return c.HandleDamage(senderID, x, y, width, height)
}

func (c *Client) HandleFrame(senderID uint32, callback uint32) error {
	c.mu.Lock()
	c.registerObject(callback, ObjCallback)
	c.mu.Unlock()

	w := c.conn.WriterRef()
	timeMs := uint32(c.NextSerial() * 16)
	_ = wayland.SendWlCallbackDone(w, callback, timeMs)
	return wayland.SendWlDisplayDeleteId(w, 1, callback)
}

func (c *Client) HandleGetRelease(senderID uint32, callback uint32) error {
	return nil
}

func (c *Client) HandleSetOpaqueRegion(senderID uint32, region uint32) error {
	return nil
}

func (c *Client) HandleSetInputRegion(senderID uint32, region uint32) error {
	return nil
}

func (c *Client) HandleCommit(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.surfaces {
		if c.surfaces[i].Active && c.surfaces[i].ID == senderID {
			// Atomic state commit: pending -> current
			c.surfaces[i].Current = c.surfaces[i].Pending
			c.surfaces[i].Pending.HasDamage = false

			// Notify compositor of surface update
			c.server.onSurfaceCommitted(&c.surfaces[i])
			return nil
		}
	}
	return fmt.Errorf("surface %d not found for commit", senderID)
}

func (c *Client) HandleSetBufferTransform(senderID uint32, transform int32) error {
	return nil
}

func (c *Client) HandleSetBufferScale(senderID uint32, scale int32) error {
	return nil
}

func (c *Client) HandleOffset(senderID uint32, x int32, y int32) error {
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
