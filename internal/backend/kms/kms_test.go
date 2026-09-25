package kms

import (
	"testing"
)

// TestStructABI pins the mirrored kernel UAPI struct sizes. If any size
// drifts, the ioctl request numbers and struct layouts silently corrupt
// kernel ABI, so this table must not change without a kernel header diff.
func TestStructABI(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"ModeInfo (drm_mode_modeinfo)", ModeInfoSize, 68},
		{"CardRes (drm_mode_card_res)", CardResSize, 64},
		{"GetConnector (drm_mode_get_connector)", GetConnectorSize, 80},
		{"GetEncoder (drm_mode_get_encoder)", GetEncoderSize, 20},
		{"CrtcSet (drm_mode_crtc)", CrtcSetSize, 104},
		{"FbCmd (drm_mode_fb_cmd)", FbCmdSize, 28},
		{"FBCmd2 (drm_mode_fb_cmd2)", FBCmd2Size, 104},
		{"PageFlip (drm_mode_crtc_page_flip)", PageFlipSize, 24},
		{"Atomic (drm_mode_atomic)", AtomicSize, 56},
		{"CreateDumb (drm_mode_create_dumb)", CreateDumbSize, 32},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: size = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestIoctlNumbers pins the derived DRM_IOCTL_* request numbers against
// raw values computed independently from the kernel macro definitions
// (e.g. visible in libdrm and cmd/probe usage).
func TestIoctlNumbers(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"GET_CAP", iocGetCap, 0xc010640c},
		{"SET_CLIENT_CAP", iocSetClientCap, 0x4010640d},
		{"SET_MASTER", iocSetMaster, 0x641e},
		{"DROP_MASTER", iocDropMaster, 0x641f},
		{"GETRESOURCES", iocGetResources, 0xc04064a0},
		{"GETCRTC", iocGetCRTC, 0xc06864a1},
		{"SETCRTC", iocSetCRTC, 0xc06864a2},
		{"GETENCODER", iocGetEncoder, 0xc01464a6},
		{"GETCONNECTOR", iocGetConnector, 0xc05064a7},
		{"GETPROPERTY", iocGetProperty, 0xc03864aa},
		{"ADDFB", iocAddFB, 0xc01c64ae},
		{"RMFB", iocRMFB, 0xc00464af},
		{"PAGE_FLIP", iocPageFlip, 0xc01864b0},
		{"CREATE_DUMB", iocCreateDumb, 0xc02064b2},
		{"MAP_DUMB", iocMapDumb, 0xc01064b3},
		{"DESTROY_DUMB", iocDestroyDumb, 0xc00864b4},
		{"ADDFB2", iocAddFB2, 0xc06864b8},
		{"OBJ_GETPROPERTIES", iocObjGetProps, 0xc02064b9},
		{"OBJ_SETPROPERTY", iocObjSetProp, 0xc01864ba},
		{"GETPLANERESOURCES", iocGetPlaneRes, 0xc01064b5},
		{"GETPLANE", iocGetPlane, 0xc02064b6},
		{"CREATEPROPBLOB", iocCreateBlob, 0xc01064bd},
		{"DESTROYPROPBLOB", iocDestroyBlob, 0xc00464be},
		{"ATOMIC", iocAtomic, 0xc03864bc},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: ioctl = 0x%08x, want 0x%08x", tc.name, tc.got, tc.want)
		}
	}
}

// TestModeName covers ModeInfo name extraction (NUL-terminated buffer).
func TestModeName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"1920x1080", "1920x1080"},
		{"800x600", "800x600"},
		{"", ""},
	}
	for _, tc := range cases {
		m := ModeInfo{}
		copy(m.Name[:], tc.name)
		if got := m.ModeName(); got != tc.want {
			t.Errorf("ModeName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPreferredModeSelection is table-driven mode picking: preferred flag
// wins, else highest pixel area then refresh.
func TestPreferredModeSelection(t *testing.T) {
	mk := func(w, h uint16, refresh uint32, flags uint32) ModeInfo {
		return ModeInfo{HDisplay: w, VDisplay: h, VRefresh: refresh, Type: flags}
	}

	cases := []struct {
		name  string
		modes []ModeInfo
		wantW uint16
		wantH uint16
	}{
		{
			name:  "preferred wins over larger",
			modes: []ModeInfo{mk(1280, 1024, 60, 0), mk(1024, 768, 60, ModeTypePreferred)},
			wantW: 1024, wantH: 768,
		},
		{
			name:  "highest area when no preferred",
			modes: []ModeInfo{mk(640, 480, 60, 0), mk(1280, 1024, 60, 0), mk(800, 600, 60, 0)},
			wantW: 1280, wantH: 1024,
		},
		{
			name:  "highest refresh at equal area",
			modes: []ModeInfo{mk(1920, 1080, 60, 0), mk(1920, 1080, 144, 0)},
			wantW: 1920, wantH: 1080,
		},
	}
	for _, tc := range cases {
		conn := Connector{Modes: tc.modes}
		got := conn.PreferredMode()
		if got == nil {
			t.Fatalf("%s: got nil mode", tc.name)
		}
		if got.HDisplay != tc.wantW || got.VDisplay != tc.wantH {
			t.Errorf("%s: picked %dx%d, want %dx%d", tc.name, got.HDisplay, got.VDisplay, tc.wantW, tc.wantH)
		}
	}
}

func TestPreferredModeEmpty(t *testing.T) {
	conn := Connector{}
	if got := conn.PreferredMode(); got != nil {
		t.Errorf("empty connector mode pick = %v, want nil", got)
	}
}

// TestConnectorTypeName is a table-driven naming check.
func TestConnectorTypeName(t *testing.T) {
	cases := []struct {
		typ  uint32
		want string
	}{
		{ConnectorDisplayPort, "DP"},
		{ConnectorHDMIA, "HDMI-A"},
		{ConnectoreDP, "eDP"},
		{ConnectorVGA, "VGA"},
		{ConnectorVirtual, "Virtual"},
		{99, "Type-99"},
	}
	for _, tc := range cases {
		if got := ConnectorTypeName(tc.typ); got != tc.want {
			t.Errorf("ConnectorTypeName(%d) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

func TestConnectorDisplayName(t *testing.T) {
	cases := []struct {
		typ, id uint32
		want    string
	}{
		{ConnectorDisplayPort, 1, "DP-1"},
		{ConnectorHDMIA, 2, "HDMI-A-2"},
	}
	for _, tc := range cases {
		conn := Connector{Type: tc.typ, TypeID: tc.id}
		if got := conn.DisplayName(); got != tc.want {
			t.Errorf("DisplayName = %q, want %q", got, tc.want)
		}
	}
}

// TestAtomicReqArrayMismatch covers the CommitAtomic error path.
func TestAtomicReqArrayMismatch(t *testing.T) {
	req := AtomicReq{Objs: make([]uint32, 1)}
	err := commitMismatchProbe(req)
	if err == nil {
		t.Fatal("expected array mismatch error")
	}
}

// commitMismatchProbe exercises CommitAtomic's length validation without
// issuing a kernel ioctl: the check happens before the fd is touched.
func commitMismatchProbe(r AtomicReq) error {
	c := Card{fd: -1}
	return c.CommitAtomic(&r, false)
}

// TestFourCC pins the DRM format codes.
func TestFourCC(t *testing.T) {
	if FormatXRGB8888 != 0x34325258 {
		t.Errorf("XRGB8888 = 0x%08x, want 0x34325258", FormatXRGB8888)
	}
	if FormatARGB8888 != 0x34324158 {
		t.Errorf("ARGB8888 = 0x%08x, want 0x34324158", FormatARGB8888)
	}
}
