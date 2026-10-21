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

# Prefer SSH: most contributors already have a working key, while `gh auth` may
# not be configured. Falls back to HTTPS (which then needs a token) if the SSH
# probe fails.
SSH_URL="git@github.com:$OWNER/$REPO.git"
HTTPS_URL="https://github.com/$OWNER/$REPO.git"

have_gh_auth() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

repo_exists() {
  # git ls-remote works with either transport and needs no API token.
  GIT_SSH_COMMAND="ssh -o BatchMode=yes" git ls-remote "$SSH_URL" >/dev/null 2>&1
}

pick_url() {
  if GIT_SSH_COMMAND="ssh -o BatchMode=yes" ssh -T git@github.com 2>&1 | grep -q "successfully authenticated"; then
    echo "$SSH_URL"
  else
    echo "$HTTPS_URL"
  fi
}
REMOTE="$(pick_url)"

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
          /tmp/yuki-profile-sign -dir profiles -key /tmp/key.pem \
            -key-id "$PROFILE_KEY_ID" -pub-out /tmp/pub.b64

      - name: Stash signed output for verification
        run: |
          mkdir -p /tmp/registry/profiles
          cp profiles/index.json profiles/index.json.sig /tmp/registry/profiles/

      # Verify the documents we just produced with the public half of the same
      # key. Running the main repo's registry tests instead would only exercise
      # their own fixtures and prove nothing about this index.
      - name: Verify the fresh signature
        env:
          PROFILE_KEY_ID: ${{ vars.PROFILE_KEY_ID || 'yuki-profiles-2026' }}
        run: |
          set -euo pipefail
          cd /tmp/yt
          cat > /tmp/verify_test.go <<'GO'
          package registry

          import (
          	"crypto/ed25519"
          	"encoding/base64"
          	"os"
          	"testing"
          )

          func TestFreshlySignedIndex(t *testing.T) {
          	pubB64, err := os.ReadFile("/tmp/pub.b64")
          	if err != nil {
          		t.Fatal(err)
          	}
          	pub, err := base64.StdEncoding.DecodeString(string(trimNL(pubB64)))
          	if err != nil || len(pub) != ed25519.PublicKeySize {
          		t.Fatalf("bad public key: %v", err)
          	}
          	rawIndex, err := os.ReadFile("/tmp/registry/profiles/index.json")
          	if err != nil {
          		t.Fatal(err)
          	}
          	rawSig, err := os.ReadFile("/tmp/registry/profiles/index.json.sig")
          	if err != nil {
          		t.Fatal(err)
          	}
          	// The trust map is keyed by the key_id embedded in the signature, so
          	// an invented name here would always report "not trusted".
          	keyID := os.Getenv("PROFILE_KEY_ID")
          	if keyID == "" {
          		keyID = "yuki-profiles-2026"
          	}
          	if _, err := VerifyIndex(rawIndex, rawSig, map[string]ed25519.PublicKey{keyID: pub}); err != nil {
          		t.Fatalf("index failed verification: %v", err)
          	}
          }

          func trimNL(b []byte) string {
          	for len(b) > 0 && (b[len(b)-1] == 10 || b[len(b)-1] == 13) {
          		b = b[:len(b)-1]
          	}
          	return string(b)
          }
          GO
          cp /tmp/verify_test.go internal/cloudprofile/registry/zz_fresh_test.go
          go test ./internal/cloudprofile/registry -run TestFreshlySignedIndex -v -count=1

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
            # Another push may land between checkout and here (a manual sync,
            # or a second dispatch). Rebase so the signed index is applied on
            # top of the current tip instead of failing with "fetch first".
            for attempt in 1 2 3; do
              if git push; then
                break
              fi
              echo "push rejected (attempt $attempt); rebasing on origin/main"
              git pull --rebase --autostash origin main
              sleep $((attempt * 3))
            done
            git push
          fi

      # Mirrored bytes must be identical to what GitHub serves: the client
      # verifies the signature, but a stale R2 copy would silently win the
      # fallback race and serve an old catalogue.
      - name: Sync to Cloudflare R2
        if: ${{ env.R2_ENDPOINT != '' }}
        # The AWS CLI only reads AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, so
        # map our R2_* secrets onto those names here.
        env:
          AWS_ACCESS_KEY_ID: ${{ secrets.R2_ACCESS_KEY_ID }}
          AWS_SECRET_ACCESS_KEY: ${{ secrets.R2_SECRET_ACCESS_KEY }}
          AWS_DEFAULT_REGION: auto
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
          # Origin mirrors must match exactly; the jsDelivr CDN is allowed to
          # lag, because its cache lifetime is measured in hours and cannot be
          # purged from CI. It is still compared and reported as a warning.
          urls=(
            "https://raw.githubusercontent.com/OWNER/yukitunnel-profiles/main/profiles/index.json"
            "https://tunnel.yuuuuukiu.top/profiles/index.json"
          )
          cdn_url="https://cdn.jsdelivr.net/gh/OWNER/yukitunnel-profiles@main/profiles/index.json"
          # jsDelivr caches for minutes and R2 for a few hundred seconds, so a
          # freshly pushed index can legitimately lag. Retry before failing:
          # the point is to catch a mirror that is *permanently* stale, not to
          # trip over normal CDN propagation.
          declare -a sums=()
          for attempt in 1 2 3 4 5 6; do
            sums=()
            for u in "${urls[@]}"; do
              # The R2 custom domain sits behind Cloudflare, which challenges
              # curl's default UA. A browser UA is enough to get the bytes.
              if body=$(curl -fsSL --max-time 20 \
                  -A "Mozilla/5.0 (compatible; YukiTunnel-CI/1.0)" "$u"); then
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
            agree=1
            first="${sums[0]}"
            for s in "${sums[@]}"; do
              [ "$s" != "$first" ] && agree=0
            done
            if [ "$agree" = "1" ]; then
              echo "origin mirrors agree"
              # Advisory CDN comparison: never fails the job.
              if cdn=$(curl -fsSL --max-time 20 -A "Mozilla/5.0 (compatible; YukiTunnel-CI/1.0)" "$cdn_url" 2>/dev/null); then
                cdn_sum=$(printf '%s' "$cdn" | sha256sum | cut -d' ' -f1)
                if [ "$cdn_sum" = "$first" ]; then
                  echo "CDN matches origin"
                else
                  echo "::warning::jsDelivr still serving ${cdn_sum:0:16} (origin ${first:0:16}); it will converge within its TTL"
                fi
              else
                echo "::warning::jsDelivr unreachable"
              fi
              exit 0
            fi
            if [ "$attempt" -lt 6 ]; then
              echo "mirrors still propagating (attempt $attempt/6); waiting 45s"
              sleep 45
            fi
          done
          echo "::error::origin mirrors disagree after 6 attempts — a client could pin a stale index"
          exit 1

          # Unreachable: the loop above only exits 0 or 1. The CDN check below
          # is intentionally advisory so an hours-long jsDelivr cache does not
          # block a release.
YML
sed -i.bak -e "s/OWNER-DOMAIN/$OWNER.top/g" -e "s/OWNER/$OWNER/g" -e "s/BUCKET_NAME/$BUCKET/g" "$WORK/.github-workflow-sign.yml" && rm -f "$WORK/.github-workflow-sign.yml.bak"

if repo_exists; then
  echo "    repo exists, syncing files via $REMOTE"
  git clone --depth 1 "$REMOTE" "$WORK/clone"
  mkdir -p "$WORK/clone/profiles" "$WORK/clone/.github/workflows"
  cp "$WORK/profiles/"*.yaml "$WORK/clone/profiles/"
  cp "$WORK/README.md" "$WORK/clone/README.md"
  mv "$WORK/.github-workflow-sign.yml" "$WORK/clone/.github/workflows/sign.yml"
  (
    cd "$WORK/clone"
    git add -A
    if git diff --staged --quiet; then
      echo "    nothing to update"
    else
      git commit -qm "chore: sync profiles from main repo"
      git push origin HEAD:main
    fi
  )
else
  echo "    repo does not exist yet"
  if have_gh_auth; then
    echo "    creating with gh"
    gh repo create "$OWNER/$REPO" --public \
      --description "Signed cloud firewall profiles for YukiTunnel" \
      --disable-wiki >/dev/null
  else
    cat >&2 <<MANUAL

────────────────────────────────────────────────────────────────
需要你先手动创建空仓库（gh 未登录，且创建仓库必须走 API）：

  1. 打开 https://github.com/new
  2. Repository name: $REPO
  3. Owner: $OWNER
  4. 选 Public
  5. **不要**勾选 "Add a README file" / .gitignore / license（必须是空仓库）
  6. 点 Create repository
  7. 回来重新执行本条命令

或者先登录 gh，让脚本自动建：

  gh auth login
  bash scripts/init-profiles-repo.sh
────────────────────────────────────────────────────────────────
MANUAL
    exit 1
  fi

  echo "    seeding via $REMOTE"
  (
    cd "$WORK"
    git init -q .
    mkdir -p .github/workflows
    mv .github-workflow-sign.yml .github/workflows/sign.yml
    git add -A
    git commit -qm "feat: initial profile registry"
    git branch -M main
    git remote add origin "$REMOTE"
    git push -u origin main
  )
fi

echo
cat <<NEXT

════════════════════════════════════════════════════════════════
仓库已就绪：https://github.com/$OWNER/$REPO

还需要两步才能出签名索引。

── 1. 配置 4 个 Secret + 2 个 Variable ─────────────────────────

  https://github.com/$OWNER/$REPO/settings/secrets/actions

  Secrets（敏感值）:
    PROFILE_SIGNING_KEY    base64 -w0 secrets/profile-signing.key
    R2_ACCESS_KEY_ID       Cloudflare R2 Token 的 Access Key ID
    R2_SECRET_ACCESS_KEY   Cloudflare R2 Token 的 Secret
    R2_ENDPOINT            https://<account-id>.r2.cloudflarestorage.com

  Variables（普通配置）:
    R2_BUCKET              $BUCKET
    PROFILE_KEY_ID         yuki-profiles-2026

  命令行等价（gh 已登录时）:
    gh secret set PROFILE_SIGNING_KEY --repo $OWNER/$REPO < <(base64 -w0 secrets/profile-signing.key)
    gh secret set R2_ACCESS_KEY_ID     --repo $OWNER/$REPO
    gh secret set R2_SECRET_ACCESS_KEY --repo $OWNER/$REPO
    gh secret set R2_ENDPOINT          --repo $OWNER/$REPO
    gh variable set R2_BUCKET      --repo $OWNER/$REPO --body $BUCKET
    gh variable set PROFILE_KEY_ID --repo $OWNER/$REPO --body yuki-profiles-2026

  完整说明见 docs/SETUP-SECRETS.md

── 2. 跑一次签名 ──────────────────────────────────────────────

  https://github.com/$OWNER/$REPO/actions
    → "Sign and publish registry" → Run workflow

  它会: 校验 profile → 生成 index.json{,.sig} → 提交 → 推送 R2 → 比对三源

── 验证 ───────────────────────────────────────────────────────

  curl -fsSL https://cdn.jsdelivr.net/gh/$OWNER/$REPO@main/profiles/index.json | head -c 200
  curl -fsSL https://tunnel.yuuuuukiu.top/profiles/index.json | head -c 200
  ./bin/yuki-server profile index

  期望最后一行为「已验签（key_id: yuki-profiles-2026）」。
  若显示 builtin，说明镜像还没生效，客户端退回了内置副本。
════════════════════════════════════════════════════════════════
NEXT
