#!/usr/bin/env bash
# Build the WebUI and every Go binary, embedding the WebUI into yuki-server.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
if [ -z "${VERSION:-}" ]; then
  # Prefer an exact tag; otherwise pre-release form "0.1.0-<sha>" so a dev build
  # never looks like a released version (git describe --always would return the
  # bare commit hash and masquerade as the version).
  if VERSION="$(git describe --tags --exact-match 2>/dev/null)"; then
    :
  else
    VERSION="0.1.0-${COMMIT}"
  fi
fi
LDFLAGS="-s -w -X github.com/yuuuuukiu/yukitunnel/internal/version.Version=${VERSION} -X github.com/yuuuuukiu/yukitunnel/internal/version.Commit=${COMMIT}"

echo "==> Building WebUI"
if command -v pnpm >/dev/null 2>&1; then
  (cd web && pnpm install --frozen-lockfile && pnpm build)
elif command -v npm >/dev/null 2>&1; then
  (cd web && npm ci && npm run build)
else
  echo "pnpm/npm not found; skipping WebUI build" >&2
fi

echo "==> Embedding WebUI"
# The .keep file must survive: `go:embed all:webdist` fails on a fresh clone
# when the directory is empty, so the placeholder is tracked in git.
KEEP=internal/server/webdist/.keep
KEEP_BACKUP=$(mktemp)
[ -f "$KEEP" ] && cp "$KEEP" "$KEEP_BACKUP"
rm -rf internal/server/webdist
mkdir -p internal/server/webdist
if [ -d web/dist ]; then
  cp -R web/dist/. internal/server/webdist/
else
  echo "web/dist missing; server will start without a bundled UI" >&2
  echo "placeholder" > "$KEEP"
fi
if [ -s "$KEEP_BACKUP" ] && [ ! -f "$KEEP" ]; then
  # cp preserves mktemp's 0600; restore normal permissions explicitly.
  cp "$KEEP_BACKUP" "$KEEP"
  chmod 644 "$KEEP"
fi
rm -f "$KEEP_BACKUP"

echo "==> Building Go binaries"
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o bin/yuki-server ./cmd/yuki-server
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o bin/yuki ./cmd/yuki
# Operator tool for validating cloud profiles against a real account.
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o bin/yuki-live-test ./cmd/yuki-live-test

echo "==> Done"
ls -lh bin/
