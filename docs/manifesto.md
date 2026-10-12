# Stratifi: The Spatial Compositor Manifesto

## The Negotiation Problem

The Linux desktop is not a system. It is a ceasefire between parties that do not trust each other.

The kernel owns the framebuffer. The display server owns pixels — in principle. The compositor owns composition, optionally. The window manager owns layout, as a separate client. The toolkit owns decorations, because no server could be trusted to exist. The theme owns appearance, but only for its own toolkit. And the window itself — the thing the user actually interacts with — is owned by nobody.

Every boundary between these layers is therefore a negotiation, and every negotiation leaves a scar: `_GTK_FRAME_EXTENTS`, `_NET_WM_*`, CSD-versus-SSD fallbacks, Xwayland translation, portals, `is_composited()` probes, per-toolkit CSS. The most basic guarantee a desktop can offer — that a window looks and behaves the same everywhere — is explicitly nobody's job.

For thirty years this produced a false dichotomy: the chaotic sprawl of floating rectangles versus the brittle geometry of rigid tiling. The infinite horizontal ribbon broke the deadlock and then introduced its own trap: a one-dimensional tape where context collapses to immediate neighbours and the user spends the day side-scrolling.

**Stratifi** ends the negotiation by ending the plurality. It is a single-authority compositor built from scratch in pure Go (no cgo), rendering natively through Vulkan. It treats the desktop as **kinetic spatial stratification** — an elastic, multi-plane manifold of tasks rather than a flat canvas or a novelty 3D stage. One authority owns the surface, its chrome, its composition, and its frame timing — and the session outlives that authority.

---

## 1. Foundational Tenets

**One authority owns the window.** Clients render content. Stratifi renders everything else: borders, titles, shadows, strata decoration, focus, and every transition. There are no client-side decorations. This is not a theme decision; it is the architecture. It deletes an entire class of bugs — mismatched chrome, invisible resize regions, opaque shadow boxes, per-toolkit appearance drift — by removing the boundary where they were born.

**Legibility is inviolable.** No warped text, no angled terminals, no skeuomorphic 3D. Vulkan exists here for sub-pixel kinetic physics, dynamic LOD filtering, and optical depth cues — never for effects that degrade reading. Any animation that makes text harder to read is a bug.

**Motion is deterministic.** Springs, damping, and momentum are not decoration; they are the input model. But the same gesture from the same state must always produce the same result. Physics is a predictability tool, not a slot machine.

**Responsiveness is 1:1 and tactile.** Touchpad and precision-wheel input is coupled directly to physical constants — critically-damped oscillators, real mass, real drag — instead of discrete jump-cuts. The pointer feels connected to a surface, not to a state machine.

**Search is first-class.** Spatial memory does not scale past a few dozen windows. The fastest way to a window is usually to type its name. Stratifi ships an omni-palette as a core primitive, not a plugin.

**The session outlives the compositor.** A crash or an update must never take down running applications. Session state and client communication are isolated from layout and rendering, by construction.

---

## 2. Spatial Architecture: X → R → Z

The ribbon is not thrown away; it is the local coordinate system of a larger manifold. Stratifi composes three spatial axes.

### The Ribbon (X)

Scrollable columns remain the base working unit: a familiar, stable axis for sequential work, with no window overlap and no geometry negotiation.

### Radial strata (R)

The tape's weakness is that the cost of reaching a window grows with its position. Radial organization bounds that cost by geometry rather than index: active focus sits at the core, immediate dependencies in the inner orbit, ambient background tasks on the perimeter. Moving between strata is a bounded rotational gesture or a constant-distance radial jump — the cost of a neighbour does not depend on how many neighbours exist. This is an interaction property, not a complexity class: it is about what the hand and eye must do, and it is why recall collapses less often than on a tape.

### Altitude and temporal depth (Z)

Pulling the camera back is not an Exposé gimmick; it is continuous semantic zoom. At Z=0 content is 1:1 and pixel-sharp. As altitude rises, viewports scale smoothly to reveal task constellations and peripheral work without a layout recalculation. Inactive and closed tasks sink backward along the same axis into a temporal horizon, so recovering a prior working state is a depth scrub rather than an archaeology project.

### Kinematic tethers

Windows keep associative links — editor to compiler to preview, terminal to its log. A tether is an elastic relationship that survives spatial distance, letting the user spring between related surfaces regardless of where the layout has put them.

### Semantic mass

Surfaces carry mass, and mass shapes navigation: a focused editor and its live build feel heavier than a scratch terminal. Crucially, mass is derived from **semantic state** — role, recency, relationship — fixed at layout time and deterministic. It is never driven by ambient machine load. A user must never be unable to predict how their own gesture will feel because a compiler happened to be running.

---

## 3. Feel and Interaction

- **Deterministic physics:** same state plus same input yields the same trajectory, every time.
- **Reduce motion is a right, not a setting buried in a menu.** A committed instant mode reproduces every action without animation, for accessibility and for users who simply want speed.
- **A keyboard grammar, not just gestures.** Kinetic input must not exclude keyboard-first users. Stratifi defines a modal keyboard model with counts and a which-key-style discovery layer, so the compositor is as fast from the home row as from the trackpad.
- **Multi-monitor is a first-class spatial concern.** Orbits and Z-depth are defined per output, with an explicit model for how strata span physical displays, rather than "the second monitor is where the radial layout went."

---

## 4. Architecture: A Durable Server, A Disposable Core

Stratifi's distinguishing engineering claim is that **the compositor is not the session**. Wayland's cardinal regression relative to X11 is exactly this: crash the compositor and every client dies with it. Stratifi restores the split — and then improves on it.

```
[ Wayland clients: foot, Alacritty, Chromium, ... ]
                 │  $WAYLAND_DISPLAY (wayland-0)
                 ▼
      ┌───────────────────────────────┐
      │        stratifi-session        │  durable Go daemon
      │  • owns the Wayland socket     │  owns everything the kernel
      │  • owns DRM/KMS master + mode  │  and the OS session care about
      │  • owns input devices          │  survives every core restart
      │  • holds client buffers (fds)  │
      └───────────────┬───────────────┘
                      │  internal socket + dmabuf handles
                      ▼
      ┌───────────────────────────────┐
      │         stratifi-core         │  disposable renderer
      │  • layout, animation, physics  │  hot-swappable via syscall.Exec
      │  • Vulkan scenegraph           │  rebuilt in <100ms
      │  • composites + presents       │
      └───────────────────────────────┘
```

The critical design decision: **the durable process is not a forwarding proxy — it is the server.** It owns the Wayland socket, the DRM/KMS master fd and mode, and the input devices. The core is a renderer that the daemon drives. This matters because a forwarding proxy still leaves the hard part to the core: protocol state. Buffers and `SCM_RIGHTS` are easy to relay; reconstructing every `wl_surface`, `xdg_surface`, and dmabuf handshake is not. By making the daemon the protocol owner, a core restart becomes what it should be — the compositor blinking, not the session dying.

**What survives a core reload:** every client and its file descriptors, the Wayland protocol state, DRM master and the current mode, all input devices, and the last presented frame (held on screen while the new core comes up).

**What is rebuilt:** the scenegraph, layout, focus, and animation state — from the daemon's authoritative client state, in well under 100ms.

The protocol-level details — the dmabuf and syncobj lifecycle across processes, DRM/KMS master handover, the restart state machine, and the failure modes — are worked through in [`architecture-durable-core.md`](architecture-durable-core.md).

---

## 5. Engineering Foundations

- **Pure Go, no cgo.** Vulkan and DRM/KMS are reached through `purego` runtime binding — native performance, no C toolchain in the build, no foreign-function marshalling in the hot path.
- **Data-oriented hot paths.** Input, layout, the vblank tick, and rendering operate on flat, contiguous memory with generational indices (`WindowID uint32`) and zero heap allocation in steady state, so the garbage collector never introduces a frame spike.
- **A frame budget as a contract.** Animation is soft real-time: the compositor is designed to meet its vblank deadline the way a game engine does, not on a best-effort basis.
- **Every change is proven.** Table-driven tests for layout and parsing, allocation benchmarks (`-benchmem`), and hardware cache-counter profiling (`perf stat`) on the hot paths.
- **Code that is easy to delete.** The desktop mess is decades of accretion that nobody can remove. Stratifi's contribution to the genre is a codebase whose explicit design goal is that whole subsystems can be deleted without a rewrite.

---

## 6. Bounded Unknowns

Ambition is cheap; scope is honest. The following are committed directions, explicitly out of the critical path:

- **Xwayland** for legacy clients, after the native path is solid.
- **Portals** for screenshots, file pickers, and desktop integration.
- **Multi-monitor, HiDPI/fractional scaling, VRR, and color management** — designed for once, centrally, rather than negotiated per toolkit.
- **A software/llvmpipe fallback**, because a compositor that cannot run in a VM will not attract contributors.
- **Prior art we build on and depart from:** X11's server/WM separation, systemd's socket activation, and SurfaceFlinger's durable-vs-disposable split. Stratifi's novelty is combining that durability with Wayland's single authority over the surface.

---

*One authority. Nothing negotiated. A session that outlives the code that draws it.*
