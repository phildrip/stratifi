package layout

// Point represents a 2D coordinate on the screen.
type Point struct {
	X int32
	Y int32
}

// Rect represents a 2D rectangle.
// It is exactly 16 bytes, packing 4 Rects per 64-byte CPU cache line.
type Rect struct {
	X int32
	Y int32
	W int32
	H int32
}

// Contains returns true if the point falls inside the rectangle boundaries.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

// Inset shrinks the rectangle evenly by amount on all 4 sides.
func (r Rect) Inset(amount int32) Rect {
	if r.W <= amount*2 || r.H <= amount*2 {
		return Rect{X: r.X, Y: r.Y, W: 0, H: 0}
	}
	return Rect{
		X: r.X + amount,
		Y: r.Y + amount,
		W: r.W - amount*2,
		H: r.H - amount*2,
	}
}

// Intersects returns true if two rectangles overlap.
func (r Rect) Intersects(o Rect) bool {
	return r.X < o.X+o.W && r.X+r.W > o.X && r.Y < o.Y+o.H && r.Y+r.H > o.Y
}

// Area returns the total area in square pixels.
func (r Rect) Area() int64 {
	if r.W <= 0 || r.H <= 0 {
		return 0
	}
	return int64(r.W) * int64(r.H)
}
