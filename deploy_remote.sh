#!/usr/bin/env bash
set -e

cd ~/medXfer

ARCH=$(uname -m)
echo "[*] Remote architecture: $ARCH"

# Determine installation directory based on OS
if [ -n "$TERMUX_VERSION" ] || [ -d "/data/data/com.termux" ]; then
    INSTALL_DIR="/data/data/com.termux/files/usr/bin"
    echo "[*] Detected Android / Termux environment ($INSTALL_DIR)"
else
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR" "$HOME/bin"
    echo "[*] Detected Standard Linux environment ($INSTALL_DIR)"
fi

BUILD_SUCCESS=0

# 1. Attempt native build if Go compiler is installed
if command -v go &>/dev/null; then
    echo "[*] Go compiler found: $(go version)"
    echo "[*] Compiling medXfer natively..."
    go mod tidy 2>/dev/null || true
    if go build -buildvcs=false -ldflags="-s -w" -o "$INSTALL_DIR/xfer" ./cmd/xfer/main.go; then
        chmod +x "$INSTALL_DIR/xfer"
        BUILD_SUCCESS=1
        echo "[+] Native build successful: $INSTALL_DIR/xfer"
    fi
fi

# 2. Fallback to pre-compiled binary if Go is not present or build failed
if [ "$BUILD_SUCCESS" -ne 1 ]; then
    echo "[*] Installing pre-compiled binary..."
    if [ "$ARCH" = "x86_64" ] && [ -f "./bin/xfer-linux-amd64" ]; then
        cp ./bin/xfer-linux-amd64 "$INSTALL_DIR/xfer"
        chmod +x "$INSTALL_DIR/xfer"
        echo "[+] Installed x86_64 binary to $INSTALL_DIR/xfer"
    elif ([ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]) && [ -f "./bin/xfer-android-arm64" ]; then
        cp ./bin/xfer-android-arm64 "$INSTALL_DIR/xfer"
        chmod +x "$INSTALL_DIR/xfer"
        echo "[+] Installed aarch64 binary to $INSTALL_DIR/xfer"
    else
        echo "[-] No compatible pre-compiled binary found for $ARCH"
        exit 1
    fi
fi

# Also place in ~/bin as standard fallback
mkdir -p "$HOME/bin"
cp "$INSTALL_DIR/xfer" "$HOME/bin/xfer" 2>/dev/null || true

# Ensure PATH includes .local/bin in .bashrc if not present
if [ -f "$HOME/.bashrc" ]; then
    if ! grep -q '\.local/bin' "$HOME/.bashrc"; then
        echo 'export PATH="$HOME/.local/bin:$HOME/bin:$PATH"' >> "$HOME/.bashrc"
    fi
fi

echo "[+] Binary verification:"
"$INSTALL_DIR/xfer" || true
echo "[+] medXfer remote installation complete!"

