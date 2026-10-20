#!/usr/bin/env bash
set -e

echo "[*] Building medXfer..."

if ! command -v go &> /dev/null; then
    echo "[-] Error: Go compiler not found. Please install Go first."
    exit 1
fi

mkdir -p bin

export CGO_ENABLED=0
# One build: the web dashboard and its "devui" tag were removed at gate G7 (DEV-19-R).
TAGS=""
go build $TAGS -ldflags="-s -w" -o bin/xfer ./cmd/xfer

echo "[+] Build successful: ./bin/xfer"

if [ -n "$TERMUX_VERSION" ]; then
    cp bin/xfer "$PREFIX/bin/xfer"
    chmod +x "$PREFIX/bin/xfer"
    echo "[+] Termux detected: Installed to $PREFIX/bin/xfer"
elif [ -d "$HOME/.local/bin" ]; then
    cp bin/xfer "$HOME/.local/bin/xfer"
    chmod +x "$HOME/.local/bin/xfer"
    echo "[+] Installed to $HOME/.local/bin/xfer"
fi
