# Positioning: Stratifi vs. the Wayland Tiling Compositors

Companion to [`manifesto.md`](manifesto.md). The point of this document is to make the
delta crisp. Scrollable columns alone are not a differentiator — niri already did that,
and did it well. Stratifi has to earn its place on axes niri, sway, and river do not
occupy simultaneously.

## Comparison

| | **niri** | **sway** | **river** | **stratifi** |
| --- | --- | --- | --- | --- |
| Language / build deps | Rust, wlroots | C, wlroots | Zig, wlroots | **Go, `purego`, no cgo** |
| Base / compositor core | wlroots | wlroots | wlroots | **from scratch (own KMS, protocol, renderer)** |
| Renderer | wlroots (GL) | wlroots (GL) | wlroots (GL) | **own Vulkan engine** |
| Layout model | scrollable columns | manual tiling (i3) | dynamic tiling | **X ribbon + radial strata + Z depth** |
| Decorations | server-side | server-side | server-side | **server-side, no CSD ever** |
| Compositor *is* the session | yes | yes | yes | **no — durable session, disposable core** |
| Survives crash / update | no (clients die) | no | no | **yes, by construction** |
| Config model | KDL file | i3-like file | *external clients only* | built-in + IPC, first-class |
| Built-in shell (bar / launcher / lock) | partially | separate binaries | separate clients | **in the compositor** |
| Motion / physics | smooth animations | minimal | minimal | **deterministic kinetic physics** |
| Reduce-motion / accessibility mode | partial | partial | partial | **committed principle** |
| Input model | keyboard + touchpad gestures | keyboard-first | keyboard + config | **keyboard grammar + gestures + pointer** |

*(Hyprland — a non-wlroots, animation-first C++ compositor — is the closest point in the
design space on feel and the farthest on architecture. It is worth naming in public
writing for honesty.)*

## The three axes that actually differentiate

### 1. Interaction — the spatial model (X → R → Z)
Niri owns the ribbon; Stratifi extends it into a manifold. Radial strata bound the cost of
reaching a window by geometry rather than index, and Z-depth provides semantic zoom and
temporal history. This is the axis a user *feels*, and it is the headline.

### 2. Architecture — the compositor is not the session
Every existing Wayland compositor conflates the compositor with the session: crash it and
your clients die. Stratifi restores X11's durable-server / disposable-WM split, and pushes
it further by keeping KMS, input, buffers, and sync objects in the durable half. This is
the moat. It is also the claim most likely to be met with "why doesn't wlroots do that?"

### 3. Engineering — a from-scratch, data-oriented pipeline
Pure Go with no cgo, a native Vulkan renderer, data-oriented hot paths with a zero
allocation steady state, and a frame budget treated as a contract. This is why the physics
can be kinetic without breaking legibility, and why the thing can be updated live.

## What we are explicitly *not* claiming

- **Not more protocol coverage than wlroots.** wlroots is years of accumulated protocols,
  hardware quirks, and multi-GPU handling. Stratifi starts blind and will not match that
  coverage for a long time. Position on architecture and interaction, not on feature
  parity.
- **Not more stable today.** A durable session is a *design* guarantee; until it is built
  and hammered, wlroots-based compositors are the safer bet.
- **Not for everyone.** A spatial/physics model with server-side chrome and no CSD is a
  strong opinion. Some users want classic floating or classic i3. That is fine.

## The one-line pitch

> niri gave us the ribbon. Stratifi keeps it, adds a radial and a depth axis, and — unlike
> every Wayland compositor before it — doesn't take your session with it when it updates.
