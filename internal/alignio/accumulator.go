package alignio

import "fmt"

// SpanFunc inspects a buffer of at least minHeader bytes and returns the total
// byte span (header + body) of the next framed message, or an error if the
// header is malformed.
type SpanFunc func(buf []byte) (int, error)

// Accumulator frames discrete protocol messages across arbitrary stream-socket
// reads. One recv may deliver a partial header, a partial body, or multiple
// coalesced messages; Next yields exactly one complete message at a time.
type Accumulator struct {
	raw []byte
	buf []byte
}

// Init binds the accumulator to a pre-allocated backing slice so steady-state
// reads reuse capacity without heap allocations. Calling Append on a zero-value
// Accumulator also works (allocating on demand).
func (a *Accumulator) Init(backing []byte) {
	a.raw = backing[:0]
	a.buf = a.raw
}

// Append adds newly read stream bytes to the rolling buffer. When all prior
// bytes have been consumed and a backing slice was provided via Init, the
// slice header is reset to the start of the backing array first.
func (a *Accumulator) Append(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if len(a.buf) == 0 && cap(a.raw) > 0 {
		a.buf = a.raw[:0]
	}
	a.buf = append(a.buf, chunk...)
}

// Next returns the next complete message of length >= minHeader as determined
// by spanFn.
//
//   - If fewer than minHeader bytes or fewer than span bytes are buffered,
//     Next returns (nil, false, nil) so the caller can read more from the socket.
//   - If spanFn returns an error (or a span smaller than minHeader), Next
//     returns (nil, false, err).
//   - Otherwise Next returns (msg, true, nil) and advances past msg. The
//     returned slice aliases the accumulator buffer until the next Append
//     that rewinds an empty buffer.
func (a *Accumulator) Next(minHeader int, spanFn SpanFunc) ([]byte, bool, error) {
	if len(a.buf) < minHeader {
		return nil, false, nil
	}
	span, err := spanFn(a.buf)
	if err != nil {
		return nil, false, err
	}
	if span < minHeader {
		return nil, false, fmt.Errorf("alignio: invalid frame span %d (< header %d)", span, minHeader)
	}
	if len(a.buf) < span {
		return nil, false, nil
	}
	msg := a.buf[:span]
	a.buf = a.buf[span:]
	return msg, true, nil
}

// HasNext reports whether at least one complete message is already buffered
// without consuming it. It never blocks.
func (a *Accumulator) HasNext(minHeader int, spanFn SpanFunc) bool {
	if len(a.buf) < minHeader {
		return false
	}
	span, err := spanFn(a.buf)
	return err == nil && span >= minHeader && len(a.buf) >= span
}

// Len returns the number of unconsumed bytes currently buffered.
func (a *Accumulator) Len() int {
	return len(a.buf)
}
