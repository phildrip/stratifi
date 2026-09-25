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
	ObjOutput
	ObjSeat
	ObjPointer
	ObjKeyboard
	ObjSubcompositor
	ObjSubsurface
	ObjDataDeviceManager
	ObjDataDevice
	ObjDataSource
	ObjDataOffer

	// Screen capture protocol objects (ext-image-copy-capture-v1).
	ObjCaptureSourceManagerOutput
	ObjCaptureSourceManagerToplevel
	ObjCaptureSource
	ObjCaptureSession
	ObjCaptureFrame
	ObjCaptureCursorSession
	ObjImageCopyCaptureManager
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

// XdgSurface represents the xdg_surface wrapper around a wl_surface.
type XdgSurface struct {
	ID         uint32
	SurfaceID  uint32
	ToplevelID uint32
	Configured bool
	LastSerial uint32
	Active     bool
}

// XdgToplevel tracks properties of an application window.
type XdgToplevel struct {
	ID           uint32
	XdgSurfaceID uint32
	Title        string
	AppID        string
	MaxWidth     int32
	MaxHeight    int32
	MinWidth     int32
	MinHeight    int32
	Maximized    bool
	Fullscreen   bool
	Activated    bool
	Active       bool
}
