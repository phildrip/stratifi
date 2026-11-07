package kms

import (
	"fmt"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Property / blob ABI structures (<drm_mode.h>).
// ---------------------------------------------------------------------------

// GetProperty is struct drm_mode_get_property.
type GetProperty struct {
	ValuesPtr   uint64
	EnumBlobPtr uint64
	PropID      uint32
	Flags       uint32
	Name        [32]byte
}

// PropBlob flags (<drm_mode.h>).
const (
	PropRange     uint32 = 1 << 1
	PropEnum      uint32 = 1 << 3
	PropBlob      uint32 = 1 << 4
	PropBitmask   uint32 = 1 << 5
	PropImmutable uint32 = 1 << 2
)

// CreateBlob is struct drm_mode_create_blob.
type CreateBlob struct {
	Data   uint64
	Length uint32
	BlobID uint32
}

// DestroyBlob is struct drm_mode_destroy_blob.
type DestroyBlob struct {
	ID uint32
}

// GetPlaneRes is struct drm_mode_get_plane_res.
type GetPlaneRes struct {
	PlaneIDPtr  uint64
	CountPlanes uint32
}

// GetPlane is struct drm_mode_get_plane (u64 format_type_ptr comes last).
type GetPlane struct {
	PlaneID          uint32
	CrtcID           uint32
	FBID             uint32
	PossibleCrtcs    uint32
	GammaSize        uint32
	CountFormatTypes uint32
	FormatTypesPtr   uint64
}

// Plane is an enumerated KMS plane.
type Plane struct {
	ID      uint32
	CrtcID  uint32
	FBID    uint32
	Formats []uint32
	Type    uint32 // DRM_PLANE_TYPE_* via "type" enum prop value
	props   map[string]uint32
}

// Plane types (enum drm_plane_type).
const (
	PlaneTypeOverlay uint32 = 0
	PlaneTypePrimary uint32 = 1
	PlaneTypeCursor  uint32 = 2
)

// AtomicReq is a prepared atomic commit: three parallel arrays of object IDs,
// property IDs, and property values. Callers must pre-allocate the slices
// once and reuse them across frames to keep the hot path allocation-free.
type AtomicReq struct {
	Objs     []uint32
	Props    []uint32
	Values   []uint64
	Flags    uint32
	UserData uint64
}

// Reset empties the request for the next frame while keeping capacity.
func (r *AtomicReq) Reset() {
	r.Objs = r.Objs[:0]
	r.Props = r.Props[:0]
	r.Values = r.Values[:0]
}

// Set appends one property value for obj. Caller pre-sizes slices.
func (r *AtomicReq) Set(obj, prop uint32, value uint64) {
	r.Objs = append(r.Objs, obj)
	r.Props = append(r.Props, prop)
	r.Values = append(r.Values, value)
}

// SetAtomic requests commit of the prepared request to the kernel.
// testOnly validates without applying; returns the failure reason otherwise.
func (c *Card) CommitAtomic(r *AtomicReq, testOnly bool) error {
	if len(r.Objs) != len(r.Props) || len(r.Props) != len(r.Values) {
		return fmt.Errorf("kms: atomic req arrays out of sync (%d/%d/%d)",
			len(r.Objs), len(r.Props), len(r.Values))
	}

	flags := r.Flags
	if testOnly {
		flags |= AtomicTestOnly
	}

	a := Atomic{
		Flags:         flags,
		CountObjs:     uint32(len(r.Objs)),
		ObjsPtr:       0,
		CountPropsPtr: uint64(len(r.Props)),
		PropsPtr:      0,
		PropValuesPtr: 0,
		UserData:      r.UserData,
	}
	if len(r.Objs) > 0 {
		a.ObjsPtr = uint64(uintptr(unsafe.Pointer(&r.Objs[0])))
		a.PropsPtr = uint64(uintptr(unsafe.Pointer(&r.Props[0])))
		a.PropValuesPtr = uint64(uintptr(unsafe.Pointer(&r.Values[0])))
	}
	return c.ioctl(iocAtomic, unsafe.Pointer(&a))
}

// GetObjProps enumerates an object's properties, returning property IDs and
// values keyed by name.
func (c *Card) GetObjProps(objID, objType uint32) (map[string]uint32, map[string]uint64, error) {
	// Count pass.
	op := ObjProps{ObjID: objID, ObjType: objType}
	if err := c.ioctl(iocObjGetProps, unsafe.Pointer(&op)); err != nil {
		return nil, nil, err
	}
	n := int(op.CountProps)
	if n == 0 {
		return map[string]uint32{}, map[string]uint64{}, nil
	}

	propIDs := make([]uint32, n)
	values := make([]uint64, n)
	op2 := ObjProps{
		ObjID:      objID,
		ObjType:    objType,
		CountProps: op.CountProps,
	}
	op2.PropsPtr = uint64(uintptr(unsafe.Pointer(&propIDs[0])))
	op2.PropValuesPtr = uint64(uintptr(unsafe.Pointer(&values[0])))
	if err := c.ioctl(iocObjGetProps, unsafe.Pointer(&op2)); err != nil {
		return nil, nil, err
	}

	props := make(map[string]uint32, n)
	propValues := make(map[string]uint64, n)
	for i := 0; i < n; i++ {
		name, err := c.PropName(propIDs[i])
		if err != nil {
			continue
		}
		props[name] = propIDs[i]
		propValues[name] = values[i]
	}
	return props, propValues, nil
}

// PropName resolves a property object ID to its name.
func (c *Card) PropName(propID uint32) (string, error) {
	gp := GetProperty{PropID: propID}
	if err := c.ioctl(iocGetProperty, unsafe.Pointer(&gp)); err != nil {
		return "", err
	}
	end := len(gp.Name)
	for i, c := range gp.Name {
		if c == 0 {
			end = i
			break
		}
	}
	return string(gp.Name[:end]), nil
}

// PropID looks up a cached property ID by name on a connector.
func (c *Connector) PropID(name string) uint32 { return c.props[name] }

// PropID looks up a cached property ID by name on a CRTC.
func (p *CRTC) PropID(name string) uint32 { return p.props[name] }

// PropID looks up a cached property ID by name on a plane.
func (p *Plane) PropID(name string) uint32 { return p.props[name] }

// CreateBlob uploads an opaque blob (e.g. a ModeInfo) and returns its ID.
func (c *Card) CreateBlob(data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("kms: CreateBlob: empty data")
	}
	cb := CreateBlob{
		Data:   uint64(uintptr(unsafe.Pointer(&data[0]))),
		Length: uint32(len(data)),
	}
	if err := c.ioctl(iocCreateBlob, unsafe.Pointer(&cb)); err != nil {
		return 0, err
	}
	return cb.BlobID, nil
}

// DestroyBlob releases a blob ID.
func (c *Card) DestroyBlob(id uint32) error {
	db := DestroyBlob{ID: id}
	return c.ioctl(iocDestroyBlob, unsafe.Pointer(&db))
}

// enumeratePlanes lists all planes (requires UNIVERSAL_PLANES client cap).
func (c *Card) enumeratePlanes() error {
	res := GetPlaneRes{}
	if err := c.ioctl(iocGetPlaneRes, unsafe.Pointer(&res)); err != nil {
		return err
	}
	n := int(res.CountPlanes)
	if n == 0 {
		return nil
	}
	planeIDs := make([]uint32, n)
	res2 := GetPlaneRes{CountPlanes: res.CountPlanes}
	res2.PlaneIDPtr = uint64(uintptr(unsafe.Pointer(&planeIDs[0])))
	if err := c.ioctl(iocGetPlaneRes, unsafe.Pointer(&res2)); err != nil {
		return err
	}

	for _, id := range planeIDs {
		gp := GetPlane{PlaneID: id}
		if err := c.ioctl(iocGetPlane, unsafe.Pointer(&gp)); err != nil {
			return err
		}
		p := &Plane{
			ID:     id,
			CrtcID: gp.CrtcID,
			FBID:   gp.FBID,
		}
		if gp.CountFormatTypes > 0 {
			formats := make([]uint32, gp.CountFormatTypes)
			gp2 := GetPlane{
				PlaneID:          id,
				CountFormatTypes: gp.CountFormatTypes,
				FormatTypesPtr:   uint64(uintptr(unsafe.Pointer(&formats[0]))),
			}
			if err := c.ioctl(iocGetPlane, unsafe.Pointer(&gp2)); err != nil {
				return err
			}
			p.Formats = formats
		}
		if props, values, err := c.GetObjProps(id, ObjTypePlane); err == nil {
			p.props = props
			p.Type = uint32(values["type"])
		}
		c.Planes = append(c.Planes, p)
	}
	return nil
}

// PrimaryPlaneFor returns the primary plane bound to the given CRTC.
func (c *Card) PrimaryPlaneFor(crtcID uint32) *Plane {
	for _, p := range c.Planes {
		if p.CrtcID == crtcID && p.Type == PlaneTypePrimary {
			return p
		}
	}
	// Fall back: first plane bound to the CRTC.
	for _, p := range c.Planes {
		if p.CrtcID == crtcID {
			return p
		}
	}
	return nil
}

// enableProps preloads atomic property IDs for the objects a mode commit
// touches. Idempotent: caches on first call.
func (c *Card) preloadProps() error {
	for _, conn := range c.Connectors {
		if conn.props != nil {
			continue
		}
		props, _, err := c.GetObjProps(conn.ID, ObjTypeConnector)
		if err != nil {
			return fmt.Errorf("kms: connector %d props: %w", conn.ID, err)
		}
		conn.props = props
	}
	for _, crtc := range c.CRTCs {
		if crtc.props != nil {
			continue
		}
		props, _, err := c.GetObjProps(crtc.ID, ObjTypeCRTC)
		if err != nil {
			return fmt.Errorf("kms: crtc %d props: %w", crtc.ID, err)
		}
		crtc.props = props
	}
	return nil
}

// SetModeAtomic performs an atomic modeset: binds connector to CRTC, arms the
// CRTC with the given mode and scanout framebuffer. Requires the ATOMIC
// client capability and DRM master. Values validated by TEST_ONLY first.
func (c *Card) SetModeAtomic(conn *Connector, crtc *CRTC, mode *ModeInfo, fbID uint32) error {
	if err := c.preloadProps(); err != nil {
		return err
	}

	connCRTC := conn.PropID(propCRTCID)
	crtcActive := crtc.PropID(propActive)
	crtcModeID := crtc.PropID(propModeID)
	if connCRTC == 0 || crtcActive == 0 || crtcModeID == 0 {
		return fmt.Errorf("kms: driver missing required atomic props (connector CRTC_ID=%d crtc ACTIVE=%d MODE_ID=%d)",
			connCRTC, crtcActive, crtcModeID)
	}

	modeBlob, err := c.CreateBlob(unsafe.Slice((*byte)(unsafe.Pointer(mode)), ModeInfoSize))
	if err != nil {
		return fmt.Errorf("kms: MODE_ID blob upload: %w", err)
	}
	defer func() { _ = c.DestroyBlob(modeBlob) }()

	req := &c.atomicReq
	req.Reset()
	req.Flags = AtomicAllowModeSet
	req.Set(conn.ID, connCRTC, uint64(crtc.ID))
	req.Set(crtc.ID, crtcActive, 1)
	req.Set(crtc.ID, crtcModeID, uint64(modeBlob))

	// Attach the scanout through the primary plane when one is exposed.
	if plane := c.PrimaryPlaneFor(crtc.ID); plane != nil && fbID != 0 {
		fb := plane.PropID(propFBID)
		planeCRTC := plane.PropID(propCRTCID)
		if fb != 0 && planeCRTC != 0 {
			req.Set(plane.ID, fb, uint64(fbID))
			req.Set(plane.ID, planeCRTC, uint64(crtc.ID))
		}
	}

	// TEST_ONLY validation pass.
	if err := c.CommitAtomic(req, true); err != nil {
		return fmt.Errorf("kms: atomic TEST_ONLY rejected: %w", err)
	}
	if err := c.CommitAtomic(req, false); err != nil {
		return fmt.Errorf("kms: atomic commit failed: %w", err)
	}
	return nil
}

// PageFlipAtomic flips the scanout on the primary plane in the next vblank
// without a modeset. Reuses the pre-allocated request buffer: allocation-free
// per frame.
func (c *Card) PageFlipAtomic(conn *Connector, crtc *CRTC, fbID uint32, userData uint64) error {
	plane := c.PrimaryPlaneFor(crtc.ID)
	if plane == nil {
		// No universal planes: fall back to legacy flip.
		return c.PageFlip(crtc.ID, fbID, userData)
	}
	fb := plane.PropID(propFBID)
	planeCRTC := plane.PropID(propCRTCID)
	if fb == 0 || planeCRTC == 0 {
		return c.PageFlip(crtc.ID, fbID, userData)
	}

	req := &c.atomicReq
	req.Reset()
	req.Flags = AtomicPageFlip | AtomicNonblock
	req.Set(plane.ID, fb, uint64(fbID))
	req.Set(plane.ID, planeCRTC, uint64(crtc.ID))
	req.UserData = userData
	return c.CommitAtomic(req, false)
}

// SetObjProp sets a single KMS object property via DRM_IOCTL_MODE_OBJ_SETPROPERTY.
func (c *Card) SetObjProp(objID, objType, propID uint32, value uint64) error {
	if propID == 0 {
		return fmt.Errorf("kms: SetObjProp: zero property id on obj %d", objID)
	}
	sp := ObjSetProp{
		Value:   value,
		PropID:  propID,
		ObjID:   objID,
		ObjType: objType,
	}
	return c.ioctl(iocObjSetProp, unsafe.Pointer(&sp))
}

// SetDPMS powers the display pipeline on or off. On atomic drivers it commits
// CRTC ACTIVE=1/0 (with ALLOW_MODESET); if atomic ACTIVE commit fails or is
// unavailable, it falls back to the connector's DPMS property (DRM_MODE_DPMS_ON
// / DRM_MODE_DPMS_OFF).
func (c *Card) SetDPMS(conn *Connector, crtc *CRTC, on bool) error {
	if conn == nil || crtc == nil {
		return fmt.Errorf("kms: SetDPMS: nil connector or CRTC")
	}
	if err := c.preloadProps(); err != nil {
		return err
	}

	if crtcActive := crtc.PropID(propActive); crtcActive != 0 {
		var val uint64
		if on {
			val = 1
		}
		req := &c.atomicReq
		req.Reset()
		req.Flags = AtomicAllowModeSet
		req.Set(crtc.ID, crtcActive, val)
		if err := c.CommitAtomic(req, true); err == nil {
			if err := c.CommitAtomic(req, false); err == nil {
				return nil
			}
		}
	}

	dpmsProp := conn.PropID(propDPMS)
	if dpmsProp == 0 {
		return fmt.Errorf("kms: SetDPMS: neither atomic CRTC ACTIVE nor connector DPMS property succeeded")
	}
	mode := DPMSOff
	if on {
		mode = DPMSOn
	}
	return c.SetObjProp(conn.ID, ObjTypeConnector, dpmsProp, mode)
}
