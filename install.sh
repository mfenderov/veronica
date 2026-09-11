#!/bin/sh
set -e

REPO="mfenderov/veronica"
BINARY="veronica"

# Verify required dependencies
command -v curl >/dev/null 2>&1 || {
  echo "Error: 'curl' is required to install Veronica but is not installed." >&2
  exit 1
}

command -v tar >/dev/null 2>&1 || {
  echo "Error: 'tar' is required to extract Veronica but is not installed." >&2
  exit 1
}

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Darwin) OS_NAME="darwin" ;;
  Linux)  OS_NAME="linux" ;;
  *)
    echo "Error: Unsupported operating system: $OS" >&2
    echo "Veronica currently supports Darwin (macOS) and Linux." >&2
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
    echo "Veronica currently supports amd64 (x86_64) and arm64 (aarch64)." >&2
    exit 1
    ;;
esac

# Determine destination directory (allow BINDIR or DEST_DIR override)
DEST_DIR="${BINDIR:-${DEST_DIR:-}}"
if [ -z "$DEST_DIR" ]; then
  if [ -d "$HOME/.local/bin" ] && case ":$PATH:" in *":$HOME/.local/bin:"*) true;; *) false;; esac; then
    DEST_DIR="$HOME/.local/bin"
  elif [ -d "$HOME/bin" ] && case ":$PATH:" in *":$HOME/bin:"*) true;; *) false;; esac; then
    DEST_DIR="$HOME/bin"
  else
    DEST_DIR="$HOME/.local/bin"
  fi
fi

mkdir -p "$DEST_DIR"

if [ ! -w "$DEST_DIR" ]; then
  echo "Error: Destination directory $DEST_DIR is not writable." >&2
  echo "Try running with a writable BINDIR (e.g., BINDIR=\$HOME/.local/bin) or sudo." >&2
  exit 1
fi

# Resolve version tag: check VERSION or VERONICA_VERSION override first
TARGET_VERSION="${VERONICA_VERSION:-${VERSION:-}}"
if [ -n "$TARGET_VERSION" ]; then
  case "$TARGET_VERSION" in
    v*) LATEST_TAG="$TARGET_VERSION" ;;
    *)  LATEST_TAG="v$TARGET_VERSION" ;;
  esac
else
  # Resolve latest release tag without API rate limits via HTTP redirect
  LATEST_URL=$(curl -fsSL -o /dev/null -w "%{url_effective}" "https://github.com/$REPO/releases/latest" 2>/dev/null || true)
  LATEST_TAG="${LATEST_URL##*/}"

  # Fall back to GitHub REST API if redirect did not resolve to a tag
  if [ -z "$LATEST_TAG" ] || [ "$LATEST_TAG" = "latest" ] || [ "$LATEST_TAG" = "releases" ]; then
    LATEST_TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/' || true)
  fi

  # Final fallback if network or API is unavailable
  if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="v0.1.0"
  fi
fi

VERSION_NUM="${LATEST_TAG#v}"
TARBALL_NAME="veronica_${VERSION_NUM}_${OS_NAME}_${ARCH_NAME}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$TARBALL_NAME"

echo "🛰️  Installing Veronica $LATEST_TAG ($OS_NAME/$ARCH_NAME) to $DEST_DIR..."

TMP_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t "veronica.XXXXXX")
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "Downloading $DOWNLOAD_URL..."
if ! curl -fsSL -o "$TMP_DIR/$TARBALL_NAME" "$DOWNLOAD_URL"; then
  echo "Error: Failed to download $DOWNLOAD_URL" >&2
  echo "Please verify release $LATEST_TAG exists and provides an archive for $OS_NAME/$ARCH_NAME." >&2
  exit 1
fi

if ! tar -xzf "$TMP_DIR/$TARBALL_NAME" -C "$TMP_DIR"; then
  echo "Error: Failed to extract $TARBALL_NAME." >&2
  exit 1
fi

if [ ! -f "$TMP_DIR/$BINARY" ]; then
  echo "Error: Expected binary '$BINARY' not found in downloaded archive." >&2
  exit 1
fi

mv -f "$TMP_DIR/$BINARY" "$DEST_DIR/$BINARY"
chmod +x "$DEST_DIR/$BINARY"

echo "✅ Veronica installed successfully to $DEST_DIR/$BINARY!"

# Check if DEST_DIR is in PATH
case ":$PATH:" in
  *":$DEST_DIR:"*) ;;
  *)
    echo ""
    echo "⚠️  Note: $DEST_DIR is not in your PATH."
    echo "   Add it to your shell configuration (e.g. ~/.bashrc or ~/.zshrc):"
    echo "     export PATH=\"$DEST_DIR:\$PATH\""
    ;;
esac

echo ""
echo "Run '$BINARY version' to verify, or '$BINARY tui' to launch the interactive dashboard."
