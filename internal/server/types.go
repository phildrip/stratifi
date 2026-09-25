package server

import (
	"golang.org/x/sys/unix"
)

// ObjectType identifies the Wayland interface implemented by an object slot.
type ObjectType uint16

const (
	ObjNone ObjectType = iota
	ObjDisplay
	ObjRegistry
	ObjCallback
	ObjCompositor
	ObjShm
	ObjShmPool
	ObjBuffer
	ObjSurface
	ObjRegion
	ObjXdgWmBase
	ObjXdgSurface
	ObjXdgToplevel
)

// ObjectSlot represents an entry in the client's object lookup table.
type ObjectSlot struct {
	ID     uint32
	Type   ObjectType
	Active bool
}

// ShmPool represents a memory-mapped shared memory pool created by a client.
type ShmPool struct {
	ID   uint32
	FD   int
	Size int
	Data []byte
}

// Close unmaps the shared memory and closes the file descriptor.
func (p *ShmPool) Close() error {
	if len(p.Data) > 0 {
		_ = unix.Munmap(p.Data)
		p.Data = nil
	}
	if p.FD >= 0 {
		_ = unix.Close(p.FD)
		p.FD = -1
	}
	return nil
}

// ShmBuffer represents a 2D pixel buffer inside an ShmPool.
type ShmBuffer struct {
	ID       uint32
	PoolID   uint32
	Offset   int32
	Width    int32
	Height   int32
	Stride   int32
	Format   uint32
	Released bool
}

// SurfaceState holds current and pending state for a wl_surface.
type SurfaceState struct {
	AttachedBufferID uint32
	BufferOffsetX    int32
	BufferOffsetY    int32
	DamageX          int32
	DamageY          int32
	DamageW          int32
	DamageH          int32
	HasDamage        bool
}

// Surface represents a client Wayland surface (window, popup, or subsurface).
type Surface struct {
	ID       uint32
	ClientID uint32
	Current  SurfaceState
	Pending  SurfaceState
	Active   bool
}
