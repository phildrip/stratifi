# Data-Oriented Design (DOD) Guidelines for stratifi

`stratifi` is a Wayland compositor and window manager. Compositors are **soft real-time interactive graphics engines**. To achieve fluid 120Hz/144Hz/240Hz frame rates and responsive mouse/touchpad gesture tracking without micro-stutters or GC latency spikes in Go, **Data-Oriented Design (DOD) is mandatory across the core loop**.

---

## 1. The Core Rationale: Why DOD in Go?

### Eliminating Garbage Collection on Hot Paths
Go's concurrent mark-and-sweep GC is fast, but it must trace every pointer on the heap:
* **The Pointer Graph Trap (OOP):** If windows, surfaces, popups, outputs, and inputs are represented as deeply nested pointer networks (`*Window -> []*Surface -> *Buffer`), the GC must recursively traverse thousands of pointers during each sweep.
* **The DOD Solution (POD & Indices):** Slices of Plain Old Data (integers, floats, fixed arrays) containing **no pointers** are treated as opaque memory by the Go runtime. The GC inspects them in a single step and moves on.
* **Goal:** **Zero heap allocations (0 B/op)** in the input dispatch loop, layout calculation, and frame rendering loop.

---

## 2. Key Architecture Patterns for `stratifi`

### A. SlotMaps and Generational Indices (No Pointer References)
Never store pointers between compositor entities. Use compact, generational indices:

```go
type WindowID uint32 // High 16 bits = generation, Low 16 bits = index

type WindowSlot struct {
    Generation uint16
    Active     bool
}

// Flat contiguous storage
type WindowTable struct {
    Slots []WindowSlot
    
    // Contiguous component arrays (SoA or cache-friendly AoS)
    Rects     []Rect     // X, Y, Width, Height
    Titles    [][64]byte // Fixed-size or interned string table
    Outputs   []uint8    // Monitor index
    Flags     []uint32   // Floating, focused, minimized, etc.
}
```

* **Safety:** When a window closes, increment its `generation`. Any stale references using an old `WindowID` immediately fail validation instead of corrupting memory or dangling.
* **Cache Coherence:** Iterating over all window rectangles during mouse hit-testing or layout calculation reads contiguous memory sequentially, maximizing CPU L1/L2 cache line hits.

---

### B. Pre-Allocated Ring Buffers for Hardware & Wire I/O
Never allocate slices inside event loops:

```go
// Pre-allocated ring buffer for libinput events
type InputRingBuffer struct {
    Events [1024]InputEvent
    Head   uint32
    Tail   uint32
}
```

* Sockets, DMA-BUF file descriptor passing, and input queues must pull from and write to pre-allocated circular buffers.
* Scratch buffers for layout math or vertex generation must be reusable (using persistent structs or `sync.Pool` if variable-length).

---

### C. Structure of Arrays (SoA) for Throughput Math
When performing operations across many entities (e.g. rendering passes, collision/snapping checks, animation ticks):
* Group hot fields together: `[]Rect` (X, Y, W, H) processed in tight SIMD-friendly loops.
* Keep cold fields (window title, application ID, Wayland client PID) in separate arrays so they do not pollute cache lines during layout or rendering iterations.

---

### D. Value Semantics for Small Geometry
* Small types (`Rect`, `Point`, `Color`, `Extent`) must always be passed and returned **by value**.
* This keeps variables on the stack and completely avoids escape analysis pushing them to the heap.

---

## 3. CPU Cache Hierarchy & Memory Alignment

Modern x86-64 CPUs have 64-byte cache lines. Poor layout causes severe performance degradation via cache misses and false sharing:

* **Linear Traversal & Prefetching:** Keep active data strictly contiguous (`[]Rect`, `[]EntityState`). The CPU hardware prefetcher detects sequential strides and loads lines into L1 cache before the loop requests them. Avoid random index hopping.
* **64-Byte Cache Line Alignment & False Sharing:** If two goroutines concurrently write to independent variables located on the same 64-byte cache line, the CPU's cache coherency protocol (MESI) bounces the cache line back and forth, tanking throughput. Pad concurrently mutated shared structs to 64 bytes (`_ [64]byte` or `cpu.CacheLinePad`).
* **Compact Hot Structs:** Ensure hot structs fit neatly into fractions of a 64-byte line (e.g. 16-byte `Rect{X, Y, W, H int32}` allows 4 rectangles per cache line).

---

## 4. Enforcement, Benchmarking & Hardware Profiling

Compositor performance is measured in hardware reality, not theoretical big-O notation.

### A. Zero-Allocation Benchmarks (`-benchmem`)
Every core path (layout engine, wire decoder, input pump) must have table-driven benchmarks measuring allocations:
```bash
go test -bench=. -benchmem ./internal/...
```
* **Strict Rule:** Core layout calculations, wire packet decoders, and event pumps MUST report `0 B/op` and `0 allocs/op`.
* Any PR or change introducing an allocation on the hot path will be rejected.

### B. CPU Cache Profiling (`perf stat`)
We measure L1 and Last Level Cache (LLC) misses using Linux `perf`:
```bash
# Measure L1 D-Cache and LLC miss rates on layout/hot benchmarks:
perf stat -e L1-dcache-loads,L1-dcache-load-misses,LLC-loads,LLC-load-misses \
  go test -bench=BenchmarkLayout -benchtime=2s -run=^$ ./internal/layout
```
* **Target:** L1 data cache miss rate on hot array iterations should remain below 1-2%.
* **Target:** LLC (L3) miss rate on hot paths must be near 0%.

### C. Escape Analysis Audits
Audit the compiler's escape decisions to prevent stack-to-heap leaks:
```bash
go build -gcflags="-m -m" ./... 2>&1 | grep "escapes to heap"
```
Ensure no geometry, transient packets, or events escape to the heap.

### D. Pprof Profiling
Profile CPU cycles and hotspots periodically:
```bash
go test -bench=. -cpuprofile=cpu.prof ./internal/...
go tool pprof -top cpu.prof
```

