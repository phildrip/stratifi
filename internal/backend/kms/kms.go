// Package kms implements direct DRM/KMS communication via golang.org/x/sys/unix
// ioctls: connector/CRTC/encoder enumeration, dumb-buffer scanout framebuffers,
// legacy modesetting, page flips with vblank flip-complete events, and atomic
// commits.
//
// All structures mirror the Linux UAPI headers verbatim (<drm.h>, <drm_mode.h>)
// so the memory layout is ABI-exact. Per-frame hot paths (PageFlip,
// CommitAtomic) are zero-allocation: fixed structs cross the ioctl boundary by
// value with no heap churn.
package kms

import (
	"fmt"
	"unsafe"
)

// ---------------------------------------------------------------------------
// UAPI ioctl request numbers (see <drm.h>; _IOWR('d', nr, struct)).
// Encoding: dir(2b) | size(14b) | type(8b) | nr(8b)
// ---------------------------------------------------------------------------

func iowr(nr byte, size uintptr) uintptr {
	return 3<<30 | size<<16 | uintptr('d')<<8 | uintptr(nr)
}

func iow(nr byte, size uintptr) uintptr {
	return 1<<30 | size<<16 | uintptr('d')<<8 | uintptr(nr)
}

func ioctlNR(nr byte) uintptr {
	return uintptr('d')<<8 | uintptr(nr)
}

// ioctl request numbers for the operations this package implements.
const (
	reqGetCap       byte = 0x0c
	reqSetClientCap byte = 0x0d
	reqSetMaster    byte = 0x1e
	reqDropMaster   byte = 0x1f
	reqGetResources byte = 0xa0
	reqGetCRTC      byte = 0xa1
	reqSetCRTC      byte = 0xa2
	reqGetEncoder   byte = 0xa6
	reqGetConnector byte = 0xa7
	reqGetPropBlob  byte = 0xac
	reqGetProp      byte = 0xaa
	reqAddFB        byte = 0xae
	reqRMFB         byte = 0xaf
	reqPageFlip     byte = 0xb0
	reqCreateDumb   byte = 0xb2
	reqMapDumb      byte = 0xb3
	reqDestroyDumb  byte = 0xb4
	reqAddFB2       byte = 0xb8
	reqObjGetProps  byte = 0xb9
	reqObjSetProp   byte = 0xba
	reqGetPlaneRes  byte = 0xb5
	reqGetPlane     byte = 0xb6
	reqCreateBlob   byte = 0xbd
	reqDestroyBlob  byte = 0xbe
	reqAtomic       byte = 0xbc
	reqPrimeToFD    byte = 0x2d
)

// ---------------------------------------------------------------------------
// Kernel ABI structures (mirrored verbatim from <drm_mode.h> / <drm.h>).
// Field order and alignment must not change: these are passed to the kernel
// through the ioctl boundary by pointer.
// ---------------------------------------------------------------------------

// ModeInfo is struct drm_mode_modeinfo.
type ModeInfo struct {
	Clock      uint32 // pixel clock in kHz
	HDisplay   uint16
	HSyncStart uint16
	HSyncEnd   uint16
	HTotal     uint16
	HSkew      uint16
	VDisplay   uint16
	VSyncStart uint16
	VSyncEnd   uint16
	VTotal     uint16
	VScan      uint16

	VRefresh uint32
	Flags    uint32
	Type     uint32
	Name     [32]byte
}

// Mode type / flag constants (<drm_mode.h>).
const (
	ModeTypePreferred uint32 = 1 << 3 // DRM_MODE_TYPE_PREFERRED
	ModeFlagInterlace uint32 = 1 << 4
)

// ModeInfoSize is the UAPI size of struct drm_mode_modeinfo.
const ModeInfoSize = unsafe.Sizeof(ModeInfo{})

// UAPI struct sizes for the remaining mirrored structs.
const (
	CardResSize      = unsafe.Sizeof(CardRes{})
	GetConnectorSize = unsafe.Sizeof(GetConnector{})
	GetEncoderSize   = unsafe.Sizeof(GetEncoder{})
	CrtcSetSize      = unsafe.Sizeof(CrtcSet{})
	FbCmdSize        = unsafe.Sizeof(FbCmd{})
	FBCmd2Size       = unsafe.Sizeof(FBCmd2{})
	PageFlipSize     = unsafe.Sizeof(PageFlip{})
	AtomicSize       = unsafe.Sizeof(Atomic{})
	CreateDumbSize   = unsafe.Sizeof(CreateDumb{})
)

// CardRes is struct drm_mode_card_res for DRM_IOCTL_MODE_GETRESOURCES.
type CardRes struct {
	FbIDPtr         uint64
	CrtcIDPtr       uint64
	ConnectorIDPtr  uint64
	EncoderIDPtr    uint64
	CountFbs        uint32
	CountCrtcs      uint32
	CountConnectors uint32
	CountEncoders   uint32
	MinWidth        uint32
	MaxWidth        uint32
	MinHeight       uint32
	MaxHeight       uint32
}

// GetConnector is struct drm_mode_get_connector.
type GetConnector struct {
	EncodersPtr   uint64
	ModesPtr      uint64
	PropsPtr      uint64
	PropValuesPtr uint64

	CountModes    uint32
	CountProps    uint32
	CountEncoders uint32
	EncoderID     uint32
	ConnectorID   uint32
	Type          uint32
	TypeID        uint32
	Connection    uint32
	MMWidth       uint32
	MMHeight      uint32
	SubPixel      uint32
	Pad           uint32
}

// GetEncoder is struct drm_mode_get_encoder.
type GetEncoder struct {
	EncoderID      uint32
	EncoderType    uint32
	CrtcID         uint32
	PossibleCrtcs  uint32
	PossibleClones uint32
}

// CrtcSet is struct drm_mode_crtc.
type CrtcSet struct {
	SetConnectorsPtr uint64
	CountConnectors  uint32
	CrtcID           uint32
	FbID             uint32
	X                uint32
	Y                uint32
	GammaSize        uint32
	ModeValid        uint32
	Mode             ModeInfo
}

// FbCmd is struct drm_mode_fb_cmd (ADDFB/RMFB/GETFB).
type FbCmd struct {
	FbID   uint32
	Width  uint32
	Height uint32
	Pitch  uint32
	Bpp    uint32
	Depth  uint32
	Handle uint32
}

// FBCmd2 is struct drm_mode_fb_cmd2 (ADDFB2/GETFB2).
type FBCmd2 struct {
	FbID        uint32
	Width       uint32
	Height      uint32
	PixelFormat uint32
	Flags       uint32

	Handles   [4]uint32
	Pitches   [4]uint32
	Offsets   [4]uint32
	Modifiers [4]uint64
}

// FBModifiers enables the modifier field in FBCmd2.
const FBModifiers uint32 = 1 << 1

// PageFlip is struct drm_mode_crtc_page_flip.
type PageFlip struct {
	CrtcID   uint32
	FbID     uint32
	Flags    uint32
	Reserved uint32
	UserData uint64
}

// PageFlip is struct drm_mode_crtc_page_flip flags.
const (
	PageFlipEvent uint32 = 0x01
	PageFlipAsync uint32 = 0x02
)

// AtomicFlags for struct drm_mode_atomic flags.
const (
	AtomicTestOnly     uint32 = 1 << 0
	AtomicNonblock     uint32 = 1 << 1
	AtomicAllowModeSet uint32 = 1 << 2
	AtomicPageFlip     uint32 = 1 << 3
)

// Atomic is struct drm_mode_atomic.
type Atomic struct {
	Flags         uint32
	CountObjs     uint32
	ObjsPtr       uint64
	CountPropsPtr uint64
	PropsPtr      uint64
	PropValuesPtr uint64
	Reserved      uint64
	UserData      uint64
}

// ObjProps is struct drm_mode_obj_get_properties.
type ObjProps struct {
	PropsPtr      uint64
	PropValuesPtr uint64
	CountProps    uint32
	ObjID         uint32
	ObjType       uint32
	Pad           uint32
}

// ObjSetProp is struct drm_mode_obj_set_property.
type ObjSetProp struct {
	Value   uint64
	PropID  uint32
	ObjID   uint32
	ObjType uint32
	Pad     uint32
}

// DRM object type magic IDs (<drm_mode.h>).
const (
	ObjTypeCRTC      uint32 = 0xcccccccc
	ObjTypeConnector uint32 = 0xc0c0c0c0
	ObjTypeEncoder   uint32 = 0xe0e0e0e0
	ObjTypeFB        uint32 = 0xfbfbfbfb
	ObjTypePlane     uint32 = 0xeeeeeeee
)

// CreateDumb is struct drm_mode_create_dumb.
type CreateDumb struct {
	Height uint32
	Width  uint32
	Bpp    uint32
	Flags  uint32
	Handle uint32
	Pitch  uint32
	Size   uint64
}

// DestroyDumb is struct drm_mode_destroy_dumb.
type DestroyDumb struct {
	Handle uint32
	Pad    uint32
}

// MapDumb is struct drm_mode_map_dumb.
type MapDumb struct {
	Handle uint32
	Pad    uint32
	Offset uint64
}

// GetCap is struct drm_get_cap.
type GetCap struct {
	Capability uint64
	Value      uint64
}

// SetClientCap is struct drm_set_client_cap.
type SetClientCap struct {
	Capability uint64
	Value      uint64
}

// DRM capability / client capability constants (<drm.h>).
const (
	CapDumbBuffer uint64 = 0x1

	ClientCapUniversalPlanes uint64 = 2
	ClientCapAtomic          uint64 = 3
)

// EventVBlank is struct drm_event_vblank as read off the DRM fd on flip
// completion. The leading fixed 8-byte header (type, length) precedes it in
// the stream; Head below captures that pair together with them.
type EventVBlank struct {
	Type     uint32
	Length   uint32
	UserData uint64
	Seconds  uint64
	USecs    uint64
	Sequence uint32
	CrtcID   uint32
}

// EventTypeFlipComplete identifies a page-flip completion event.
const EventTypeFlipComplete uint32 = 0x04

// DRM format FourCC codes (<drm_fourcc.h>).
const (
	FormatXRGB8888 uint32 = 0x34325258 // fourcc_code('X','R','2','4')
	FormatARGB8888 uint32 = 0x34324158 // fourcc_code('A','R','2','4')
)

// Connector status values (enum drm_connector_status).
const (
	StatusConnected    uint32 = 1
	StatusDisconnected uint32 = 2
	StatusUnknown      uint32 = 3
)

// DPMS power states (<drm_mode.h> DRM_MODE_DPMS_*).
const (
	DPMSOn      uint64 = 0
	DPMSStandby uint64 = 1
	DPMSSuspend uint64 = 2
	DPMSOff     uint64 = 3
)

// Common connector type constants (<drm_mode.h>).
const (
	ConnectorUnknown     uint32 = 0
	ConnectorVGA         uint32 = 1
	ConnectorDisplayPort uint32 = 10
	ConnectorHDMIA       uint32 = 11
	ConnectorHDMIB       uint32 = 12
	ConnectoreDP         uint32 = 14
	ConnectorVirtual     uint32 = 15
)

// ConnectorTypeName returns the human-readable UAPI name of a connector type.
func ConnectorTypeName(t uint32) string {
	switch t {
	case ConnectorUnknown:
		return "Unknown"
	case ConnectorVGA:
		return "VGA"
	case ConnectorDisplayPort:
		return "DP"
	case ConnectorHDMIA:
		return "HDMI-A"
	case ConnectorHDMIB:
		return "HDMI-B"
	case ConnectoreDP:
		return "eDP"
	case ConnectorVirtual:
		return "Virtual"
	default:
		return fmt.Sprintf("Type-%d", t)
	}
}
