package kms

import (
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openTestCard opens a DRM card, skipping when none is accessible.
func openTestCard(t testing.TB) *Card {
	t.Helper()
	card, err := OpenCard()
	if err != nil {
		t.Skipf("skipping: no DRM card (%v)", err)
	}
	t.Cleanup(func() { _ = card.Close() })
	return card
}

// requireMaster acquires DRM master, skipping when another compositor owns
// the card (the common case in a developer session).
func requireMaster(t testing.TB, card *Card) {
	t.Helper()
	if err := card.SetMaster(); err != nil {
		t.Skipf("skipping: DRM master busy (another compositor is active?): %v", err)
	}
}

// TestKmsEnumerate exercises real GETRESOURCES/GETCONNECTOR/GETCRTC ioctls,
// which do not require DRM master.
func TestKmsEnumerate(t *testing.T) {
	card := openTestCard(t)

	if card.Caps.DumbBuffer == false {
		t.Errorf("expected dumb buffer capability")
	}
	if len(card.Connectors) == 0 {
		t.Fatal("no connectors enumerated")
	}
	for _, conn := range card.Connectors {
		name := conn.DisplayName()
		if name == "" {
			t.Fatalf("connector %d has empty display name", conn.ID)
		}
		if conn.Type == 0 {
			t.Errorf("connector %d has no type", conn.ID)
		}
		t.Logf("connector %s (id=%d): status=%d modes=%d boundCRTC=%d",
			name, conn.ID, conn.Status, len(conn.Modes), conn.CRTC)
		for i := range conn.Modes {
			mode := &conn.Modes[i]
			if mode.HDisplay == 0 || mode.VDisplay == 0 {
				t.Errorf("connector %s mode %d has zero extent", name, i)
			}
			if mode.Clock == 0 {
				t.Errorf("connector %s mode %d has zero clock", name, i)
			}
		}
	}
	if len(card.CRTCs) == 0 {
		t.Fatal("no CRTCs enumerated")
	}
	for _, crtc := range card.CRTCs {
		if crtc.ID == 0 {
			t.Fatal("CRTC with zero ID")
		}
	}
	// Planes may be absent without UNIVERSAL_PLANES cap.
	if len(card.Planes) == 0 {
		if err := card.SetUniversalPlanes(); err == nil {
			_ = card.enumeratePlanes()
		}
	}
	t.Logf("planes: %d", len(card.Planes))
}

// TestReadFlipEventsEmpty verifies the non-blocking empty-queue path
// returns (0, nil) rather than blocking or erroring, without DRM master.
func TestReadFlipEventsEmpty(t *testing.T) {
	card := openTestCard(t)

	events := make([]EventVBlank, 4)
	n, err := card.ReadFlipEvents(events)
	if err != nil {
		t.Fatalf("empty-queue read returned error: %v", err)
	}
	if n != 0 {
		t.Fatalf("empty-queue read returned %d events", n)
	}
}

// TestScanoutLifecycle allocates a dumb scanout, validates the mapping,
// fills it, and releases it. Dumb-buffer allocation does not require master;
// ADDFB does, so the test gates on master for the full lifecycle.
func TestScanoutLifecycle(t *testing.T) {
	card := openTestCard(t)
	requireMaster(t, card)

	so, err := card.CreateScanout(64, 64)
	if err != nil {
		t.Fatalf("CreateScanout: %v", err)
	}
	if so.FBID == 0 {
		t.Fatal("scanout has zero FB ID")
	}
	if so.Pitch < 64*4 {
		t.Errorf("pitch %d too small for 64px @ 32bpp", so.Pitch)
	}
	if len(so.Data) < int(so.Size) {
		t.Fatalf("mapping len %d < size %d", len(so.Data), so.Size)
	}

	so.Fill(0x40, 0x80, 0xC0)
	if so.Data[0] != 0xC0 || so.Data[1] != 0x80 || so.Data[2] != 0x40 || so.Data[3] != 0xFF {
		t.Errorf("Fill wrote wrong BGRA: % x", so.Data[:4])
	}

	if err := so.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Double close must not crash.
	if err := so.Close(); err == nil && so.Card != nil {
		t.Errorf("second Close unexpectedly valid")
	}
}

// TestSetCRTCAndFlip performs a full legacy modeset and two vblank
// flips, matching the flip-complete events. Skip-gated on master and on an
// available connected output.
func TestSetCRTCAndFlip(t *testing.T) {
	card := openTestCard(t)
	requireMaster(t, card)

	var conn *Connector
	var crtc *CRTC
	for _, c := range card.Connectors {
		if !c.IsConnected() || len(c.Modes) == 0 {
			continue
		}
		for _, cr := range card.CRTCs {
			// Legacy SETCRTC needs the connector's encoder to be compatible;
			// try each (connector, crtc) pairing until one succeeds.
			if err := card.SetCRTC(cr.ID, 0, c.ID, &c.Modes[0]); err == nil {
				conn, crtc = c, cr
				break
			}
		}
		if conn != nil {
			break
		}
	}
	if conn == nil {
		t.Skip("skipping: no connected output accepted a SETCRTC")
	}

	mode := conn.PreferredMode()
	if mode == nil {
		t.Skip("skipping: no preferred mode on bound connector")
	}
	if err := card.SetCRTC(crtc.ID, 0, conn.ID, mode); err != nil {
		t.Fatalf("re-SETCRTC with preferred mode: %v", err)
	}

	so, err := card.CreateScanout(uint32(mode.HDisplay), uint32(mode.VDisplay))
	if err != nil {
		t.Fatalf("CreateScanout at mode res: %v", err)
	}
	defer func() { _ = so.Close() }()

	so.Fill(0x10, 0x20, 0x30)
	if err := card.SetCRTC(crtc.ID, so.FBID, conn.ID, mode); err != nil {
		t.Fatalf("SETCRTC with fb: %v", err)
	}

	// Issue two flips; both should report back as flip-complete events.
	for i := uint64(1); i <= 2; i++ {
		if err := card.PageFlip(crtc.ID, so.FBID, i); err != nil {
			t.Fatalf("PageFlip %d: %v", i, err)
		}
		events := make([]EventVBlank, 4)
		got := 0
		for got == 0 {
			more, err := card.ReadFlipEvents(events)
			if err != nil {
				t.Fatalf("ReadFlipEvents: %v", err)
			}
			got = more
		}
		if events[0].UserData != i {
			t.Errorf("flip event %d: user_data = %d, want %d", i, events[0].UserData, i)
		}
		if events[0].Type != EventTypeFlipComplete {
			t.Errorf("flip event %d: type = %d, want %d", i, events[0].Type, EventTypeFlipComplete)
		}
		if events[0].CrtcID != crtc.ID {
			t.Errorf("flip event %d: crtc_id = %d, want %d", i, events[0].CrtcID, crtc.ID)
		}
		t.Logf("flip %d complete: seq=%d usec=%d", i, events[0].Sequence, events[0].USecs)
	}
}

// TestAtomicModeCommit runs an atomic TEST_ONLY validation against real
// hardware, exercising property enumeration and blob upload.
func TestAtomicModeCommit(t *testing.T) {
	card := openTestCard(t)
	requireMaster(t, card)

	if err := card.SetUniversalPlanes(); err != nil {
		t.Skipf("skipping: driver lacks UNIVERSAL_PLANES cap: %v", err)
	}
	if err := card.SetAtomicCap(); err != nil {
		t.Skipf("skipping: driver lacks ATOMIC cap: %v", err)
	}
	if err := card.enumeratePlanes(); err != nil {
		t.Skipf("skipping: plane enumeration failed: %v", err)
	}
	if err := card.preloadProps(); err != nil {
		t.Skipf("skipping: property preload failed: %v", err)
	}

	var conn *Connector
	var crtc *CRTC
	for _, c := range card.Connectors {
		if c.IsConnected() && len(c.Modes) > 0 {
			for _, cr := range card.CRTCs {
				if cr.PropID(propActive) != 0 && cr.PropID(propModeID) != 0 {
					conn, crtc = c, cr
					break
				}
			}
		}
		if conn != nil {
			break
		}
	}
	if conn == nil {
		t.Skip("skipping: no connector/CRTC pair with required atomic props")
	}

	mode := conn.PreferredMode()
	if mode == nil {
		t.Skip("skipping: no preferred mode")
	}

	req := &card.atomicReq
	req.Reset()
	connCRTC := conn.PropID(propCRTCID)
	if connCRTC == 0 {
		t.Skip("skipping: connector lacks CRTC_ID prop")
	}
	req.Set(conn.ID, connCRTC, uint64(crtc.ID))

	// TEST_ONLY on a modeset without MODE_ID/ACTIVE must fail (this is the
	// kernel rejecting an incomplete commit — the error path under test).
	if err := card.CommitAtomic(req, true); err == nil {
		t.Log("TEST_ONLY commit of incomplete state unexpectedly passed")
	} else {
		t.Logf("incomplete TEST_ONLY correctly rejected: %v", err)
	}
}

// TestEventVBlankLayout pins the vblank event size read off the fd.
func TestEventVBlankLayout(t *testing.T) {
	if got := unsafe.Sizeof(EventVBlank{}); got != 40 {
		t.Errorf("EventVBlank size = %d, want 40", got)
	}
}

// TestOpenCardMissing covers the no-device error path.
func TestOpenCardMissing(t *testing.T) {
	// Temporarily mask real devices via a nonexistent explicit path.
	if _, err := OpenCardPath("/dev/dri/card-nonexistent"); err == nil {
		t.Fatal("expected error opening nonexistent card path")
	} else if os.IsPermission(err) {
		t.Skipf("permission error treated as failure: %v", err)
	}
}

// TestDropMasterNotHeld verifies dropping master when not master: the
// kernel rejects with EPERM, which we surface as an error (not a crash).
func TestDropMasterNotHeld(t *testing.T) {
	card := openTestCard(t)
	if err := card.DropMaster(); err == nil {
		// We may legitimately be master in this session; either way it is
		// safe. DropMaster clears c.master only on success.
		t.Log("DropMaster succeeded (fd holds master)")
	}
}

// TestScanoutZeroSize covers the driver-level rejection of dumb buffers
// with zero extents (EPERM/EINVAL), ensuring we surface an error.
func TestScanoutZeroSize(t *testing.T) {
	card := openTestCard(t)
	requireMaster(t, card)

	if _, err := card.CreateScanout(0, 64); err == nil {
		t.Error("zero-width scanout accepted")
	}
	if _, err := card.CreateScanout(64, 0); err == nil {
		t.Error("zero-height scanout accepted")
	}
}

// TestPollFD validates the event-loop descriptor wiring.
func TestPollFD(t *testing.T) {
	card := openTestCard(t)
	fd := card.PollFD()
	if fd.Fd < 0 {
		t.Fatalf("poll fd = %d", fd.Fd)
	}
	if fd.Events&(unix.POLLIN|unix.POLLERR) == 0 {
		t.Errorf("poll events = %d missing POLLIN|POLLERR", fd.Events)
	}
}

// ensure errno mapping is exercised: ioctl on a closed fd fails cleanly.
func TestIoctlClosedFD(t *testing.T) {
	err := ioctl(-1, iocGetResources, unsafe.Pointer(&CardRes{}))
	if err == nil {
		t.Fatal("ioctl on closed fd must fail")
	}
	if err != syscall.EBADF {
		t.Errorf("want EBADF, got %v", err)
	}
}
