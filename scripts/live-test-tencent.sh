#!/usr/bin/env bash
# Live integration test against a real Tencent Cloud VPC security group.
#
#   export YUKI_TENCENT_SECRET_ID=...
#   export YUKI_TENCENT_SECRET_KEY=...
#   export YUKI_REGION=ap-guangzhou
#   export YUKI_SECURITY_GROUP_ID=sg-xxxxxxxx
#   export YUKI_TEST_PORT=20000
#   bash scripts/live-test-tencent.sh
#
# Tencent's CreateSecurityGroupPolicies appends a rule; the engine's
# idempotent_check queries DescribeSecurityGroupPolicies first, so a second run
# must report "already present" without touching the API again.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PROFILE="profiles/tencent-vpc-security-group.yaml"
BIN="${YUKI_LIVE_BIN:-$ROOT/bin/yuki-live-test}"

die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[1;36m==> %s\033[0m\n' "$*"; }

[ -n "${YUKI_TENCENT_SECRET_ID:-}" ] || die "缺少 YUKI_TENCENT_SECRET_ID"
[ -n "${YUKI_TENCENT_SECRET_KEY:-}" ] || die "缺少 YUKI_TENCENT_SECRET_KEY"
[ -n "${YUKI_REGION:-}" ] || die "缺少 YUKI_REGION（例如 ap-guangzhou）"
[ -n "${YUKI_SECURITY_GROUP_ID:-}" ] || die "缺少 YUKI_SECURITY_GROUP_ID"
PORT="${YUKI_TEST_PORT:-20000}"
PROTO="${YUKI_TEST_PROTO:-tcp}"

case "$PORT" in
  ''|*[!0-9]*) die "YUKI_TEST_PORT 必须是数字" ;;
esac
if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
  die "YUKI_TEST_PORT 必须在 1-65535 之间"
fi

if [ ! -x "$BIN" ]; then
  info "构建 yuki-live-test"
  go build -o "$BIN" ./cmd/yuki-live-test
fi

info "检查本机时间偏差（TC3 签名允许 ±5 分钟，这里按 15 分钟预警）"
if ! "$BIN" -file "$PROFILE" -port "$PORT" -proto "$PROTO" \
      -param "region=$YUKI_REGION" -param "security_group_id=$YUKI_SECURITY_GROUP_ID" \
      -dry-run 2>&1 | grep -q 'clock-skew.*PASS\|ALL PASS'; then
  die "请先校准系统时间（chronyc makestep / w32tm /resync）"
fi

info "腾讯云联调：$YUKI_REGION / $YUKI_SECURITY_GROUP_ID / $PORT/$PROTO"
set +e
"$BIN" \
  -profile tencent-vpc-security-group \
  -file "$PROFILE" \
  -port "$PORT" \
  -proto "$PROTO" \
  -param "region=$YUKI_REGION" \
  -param "security_group_id=$YUKI_SECURITY_GROUP_ID" \
  -param "source_cidr=${YUKI_SOURCE_CIDR:-0.0.0.0/0}"
rc=$?
set -e

if [ "$rc" -ne 0 ]; then
  cat >&2 <<'HINT'

联调失败时按顺序排查：
  1. AuthFailure.SignatureFailure → SecretKey 错误，或本机时间偏差超过 5 分钟
  2. UnauthorizedOperation       → 子账号缺少 vpc:CreateSecurityGroupPolicies 等权限
  3. InvalidParameterValue       → 端口格式（腾讯云用 8000-8010，单端口写 8000）
  4. LimitExceeded               → 安全组规则数量达到上限
  5. ResourceNotFound            → 安全组 ID 或 Region 不对

腾讯云按 PolicyIndex 排序，脚本只追加/删除五元组完全匹配的规则，不会影响
其他规则。失败后请在控制台确认没有残留的 "YukiTunnel managed rule"。
HINT
  exit "$rc"
fi

info "联调通过。请把结果填入 docs/ACCEPTANCE-PROFILES.md 的记录表。"
