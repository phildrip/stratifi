#!/bin/sh
# stratifi quality gate. Run before committing; CI runs the same steps.
#   format -> vet -> build -> test -> race -> 0-alloc hot paths
set -eu
cd "$(dirname "$0")/.."

say() { printf '\n==> %s\n' "$*"; }

say "gofmt"
unformatted=$(gofmt -l ./cmd ./internal ./spike)
if [ -n "$unformatted" ]; then
	echo "unformatted files:" >&2
	echo "$unformatted" >&2
	exit 1
fi

say "go vet"
go vet ./...

say "go build"
go build ./...

say "go test"
go test ./...

say "go test -race"
go test -race ./...

say "0-alloc hot paths"
./scripts/check-allocs.sh

printf '\nAll gates passed.\n'
