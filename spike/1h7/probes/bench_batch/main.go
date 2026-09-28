package main

// The decisive design question: per-call FFI hops (current design: one cgo
// call per quad) vs ONE cgo call per frame carrying the whole draw list
// (batched). Measure both.
import (
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

/*
typedef struct { float x,y,w,h,r,g,b,a; } Quad;
static int c_draw_one(Quad* q) { return (int)q->x; }
static int c_draw_many(Quad* qs, int n) { int s=0; for (int i=0;i<n;i++) s+=(int)qs[i].x; return s; }
*/
import "C"

type quad struct{ x, y, w, h, r, g, b, a float32 }

func bench(name string, f func()) {
	const N = 1_000_000
	start := time.Now()
	f()
	d := time.Since(start)
	fmt.Printf("%-34s %9.1f ns/op\n", name, float64(d.Nanoseconds())/float64(N))
	runtime.KeepAlive(0)
}

func main() {
	const ( N = 1_000_000; quads = 4 ) // typical chrome quads per window; the frame has a handful
	qs := make([]quad, quads)
	cq := make([]C.Quad, quads)
	for i := range qs { cq[i].x = C.float(float32(i)) }
	var one quad; oneC := (*C.Quad)(unsafe.Pointer(&one))

	bench("cgo: 1 call/quad (current)", func() {
		for i := 0; i < N; i++ {
			for j := 0; j < quads; j++ { _ = int(C.c_draw_many(&cq[j], 1)) }
		}
	})
	bench("cgo: 1 call/frame (batched)", func() {
		for i := 0; i < N; i++ { _ = int(C.c_draw_many(&cq[0], quads)) }
	})
	bench("pure go: 0 FFI, 1 pass/frame", func() {
		for i := 0; i < N; i++ {
			for j := 0; j < quads; j++ { _ = int(oneC.x) }
		}
	})
	_ = one
}
