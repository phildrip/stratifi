# stratifi

A block-based Wayland compositor and window manager, written in Go with a
data-oriented core. It tiles in [niri]-style scrollable columns, understands
touchpad gestures, runs layer-shell panels, and is being built to run as a
daily-driver session on bare metal (DRM/KMS) as well as nested inside another
compositor.

> **Status:** nested and bare-metal paths work; the bare-metal *live modeset*
> path is verified up to the point of needing a VT/DRM master. Expect rough
> edges — this is a daily-driver-in-progress, not a finished product.

## Build

```sh
go build ./...                       # everything
go build -o stratifi ./cmd/stratifi  # the compositor
go build -o stratifi-session  ./cmd/stratifi-session
go build -o stratifi-launch   ./cmd/stratifi-launch
```

Requires Go and a Vulkan loader at runtime for the GPU path (`libvulkan.so`).
The compositor does **not** link libinput; input is pure Go (`evdev`) on bare
metal, and comes from the parent compositor when nested.

## Run

### Nested (development)

Nested runs inside your current Wayland session and is the easiest way to try
it. The parent draws the pointer, so you don't need the cursor plane.

```sh
STRATIFI_GPU=1 ./stratifi &
WAYLAND_DISPLAY=wayland-stratifi-0 foot &
WAYLAND_DISPLAY=wayland-stratifi-0 waybar &   # optional panel
```

`scripts/nested-dev.sh` wraps this: `up`, `down`, `status`, `smoke`, `--kms`.

### Bare metal (DRM/KMS)

Needs DRM master — i.e. a VT with no other compositor. Switch to a spare VT
(`Ctrl+Alt+F3`), log in, then:

```sh
STRATIFI_KMS=1 STRATIFI_GPU=1 ./stratifi
```

### As a session (display manager)

```sh
install -Dm644 contrib/stratifi.desktop /usr/share/wayland-sessions/stratifi.desktop
install -Dm755 contrib/stratifi-session   /usr/local/bin/stratifi-session
```

Then pick **Stratifi** in your display manager's session menu.

### Environment

| Variable | Effect |
| :--- | :--- |
| `STRATIFI_KMS=1` | DRM/KMS backend (bare metal). |
| `STRATIFI_GPU=1` | Vulkan renderer; unset falls back to the CPU blitter. |
| `STRATIFI_SESSION_SOCKET` | Control socket of `stratifi-session`; when set, spawned clients are its children (they survive a compositor restart). |
| `STRATIFI_VK_DEVICE` / `STRATIFI_VK_VALIDATION` / `STRATIFI_TRACE` | Vulkan device override, validation layers, tracing. |

## Configuration

Read from `$XDG_CONFIG_HOME/stratifi/config` (else `~/.config/stratifi/config`).
Missing file = defaults. Duplicate scalar keys and malformed lines are a loud
startup error (a typo that silently does nothing is worse).

```conf
# geometry
gap 8                 # pixels between windows
border_width 3
titlebar_height 28
column_width 0.5      # scrollable column width, fraction of the output

# keybindings
bind Super+Return spawn foot
bind Super+q close
bind Super+f fullscreen

# gestures (touchpad; KMS/evdev only — nested has no touchpad source)
bind_gesture swipe left 3 workspace_next

# run at startup (via sh -c, routed through the session supervisor)
exec dbus-update-activation-environment --systemd WAYLAND_DISPLAY
exec swayidle -w timeout 300 'swaylock -f'
```

Lines ending in `\` continue on the next line.

### Keys

`bind COMBO ACTION [ARGS...]`. Modifiers: `Super` (aliases `Mod4`, `Win`),
`Ctrl`/`Control`, `Alt`/`Mod1`, `Shift` (case-insensitive). The last component
is the key:

```
a–z  0–9  minus equal backspace tab space enter return
escape semicolon grave comma dot slash
f1–f12  home end delete up down left right
```

### Actions

| Action | Effect |
| :--- | :--- |
| `spawn <cmd> [args...]` | Launch a client (routed through the session supervisor when set). |
| `close` / `quit` | Close the active window / exit the compositor. |
| `focus_next` / `focus_prev` | Focus the next/previous window. |
| `focus_col_next` / `focus_col_prev` | Scroll to the next/previous column. |
| `column_next` / `column_prev` | Aliases for `focus_col_next` / `focus_col_prev`. |
| `move_col_left` / `move_col_right` | Reorder the active column in the strip. |
| `focus_window_up` / `focus_window_down` | Focus within the active column's stack. |
| `move_window_up` / `move_window_down` | Reorder the active window within its column. |
| `fullscreen` | Toggle fullscreen for the active window. |
| `workspace <n>` / `move_to_workspace <n>` | Switch to / move the window to workspace *n*. |
| `workspace_back_and_forth` | Toggle to the previously visited workspace. |
| `reload` | Re-read the config file: bindings and layout metrics, live. |
| `workspace_next` / `workspace_prev` | Cycle workspaces. |
| `overview` | Recognised; not implemented yet. |

### Gestures

`bind_gesture <type> [direction] [fingers] <action>`. Direction and finger
count are optional filters.

```conf
bind_gesture swipe left 3  workspace_next
bind_gesture swipe right 3 workspace_prev
bind_gesture swipe up 4    overview
bind_gesture pinch 0 0     overview
```

### The launcher

`stratifi-launch` is an fzf-driven combi launcher (windows + installed apps +
"run a command"). Bind it and it opens its own terminal:

```conf
bind Super+d spawn stratifi-launch
# no compositor needed to inspect what it would show:
stratifi-launch --list
```

It talks to the compositor over `$XDG_RUNTIME_DIR/stratifi-ipc.sock`
(`LIST`, `FOCUS <id>`, `SPAWN <argv>`).

## Testing

```sh
scripts/check.sh           # the gate: format, vet, build, test, race, 0-alloc
scripts/check-allocs.sh    # just the hot-path 0-alloc assertion
scripts/lint.sh            # golangci-lint (optional; CI installs a current one)
go test ./...              # the suite
go test -race ./...
```

Hot paths must be **0 allocs/op** and `scripts/check-allocs.sh` fails the gate
if any regresses (see `skills/data-oriented-design.md`); CI runs the same
suite. Live bring-up smoke test: `scripts/nested-dev.sh smoke`.

## Architecture

- `cmd/stratifi` — the compositor: actor goroutine owning all scene state,
  driven by plain-data events (server callbacks and the input pump send, one
  goroutine renders).
- `internal/server` — the Wayland server (protocol handlers, per-client state).
- `internal/layout` — the window model: tagged columns, niri scrollable
  layout, workspaces.
- `internal/compositor` — the frame loop and the backend-agnostic `DrawList`.
- `internal/render/vulkan` — the Vulkan renderer; `internal/backend/kms` and
  `internal/backend/nested` — the two backends.
- `internal/protocol/*` — generated protocol bindings (from `protocol/specs`).
- `internal/ipc`, `internal/session` — the launcher control channel and the
  session supervisor.

Design principles live in `AGENTS.md` and `skills/`.

## Roadmap (highlights)

XWayland, multi-output, IME; live KMS modeset on a VT; screen-sharing
(PipeWire/portal); a pure-Go renderer FFI path so the binary runs without
libvulkan.

[niri]: https://github.com/YaLTeR/niri
