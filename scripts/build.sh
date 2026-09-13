#!/usr/bin/env bash
# Build the pig binary → <repo root>/pig (i.e. ./pig when run from the root).
# Usage: scripts/build.sh [extra go build flags...]
#   scripts/build.sh              # fully static release build (default)
#   scripts/build.sh -race        # race-instrumented dev build
set -euo pipefail

# Resolve repo root from the script's own location (works from any cwd).
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$root"

# Default to the fully static release binary: CGO disabled (no libc
# dependency, runs anywhere), reproducible paths, debug info stripped
# (~28 MB; panic tracebacks stay readable either way).
# -race requires cgo and cannot combine with CGO_ENABLED=0, so when the
# caller passes -race fall back to a plain instrumented build.
static_flags=(-trimpath -ldflags "-s -w")
if [[ " $* " == *" -race "* ]]; then
	static_flags=()
else
	export CGO_ENABLED=0
fi

go build -o "$root/pig" "${static_flags[@]}" "$@" ./cmd/pig

echo "built $root/pig ($(du -h "$root/pig" | cut -f1))"
