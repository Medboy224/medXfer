#!/usr/bin/env bash
# G2a prototype: builds the Go core as a C library for Linux (plan §4.4, G2A-01).
# Usage, from the repository root:  spikes/g2a/build.sh
# Prerequisites: Go 1.24+ and a C compiler (gcc or clang) on the PATH.
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd)
out="$root/app/native/linux/libmedxfer.so"
mkdir -p "$(dirname "$out")"
start=$(date +%s.%N)
# -s -w: no symbol table (size, G2A-11); -trimpath: no local paths in the binary.
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o "$out" "$root/core"
rm -f "${out%.so}.h"
printf '%-16s %8.1f Mo  %5.1f s  %s\n' "linux/$(go env GOARCH)" \
  "$(echo "$(stat -c %s "$out") / 1048576" | bc -l)" "$(echo "$(date +%s.%N) - $start" | bc -l)" "$out"
