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
	ObjLayerShell
	ObjLayerSurface
	ObjXdgOutputManager
	ObjXdgOutput
	ObjIdleNotifier
	ObjIdleNotification
	ObjIdleInhibitManager
	ObjIdleInhibitor
	ObjTextInputManager
	ObjTextInput
	ObjForeignToplevelList
	ObjForeignToplevelHandle

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

// Subsurface is one wl_subsurface: a child surface composited relative to
// its parent wl_surface. Position is parent-relative. Sync defers state
// application until the parent surface commits (the protocol default);
// Desync applies on the subsurface's own commit.
type Subsurface struct {
	ID        uint32
	SurfaceID uint32 // the child wl_surface
	ParentID  uint32 // the parent wl_surface object id
	X, Y      int32  // current position, relative to the parent surface origin

	PendingX, PendingY int32 // set_position writes here
	HasPending         bool

	Sync   bool // true = synchronized (default)
	Active bool

	// Last emitted state, so a parent's repeated commits do not spam the
	// actor with identical updates.
	lastX, lastY, lastW, lastH int32
	lastMapped                 bool
	lastOrder                  int32
	emitted                    bool
}

// LayerSurface is one zwlr_layer_surface_v1: a panel/bar/lock surface
// composited in a layer above or below the windows, positioned by anchors
// and margins, optionally reserving an exclusive zone the tiling must not
// cover.
type LayerSurface struct {
	ID        uint32
	SurfaceID uint32 // the wl_surface
	OutputID  uint32 // requested wl_output (0 = compositor default)
	Layer     uint32 // 0 background, 1 bottom, 2 top, 3 overlay
	Namespace string

	Width, Height         int32 // requested size; 0 = fill the anchored dimension
	Anchor                uint32
	ExclusiveZone         int32
	Margin                [4]int32 // top, right, bottom, left
	KeyboardInteractivity uint32

	Configured bool
	LastSerial uint32
	Active     bool
}

// LayerSurfaceInfo is the actor-facing snapshot of a layer surface (the
// fields the compositor needs to place and draw it).
type LayerSurfaceInfo struct {
	ClientID  uint32
	SurfaceID uint32
	Layer     uint32
	Namespace string

	Width, Height         int32
	Anchor                uint32
	ExclusiveZone         int32
	Margin                [4]int32
	KeyboardInteractivity uint32
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

// TextInputState holds the double-buffered zwp_text_input_v3 state for a client.
type TextInputState struct {
	Enabled         bool
	SurroundingText string
	Cursor          int32
	Anchor          int32
	ChangeCause     uint32
	ContentHint     uint32
	ContentPurpose  uint32
	CursorRect      [4]int32 // x, y, w, h in surface-local coords
	Serial          uint32   // number of commit requests processed
}

// TextInput tracks one zwp_text_input_v3 instance bound to a seat.
type TextInput struct {
	ID      uint32
	SeatID  uint32
	Surface uint32 // currently entered surface (0 = none)
	Pending TextInputState
	Current TextInputState
	Active  bool
}
