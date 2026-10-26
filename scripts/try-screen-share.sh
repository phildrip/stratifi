#!/bin/sh
# try-screen-share.sh — try browser window/screen sharing against stratifi
# WITHOUT replacing your session.
#
#   scripts/try-screen-share.sh nested [browser]   # nested (default, no switch)
#   scripts/try-screen-share.sh kms    [browser]   # bare metal — run from a spare VT
#
# It starts stratifi if it isn't already up, then launches a PRIVATE D-Bus
# session containing xdg-desktop-portal, xdg-desktop-portal-wlr and the
# browser, with WAYLAND_DISPLAY/XDG_CURRENT_DESKTOP pointed at stratifi. That
# private bus is the trick: it makes the portal capture stratifi rather than
# your outer session (a portal in your normal session would attach to sway).
#
# Requirements: xdg-desktop-portal, xdg-desktop-portal-wlr, dbus-run-session,
# a running pipewire, and the portal config
# ~/.config/xdg-desktop-portal/stratifi-portals.conf (see README).
set -eu
cd "$(dirname "$0")/.."

MODE="${1:-nested}"
BROWSER="${2:-${BROWSER:-firefox}}"
SOCKET="${STRATIFI_SOCKET:-wayland-stratifi-0}"
RUNTIME="${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR must be set}"
TMP="${STRATIFI_SHARE_TMP:-${TMPDIR:-/tmp}}"
mkdir -p "$TMP"
LOG="$TMP/stratifi-sharescreen.log"
PORTAL_LOG="$TMP/stratifi-portal.log"

say() { printf '\n==> %s\n' "$*"; }
warn() { printf '[!] %s\n' "$*" >&2; }
die() { printf '[-] %s\n' "$*" >&2; exit 1; }

case "$MODE" in
	nested | kms) ;;
	*) die "usage: $0 [nested|kms] [browser]" ;;
esac

# --- prerequisites -----------------------------------------------------------
command -v dbus-run-session >/dev/null 2>&1 || die "missing dbus-run-session (dbus)"
command -v "$BROWSER" >/dev/null 2>&1 || die "browser '$BROWSER' not found (pass one: $0 $MODE firefox)"
PORTAL=""
for p in /usr/libexec/xdg-desktop-portal /usr/lib/xdg-desktop-portal; do
	[ -x "$p" ] && PORTAL="$p" && break
done
[ -n "$PORTAL" ] || die "xdg-desktop-portal not found"
for p in /usr/libexec/xdg-desktop-portal-wlr /usr/lib/xdg-desktop-portal-wlr; do
	[ -x "$p" ] && XDPW="$p" && break
done
[ -n "${XDPW:-}" ] || die "xdg-desktop-portal-wlr not installed (needed for Wayland screen capture)"
[ -S "$RUNTIME/pipewire-0" ] || warn "no pipewire socket at $RUNTIME/pipewire-0 — the stream will fail; start pipewire"

# --- build + start stratifi --------------------------------------------------
say "building stratifi"
mkdir -p bin
go build -o bin/stratifi ./cmd/stratifi

if [ -S "$RUNTIME/$SOCKET" ] && pgrep -x stratifi >/dev/null 2>&1; then
	say "stratifi already running on $SOCKET"
else
	case "$MODE" in
		kms)
			[ -t 0 ] || die "kms mode must run from the target VT (Ctrl+Alt+F3, log in)"
			say "starting stratifi (KMS, bare metal)"
			setsid env STRATIFI_KMS=1 STRATIFI_GPU=1 bin/stratifi </dev/null >"$LOG" 2>&1 &
			;;
		nested)
			say "starting stratifi (nested)"
			setsid env STRATIFI_GPU=1 bin/stratifi </dev/null >"$LOG" 2>&1 &
			;;
	esac
	i=0
	while [ ! -S "$RUNTIME/$SOCKET" ]; do
		i=$((i + 1))
		[ "$i" -gt 100 ] && die "stratifi did not create $SOCKET (see $LOG)"
		sleep 0.1
	done
	say "stratifi up on $SOCKET (log: $LOG)"
fi

cat <<EOF

    Open a client inside stratifi to have something to share, e.g.:

        WAYLAND_DISPLAY=$SOCKET foot &
        WAYLAND_DISPLAY=$SOCKET waybar &      # optional

    Then launch the browser below, start a call, click Share, and pick
    "Entire screen" or a specific window.

EOF

say "starting a private D-Bus session: portal + $BROWSER -> stratifi"
printf '    WAYLAND_DISPLAY=%s  XDG_CURRENT_DESKTOP=stratifi\n' "$SOCKET"
printf '    portal log: %s\n' "$PORTAL_LOG"
printf '    stop stratifi with: pkill -x stratifi\n\n'

# The private bus isolates portal configuration from the outer session while
# sharing the existing pipewire socket (xdpw only needs the socket).
export WAYLAND_DISPLAY="$SOCKET"
export XDG_CURRENT_DESKTOP=stratifi
export XDG_SESSION_TYPE=wayland
export XDG_SESSION_DESKTOP=stratifi
export STRATIFI_PORTAL_LOG="$PORTAL_LOG"
export STRATIFI_PORTAL_BIN="$PORTAL"
export STRATIFI_BROWSER="$BROWSER"
exec dbus-run-session -- sh -c '
	set -u
	"$STRATIFI_PORTAL_BIN" >"$STRATIFI_PORTAL_LOG" 2>&1 &
	portal=$!
	sleep 1
	if ! kill -0 "$portal" 2>/dev/null; then
		echo "[-] xdg-desktop-portal exited; see $STRATIFI_PORTAL_LOG" >&2
	else
		echo "[+] xdg-desktop-portal running (private bus)"
	fi
	"$STRATIFI_BROWSER" || true
	kill "$portal" 2>/dev/null || true
'
