# Architecture Landscape: Responsibility in the Linux Desktop Stack

Companion to [`manifesto.md`](manifesto.md) and [`positioning.md`](positioning.md).

## Purpose

This is an architect's review of the existing Linux desktop stack, written to answer one
question: **who owns what, and where is that ownership broken?**

It exists because the stack has *components* but almost no *bounded contexts*. Reading
about Wayland, Xorg, D-Bus, or portals tends to produce the same reaction — "what is this
actually responsible for?" — because in most cases that question was never answered, or
answered differently at every layer. Stratifi's scope decisions are downstream of this
document, so it is worth writing down precisely.

## How to read it

Each domain below lists the **clean owner** (who should own it in a well-designed system),
the **actual owner** (who owns it today, if anyone), and a **state** verdict. The verdicts
are the interesting part:

- **clean** — one owner, defined contract.
- **contested** — more than one claimant, no referee.
- **undefined** — no owner at all.
- **leaked** — a component owning something that belongs to another context.
- **no authority** — a standard exists de facto but nothing is normative.

---

## 1. The responsibility matrix

| Domain | Clean owner | Actual owner | State |
| --- | --- | --- | --- |
| Scanout / modesetting | kernel DRM/KMS | kernel DRM/KMS | **clean** |
| GPU buffers / memory | kernel GEM + dma-buf, GBM for allocation | kernel + GBM | **mostly clean** |
| Sync / fences | kernel dma-fence / dma-syncobj | kernel; *policy* unclear for years, now `wp_linux_drm_syncobj_v1` | **recently pinned** |
| Input devices | kernel evdev → libinput (normalise) → compositor (route/focus) | same | **clean** |
| Surface composition | the display server | Wayland: the compositor. X11: an *optional external* compositor | **contested / dual meaning** |
| Window management (layout, focus, workspaces) | one spec'd, replaceable component | X11: a WM via ICCCM/EWMH. Wayland: *internal* to the compositor, unspecified | **undefined on Wayland** |
| Decorations | compositor (server-side) | contested — toolkit CSD usually wins | **contested** |
| Session / seat | one session manager | logind + display manager + compositor + D-Bus session | **duplicated** |
| Shell (bar / launcher / lock / notifications) | the desktop | varies; compositor or external apps | **varies** |
| Configuration / settings | one service with a published contract | GSettings / KDE / dconf / XSettings / toolkit-specific | **undefined** |
| Appearance / theming | one theme system | per-toolkit CSS and asset formats | **fragmented** |
| Clipboard / drag-and-drop | protocol | `wl_data_device` in-session; de-facto `wlr-data-control` across instances | **partly spec'd** |
| Screenshots / screen capture | protocol | portal + PipeWire + compositor | **bolted on** |
| Notifications | protocol | D-Bus `org.freedesktop.Notifications` | **out-of-band** |
| Global shortcuts / media keys | protocol | portal (`GlobalShortcuts`) + compositor + app | **late / uneven** |
| Input methods (IME) | compositor + one service | `text-input-v3` + ibus / fcitx daemons over D-Bus | **split** |
| Accessibility | core protocol | AT-SPI over D-Bus + toolkit-specific bridges | **bolted on** |
| Colour management / HDR | protocol | `wp_color_management` (very new), otherwise unowned | **undefined** |
| X11 compatibility | a *bounded* shim | Xwayland — permanent, load-bearing | **unclear boundary** |
| Standards / governance | a normative spec + an authority | wayland-protocols (consensus) + wlroots / GNOME / KDE (de facto) | **no authority** |

---

## 2. The five pathologies

### 2.1 Undefined — nobody's job
By far the most common failure. Not *badly owned* — *unowned*.

- **Window-management policy on Wayland.** X11 has EWMH: a WM is a separate client, and
  the spec says how it cooperates with the server. Wayland inlines window management into
  the compositor and specifies none of it. Layout, focus, workspaces, and stacking have no
  contract.
- **Configuration.** There is no desktop settings service with a normative contract.
  GSettings is GNOME's, `kdeglobals` is KDE's, XSettings is a relic, and every toolkit
  reads its own files.
- **Appearance.** Theming is per-toolkit; there is no cross-toolkit theme contract.
- **Accessibility authority.** AT-SPI works, but there is no owner who guarantees
  behaviour across compositors and toolkits.
- **Colour management / HDR.** Only now acquiring a protocol, with extensive unowned
  behaviour in between.

### 2.2 Contested — two owners, no referee
- **Composition.** On X11 a compositor is an *optional external client* that may or may not
  exist (hence `is_composited()` probes and fallback rendering paths). On Wayland the
  compositor *is* the display server. The same word names opposite ownership models, and
  code written against one assumption breaks under the other.
- **Decorations.** The compositor may offer server-side decorations
  (`xdg-decoration`); toolkits may draw client-side ones; both are "valid," and the result
  is whichever showed up first winning, plus the entire class of desktop-specific bugs
  (invisible resize regions, opaque shadow boxes, per-toolkit drift).
- **The session.** logind owns the seat and device grants, the display manager starts the
  session, the compositor is the session leader, and the D-Bus session bus is a fourth
  lifecycle. Four claimants, one word.

### 2.3 Leaked — a component owning what it shouldn't
- **Toolkits own desktop policy.** GTK, Qt, and Chromium each decide decoration, sizing,
  IME, and portal behaviour. Desktop UX is therefore authored by toolkits, not by the
  desktop.
- **D-Bus is a transport masquerading as an architecture.** It performs name routing and
  activation correctly. But the desktop's cross-process *services* — notifications,
  portals, secrets, MPRIS, settings — are hundreds of independently-designed interfaces
  with no schema authority. D-Bus is not responsible for that; nothing is.
- **Portals are a catch-all.** They exist because the protocol refused to specify whole
  domains. They are a pragmatic bridge, but they are not a responsibility model: each
  portal interface is unilaterally specified, and portal *backends* must re-implement the
  same behaviour per desktop.

### 2.4 No authority
There is no EWMH-equivalent for the modern desktop. "Standard Wayland behaviour" is the
intersection of three informal sources:

1. `wayland-protocols` — consensus-gated, deliberately slow.
2. **wlroots** — a *library*, not a spec, that became de facto standard by adoption.
3. **GNOME and KDE** — whose implementations are what applications actually test against.

Nothing is normative. So applications must feature-detect, and feature-detection is how a
"simple" protocol becomes a negotiation again.

### 2.5 Wrong granularity
- **Xorg** is a monolith. Server, drawing, input, modesetting, and session in one process,
  with extensions accreted over decades. Owns too much.
- **Wayland's core** is deliberately minimal. Owns too little, and the compositor absorbs
  the overflow.
- Nothing sits coherently **between** the two. There is no well-scoped middle layer with an
  explicit responsibility.

---

## 3. Vocabulary collisions

The nouns do not have stable referents across layers. This is a large part of why the
stack reads as incoherent, and it must be fixed in any new responsibility model.

| Term | Meaning A | Meaning B |
| --- | --- | --- |
| **compositor** | X11: optional external client (e.g. picom) | Wayland: the display server itself |
| **session** | logind session (seat + devices) | D-Bus session / "the compositor session" (running apps) |
| **seat** | logind `seat0`: a set of hardware | `wl_seat`: an input-capability object |
| **server** | X "server": owns drawing, input, display, session | Wayland "server": the compositor |
| **portal** | a D-Bus service | "the standard" for what the standard won't specify |
| **surface** | X11: a window/drawable | Wayland: a buffer-presentation object with no role |

---

## 4. The missing artifacts

Everything above is the consequence of never having written four things down. An architect
would specify:

1. **Bounded contexts with named owners.** Each domain has exactly one owner and a
   one-sentence responsibility. A domain with two owners is a defect; a domain with none is
   a defect.
2. **Contracts at the boundaries.** Versioned specs with invariants — and, critically, a
   *normative* authority for policy, not merely consensus.
3. **Explicit state ownership.** For every piece of state (buffers, sync objects, focus,
   configuration, session), exactly one process holds the authoritative copy. "Who holds
   it" is a decision, not an accident of which process opened the fd first.
4. **Failure domains.** Every boundary is a blast radius. The question "if this crashes,
   what dies with it?" was never asked; on Wayland the answer is "everything."

---

## 5. What stratifi owns — decision record

Read through the matrix, stratifi's stance becomes a set of explicit decisions rather than
implied ones. Each is a *responsibility claim*, and each belongs in the manifesto.

### Owns (single authority)
- **Window-management policy** — layout, focus, workspaces, stacking. The thing Wayland
  left unspecified, Stratifi specifies.
- **Decorations** — server-side, always. No CSD as the default.
- **Composition** — one meaning, one owner.
- **Shell** — bar, launcher, lock, notifications, rendered by the compositor.
- **Configuration and appearance** — one model, one theme system, cross-toolkit by
  construction.
- **Input routing and grabs.**
- **Session durability** — clients outlive the core (see
  [`architecture-durable-core.md`](architecture-durable-core.md)).

### Consumes (clean upstream contracts)
- Kernel **DRM/KMS**, **dma-buf/GEM**, **dma-fence/dma-syncobj**, **evdev**.
- **logind** for seat and device grants.
- **Wayland** as the client transport.

### Bridges (bounded, explicitly temporary or scoped)
- **Xwayland** — X11 compatibility, with a defined boundary rather than "permanent by
  accident."
- **xdg-desktop-portal**, **PipeWire**, and **D-Bus** where a cross-application service
  interface is unavoidable (capture, sandboxed file access, IME).
- **Input methods** — `text-input` plus an external engine, treated as a bounded service.

### Refuses
- CSD-driven policy delegation as the *default*.
- Feature-detection-driven behaviour as the normal way to learn what the desktop wants.
- Ungoverned per-service D-Bus interfaces as the desktop API.
- The equation *compositor = session*.

### Publishes (the missing authority)
- Stratifi cannot unilaterally become *the* standard, but it can publish a **normative
  policy spec** — the EWMH-equivalent the desktop has been missing — so that its decisions
  are documented, versioned, and portable rather than an implementation detail.

---

## Appendix: quick glossary of the load-bearing specs

- `xdg-shell` — the actual window-management protocol surface (roles, configure).
- `xdg-decoration` — optional server-side decorations; often ignored by toolkits.
- `wp_linux_drm_syncobj_v1` — explicit synchronisation (dma-syncobj fds).
- `ext-session-lock-v1` / `ext-idle-notify-v1` — lock and idle.
- `wlr-data-control`, `wlr-screencopy` — de facto protocols, not `wayland-protocols`.
- `xdg-desktop-portal` — D-Bus front end for privileged capabilities; backends per desktop.
- `wayland-protocols` — the consensus gate for new protocols.
- EWMH / ICCCM — X11's window-management contract; the thing Wayland has no analogue for.
