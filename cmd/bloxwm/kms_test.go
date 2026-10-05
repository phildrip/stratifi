package main

import (
	"testing"

	"bloxwm/internal/backend/kms"
)

// TestKmsDisplayOutputMethods: the scanout-backed Output must report the
// mode geometry and expose the dumb-buffer pixels. Allocation needs no DRM
// master, so this runs under sway; Present/flip needs master (VT step).
func TestKmsDisplayOutputMethods(t *testing.T) {
	card, err := kms.OpenCard()
	if err != nil {
		t.Skipf("no DRM card: %v", err)
	}
	defer card.Close()

	var conn *kms.Connector
	for _, c := range card.Connectors {
		if c.IsConnected() {
			conn = c
			break
		}
	}
	if conn == nil {
		t.Skip("no connected connector")
	}
	mode := conn.PreferredMode()
	if mode == nil {
		t.Skip("no modes on connector")
	}

	// Small scanout regardless of mode size: geometry methods must still
	// agree with the objects they wrap.
	scan, err := card.CreateScanout(64, 64)
	if err != nil {
		t.Skipf("scanout unavailable here: %v", err)
	}
	defer scan.Close()

	d := &kmsDisplay{card: card, scan: scan, conn: conn, mode: mode}
	_ = d.crtc // flip path needs master; not exercised here

	if got := d.Width(); got != int32(mode.HDisplay) {
		t.Errorf("Width = %d, want mode %d", got, mode.HDisplay)
	}
	if got := d.Height(); got != int32(mode.VDisplay) {
		t.Errorf("Height = %d, want mode %d", got, mode.VDisplay)
	}
	if got := d.Stride(); got != int32(scan.Pitch) {
		t.Errorf("Stride = %d, want scanout pitch %d", got, scan.Pitch)
	}
	if len(d.Pixels()) == 0 {
		t.Error("Pixels() empty")
	}

	// CPU writes through Pixels() must land in scanout memory.
	scan.Fill(0x11, 0x22, 0x33)
	px := d.Pixels()
	if px[0] != 0x33 || px[1] != 0x22 || px[2] != 0x11 {
		t.Errorf("Pixels()[0:3] = % x, want 33 22 11", px[:3])
	}
}
