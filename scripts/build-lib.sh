#!/usr/bin/env bash
# Builds medXfer as a C library for this host: Linux, or Android under Termux (WP-6.4 of the
# J6a plan; bible §10.4). Windows and cross-compiled Android: scripts/build-lib.ps1.
# Usage, from the repository root:
#   scripts/build-lib.sh            # build/lib/<os>-<arch>/libmedxfer.so and its header
#   scripts/build-lib.sh --smoke    # also compile and run cmd/libmedxfer/testdata/smoke.c
# Prerequisites: Go and a C compiler (gcc or clang) on the PATH.
set -euo pipefail
export LC_NUMERIC=C # awk needs "." as decimal separator
root=$(cd "$(dirname "$0")/.." && pwd)
os=$(go env GOOS)
arch=$(go env GOARCH)
out="$root/build/lib/$os-$arch"
mkdir -p "$out"
start=$(date +%s.%N)
# -s -w: no symbol table (size, G2A-11); -trimpath: no local paths in the library.
CGO_ENABLED=1 go build -trimpath -tags libmedxfer -buildmode=c-shared -ldflags="-s -w" \
  -o "$out/libmedxfer.so" "$root/cmd/libmedxfer"
size=$(stat -c %s "$out/libmedxfer.so")
awk -v arch="$os/$arch" -v size="$size" -v t0="$start" -v t1="$(date +%s.%N)" -v file="$out/libmedxfer.so" \
  'BEGIN { printf "%-16s %8.1f Mo  %5.1f s  %s\n", arch, size / 1048576, t1 - t0, file }'
# G2A-11: warn past 20 MB.
if [ "$size" -gt $((20 * 1024 * 1024)) ]; then echo "WARNING: library over 20 MB (G2A-11)"; fi

if [ "${1:-}" = "--smoke" ]; then
  cc=${CC:-cc}
  libs="-lpthread"
  if [ "$os" = "android" ]; then libs=""; fi # Bionic: threads are in libc
  # shellcheck disable=SC2086
  "$cc" -O1 -o "$out/smoke" "$root/cmd/libmedxfer/testdata/smoke.c" -I"$out" -L"$out" -lmedxfer $libs
  data=$(mktemp -d)
  trap 'rm -rf "$data"' EXIT
  LD_LIBRARY_PATH="$out" "$out/smoke" "$data"
fi
