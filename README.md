# stratifi

**stratifi** is a block-based Wayland compositor and window manager written in Go with a strict **Data-Oriented Design (DOD)** core.

Built from first principles without `wlroots`, `libinput`, or build-time `libvulkan.so` linkage, stratifi combines [niri]-style scrollable column tiling, floating windows, scratchpads, and multi-output support with a predictable, **zero-allocation (`0 allocs/op`)** hot path across input routing, layout computation, Wayland wire framing, XWayland event handling, and frame rendering.

---

## Highlights

- **Scrollable Column Tiling + Floating Layer:** Arrange windows in an infinite horizontal strip of stackable columns, toggle floating windows with `Super`+drag move/resize, stash windows in a scratchpad, and switch named keybinding modes (`mode resize { ... }`).
- **Zero-Allocation Hot Paths:** Flat contiguous memory, generational IDs, lock-free ring buffers, and pre-allocated draw lists keep the compositor free of garbage-collection pauses. Every core hot path is gated at `0 allocs/op` in CI.
- **Bare-Metal DRM/KMS & Nested Backends:** Drives Linux DRM/KMS directly via `ioctl` (including atomic commits, DPMS power management, and a hardware cursor plane via `DRM_IOCTL_MODE_CURSOR2`) or runs nested inside an existing Wayland session for rapid development.
- **Runtime-Loaded Vulkan Renderer:** Loads `libvulkan.so.1` dynamically at runtime via `dlopen` (`VK_NO_PROTOTYPES`) with zero build-time Vulkan linkage and seamless fallback to the CPU blitter.
- **Pure-Go Input & Gestures:** Reads `/dev/input/event*` directly in Go (no `libinput`), supports live XKB keymap compilation/reloading (`xkb_layout`), and recognizes multi-touch touchpad swipes and pinches.
- **Multi-Output & Hotplug:** Supports `join` and `mirror` output policies, dynamic `wl_output` hotplug (`wl_registry.global` / `global_remove`), and per-output `zxdg_output_v1` geometry.
- **XWayland & IME:** Includes a pure-Go X11 display allocator, zero-allocation X11 window manager (`XWM`), `xwayland_shell_v1` surface association, and `zwp_text_input_manager_v3` for input methods.
- **Browser Screen Sharing:** Implements `ext-image-copy-capture-v1` and `ext-foreign-toplevel-list-v1` for whole-output and per-window capture via `xdg-desktop-portal-wlr` and PipeWire.
- **Crash-Resilient Session Supervisor:** `stratifi-session` supervises the compositor and owns spawned clients so a compositor restart does not tear down your applications.

---

## Build

Requires Go 1.24+ on Linux. No `libinput` or `libvulkan-dev` linkage is required to compile; at runtime, `libvulkan.so.1` is loaded on demand when `STRATIFI_GPU=1` is set.

```sh
go build ./...                                   # build everything
go build -o bin/stratifi         ./cmd/stratifi
go build -o bin/stratifi-session ./cmd/stratifi-session
go build -o bin/stratifi-launch  ./cmd/stratifi-launch
```

---

## Running stratifi

### Nested (Inside Your Current Wayland Session)

The fastest way to try stratifi without leaving your desktop:

```sh
STRATIFI_GPU=1 ./bin/stratifi &
WAYLAND_DISPLAY=wayland-stratifi-0 foot &
WAYLAND_DISPLAY=wayland-stratifi-0 waybar &   # optional layer-shell panel
```

`scripts/nested-dev.sh` automates this workflow (`up`, `down`, `status`, `smoke`, `--kms`).

### Bare Metal (DRM/KMS on a Linux VT)

Switch to an unused virtual terminal (`Ctrl+Alt+F3`), log in, and run:

```sh
STRATIFI_KMS=1 STRATIFI_GPU=1 ./bin/stratifi
```

Make sure your user belongs to the `input`, `video`, and `render` groups so stratifi can open `/dev/input/event*` and `/dev/dri/card*`.

### Display Manager (GDM / SDDM / greetd)

Install the binaries and session files:

```sh
sudo install -Dm755 bin/stratifi              /usr/local/bin/stratifi
sudo install -Dm755 bin/stratifi-launch       /usr/local/bin/stratifi-launch
sudo install -Dm755 contrib/stratifi-session  /usr/local/bin/stratifi-session
sudo install -Dm644 contrib/stratifi.desktop  /usr/share/wayland-sessions/stratifi.desktop
```

Log out and select **Stratifi** from the session menu (in GDM, click your user, then the gear icon in the bottom-right corner).

### Environment Variables

| Variable | Effect |
| :--- | :--- |
| `STRATIFI_KMS=1` | Enable the bare-metal DRM/KMS backend. |
| `STRATIFI_GPU=1` | Enable the Vulkan renderer (`dlopen`); unset uses the CPU blitter. |
| `STRATIFI_SESSION_SOCKET` | Control socket of `stratifi-session`; spawned clients become children of the supervisor. |
| `STRATIFI_VK_DEVICE` | Override Vulkan physical device selection. |
| `STRATIFI_VK_VALIDATION` | Enable Vulkan validation layers. |
| `STRATIFI_TRACE` | Enable verbose compositor event tracing. |

---

## Configuration

stratifi reads `$XDG_CONFIG_HOME/stratifi/config` (falling back to `~/.config/stratifi/config`). A missing file uses built-in defaults; unknown directives, duplicate scalar keys, and malformed lines fail loudly at startup. Lines ending in `\` continue onto the next line.

```conf
# Geometry & display
gap 8
border_width 3
titlebar_height 28
column_width 0.5          # fraction of output width per scrollable column
xkb_layout gb             # or sway-style: input * xkb_layout "gb"
output_scale 1.25         # or sway-style: output HDMI-A-1 scale 1.25
output_policy join        # "join" (default) or "mirror"
output DP-1 pos 0 0 scale 1.0
output HDMI-A-1 pos 1920 0 scale 1.25
xwayland on_demand        # "on_demand" (default), "enable", or "disable"

# Keybindings
bind Super+Return spawn foot
bind Super+d      spawn stratifi-launch
bind Super+q      close
bind Super+f      fullscreen
bind Super+Shift+space floating_toggle
bind Super+Shift+minus move_scratchpad
bind Super+minus       scratchpad_show
bind Super+r           mode resize

# Named modes (sway-style)
mode "resize" {
    bind Left  resize_col_width -10
    bind Right resize_col_width 10
    bind Up    resize_window_height -10
    bind Down  resize_window_height 10
    bind Escape mode default
}

# Touchpad gestures (KMS/evdev)
bind_gesture swipe left 3  workspace_next
bind_gesture swipe right 3 workspace_prev

# Startup commands (executed via sh -c)
exec dbus-update-activation-environment --systemd WAYLAND_DISPLAY
exec swayidle -w \
     timeout 300 'swaylock -f' \
     timeout 600 'stratifi-launch --dpms off' \
     resume 'stratifi-launch --dpms on'
```

### Supported Actions

| Action | Description |
| :--- | :--- |
| `spawn <cmd> [args...]` | Launch a client (via `stratifi-session` when active). |
| `close` / `quit` | Gracefully close the focused window / exit stratifi. |
| `focus_next` / `focus_prev` | Cycle focus across windows. |
| `focus_col_next` / `focus_col_prev` | Scroll focus to the next/previous column (`column_next` / `column_prev`). |
| `move_col_left` / `move_col_right` | Reorder the active column in the horizontal strip. |
| `focus_window_up` / `focus_window_down` | Move focus within the active column stack. |
| `move_window_up` / `move_window_down` | Reorder the active window within its column. |
| `fullscreen` | Toggle fullscreen on the active window. |
| `floating_toggle` / `focus_mode_toggle` | Toggle floating mode / switch focus between tiled and floating layers. |
| `move_scratchpad` / `scratchpad_show` | Send window to scratchpad / toggle scratchpad visibility. |
| `mode <name>` | Switch keybinding mode (`default` returns to base bindings). |
| `resize_col_width <delta>` | Resize the active column width (e.g. `-10`, `+10`, or fractional). |
| `resize_window_height <delta>` | Adjust the active window's height weight in its column. |
| `workspace <n>` / `move_to_workspace <n>` | Switch to or move the active window to workspace *n*. |
| `workspace_next` / `workspace_prev` / `workspace_back_and_forth` | Navigate workspaces. |
| `reload` | Live-reload config (bindings, XKB layout, geometry). |
| `dpms [on\|off\|toggle]` | Control KMS display power (wakes automatically on input). |

### Launcher (`stratifi-launch`)

`stratifi-launch` is an `fzf`-driven combi launcher combining open windows, installed `.desktop` applications, and arbitrary commands over `$XDG_RUNTIME_DIR/stratifi-ipc.sock`.

```sh
stratifi-launch --list   # inspect entries without a running compositor
```

---

## Screen Sharing (Browser Video Calls)

stratifi implements `ext-image-copy-capture-v1`, driven by `xdg-desktop-portal-wlr` over PipeWire. Configure your portal once in `~/.config/xdg-desktop-portal/stratifi-portals.conf`:

```ini
[preferred]
org.freedesktop.impl.portal.ScreenCast=wlr;
org.freedesktop.impl.portal.Screenshot=wlr;
default=gtk;
```

To test screen sharing in a nested session without replacing your desktop:

```sh
scripts/try-screen-share.sh nested [browser]
```

---

## Architecture

```
cmd/
  stratifi/          Compositor actor loop, KMS/nested wiring, IPC & actions
  stratifi-session/  Crash-resilient session supervisor
  stratifi-launch/   fzf combi launcher (windows + desktop apps + run)
  wayland-gen/       XML-to-Go Wayland protocol code generator
internal/
  wire/              Zero-alloc Wayland wire reader/writer & SCM_RIGHTS FD passing
  server/            Wayland protocol server (core, xdg-shell, layer-shell, IME, capture, xwayland-shell)
  layout/            Scrollable column strip, floating layer, scratchpad, workspaces
  compositor/        FrameLoop, backend-agnostic DrawList, zero-alloc MultiOutput
  render/vulkan/     Runtime-loaded (dlopen) Vulkan compositor renderer
  backend/kms/       Direct Linux DRM/KMS ioctls, atomic commits, hardware cursor plane
  backend/nested/    Parent Wayland backend for nested development
  input/             Pure-Go evdev reader, lock-free ring buffer, gestures, XKB keymaps
  xwayland/          X11 display allocator, Xwayland spawner, zero-alloc XWM
  config/            Strict line-oriented config parser with sway compatibility
```

---

## Verification & Quality Gates

Every change is verified against formatting, `go vet`, full unit/integration tests, the Go race detector, and `0 allocs/op` hot-path benchmarks:

```sh
./scripts/check.sh          # full gate: gofmt, go vet, build, test, -race, 0-alloc
./scripts/check-allocs.sh   # hot-path 0-alloc benchmark assertion only
./scripts/lint.sh           # golangci-lint
```

---

## License

Licensed under the [Apache License, Version 2.0](LICENSE).

[niri]: https://github.com/YaLTeR/niri
