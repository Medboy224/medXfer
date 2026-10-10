#!/usr/bin/env bash
# Checks that the production binary carries no diagnostic or development code
# (DEV-10, DEV-12, DEV-19): faultconn, faultfs, the engine fault hooks, testkit and the
# testing package, pprof, and the web dashboard (devui).
# Usage: scripts/check-prod-binary.sh [extra go build flags]
# Self-tests: "-tags diag" and "-tags devui" must both fail.
set -euo pipefail

forbidden='pkg/diag/faultconn|pkg/diag/faultfs|pkg/testkit|net/http/pprof|^testing$'
forbidden_symbols="pkg/diag/faultconn|pkg/diag/faultfs|pkg/testkit|net/http/pprof|engine\.SetConnWrapper|engine\.SetStorageWrapper|engine\.buffersPeak|api\.\(\*DaemonServer\)\.(handleBrowse|handleUpload|handleFSList|handleFSMkdir)"
# The dashboard page is a string constant: it leaves no symbol, so look for its text.
forbidden_text='Live Transfer Telemetry'

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
if grep -qa "$forbidden_text" "$tmp/xfer.bin"; then
  echo "Le tableau de bord web (devui) est dans le binaire de production."; status=1
fi

if [ "$status" -eq 0 ]; then echo "Binaire de production propre."; fi
exit "$status"
