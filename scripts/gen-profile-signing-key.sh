#!/usr/bin/env bash
# Generate the Ed25519 key pair that signs the cloud profile registry index.
#
#   bash scripts/gen-profile-signing-key.sh [output-dir]
#
# Outputs:
#   <dir>/profile-signing.key   PKCS#8 PEM private key  (NEVER commit this)
#   <dir>/profile-signing.pub   base64 raw public key    (pin into the binary)
#
# The public key must replace builtinProfileKeyB64 in
# internal/server/profilekeys.go. The private key must be stored in the CI
# secret PROFILE_SIGNING_KEY as base64 of the PEM file.
set -euo pipefail

OUT_DIR="${1:-$(pwd)/secrets}"
mkdir -p "$OUT_DIR"
chmod 700 "$OUT_DIR"

KEY="$OUT_DIR/profile-signing.key"
PUB="$OUT_DIR/profile-signing.pub"

if [ -f "$KEY" ]; then
  echo "error: $KEY already exists; refusing to overwrite a signing key." >&2
  echo "       Move it away first if you really want to rotate." >&2
  exit 1
fi

if ! command -v openssl >/dev/null 2>&1; then
  echo "error: openssl is required" >&2
  exit 1
fi

echo "==> generating Ed25519 private key"
openssl genpkey -algorithm ED25519 -out "$KEY"
chmod 600 "$KEY"

echo "==> deriving public key (base64 raw, 32 bytes)"
# -pubout emits SubjectPublicKeyInfo DER; the last 32 bytes are the raw key.
openssl pkey -in "$KEY" -pubout -outform DER \
  | tail -c 32 \
  | openssl base64 -A > "$PUB"

PUB_B64="$(cat "$PUB")"
echo
echo "──────────────────────────────────────────────────────────────"
echo "Public key (base64):"
echo
echo "  $PUB_B64"
echo
echo "Replace the constant in internal/server/profilekeys.go:"
echo
echo "  const builtinProfileKeyB64 = \"$PUB_B64\""
echo
echo "Or set it for a local run:"
echo
echo "  export YUKI_PROFILE_PUBKEY=$PUB_B64"
echo "──────────────────────────────────────────────────────────────"
echo
echo "Private key: $KEY  (mode 0600, git-ignored)"
echo "Add it to GitHub Actions secrets as PROFILE_SIGNING_KEY:"
echo
echo "  base64 -i $KEY | pbcopy      # macOS"
echo "  base64 -w0 $KEY             # Linux, then paste the output"
echo
echo "Then set the secret:"
echo
echo "  gh secret set PROFILE_SIGNING_KEY < <(base64 -w0 $KEY)"
echo
echo "WARNING: never commit the private key. Rotating it invalidates every"
echo "         published index signature; re-sign and re-publish in one step."
