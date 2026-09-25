package ballistics

import (
	"math"
)

// CurveProfile selects the pointer acceleration mathematical model.
type CurveProfile uint8

const (
	// ProfileLinear applies a constant sensitivity multiplier without velocity-based curve (1:1 esports mode).
	ProfileLinear CurveProfile = iota
	// ProfileAdaptiveSigmoid applies a smooth sigmoid curve for precision at low speeds and swift multi-monitor flicks.
	ProfileAdaptiveSigmoid
	// ProfileCustom uses custom power coefficients.
	ProfileCustom
)

// Config defines pointer acceleration parameters.
type Config struct {
	Profile    CurveProfile
	Speed      float64 // Overall sensitivity multiplier (1.0 = normal)
	Threshold  float64 // Speed threshold (px/ms) where acceleration begins
	MaxAccel   float64 // Maximum acceleration factor ceiling
	Exponent   float64 // Power exponent for custom curve
	Smoothing  float64 // Low-pass filter weight (0.0 to 1.0, 0.7 = recommended)
}

// DefaultConfig provides ergonomic desktop defaults.
func DefaultConfig() Config {
	return Config{
		Profile:   ProfileAdaptiveSigmoid,
		Speed:     1.0,
		Threshold: 0.4,  // 0.4 px/ms (~400 px/sec)
		MaxAccel:  3.5,  // 3.5x max speed boost
		Exponent:  2.0,
		Smoothing: 0.65,
	}
}

// LinearConfig provides unaccelerated 1:1 gaming precision.
func LinearConfig(speed float64) Config {
	if speed <= 0 {
		speed = 1.0
	}
	return Config{
		Profile: ProfileLinear,
		Speed:   speed,
	}
}

// BallisticsEngine tracks optical sensor velocity and computes accelerated subpixel displacements.
type BallisticsEngine struct {
	cfg Config

	lastTimeUsec uint64
	smoothedVel  float64 // Low-pass filtered velocity (px/ms)
}

// NewBallisticsEngine constructs a ballistics engine with the given configuration.
func NewBallisticsEngine(cfg Config) *BallisticsEngine {
	if cfg.Speed <= 0 {
		cfg.Speed = 1.0
	}
	if cfg.MaxAccel <= 0 {
		cfg.MaxAccel = 1.0
	}
	if cfg.Smoothing <= 0 || cfg.Smoothing > 1.0 {
		cfg.Smoothing = 0.65
	}

	return &BallisticsEngine{
		cfg: cfg,
	}
}

// SetConfig updates acceleration settings on the fly.
func (b *BallisticsEngine) SetConfig(cfg Config) {
	b.cfg = cfg
}

// Accelerate takes raw hardware relative counts (dx, dy) and kernel timestamp (timeUsec),
// applying velocity tracking, low-pass smoothing, and acceleration curves.
// Operates with zero heap allocations (0 B/op, 0 allocs/op).
func (b *BallisticsEngine) Accelerate(rawDx, rawDy float64, timeUsec uint64) (accelDx, accelDy float64) {
	if rawDx == 0 && rawDy == 0 {
		return 0, 0
	}

	// Calculate delta time
	var dtMs float64
	if b.lastTimeUsec > 0 && timeUsec > b.lastTimeUsec {
		dtMs = float64(timeUsec-b.lastTimeUsec) / 1000.0
	} else {
		dtMs = 8.0 // default ~125Hz polling fallback
	}
	b.lastTimeUsec = timeUsec

	// Clamp dt to avoid division by zero or extreme pauses (1ms to 100ms)
	if dtMs < 1.0 {
		dtMs = 1.0
	} else if dtMs > 100.0 {
		dtMs = 100.0
		b.smoothedVel = 0
	}

	rawDist := math.Hypot(rawDx, rawDy)
	instantVel := rawDist / dtMs // px/ms

	// Exponential Moving Average (EMA) low-pass filter to smooth sensor noise
	b.smoothedVel = b.cfg.Smoothing*b.smoothedVel + (1.0-b.cfg.Smoothing)*instantVel

	var factor float64

	switch b.cfg.Profile {
	case ProfileLinear:
		factor = b.cfg.Speed

	case ProfileAdaptiveSigmoid:
		if b.smoothedVel <= b.cfg.Threshold {
			// Sub-threshold: precision mode (1.0x or slightly lower)
			factor = b.cfg.Speed
		} else {
			// Sigmoid acceleration: 1 + (MaxAccel - 1) / (1 + e^(-k * (v - v0)))
			vNorm := (b.smoothedVel - b.cfg.Threshold) / b.cfg.Threshold
			sig := (2.0 / (1.0 + math.Exp(-vNorm))) - 1.0 // 0.0 to 1.0
			factor = b.cfg.Speed * (1.0 + (b.cfg.MaxAccel-1.0)*sig)
		}

	case ProfileCustom:
		if b.smoothedVel <= b.cfg.Threshold {
			factor = b.cfg.Speed
		} else {
			excess := b.smoothedVel - b.cfg.Threshold
			boost := math.Pow(excess, b.cfg.Exponent)
			factor = b.cfg.Speed * math.Min(1.0+boost, b.cfg.MaxAccel)
		}
	}

	return rawDx * factor, rawDy * factor
}

// Reset clears velocity history (e.g. after cursor warp or window change).
func (b *BallisticsEngine) Reset() {
	b.lastTimeUsec = 0
	b.smoothedVel = 0
}
