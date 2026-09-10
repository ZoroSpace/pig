#!/usr/bin/env bash
# Build the pig binary → <repo root>/pig (i.e. ./pig when run from the root).
# Usage: scripts/build.sh [extra go build flags...]
#   scripts/build.sh              # regular build
#   scripts/build.sh -trimpath    # pass extra flags through to go build
#   scripts/build.sh -race        # race-instrumented build (larger, slower)
set -euo pipefail

# Resolve repo root from the script's own location (works from any cwd).
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$root"

# -ldflags "-s -w" strips debug info for a smaller release binary; omit it
# (default) so panics print readable traces during development.
go build -o "$root/pig" "$@" ./cmd/pig

echo "built $root/pig ($(du -h "$root/pig" | cut -f1))"
