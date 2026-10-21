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
# Must match the existing R2 bucket; the custom domain CNAME targets it.
BUCKET="${R2_BUCKET:-yuki-tunnel-profile}"
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
name: Sign and publish registry

on:
  push:
    paths: ['profiles/**']
  workflow_dispatch:

permissions:
  contents: write

concurrency:
  group: sign-registry
  cancel-in-progress: false

jobs:
  sign:
    runs-on: ubuntu-latest
    env:
      # Declared at job level so the step-level `if:` conditions below can
      # actually see them. Secrets are masked in logs and never echo'd.
      R2_ACCESS_KEY_ID: ${{ secrets.R2_ACCESS_KEY_ID }}
      R2_SECRET_ACCESS_KEY: ${{ secrets.R2_SECRET_ACCESS_KEY }}
      R2_ENDPOINT: ${{ secrets.R2_ENDPOINT }}
      R2_BUCKET: ${{ vars.R2_BUCKET || 'BUCKET_NAME' }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.24' }

      - name: Build the signer from the main repo
        run: |
          set -euo pipefail
          git clone --depth 1 "https://github.com/OWNER/yukitunnel" /tmp/yt
          cd /tmp/yt
          go build -trimpath -o /tmp/yuki-profile-sign ./cmd/yuki-profile-sign

      - name: Sign index.json
        env:
          PROFILE_SIGNING_KEY: ${{ secrets.PROFILE_SIGNING_KEY }}
          PROFILE_KEY_ID: ${{ vars.PROFILE_KEY_ID || 'yuki-profiles-2026' }}
        run: |
          set -euo pipefail
          [ -n "${PROFILE_SIGNING_KEY:-}" ] || { echo "::error::PROFILE_SIGNING_KEY is not set"; exit 1; }
          printf '%s' "$PROFILE_SIGNING_KEY" | base64 -d > /tmp/key.pem
          chmod 600 /tmp/key.pem
          /tmp/yuki-profile-sign -dir profiles -key /tmp/key.pem -key-id "$PROFILE_KEY_ID"

      - name: Verify the signature round-trips
        run: |
          set -euo pipefail
          cd /tmp/yt
          # The verifier lives in the main repo; run its registry tests against
          # the freshly signed documents.
          cp -R "$GITHUB_WORKSPACE/profiles/." profiles/
          go test ./internal/cloudprofile/... -run 'Signature|Registry|Official' -count=1

      - name: Commit signed index
        run: |
          set -euo pipefail
          rm -f /tmp/key.pem
          git config user.name  "github-actions[bot]"
          git config user.email "bot@OWNER-DOMAIN"
          git add profiles/index.json profiles/index.json.sig
          if git diff --staged --quiet; then
            echo "index unchanged"
          else
            git commit -m "chore: sign index"
            git push
          fi

      # Mirrored bytes must be identical to what GitHub serves: the client
      # verifies the signature, but a stale R2 copy would silently win the
      # fallback race and serve an old catalogue.
      - name: Sync to Cloudflare R2
        if: ${{ env.R2_ENDPOINT != '' }}
        run: |
          set -euo pipefail
          command -v aws >/dev/null || pip install --quiet awscli
          # Upload under profiles/ so the bucket layout matches the repo.
          for f in profiles/*.yaml profiles/index.json profiles/index.json.sig; do
            case "$f" in
              *.json|*.sig) ct=application/json ;;
              *)            ct=application/yaml ;;
            esac
            aws s3 cp "$f" "s3://$R2_BUCKET/$f" \
              --endpoint-url "$R2_ENDPOINT" \
              --content-type "$ct" \
              --cache-control "public, max-age=300"
          done
          echo "synced to s3://$R2_BUCKET/profiles/"

      - name: R2 not configured (skipped)
        if: ${{ env.R2_ENDPOINT == '' }}
        run: |
          echo "::warning::R2_ENDPOINT is not set; the mirror was not updated."
          echo "Set R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY / R2_ENDPOINT to enable it."

  verify-mirrors:
    name: Verify public mirrors agree
    needs: sign
    runs-on: ubuntu-latest
    steps:
      - name: Compare jsDelivr, raw and R2
        run: |
          set -euo pipefail
          urls=(
            "https://cdn.jsdelivr.net/gh/OWNER/yukitunnel-profiles@main/profiles/index.json"
            "https://raw.githubusercontent.com/OWNER/yukitunnel-profiles/main/profiles/index.json"
            "https://tunnel.yuuuuukiu.top/profiles/index.json"
          )
          declare -a sums=()
          for u in "${urls[@]}"; do
            if body=$(curl -fsSL --max-time 20 "$u"); then
              s=$(printf '%s' "$body" | sha256sum | cut -d' ' -f1)
              echo "OK    $u  ${s:0:16}"
              sums+=("$s")
            else
              echo "SKIP  $u (unreachable)"
            fi
          done
          if [ ${#sums[@]} -lt 2 ]; then
            echo "::warning::fewer than two mirrors reachable; skipping comparison"
            exit 0
          fi
          first="${sums[0]}"
          for s in "${sums[@]}"; do
            if [ "$s" != "$first" ]; then
              echo "::error::mirrors disagree — a client could pin a stale index"
              exit 1
            fi
          done
          echo "all reachable mirrors agree"
YML
sed -i.bak -e "s/OWNER-DOMAIN/$OWNER.top/g" -e "s/OWNER/$OWNER/g" -e "s/BUCKET_NAME/$BUCKET/g" "$WORK/.github-workflow-sign.yml" && rm -f "$WORK/.github-workflow-sign.yml.bak"

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
