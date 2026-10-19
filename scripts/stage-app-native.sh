#!/usr/bin/env bash
# Copies the core's Linux library, built by scripts/build-lib.sh, where the Flutter app takes it
# (WP-B.1 of the J6b plan): app/native/linux, installed by the runner's CMake into bundle/lib.
# Windows and Android: scripts/stage-app-native.ps1.
# Usage (from the repository root): bash scripts/build-lib.sh && bash scripts/stage-app-native.sh
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
src="$root/build/lib/linux-amd64/libmedxfer.so"
if [ ! -f "$src" ]; then
  echo "missing $src (scripts/build-lib.sh builds it)" >&2
  exit 1
fi
mkdir -p "$root/app/native/linux"
cp -f "$src" "$root/app/native/linux/libmedxfer.so"
ls -l "$root/app/native/linux/libmedxfer.so"
