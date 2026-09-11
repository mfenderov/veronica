#!/bin/sh
set -e

REPO="mfenderov/veronica"
BINARY="veronica"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Darwin) OS_NAME="darwin" ;;
  Linux)  OS_NAME="linux" ;;
  *)
    echo "Error: Unsupported operating system: $OS" >&2
    exit 1
    ;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH_NAME="amd64" ;;
  arm64|aarch64) ARCH_NAME="arm64" ;;
  *)
    echo "Error: Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# Determine destination directory
if [ -d "$HOME/.local/bin" ] && echo "$PATH" | grep -q "$HOME/.local/bin"; then
  DEST_DIR="$HOME/.local/bin"
elif [ -d "$HOME/bin" ] && echo "$PATH" | grep -q "$HOME/bin"; then
  DEST_DIR="$HOME/bin"
else
  DEST_DIR="$HOME/.local/bin"
  mkdir -p "$DEST_DIR"
fi

echo "🛰️  Installing Veronica for $OS_NAME/$ARCH_NAME to $DEST_DIR..."

# Get latest release tag
LATEST_TAG=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$LATEST_TAG" ]; then
  LATEST_TAG="v0.1.0"
fi

VERSION="${LATEST_TAG#v}"
TARBALL_NAME="veronica_${VERSION}_${OS_NAME}_${ARCH_NAME}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$TARBALL_NAME"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading $DOWNLOAD_URL..."
curl -sSL -o "$TMP_DIR/$TARBALL_NAME" "$DOWNLOAD_URL"

tar -xzf "$TMP_DIR/$TARBALL_NAME" -C "$TMP_DIR"
mv -f "$TMP_DIR/$BINARY" "$DEST_DIR/$BINARY"
chmod +x "$DEST_DIR/$BINARY"

echo "✅ Veronica installed successfully to $DEST_DIR/$BINARY!"
echo "Run '$BINARY version' to verify, or '$BINARY tui' to launch the interactive dashboard."
