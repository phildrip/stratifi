#!/usr/bin/env bash
# Dev helper: build bloxwm and run it nested inside the current Wayland
# session, with a terminal client wired to its socket.
#
#   scripts/nested-dev.sh              build, (re)start, open a client, tail log
#   scripts/nested-dev.sh --shm        use the shm blitter instead of Vulkan
#   scripts/nested-dev.sh --no-tail    don't follow the log
#   scripts/nested-dev.sh down         stop bloxwm and its clients
#   scripts/nested-dev.sh status       report what is running
#   scripts/nested-dev.sh foot         open another client
#   scripts/nested-dev.sh logs         follow the log
#   scripts/nested-dev.sh smoke        automated bring-up check, then tear down
#
# Overridable: BLOXWM_BIN  BLOXWM_SOCKET  BLOXWM_LOG  BLOXWM_CLIENT
#
# fish users cannot source a bash file; call it directly instead:
#   function blox; /path/to/bloxwm/scripts/nested-dev.sh $argv; end
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BLOXWM_BIN:-$ROOT/bin/bloxwm}"
SOCKET="${BLOXWM_SOCKET:-wayland-blox-0}"
LOG="${BLOXWM_LOG:-${TMPDIR:-/tmp}/bloxwm-nested.log}"
CLIENT="${BLOXWM_CLIENT:-foot}"
SOCKPATH="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/$SOCKET"

GPU=1
DO_FOOT=1
DO_TAIL=-1 # -1 = auto (only when interactive)

say() { printf '[dev] %s\n' "$*"; }
die() { printf '[dev] error: %s\n' "$*" >&2; exit 1; }

build() {
	say "building $BIN"
	( cd "$ROOT" && go build -o "$BIN" ./cmd/bloxwm )
}

stop() {
	pkill -x bloxwm 2>/dev/null || true
	pkill -x "$CLIENT" 2>/dev/null || true
	sleep 0.2
	rm -f "$SOCKPATH"
}

wait_ready() {
	local i
	for i in $(seq 1 200); do
		if [ -S "$SOCKPATH" ] && grep -q "active on socket" "$LOG" 2>/dev/null; then
			return 0
		fi
		if ! pgrep -x bloxwm >/dev/null; then
			die "bloxwm exited during startup; see $LOG"
		fi
		sleep 0.05
	done
	die "bloxwm did not become ready; see $LOG"
}

start() {
	[ -n "${WAYLAND_DISPLAY:-}" ] || die "no WAYLAND_DISPLAY: run this inside a Wayland session (sway)"
	rm -f "$SOCKPATH"
	: >"$LOG"
	local env_args=()
	[ "$GPU" = 1 ] && env_args+=(BLOXWM_GPU=1)
	# setsid: the compositor outlives this script and its process group,
	# so Ctrl-C on the log tail does not tear it down.
	setsid env "${env_args[@]}" "$BIN" </dev/null >"$LOG" 2>&1 &
	disown || true
	wait_ready
	local mode="shm"; [ "$GPU" = 1 ] && mode="Vulkan"
	say "up: display=$SOCKET mode=$mode log=$LOG"
}

client() {
	[ -S "$SOCKPATH" ] || die "bloxwm is not running (start it first)"
	WAYLAND_DISPLAY="$SOCKET" setsid "$CLIENT" </dev/null >/dev/null 2>&1 &
	disown || true
	say "launched $CLIENT on $SOCKET"
}

logs() { tail -n +1 -f "$LOG"; }

resolve_tail() { [ "$DO_TAIL" = -1 ] && { [ -t 1 ] && DO_TAIL=1 || DO_TAIL=0; }; }

status() {
	if pgrep -x bloxwm >/dev/null; then
		say "bloxwm running (pid $(pgrep -x bloxwm | head -1)), display=$SOCKET"
	else
		say "bloxwm not running"
	fi
	local n
	n=$(pgrep -x "$CLIENT" 2>/dev/null | wc -l)
	say "$n $CLIENT process(es)"
}

smoke() {
	# End-to-end: boot, accept a client, observe its window, tear down.
	[ -n "${WAYLAND_DISPLAY:-}" ] || die "no WAYLAND_DISPLAY: smoke needs a Wayland session"
	build
	stop
	start
	client
	local i
	for i in $(seq 1 200); do
		if grep -q "New window registered" "$LOG"; then
			stop
			say "smoke OK: client window registered"
			return 0
		fi
		sleep 0.05
	done
	printf '[dev] smoke FAILED; log tail:\n' >&2
	tail -n 20 "$LOG" >&2 || true
	stop
	exit 1
}

cmd="up"
while [ $# -gt 0 ]; do
	case "$1" in
	--gpu) GPU=1 ;;
	--shm) GPU=0 ;;
	--no-tail) DO_TAIL=0 ;;
	--tail) DO_TAIL=1 ;;
	--no-foot) DO_FOOT=0 ;;
	up | down | status | foot | logs | smoke) cmd="$1" ;;
	-h | --help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1 (try --help)" ;;
	esac
	shift
done

case "$cmd" in
up)
	build
	stop
	start
	[ "$DO_FOOT" = 1 ] && client
	resolve_tail
	if [ "$DO_TAIL" = 1 ]; then
		say "following $LOG (Ctrl-C stops following, not bloxwm)"
		logs
	fi
	;;
down)
	stop
	say "stopped"
	;;
status) status ;;
foot) client ;;
logs) logs ;;
smoke) smoke ;;
esac
