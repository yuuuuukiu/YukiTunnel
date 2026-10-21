#!/usr/bin/env bash
# Build the WebUI and every Go binary, embedding the WebUI into yuki-server.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
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
rm -rf internal/server/webdist
mkdir -p internal/server/webdist
if [ -d web/dist ]; then
  cp -R web/dist/. internal/server/webdist/
else
  echo "web/dist missing; server will start without a bundled UI" >&2
  echo "placeholder" > internal/server/webdist/.keep
fi

echo "==> Building Go binaries"
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o bin/yuki-server ./cmd/yuki-server
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o bin/yuki ./cmd/yuki
# Operator tool for validating cloud profiles against a real account.
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o bin/yuki-live-test ./cmd/yuki-live-test

echo "==> Done"
ls -lh bin/
