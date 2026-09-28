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

## Step 2 — pure-Go frame slice (device → submit → readback)

`spikevk/` drives instance → physical device → logical device → offscreen
target → clear → copy → mapped readback entirely in Go over resolved
function addresses (`spike/1h7/spikevk/`). Measured on this machine:

- `TestOpenInstanceOnLiveHardware`: instance + `RADV RENOIR`, pure Go.
- `TestFrameClearCopyReadback`: solid red renders correctly (exact bytes).
- `TestFrameRepeatedRenders`: 20×16×16 frames, fence discipline holds
  (wait → reset → record → submit → wait).
- `TestFrameCompositorSize`: 1280×720 full frame.
- `TestFallbackAbsentLoader` / `TestMissingSymbolIsNamedError`: acceptance
  halves — absent loader fails cleanly, unknown symbols error by name.
- `TestStructLayoutABI`: 46 struct sizes + 26 field offsets asserted against
  the C headers, so header drift fails loudly instead of corrupting driver
  state.

Timing (steady state): **15.8 ms/frame at 1280×720** (dominated by GPU
submit/wait latency, not FFI), **8 allocs/op** from cgo per-call overhead
(batch to one call/frame to reach ~0). The test binary links **zero**
libvulkan (ldd) — only libc — and falls back cleanly without it.

Design rules this forces on the migration (learned by hitting them):
- cgo resolves `C.*` **per file** against that file's own preamble: shared
  definitions live once in `vk_shims.go`, prototypes per calling file, and
  the prototypes must NOT be `static` (file-local = invisible at link).
- A Go func value cannot be forged from a code address (`pc == addr` crash):
  resolved symbols stay `unsafe.Pointer` and cross only into typed C shims.
- cgo forbids Go-pointer-to-Go-pointer: nested create-infos go through the
  `envelope` helper (copy in pure Go to C memory); leaf structs pass
  directly. Frame structs are leaf by construction.
- No floats cross by value (all XMM args travel in structs by pointer).

## Step 3 — textured-quad crate (the compositor's real work)

`spikevk` now renders textured quads end to end in pure Go: render pass,
framebuffer, sampler, descriptor ring, pipeline layout, graphics pipeline
(from the production `texture.vert/frag.spv`, embedded pinned copies),
texture upload via staging, draws with push constants, copy-out and
readback. New tests:

- `TestQuadTwoTexturesVisible`: two different textures, one frame, each
  visible where drawn — the descriptor-aliasing regression, now asserted
  with the ring as ordinary Go state (`descSets` + `descNext` reset per
  frame) instead of C statics.
- `TestQuadUploadRoundtrip`: exact-byte upload → sample → framebuffer.
- `BenchmarkTexturedFrame`: 1280×720 compositor-shaped frame (two client
  textures): **16.8 ms, 344 B / 10 allocs per op**. The allocs are the
  per-draw `envelope` closures; batching descriptor updates into one call
  recovers them. Wall time matches the C path's equivalent work (~15 ms),
  dominated by submit/wait latency and the 3.6 MB copy, not FFI.

Debugging notes that become migration rules: topology must be
`TRIANGLE_STRIP` (the shader emits a strip); a zero dynamic-state struct
has sType 0 and is invalid — pass nil; image-view swizzle `{0,0,0,0}` is
identity, `{0,1,2,3}` is `{IDENTITY,R,G,B}` (validation caught both, plus a
destroy-order gap now covered by `Close`).
