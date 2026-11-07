package compositor

// MemOutput is an in-memory Output implementation used for headless testing
// and software sub-output composition.
type MemOutput struct {
	width  int32
	height int32
	stride int32
	pix    []byte
}

// NewMemOutput allocates a tightly-packed ARGB8888 in-memory output.
func NewMemOutput(w, h int32) *MemOutput {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	stride := w * 4
	return &MemOutput{
		width:  w,
		height: h,
		stride: stride,
		pix:    make([]byte, int(stride)*int(h)),
	}
}

func (m *MemOutput) Width() int32            { return m.width }
func (m *MemOutput) Height() int32           { return m.height }
func (m *MemOutput) Stride() int32           { return m.stride }
func (m *MemOutput) Pixels() []byte          { return m.pix }
func (m *MemOutput) Damage(x, y, w, h int32) {}
func (m *MemOutput) Present() error          { return nil }

// PixelAt returns the ARGB8888 pixel at (x, y) or 0 if out of bounds.
func (m *MemOutput) PixelAt(x, y int32) uint32 {
	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return 0
	}
	off := int(y)*int(m.stride) + int(x)*4
	return uint32(m.pix[off]) |
		uint32(m.pix[off+1])<<8 |
		uint32(m.pix[off+2])<<16 |
		uint32(m.pix[off+3])<<24
}

// outputSlice is a flat value record per child output in a MultiOutput,
// precomputed at construction/reconfiguration so Present() never allocates.
type outputTarget struct {
	out    Output
	xOff   int32
	width  int32
	height int32
	stride int32
	pix    []byte
}

// MultiOutput multiplexes a single unified FrameLoop canvas onto N physical
// or virtual child outputs under either a horizontal "join" policy or a
// clone "mirror" policy.
//
// Hot-path contract: Damage() and Present() perform 0 heap allocations.
type MultiOutput struct {
	targets []outputTarget
	mirror  bool
	width   int32
	height  int32
	stride  int32
	pix     []byte
}

// NewMultiOutput constructs a MultiOutput over the given child outputs.
// When mirror is false ("join" policy), child outputs are arranged
// side-by-side left-to-right; when mirror is true ("mirror" policy), all
// child outputs display the primary output's canvas region.
func NewMultiOutput[T Output](outputs []T, mirror bool) *MultiOutput {
	m := &MultiOutput{
		targets: make([]outputTarget, 0, len(outputs)),
		mirror:  mirror,
	}
	var totalW, maxH int32
	for _, o := range outputs {
		w := o.Width()
		h := o.Height()
		if w <= 0 || h <= 0 {
			continue
		}
		xOff := totalW
		if mirror {
			xOff = 0
		}
		m.targets = append(m.targets, outputTarget{
			out:    o,
			xOff:   xOff,
			width:  w,
			height: h,
			stride: o.Stride(),
			pix:    o.Pixels(),
		})
		if mirror {
			if totalW == 0 || w < totalW {
				totalW = w
			}
			if maxH == 0 || h < maxH {
				maxH = h
			}
		} else {
			totalW += w
			if h > maxH {
				maxH = h
			}
		}
	}
	if totalW < 1 {
		totalW = 1
	}
	if maxH < 1 {
		maxH = 1
	}
	m.width = totalW
	m.height = maxH
	m.stride = totalW * 4
	m.pix = make([]byte, int(m.stride)*int(m.height))
	return m
}

func (m *MultiOutput) Width() int32   { return m.width }
func (m *MultiOutput) Height() int32  { return m.height }
func (m *MultiOutput) Stride() int32  { return m.stride }
func (m *MultiOutput) Pixels() []byte { return m.pix }

// Damage forwards clipped damage regions to each child output.
func (m *MultiOutput) Damage(x, y, w, h int32) {
	for i := range m.targets {
		t := &m.targets[i]
		dx := x - t.xOff
		dw := w
		if dx < 0 {
			dw += dx
			dx = 0
		}
		if dx+dw > t.width {
			dw = t.width - dx
		}
		dy := y
		dh := h
		if dy < 0 {
			dh += dy
			dy = 0
		}
		if dy+dh > t.height {
			dh = t.height - dy
		}
		if dw > 0 && dh > 0 {
			t.out.Damage(dx, dy, dw, dh)
		}
	}
}

// Present blits each child output's viewport slice from the unified canvas
// and calls Present on every child output. Zero allocations on the hot path.
func (m *MultiOutput) Present() error {
	var firstErr error
	srcStride := int(m.stride)
	for i := range m.targets {
		t := &m.targets[i]
		dstPix := t.pix
		dstStride := int(t.stride)
		copyW := t.width
		if t.xOff+copyW > m.width {
			copyW = m.width - t.xOff
		}
		copyH := t.height
		if copyH > m.height {
			copyH = m.height
		}
		if copyW > 0 && copyH > 0 && len(dstPix) >= int(copyH)*dstStride {
			rowBytes := int(copyW) * 4
			srcXBytes := int(t.xOff) * 4
			for row := 0; row < int(copyH); row++ {
				sOff := row*srcStride + srcXBytes
				dOff := row * dstStride
				copy(dstPix[dOff:dOff+rowBytes], m.pix[sOff:sOff+rowBytes])
			}
		}
		if err := t.out.Present(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
