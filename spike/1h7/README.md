# bloxwm-1h7 spike — FFI + runtime Vulkan loading

Prototypes answering the spike's questions with measurements. Do NOT wire
these into the build; they are evidence for the go/no-go.

| File | Question it answers |
|---|---|
| probe_dlopen.go | Can one binary resolve libvulkan at RUNTIME (dlopen) instead of link time? |
| probe_missing.go | When the loader is absent, does the binary fall back cleanly? |
| bench.go | Cost of one cgo hop vs an equivalent Go call (per-call FFI tax). |
| bench_batch.go | Per-quad cgo calls (current design) vs one batched call per frame. |

Reproduce: `CGO_ENABLED=1 go run ./bench.go` etc. Measured on the author's
machine (AMD RENOIR, RADV, go1.25.4 linux/amd64):

- dlopen-only cgo shim links **zero** vulkan (ldd), vs 1 for the current binary.
- Absent loader -> `dlopen` returns nil -> clean CPU fallback, no crash.
- One cgo call ≈ 33.9 ns vs 2.4 ns for the equivalent Go call (~14x).
- Current per-quad-call design: 138 ns for 4 quads; batched per-frame: 32 ns.

Overhead is <1% of a 16ms frame: cost is not the blocker; state ownership is.
