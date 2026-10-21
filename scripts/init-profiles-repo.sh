#!/usr/bin/env bash
# Create and seed the standalone profile registry repository.
#
# The registry is published from its own repo so that yuki-server releases and
# profile updates are decoupled: a profile can be fixed without shipping a new
# binary, and CI can re-sign the index without touching application code.
#
#   bash scripts/init-profiles-repo.sh [owner]
#
# Requires: gh (authenticated), git. Idempotent: re-running updates the files.
set -euo pipefail

OWNER="${1:-${GH_OWNER:-yuuuuukiu}}"
REPO="yukitunnel-profiles"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

command -v gh >/dev/null || { echo "error: gh CLI is required (https://cli.github.com)" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "error: run 'gh auth login' first" >&2; exit 1; }

echo "==> preparing $OWNER/$REPO"

# The registry repo is a thin shell around profiles/ + the signer.
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/profiles"

cp profiles/*.yaml "$WORK/profiles/"
cp scripts/gen-profile-signing-key.sh "$WORK/" 2>/dev/null || true

cat > "$WORK/README.md" <<'DOC'
# YukiTunnel Profile Registry

Signed, declarative cloud firewall profiles for [YukiTunnel](https://github.com/OWNER/yukitunnel).

```
profiles/*.yaml       声明式 profile
index.json            CI 生成，勿手改
index.json.sig        ed25519 签名，CI 生成
```

## 客户端如何使用

`yuki-server` 内置公钥会验证本仓库的 `index.json.sig`。任何源（jsDelivr、
raw.githubusercontent、Cloudflare R2）都只是传输层，签名决定可信度。

## 如何新增/修改 profile

见 [yukitunnel 仓库的 docs/PROFILE-GUIDE.md](https://github.com/OWNER/yukitunnel/blob/main/docs/PROFILE-GUIDE.md)。

## 镜像

| 源 | 地址 |
| --- | --- |
| jsDelivr | `https://cdn.jsdelivr.net/gh/OWNER/yukitunnel-profiles@main` |
| GitHub raw | `https://raw.githubusercontent.com/OWNER/yukitunnel-profiles/main` |
| R2（可选） | `https://tunnel.yuuuuukiu.top` |

三个源必须字节一致；客户端会对每个源单独验签。
DOC
sed -i.bak "s/OWNER/$OWNER/g" "$WORK/README.md" && rm -f "$WORK/README.md.bak"

cat > "$WORK/.github-workflow-sign.yml" <<'YML'
name: Sign registry

on:
  push:
    paths: ['profiles/**']
  workflow_dispatch:

permissions:
  contents: write

jobs:
  sign:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.24' }

      - name: Fetch signer from the main repo
        run: |
          git clone --depth 1 https://github.com/OWNER/yukitunnel /tmp/yt
          cd /tmp/yt && go build -o /tmp/yuki-profile-sign ./cmd/yuki-profile-sign

      - name: Validate and sign
        env:
          PROFILE_SIGNING_KEY: ${{ secrets.PROFILE_SIGNING_KEY }}
        run: |
          set -euo pipefail
          [ -n "${PROFILE_SIGNING_KEY:-}" ] || { echo "::error::PROFILE_SIGNING_KEY missing"; exit 1; }
          printf '%s' "$PROFILE_SIGNING_KEY" | base64 -d > /tmp/key.pem
          chmod 600 /tmp/key.pem
          /tmp/yuki-profile-sign -dir profiles -key /tmp/key.pem \
            -key-id "${PROFILE_KEY_ID:-yuki-profiles-2026}"

      - name: Commit signed index
        run: |
          git config user.name  "github-actions[bot]"
          git config user.email "bot@yuuuuukiu.top"
          git add profiles/index.json profiles/index.json.sig
          git diff --staged --quiet || git commit -m "chore: sign index"
          git push
YML
sed -i.bak "s/OWNER/$OWNER/g" "$WORK/.github-workflow-sign.yml" && rm -f "$WORK/.github-workflow-sign.yml.bak"

if gh repo view "$OWNER/$REPO" >/dev/null 2>&1; then
  echo "    repo exists, updating files"
  git clone --depth 1 "https://github.com/$OWNER/$REPO" "$WORK/clone" 2>/dev/null || true
  if [ -d "$WORK/clone" ]; then
    cp -R "$WORK/profiles/." "$WORK/clone/profiles/" 2>/dev/null || mkdir -p "$WORK/clone/profiles" && cp "$WORK/profiles/"*.yaml "$WORK/clone/profiles/"
    cp "$WORK/README.md" "$WORK/clone/README.md"
    mkdir -p "$WORK/clone/.github/workflows"
    mv "$WORK/.github-workflow-sign.yml" "$WORK/clone/.github/workflows/sign.yml"
    ( cd "$WORK/clone" && git add -A && git diff --staged --quiet || \
      git commit -m "chore: sync profiles from main repo" && git push ) || true
  fi
else
  echo "    creating repo"
  gh repo create "$OWNER/$REPO" --public \
    --description "Signed cloud firewall profiles for YukiTunnel" \
    --disable-wiki
  ( cd "$WORK" && git init -q . && mkdir -p .github/workflows \
      && mv .github-workflow-sign.yml .github/workflows/sign.yml \
      && git add -A && git commit -qm "feat: initial profile registry" \
      && git branch -M main \
      && git remote add origin "https://github.com/$OWNER/$REPO.git" \
      && git push -u origin main )
fi

echo
echo "下一步（必须手动做一次）："
echo "  1. gh secret set PROFILE_SIGNING_KEY --repo $OWNER/$REPO"
echo "  2. gh variable set PROFILE_KEY_ID --repo $OWNER/$REPO --body yuki-profiles-2026"
echo "  3. 在 Actions 里跑一次 'Sign registry'，生成 profiles/index.json{,.sig}"
echo "  4. 验证："
echo "       curl -fsSL https://cdn.jsdelivr.net/gh/$OWNER/$REPO@main/profiles/index.json | head -c 200"
echo "       ./bin/yuki-server profile index"
