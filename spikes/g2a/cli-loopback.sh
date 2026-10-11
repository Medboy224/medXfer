#!/usr/bin/env bash
# G2a, G2A-06 reference: the same loopback transfer as the app, with the CLI (xfer send / xfer recv)
# on the same machine. Prints the engine time reported by `xfer recv` for each trial.
# Usage: spikes/g2a/cli-loopback.sh <xfer binary> [size_mb=1024] [trials=3] [existing source file]
# Works in Git Bash (Windows), Linux and Termux.
set -euo pipefail
export LC_NUMERIC=C
xfer=$1 size=${2:-1024} trials=${3:-3} existing=${4:-}
dir=$(mktemp -d "${TMPDIR:-/tmp}/g2a_cli.XXXXXX")
trap 'rm -rf "$dir"' EXIT
mkdir -p "$dir/src"
if [ -n "$existing" ]; then # same file as the app's run, already on disk
  ln -s "$(realpath "$existing")" "$dir/src/loop.bin"
else
  head -c $((size * 1048576)) /dev/urandom > "$dir/src/loop.bin"
fi
want=$(sha256sum "$dir/src/loop.bin" | cut -d' ' -f1)
for i in $(seq 1 "$trials"); do
  rm -rf "$dir/out"; mkdir -p "$dir/out"
  port=$((19990 + i))
  "$xfer" send --port "$port" "$dir/src/loop.bin" > "$dir/send.log" 2>&1 &
  spid=$!
  code=""
  for _ in $(seq 1 50); do # the sender prints its pairing code once it is ready
    code=$(grep -ao 'Pairing Code : [0-9-]*' "$dir/send.log" | awk '{print $4}' || true)
    [ -n "$code" ] && break
    sleep 0.2
  done
  t0=$(date +%s.%N)
  echo y | "$xfer" recv -ip "127.0.0.1:$port" -code "$code" -out "$dir/out" > "$dir/recv.log" 2>&1 || true
  t1=$(date +%s.%N)
  wait "$spid" 2>/dev/null || true
  engine=$(grep -ao 'Transfer completed in [^ ]*' "$dir/recv.log" | awk '{print $4}' | tail -1)
  got=$(sha256sum "$dir/out/loop.bin" 2>/dev/null | cut -d' ' -f1 || true)
  awk -v i="$i" -v e="${engine:-?}" -v t0="$t0" -v t1="$t1" -v ok="$([ "$got" = "$want" ] && echo true || echo false)" \
    'BEGIN { printf "cli trial %d: engine %s, recv process %.3f s, hash_ok %s\n", i, e, t1 - t0, ok }'
  [ "$got" = "$want" ] || { echo "--- recv.log"; tail -5 "$dir/recv.log"; }
done
