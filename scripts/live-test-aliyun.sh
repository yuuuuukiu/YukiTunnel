#!/usr/bin/env bash
# Live integration test against a real Aliyun ECS security group.
#
#   export YUKI_ALIYUN_ACCESS_KEY_ID=...
#   export YUKI_ALIYUN_ACCESS_KEY_SECRET=...
#   export YUKI_REGION=cn-hangzhou
#   export YUKI_SECURITY_GROUP_ID=sg-xxxxxxxx
#   export YUKI_TEST_PORT=20000            # must be free in the security group
#   bash scripts/live-test-aliyun.sh
#
# The script opens the port, verifies it exists, opens it again (idempotency),
# closes it, then re-opens and closes once more to prove the revoke really took
# effect. Every step prints PASS/FAIL. It always leaves the account clean.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PROFILE="profiles/aliyun-ecs-security-group.yaml"
BIN="${YUKI_LIVE_BIN:-$ROOT/bin/yuki-live-test}"

die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[1;36m==> %s\033[0m\n' "$*"; }

[ -n "${YUKI_ALIYUN_ACCESS_KEY_ID:-}" ] || die "缺少 YUKI_ALIYUN_ACCESS_KEY_ID"
[ -n "${YUKI_ALIYUN_ACCESS_KEY_SECRET:-}" ] || die "缺少 YUKI_ALIYUN_ACCESS_KEY_SECRET"
[ -n "${YUKI_REGION:-}" ] || die "缺少 YUKI_REGION（例如 cn-hangzhou）"
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

# Clock skew makes every signature invalid; check it before touching the cloud.
info "检查本机时间偏差（Aliyun 允许 ±15 分钟）"
if ! "$BIN" -file "$PROFILE" -port "$PORT" -proto "$PROTO" \
      -param "region=$YUKI_REGION" -param "security_group_id=$YUKI_SECURITY_GROUP_ID" \
      -dry-run 2>&1 | grep -q 'clock-skew.*PASS\|ALL PASS'; then
  info "时间检查未通过，继续执行会让所有签名失效"
  die "请先校准系统时间（ntpdate / chronyc makestep / w32tm /resync）"
fi

info "阿里云联调：$YUKI_REGION / $YUKI_SECURITY_GROUP_ID / $PORT/$PROTO"
set +e
"$BIN" \
  -profile aliyun-ecs-security-group \
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
  1. SignatureDoesNotMatch  → 本机时间偏差，或 AccessKeySecret 抄错（含首尾空格）
  2. Forbidden.RAM          → 子账号缺少 ecs:AuthorizeSecurityGroup / RevokeSecurityGroup
  3. InvalidSecurityGroupId → 安全组不在该 Region，或 ID 抄错
  4. Throttling             → 触发限流，等待 1 分钟后重跑
  5. InvalidPortRange       → 端口格式问题（阿里云用 起始/结束，脚本已处理）

无论失败在哪一步，脚本都已尝试回收端口；如仍有残留，请在控制台手动删除
描述为 "YukiTunnel managed rule" 的规则。
HINT
  exit "$rc"
fi

info "联调通过。请把结果填入 docs/ACCEPTANCE-PROFILES.md 的记录表。"
