package main

// Measure the FFI hop itself: a C function call through cgo vs a direct Go
// call. This is the Q2 answer for the hot path (draw_color_quad is per-quad).
import (
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

/*
typedef struct { float x,y,w,h; } Rect;
static int c_sum(Rect* r) { return (int)(r->x + r->y + r->w + r->h); }
*/
import "C"

type rect struct{ x, y, w, h float32 }

//go:noinline
func goSum(r *rect) int { return int(r.x + r.y + r.w + r.h) }

func bench(name string, f func() int) {
	const N = 20_000_000
	start := time.Now()
	sink := 0
	for i := 0; i < N; i++ {
		sink += f()
	}
	d := time.Since(start)
	fmt.Printf("%-16s %7.1f ns/op  (sink=%d)\n", name, float64(d.Nanoseconds())/float64(N), sink)
	runtime.KeepAlive(sink)
}

func main() {
	r := rect{1, 2, 3, 4}
	cr := (*C.Rect)(unsafe.Pointer(&r))
	bench("go call (noinline)", func() int { return goSum(&r) })
	bench("cgo call", func() int { return int(C.c_sum(cr)) })
}
