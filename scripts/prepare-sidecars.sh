#!/usr/bin/env bash
# Copy the Go binaries next to the Tauri project with the target-triple suffix
# Tauri expects for externalBin, then (optionally) build the desktop bundle.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TRIPLE="${TAURI_TARGET_TRIPLE:-$(rustc -vV | awk '/host:/ {print $2}')}"
BIN_DIR="desktop/src-tauri/binaries"
mkdir -p "$BIN_DIR"

EXT=""
case "$TRIPLE" in
  *windows*) EXT=".exe" ;;
esac

echo "==> Target triple: $TRIPLE"
bash scripts/build.sh

cp "bin/yuki-server${EXT}" "$BIN_DIR/yuki-server-${TRIPLE}${EXT}"
cp "bin/yuki${EXT}"        "$BIN_DIR/yuki-${TRIPLE}${EXT}"
chmod +x "$BIN_DIR"/*

echo "==> Sidecars ready:"
ls -lh "$BIN_DIR"
