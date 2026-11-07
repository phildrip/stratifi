package main

import (
	"fmt"

	"stratifi/internal/backend/kms"
	"stratifi/internal/compositor"
	"stratifi/internal/input"
	"stratifi/internal/input/evdev"
)

// kmsDisplay is the bare-metal output: a KMS scanout dumb buffer that the
// compositor renders straight into. It implements compositor.Output so the
// frame loop and CPU fallback work unchanged; Present flips atomically.
// GPU mode renders directly into scanout memory (ScanoutExecutor + flip),
// so there is no bridge and no CPU round trip.
type kmsDisplay struct {
	card    *kms.Card
	scan    *kms.Scanout
	conn    *kms.Connector
	crtc    *kms.CRTC
	mode    *kms.Mode
	dpmsOff bool
}

var _ compositor.Output = (*kmsDisplay)(nil)

func (d *kmsDisplay) Width() int32   { return int32(d.mode.HDisplay) }
func (d *kmsDisplay) Height() int32  { return int32(d.mode.VDisplay) }
func (d *kmsDisplay) Stride() int32  { return int32(d.scan.Pitch) }
func (d *kmsDisplay) Pixels() []byte { return d.scan.Data }

// Damage is a no-op: scanout presents whole frames on flip.
func (d *kmsDisplay) Damage(x, y, w, h int32) {}

// Present flips the freshly rendered scanout buffer in the next vblank.
// When the panel is powered off via DPMS, flips are suppressed until wake.
func (d *kmsDisplay) Present() error {
	if d.dpmsOff {
		return nil
	}
	return d.card.PageFlipAtomic(d.conn, d.crtc, d.scan.FBID, 0)
}

// PowerOn reports whether the display pipeline is currently powered on.
func (d *kmsDisplay) PowerOn() bool {
	return !d.dpmsOff
}

// SetPower turns the KMS connector/CRTC power state on or off (DPMS).
func (d *kmsDisplay) SetPower(on bool) error {
	if d.dpmsOff == !on {
		return nil
	}
	if d.card != nil && d.conn != nil && d.crtc != nil {
		if err := d.card.SetDPMS(d.conn, d.crtc, on); err != nil {
			return err
		}
	}
	d.dpmsOff = !on
	return nil
}

// PrimeFD exports the scanout dumb buffer for Vulkan import (zero-copy
// render-to-scanout). The fd stays owned by the scanout.
func (d *kmsDisplay) PrimeFD() (int, error) {
	return d.card.PrimeExport(d.scan.Handle)
}

func (d *kmsDisplay) Close() error {
	if d.scan != nil {
		_ = d.scan.Close()
		d.scan = nil
	}
	if d.card != nil {
		_ = d.card.Close()
		d.card = nil
	}
	return nil
}

// setupKMSDisplay opens the DRM card, takes master, picks the first
// connected connector and its preferred mode, allocates scanout memory at
// the mode size and performs the initial atomic modeset. Requires DRM
// master: run from a VT, not nested under another compositor.
func setupKMSDisplay() (*kmsDisplay, error) {
	card, err := kms.OpenCard()
	if err != nil {
		return nil, fmt.Errorf("kms: open card: %w", err)
	}
	if err := card.SetMaster(); err != nil {
		_ = card.Close()
		return nil, fmt.Errorf("kms: cannot become DRM master (another compositor holds the card? run from a VT): %w", err)
	}

	var conn *kms.Connector
	for _, c := range card.Connectors {
		if c.IsConnected() {
			conn = c
			break
		}
	}
	if conn == nil {
		_ = card.Close()
		return nil, fmt.Errorf("kms: no connected connector")
	}
	mode := conn.PreferredMode()
	if mode == nil {
		_ = card.Close()
		return nil, fmt.Errorf("kms: connector %s has no modes", conn.DisplayName())
	}

	var crtc *kms.CRTC
	if conn.CRTC != 0 {
		crtc = card.FindCRTCByID(conn.CRTC)
	}
	if crtc == nil && len(card.CRTCs) > 0 {
		crtc = card.CRTCs[0]
	}
	if crtc == nil {
		_ = card.Close()
		return nil, fmt.Errorf("kms: no CRTC available")
	}

	scan, err := card.CreateScanout(uint32(mode.HDisplay), uint32(mode.VDisplay))
	if err != nil {
		_ = card.Close()
		return nil, fmt.Errorf("kms: scanout %dx%d: %w", mode.HDisplay, mode.VDisplay, err)
	}
	if err := card.SetModeAtomic(conn, crtc, mode, scan.FBID); err != nil {
		_ = scan.Close()
		_ = card.Close()
		return nil, fmt.Errorf("kms: atomic modeset %s: %w", mode.ModeName(), err)
	}

	fmt.Printf("[+] KMS: %s %s on CRTC %d (scanout %dx%d, fb %d)\n",
		conn.DisplayName(), mode.ModeName(), crtc.ID, mode.HDisplay, mode.VDisplay, scan.FBID)
	return &kmsDisplay{card: card, scan: scan, conn: conn, crtc: crtc, mode: mode}, nil
}

// startEvdevInput pumps /dev/input devices into the shared ring buffer for
// bare-metal mode (no parent compositor to forward events). The existing
// input-consumer loop drains the ring into the router unchanged.
func startEvdevInput(rb *input.RingBuffer, gestures *input.GestureRouter) (*evdev.EvdevPump, error) {
	pump, err := evdev.NewEvdevPump(rb)
	if err != nil {
		return nil, fmt.Errorf("kms: evdev pump: %w", err)
	}
	if gestures != nil {
		pump.SetGestureRouter(gestures)
	}
	if n := pump.ScanDevices(); n == 0 {
		_ = pump.Close()
		return nil, fmt.Errorf("kms: no accessible /dev/input/event* devices")
	}
	go func() {
		for {
			if _, err := pump.PollAndDispatch(50); err != nil {
				return
			}
		}
	}()
	return pump, nil
}
