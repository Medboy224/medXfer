#!/usr/bin/env bash
# Checks that the production binary carries no diagnostic or development code
# (DEV-10, DEV-12, DEV-19): faultconn, faultfs, the engine fault hooks, pprof, devui.
# Usage: scripts/check-prod-binary.sh [extra go build flags]
# Self-test: scripts/check-prod-binary.sh -tags diag   must fail.
set -euo pipefail

forbidden='pkg/diag/faultconn|pkg/diag/faultfs|net/http/pprof|devui'
forbidden_symbols="$forbidden|engine\.SetConnWrapper|engine\.SetStorageWrapper|engine\.buffersPeak"

status=0
deps=$(go list -deps "$@" ./cmd/xfer)
if bad=$(grep -E "$forbidden" <<<"$deps"); then
  echo "Paquets interdits dans les dépendances de cmd/xfer :"; echo "$bad"; status=1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build "$@" -o "$tmp/xfer.bin" ./cmd/xfer
if bad=$(go tool nm "$tmp/xfer.bin" | grep -E "$forbidden_symbols" | head -20); then
  echo "Symboles interdits dans le binaire de production :"; echo "$bad"; status=1
fi

if [ "$status" -eq 0 ]; then echo "Binaire de production propre."; fi
exit "$status"
