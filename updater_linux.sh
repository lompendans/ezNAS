#!/bin/sh
set -eu
TARGET=$1
STAGED=$2
CONFIG_DIR=$3
PARENT_PID=$4
BACKUP="$TARGET.eznas-backup"
PID_FILE="$CONFIG_DIR/eznas.pid"
mkdir -p "$CONFIG_DIR"
exec >> "$CONFIG_DIR/update.log" 2>&1
printf "\n[%s] Starting ezNAS updater (pid %s)\n" "$(date -Is 2>/dev/null || date)" "$"
# Wait until the updater process exits before replacing the executable.
i=0
while kill -0 "$PARENT_PID" 2>/dev/null && [ "$i" -lt 60 ]; do sleep 0.5; i=$((i+1)); done
if kill -0 "$PARENT_PID" 2>/dev/null; then echo "Updater process did not exit." >&2; exit 1; fi

# Stop the running server gracefully if its PID file identifies a live process.
if [ -f "$PID_FILE" ]; then
  SERVER_PID=$(cat "$PID_FILE" 2>/dev/null || true)
  case "$SERVER_PID" in ''|*[!0-9]*) SERVER_PID='' ;; esac
  if [ -n "$SERVER_PID" ] && [ "$SERVER_PID" != "$$" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -INT "$SERVER_PID" 2>/dev/null || true
    i=0
    while kill -0 "$SERVER_PID" 2>/dev/null && [ "$i" -lt 40 ]; do sleep 0.25; i=$((i+1)); done
    if kill -0 "$SERVER_PID" 2>/dev/null; then echo "ezNAS server did not stop gracefully; refusing to replace it." >&2; exit 1; fi
  fi
fi
cp -p "$TARGET" "$BACKUP.tmp"
mv -f "$BACKUP.tmp" "$BACKUP"
TMP="$TARGET.eznas-new"
cp "$STAGED" "$TMP"
chmod 0755 "$TMP"
mv -f "$TMP" "$TARGET"
# If a systemd user unit exists, let systemd manage the restarted process.
if command -v systemctl >/dev/null 2>&1 && systemctl --user cat eznas.service >/dev/null 2>&1; then
  if systemctl --user restart eznas.service; then
    echo "Update applied; previous executable retained at: $BACKUP"
    exit 0
  fi
  mv -f "$BACKUP" "$TARGET"
  systemctl --user restart eznas.service || true
  echo "Update failed; previous executable restored." >&2
  exit 1
fi
"$TARGET" start >/dev/null 2>&1 &
NEW_PID=$!
sleep 2
if ! kill -0 "$NEW_PID" 2>/dev/null; then
  mv -f "$BACKUP" "$TARGET"
  "$TARGET" start >/dev/null 2>&1 &
  echo "Update failed; previous executable restored." >&2
  exit 1
fi
echo "Update applied; previous executable retained at: $BACKUP"
