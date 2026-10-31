#!/bin/sh
set -eu

REPO="lompendans/ezNAS"
INSTALL_DIR="${EZNAS_INSTALL_DIR:-$HOME/.local/bin}"
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux) GOOS=linux ;;
  Darwin) echo "macOS is not supported by this installer yet." >&2; exit 1 ;;
  *) echo "Unsupported operating system: $OS" >&2; exit 1 ;;
esac
case "$ARCH" in
  x86_64|amd64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

if ! command -v curl >/dev/null 2>&1; then echo "curl is required." >&2; exit 1; fi
if ! command -v tar >/dev/null 2>&1; then echo "tar is required." >&2; exit 1; fi
if ! command -v sha256sum >/dev/null 2>&1; then echo "sha256sum is required to verify downloads." >&2; exit 1; fi

API="https://api.github.com/repos/$REPO/releases/latest"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
curl -fsSL "$API" -o "$TMP/release.json" || { echo "Could not find a published ezNAS release yet." >&2; exit 1; }
TAG="$(sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/release.json" | head -n 1)"
[ -n "$TAG" ] || { echo "No published release found for ezNAS." >&2; exit 1; }
ASSET="eznas-$GOOS-$GOARCH.tar.gz"
BASE="https://github.com/$REPO/releases/download/$TAG"
curl -fL "$BASE/$ASSET" -o "$TMP/$ASSET"
curl -fL "$BASE/checksums.txt" -o "$TMP/checksums.txt"
(cd "$TMP" && grep " $ASSET$" checksums.txt | sha256sum -c -)
mkdir -p "$TMP/unpack" "$INSTALL_DIR"
tar -xzf "$TMP/$ASSET" -C "$TMP/unpack"
install -m 0755 "$TMP/unpack/eznas" "$INSTALL_DIR/eznas"
echo "ezNAS $TAG installed to $INSTALL_DIR/eznas"
case ":$PATH:" in *":$INSTALL_DIR:"*) ;; *) echo "Add this to your shell profile if needed: export PATH=\"$INSTALL_DIR:\$PATH\"" ;; esac
echo "Run: eznas"
