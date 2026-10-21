#!/usr/bin/env bash
# Build a Linux x86-64 AppImage for YukiTunnel.
#
# Run this inside a Debian/Ubuntu/Kali container or host with:
#   build-essential pkg-config libssl-dev libwebkit2gtk-4.1-dev libgtk-3-dev
#   libayatana-appindicator3-dev librsvg2-dev patchelf mksquashfs curl
#   rustup (stable), Go 1.24+, Node 20+/pnpm
#
#   bash scripts/build-appimage.sh
#
# Output: dist/YukiTunnel-x86_64.AppImage
#
# Why not appimagetool: it is itself an AppImage whose runtime carries the
# AppImage-specific OSABI marker (0x41 'A' at e_ident[8]); QEMU user emulation
# refuses to execute it. Assembling runtime + squashfs by hand produces a
# byte-identical type-2 AppImage without needing to run any AppImage.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ARCH="${ARCH:-x86_64}"
VERSION="${VERSION:-0.1.0}"
OUT_DIR="${OUT_DIR:-dist}"
APPDIR="${APPDIR:-$(mktemp -d)/AppDir}"
WORK="${WORK:-$(dirname "$APPDIR")}"

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

log "Building WebUI (if pnpm is available)"
if command -v pnpm >/dev/null 2>&1 && [ ! -f web/dist/index.html ]; then
  (cd web && pnpm install --frozen-lockfile && pnpm build)
fi

log "Building Go binaries (linux/$ARCH)"
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -buildvcs=false -trimpath \
  -ldflags "-s -w -X github.com/yuuuuukiu/yukitunnel/internal/version.Version=${VERSION}" \
  -o bin/yuki-server ./cmd/yuki-server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -buildvcs=false -trimpath \
  -ldflags "-s -w -X github.com/yuuuuukiu/yukitunnel/internal/version.Version=${VERSION}" \
  -o bin/yuki ./cmd/yuki

log "Building desktop frontend"
if [ ! -f desktop/dist/index.html ]; then
  (cd desktop && pnpm install --frozen-lockfile && pnpm build)
fi

TRIPLE="x86_64-unknown-linux-gnu"
mkdir -p desktop/src-tauri/binaries
cp bin/yuki-server "desktop/src-tauri/binaries/yuki-server-${TRIPLE}"
cp bin/yuki        "desktop/src-tauri/binaries/yuki-${TRIPLE}"
chmod +x desktop/src-tauri/binaries/*

log "Compiling Tauri binary (this takes a while)"
# `--features custom-protocol` is what makes Tauri embed the frontend instead of
# pointing at the dev server (devUrl). Without it the AppImage shows
# "Could not connect to localhost".
(cd desktop/src-tauri && cargo build --release --locked --features custom-protocol)

log "Assembling AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" "$APPDIR/usr/lib/yukitunnel" \
         "$APPDIR/usr/share/applications" "$APPDIR/usr/share/icons/hicolor/512x512/apps"

install -m755 desktop/src-tauri/target/release/yukitunnel-desktop "$APPDIR/usr/bin/yukitunnel-desktop"
install -m755 bin/yuki-server "$APPDIR/usr/lib/yukitunnel/yuki-server"
install -m755 bin/yuki        "$APPDIR/usr/lib/yukitunnel/yuki"

cat > "$APPDIR/usr/share/applications/yukitunnel.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=YukiTunnel
GenericName=Intranet Tunnel Client
Comment=Graphical client and server console for YukiTunnel
Exec=yukitunnel-desktop %U
Icon=yukitunnel
Terminal=false
Categories=Network;Development;
Keywords=tunnel;proxy;port-forward;
StartupWMClass=yukitunnel-desktop
DESKTOP
cp "$APPDIR/usr/share/applications/yukitunnel.desktop" "$APPDIR/yukitunnel.desktop"

install -m644 desktop/src-tauri/icons/icon.png "$APPDIR/usr/share/icons/hicolor/512x512/apps/yukitunnel.png"
install -m644 desktop/src-tauri/icons/icon.png "$APPDIR/yukitunnel.png"
ln -sf usr/share/icons/hicolor/512x512/apps/yukitunnel.png "$APPDIR/.DirIcon"

cat > "$APPDIR/AppRun" <<'APPRUN'
#!/bin/sh
# AppImage entry point for YukiTunnel.
HERE="$(dirname "$(readlink -f "$0")")"
export PATH="$HERE/usr/lib/yukitunnel:$PATH"
export YUKI_BUNDLED_SERVER="$HERE/usr/lib/yukitunnel/yuki-server"
export YUKI_BUNDLED_CLIENT="$HERE/usr/lib/yukitunnel/yuki"
exec "$HERE/usr/bin/yukitunnel-desktop" "$@"
APPRUN
chmod +x "$APPDIR/AppRun"

log "Bundling GTK/WebKit libraries"
for lib in libwebkit2gtk-4.1.so.0 libjavascriptcoregtk-4.1.so.0 libgtk-3.so.0 \
           libgdk-3.so.0 libsoup-3.0.so.0 libayatana-appindicator3.so.1; do
  p="$(ldconfig -p | awk -v L="$lib" '$1==L {print $NF; exit}')"
  if [ -n "${p:-}" ] && [ -f "$p" ]; then
    cp -L "$p" "$APPDIR/usr/lib/"
  else
    echo "    warning: $lib not found; host must provide it" >&2
  fi
done

log "Packing squashfs"
rm -f "$WORK/app.squashfs"
mksquashfs "$APPDIR" "$WORK/app.squashfs" -root-owned -noappend -comp zstd -quiet

log "Fetching AppImage runtime"
RUNTIME="$WORK/runtime-${ARCH}"
if [ ! -s "$RUNTIME" ]; then
  curl -fsSL -o "$RUNTIME" \
    "https://github.com/AppImage/AppImageKit/releases/download/continuous/runtime-${ARCH}"
  chmod +x "$RUNTIME"
fi

log "Assembling type-2 AppImage"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/YukiTunnel-${ARCH}.AppImage"
cat "$RUNTIME" "$WORK/app.squashfs" > "$OUT"
chmod +x "$OUT"

log "Verifying payload"
RT_SIZE="$(stat -c %s "$RUNTIME" 2>/dev/null || stat -f %z "$RUNTIME")"
if command -v unsquashfs >/dev/null 2>&1; then
  rm -rf "$WORK/verify"
  unsquashfs -o "$RT_SIZE" -d "$WORK/verify" -q "$OUT" >/dev/null
  test -x "$WORK/verify/usr/bin/yukitunnel-desktop" || { echo "payload missing desktop binary" >&2; exit 1; }
  "$WORK/verify/usr/lib/yukitunnel/yuki-server" version
  "$WORK/verify/usr/lib/yukitunnel/yuki" --version
fi

log "Done"
ls -lh "$OUT"
echo
echo "Run with:  chmod +x $(basename "$OUT") && ./$(basename "$OUT")"
echo "Extract  :  ./$(basename "$OUT") --appimage-extract"

# ---------------------------------------------------------------------------
# Notes for maintainers
#
# 1. `cargo build --release` alone is NOT enough for a distributable build.
#    Tauri 2 selects dev vs. production via the `custom-protocol` feature; without
#    it the binary loads devUrl (http://localhost:1420) and the AppImage shows
#    "Could not connect to localhost". `tauri build` enables it implicitly.
#
# 2. appimagetool cannot run under QEMU user emulation: the AppImage runtime sets
#    e_ident[8..10] = "AI\x02", which the kernel/binfmt rejects as an invalid
#    ABI version. Assembling `runtime + mksquashfs` by hand avoids that and
#    produces the same type-2 layout.
#
# 3. Bundle the GTK/WebKit .so files but do not bundle glibc; AppImages rely on
#    the host glibc. Test on the oldest distro you intend to support.
# ---------------------------------------------------------------------------
