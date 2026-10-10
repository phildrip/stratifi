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
	ObjXdgPopup
	ObjXdgPositioner
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

	// objTypeMax marks the end of the enum for table-driven dispatch.
	objTypeMax
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

// DataSource is one client's offered data source: the mime types it
// provides for selection transfers (and DnD later).
type DataSource struct {
	ID        uint32
	MimeTypes []string
	Active    bool
}

// Selection is the compositor's clipboard: the last client to call
// wl_data_device.set_selection owns it until replaced or destroyed.
// Offers present live as DataSource records registered per client.
type Selection struct {
	OwnerID   uint32   // client id of the source's owner (0 = none)
	SourceID  uint32   // wl_data_source object id in the owner client
	MimeTypes []string // types offered, order = preference
}

// XdgPositioner accumulates the xdg_positioner state a client assembles
// before requesting a popup placement.
type XdgPositioner struct {
	ID                   uint32
	Width                int32 // requested popup size
	Height               int32
	AnchorRect           [4]int32 // x, y, w, h in parent surface coords
	AnchorMask           uint32   // edge bits for anchor point
	GravityMask          uint32   // edge bits for placement direction
	ConstraintAdjustment uint32
	OffsetX              int32
	OffsetY              int32
	ParentWidth          int32
	ParentHeight         int32
	Active               bool
}

// XdgPopup tracks one popup surface and its placement over a parent.
type XdgPopup struct {
	ID           uint32
	SurfaceID    uint32 // wl_surface
	XdgSurfaceID uint32 // owning xdg_surface (for configure serials)
	Parent       uint32 // parent xdg_surface id
	Grabs        uint32 // seat grabs taken (input modal while > 0)
	Configured   bool
	ConfiguredX  int32 // placement relative to parent body origin
	ConfiguredY  int32
	ConfiguredW  int32 // committed popup size
	ConfiguredH  int32
	LastSerial   uint32
	Active       bool
}
