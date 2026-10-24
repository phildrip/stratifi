#!/bin/sh
# Fail if any hot-path benchmark allocates. The DOD mandate is 0 allocs/op on
# the hot paths (layout, wire codec, input pump, gesture recogniser, PipeWire
# framing, the assembled frame loop); this turns the convention into a gate.
set -eu
cd "$(dirname "$0")/.."

BENCH='ZeroAlloc|WireEncoding|WireDecoding|RingBuffer|ProcessEvent|ComputeScrollable|FramingFrame|TranslateAndPush|MultiTouchSync|FrameLoop'
PKGS='./internal/layout ./internal/wire ./internal/input ./internal/input/evdev ./internal/input/gesture ./internal/pipewire ./internal/compositor'

out=$(go test -run='^$' -bench="$BENCH" -benchmem $PKGS)
echo "$out" | grep -E '^Benchmark' || true

# In -benchmem output the columns are: name iters ns/op B/op allocs/op.
if echo "$out" | awk '
	/^Benchmark/ {
		for (i = 1; i <= NF; i++) {
			if ($i == "allocs/op" && $(i - 1) + 0 != 0) {
				print "  " $1 ": " $(i - 1) " allocs/op"
				bad = 1
			}
		}
	}
	END { exit bad }
'; then
	echo "OK: hot-path benchmarks are 0 allocs/op"
else
	echo "FAIL: hot-path benchmarks must be 0 allocs/op (DOD)" >&2
	exit 1
fi
