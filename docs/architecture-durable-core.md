# Architecture Note: Durable Server, Disposable Core

Companion to [`manifesto.md`](manifesto.md) §4. This is the version of the design that
has to survive a skeptical reviewer, so it is deliberately specific about *where state
lives* and *what happens during a restart*.

## The invariant

> At no point may the loss or replacement of the rendering and layout logic terminate a
> client's Wayland connection, drop a committed buffer, change the DRM mode, or lose an
> input device.

Everything a client can observe must live in a process that **does not restart**.

The corollary is the whole design: the process clients connect to must be the process
that owns the Wayland socket, the DRM master file descriptor, and the input devices. A
forwarding proxy fails this invariant, because the *protocol state* — surfaces, role
objects, buffers, sync objects, seats — would still live in the thing that restarts.

## Ownership map

| Resource | Owner | Survives a core reload |
| --- | --- | --- |
| `$WAYLAND_DISPLAY` socket | session | yes |
| `wl_display` and every protocol object | session | yes |
| Client buffers (dmabuf / shm fds) | session (dup'd) | yes |
| Acquire / release syncobj fds | session | yes |
| DRM card node, master, KMS mode, planes | session | yes |
| Input devices (logind evdev fds) | session | yes |
| Last presented frame (scanout dmabuf) | session | yes |
| Vulkan device/context, render node | core | no — rebuilt |
| Scenegraph, layout, focus, animation | core | no — rebuilt |
| In-flight GPU submissions and fences | core | no — quiesced |

`session` = `stratifi-session` (durable daemon); `core` = `stratifi-core` (disposable
renderer).

## Buffer path

- The **core renders on the render node** (`/dev/dri/renderD128`), which needs no DRM
  master and is openable by the user.
- The **session owns the card node and DRM master** and performs atomic commits and page
  flips.
- The core allocates the composed frame as a **dmabuf** (GBM on the render node, or a
  `VK_EXT_image_drm_format_modifier` export) and passes the fd to the session, which
  imports it as a framebuffer and scans it out.
- The session advertises the **KMS plane formats and modifiers** to the core at attach
  time, so the core allocates scanout-compatible buffers. No format guessing, ever.

Client buffers arrive at the **session** (it is the server) and are `dup`'d. The session
forwards each buffer fd to the core once; the core caches the Vulkan import keyed by
buffer identity. On reload, the session re-sends fds for every live surface.

## Explicit synchronization

Modern clients use **`wp_linux_drm_syncobj_v1`**: the client hands over acquire and
release **drm syncobj fds**, which are shareable across processes via `SCM_RIGHTS`.

- The **session owns the syncobj fds**, so they persist across a reload.
- The **core waits** on the acquire point before sampling a buffer and **signals** the
  release point when its GPU work on that buffer is done.
- The session relays the release back to the client through the protocol.

The hard rule: a syncobj fd must never be allowed to close because the core restarted. It
lives in the session; the core only borrows it. A restarted core re-arms its waits against
the same fds.

For clients on the legacy implicit-sync path, the session must guarantee that the GPU has
finished with a buffer before it can be treated as reusable; it can do so by holding a
fence fd exported at commit time and waiting on it before releasing the buffer.

## Handoff state machine

```
RUNNING ──(update signal)──▶ DRAINING ──▶ FROZEN ──▶ ATTACH ──▶ RECONCILING ──▶ RUNNING
```

- **DRAINING** — the core stops accepting new input, finishes the in-flight frame,
  releases every buffer, and reports `READY`.
- **FROZEN** — the core `exec`s itself (`syscall.Exec`: same PID, non-`CLOEXEC` fds
  persist). The session holds the last presented frame on screen and buffers input
  events. Clients may still commit; the session retains the newest buffer per surface and
  **pauses** `wl_surface.frame` callbacks.
- **ATTACH** — the new image starts, inherits the control socket and render-node fd, and
  re-initializes Vulkan.
- **RECONCILING** — the core queries the session's authoritative surface table (app_id,
  title, geometry, latest buffer, role), rebuilds the scenegraph, and emits
  `xdg_toplevel.configure` wherever the new layout disagrees. The first frame is handed
  back and frame callbacks resume.

The FROZEN window should be a handful of frames (tens of milliseconds). If it exceeds a
bound, the session keeps the frozen frame and can overlay a "restarting" indicator that
the session renders itself, independent of the core.

## Failure modes and mitigations

- **Core crash (segfault) rather than self-exec.** The session sees the socket close,
  respawns a fresh core, and runs the same RECONCILING path. The PID changes; the session
  does not care.
- **Core will not come up (bad build).** Self-restart can brick the display. Mitigation is
  mandatory, not optional: retain the last-known-good binary, and after *N* consecutive
  failed attaches `exec` the previous binary and surface a diagnostic.
- **Reload during an interactive grab (move/resize).** The grab is cancelled; the session
  keeps raw input but releases the grab. Documented edge; a compositor reload is rare.
- **Input latency from the extra hop.** Mitigations: (a) a shared-memory ring buffer plus
  `io_uring` for the input path; (b) the session owns a **dedicated cursor plane** and
  moves the hardware cursor itself, so pointer motion is zero-core-latency; (c) measure
  end-to-end latency with input timestamps under load and hold it to a sub-frame budget.
- **Frame-callback starvation while FROZEN.** Pausing callbacks stalls vsync-throttled
  clients, so the window must be bounded and short.
- **Format/modifier mismatch.** The session must publish plane capabilities up front and
  the core must never allocate an un-scanoutable buffer. Prototype this handshake first;
  it is the single most likely place to lose a week.

## Why not a forwarding proxy?

A forwarding proxy keeps the client socket in one place and the protocol state in another.
On reload you must *replay* the entire object graph — every `wl_surface`, role object,
sub-surface tree, seat capability change, and in-flight sync object. That is strictly
harder and strictly more fragile than keeping the state where it already is. The session
should be the server.

## Prior art

- **X11** — the server is durable; the window manager is a disposable client. Stratifi
  restores this property to Wayland, where it was lost.
- **Android SurfaceFlinger** — the compositor is a durable system service while window
  *policy* lives in a separate process (`system_server` / WindowManager). Directly
  analogous.
- **systemd socket activation** — keep the socket, restart the service: the same
  durability pattern one layer down.
- **Arcan** — separates the display server from the window manager over a socket.

The novelty is not any single piece. It is applying the durable/disposable split to a
Wayland compositor that also owns **KMS and input**, so that updating the core is
invisible to every running client.

## Open questions to prototype early

1. The GBM / dmabuf / modifier handshake between render node and card node across
   processes.
2. `syscall.Exec` on a Go binary with a live Vulkan device: what quiescing is sufficient,
   and are inherited fds reliably usable by the new image?
3. End-to-end input and presentation latency versus a monolithic compositor.
4. Syncobj forwarding and the release-relay path under a mid-flight reload.
