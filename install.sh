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

# Persist PATH for future login shells without adding duplicate lines.
PROFILE="$HOME/.profile"
PATH_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
touch "$PROFILE"
if ! grep -Fq "# ezNAS PATH" "$PROFILE"; then
  {
    printf '\n# ezNAS PATH\n'
    printf '%s\n' "$PATH_LINE"
  } >> "$PROFILE"
fi
case ":$PATH:" in *":$INSTALL_DIR:"*) ;; *) export PATH="$INSTALL_DIR:$PATH" ;; esac

echo "ezNAS $TAG installed to $INSTALL_DIR/eznas"
echo "PATH configured in $PROFILE. Open a new login shell (or run: . ~/.profile)."
echo "Run: eznas help"

if command -v systemctl >/dev/null 2>&1 && [ -d "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}" ]; then
  printf '\n'
  printf 'Enable ezNAS to start automatically when your user session starts? [y/N] '
  IFS= read -r ANSWER || ANSWER=n
  case "$ANSWER" in
    y|Y|yes|YES)
      UNIT_DIR="$HOME/.config/systemd/user"
      mkdir -p "$UNIT_DIR"
      cat > "$UNIT_DIR/eznas.service" <<EOF
[Unit]
Description=ezNAS personal file server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$HOME
ExecStart=$INSTALL_DIR/eznas start
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF
      systemctl --user daemon-reload
      systemctl --user enable --now eznas.service
      echo "Automatic startup enabled. Check status with: systemctl --user status eznas"
      ;;
    *) echo "Automatic startup skipped. Enable later with a user systemd service if desired." ;;
  esac
fi
