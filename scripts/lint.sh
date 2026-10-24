#!/bin/sh
# golangci-lint step. Optional locally (the installed tool must be new enough
# for the repo's Go version); CI installs a current release and runs this.
set -eu
cd "$(dirname "$0")/.."

if ! command -v golangci-lint >/dev/null 2>&1; then
	echo "golangci-lint not installed; skipping (go vet already ran in check.sh)" >&2
	exit 0
fi
golangci-lint run ./...
