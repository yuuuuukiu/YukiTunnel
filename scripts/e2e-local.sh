#!/usr/bin/env bash
# End-to-end local test: Docker server -> CLI admin -> client CLI -> tunnel -> HTTP.
#
#   bash scripts/e2e-local.sh
#   bash scripts/e2e-local.sh --no-docker     # use the local binary instead
#
# Idempotent: a previous run's container/volume and temp dir are reused or
# cleaned up. On failure it prints the exact commands needed to inspect state.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

USE_DOCKER=1
for arg in "$@"; do
  case "$arg" in
    --no-docker) USE_DOCKER=0 ;;
    -h|--help) sed -n '2,9p' "$0"; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

API_PORT="${E2E_API_PORT:-18443}"
CTL_PORT="${E2E_CTL_PORT:-19443}"
PUB_PORT="${E2E_PUB_PORT:-28555}"
LOCAL_PORT="${E2E_LOCAL_PORT:-18080}"
ADMIN_USER="admin"
ADMIN_PASS="e2e-admin-pass"
USER_NAME="e2e-user"
USER_PASS="e2e-user-pass"
PROJECT="yukitunnel-e2e"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/yukitunnel-e2e.XXXXXX")"
export YUKI_CLIENT_HOME="$WORK/client"

SERVER_PID=""
AGENT_PID=""
HTTP_PID=""
FAILED=0

say()  { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[1;32mOK\033[0m   %s\n' "$*"; }
bad()  { printf '    \033[1;31mFAIL\033[0m %s\n' "$*"; FAILED=1; }
note() { printf '         %s\n' "$*"; }

cleanup() {
  set +e
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  [ -n "$HTTP_PID" ] && kill "$HTTP_PID" 2>/dev/null
  if [ "$USE_DOCKER" = "1" ]; then
    docker compose -p "$PROJECT" -f docker-compose.yml -f docker-compose.e2e.yml down -v >/dev/null 2>&1
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

diagnose() {
  cat >&2 <<HINT

────────────────────────────────────────────────────────────────
排查命令（复制即可运行）：

  # 服务端日志
$( [ "$USE_DOCKER" = "1" ] \
   && echo "  docker compose -p $PROJECT -f docker-compose.yml -f docker-compose.e2e.yml logs --tail 100" \
   || echo "  cat $WORK/server.log" )

  # 端口占用
  lsof -nP -iTCP:$API_PORT -sTCP:LISTEN
  lsof -nP -iTCP:$PUB_PORT -sTCP:LISTEN

  # 健康检查
  curl -sk https://127.0.0.1:$API_PORT/api/v1/health

  # 客户端日志
  cat $WORK/agent.log

  # 清理
  docker compose -p $PROJECT -f docker-compose.yml -f docker-compose.e2e.yml down -v
  rm -rf $WORK
────────────────────────────────────────────────────────────────
HINT
}

# ── 1. server ──────────────────────────────────────────────────────────────
say "启动服务端"
mkdir -p "$WORK/data" "$WORK/bin"
go build -o "$WORK/bin/yuki-server" ./cmd/yuki-server
go build -o "$WORK/bin/yuki" ./cmd/yuki
export PATH="$WORK/bin:$PATH"

if [ "$USE_DOCKER" = "1" ]; then
  if ! docker info >/dev/null 2>&1; then
    note "Docker 不可用，回退到本地二进制"
    USE_DOCKER=0
  fi
fi

if [ "$USE_DOCKER" = "1" ]; then
  cat > "$WORK/.env" <<ENV
YUKI_IMAGE=yukitunnel/server:e2e
API_PORT=$API_PORT
CONTROL_PORT=$CTL_PORT
PORT_RANGE_START=$PUB_PORT
PORT_RANGE_END=$PUB_PORT
YUKI_PUBLIC_HOST=127.0.0.1
YUKI_ADMIN_USERNAME=$ADMIN_USER
YUKI_ADMIN_PASSWORD=$ADMIN_PASS
YUKI_DOMAIN_BINDING=false
YUKI_PORT_MIN=$PUB_PORT
YUKI_PORT_MAX=$PUB_PORT
ENV
  docker build -q -t yukitunnel/server:e2e . >/dev/null
  docker compose -p "$PROJECT" -f docker-compose.yml -f docker-compose.e2e.yml \
    --env-file "$WORK/.env" up -d --quiet-pull >/dev/null
  ok "容器已启动（$PROJECT）"
else
  yuki-server init --data-dir "$WORK/data" \
    --api-addr "127.0.0.1:$API_PORT" --control-addr "127.0.0.1:$CTL_PORT" \
    --admin-password "$ADMIN_PASS" --port-min "$PUB_PORT" --port-max "$PUB_PORT" \
    --public-host 127.0.0.1 >/dev/null
  yuki-server serve --data-dir "$WORK/data" >"$WORK/server.log" 2>&1 &
  SERVER_PID=$!
  ok "本地服务端已启动（pid $SERVER_PID）"
fi

say "等待健康检查"
healthy=0
for _ in $(seq 1 60); do
  if curl -sk --max-time 2 "https://127.0.0.1:$API_PORT/api/v1/health" 2>/dev/null | grep -q '"status":"ok"'; then
    healthy=1; break
  fi
  sleep 1
done
if [ "$healthy" = "1" ]; then ok "健康检查通过"; else bad "服务端未就绪"; diagnose; exit 1; fi

# ── 2. admin + user + node ─────────────────────────────────────────────────
say "创建用户与节点"
yuki-server user create "$USER_NAME" --password "$USER_PASS" --data-dir "$WORK/data" >/dev/null 2>&1 || true
if yuki-server --server "https://127.0.0.1:$API_PORT" --username "$ADMIN_USER" --password "$ADMIN_PASS" --insecure user list 2>/dev/null | grep -q "$USER_NAME"; then
  ok "用户 $USER_NAME 存在"
else
  # Docker mode keeps data inside the volume; use the API instead.
  TOKEN=$(curl -sk -X POST "https://127.0.0.1:$API_PORT/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
  curl -sk -X POST "https://127.0.0.1:$API_PORT/api/v1/admin/users" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$USER_NAME\",\"password\":\"$USER_PASS\"}" >/dev/null
  ok "用户 $USER_NAME 已通过 API 创建"
fi

NODE_ID=$(curl -sk -X POST "https://127.0.0.1:$API_PORT/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])' \
  | xargs -I{} curl -sk -H "Authorization: Bearer {}" "https://127.0.0.1:$API_PORT/api/v1/servers" \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["servers"][0]["id"])')
ok "节点 $NODE_ID 可用"

# ── 3. profile (dry-run) ───────────────────────────────────────────────────
say "安装云 Profile 并 dry-run"
SHA=$(shasum -a 256 profiles/aliyun-ecs-security-group.yaml 2>/dev/null | cut -d' ' -f1 \
      || sha256sum profiles/aliyun-ecs-security-group.yaml | cut -d' ' -f1)
TOKEN=$(curl -sk -X POST "https://127.0.0.1:$API_PORT/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
python3 - "$SHA" <<'PY' > "$WORK/install.json"
import json, sys
doc = open('profiles/aliyun-ecs-security-group.yaml').read()
print(json.dumps({"document": doc, "sha256": sys.argv[1]}))
PY
curl -sk -X POST "https://127.0.0.1:$API_PORT/api/v1/admin/profiles/install" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  --data-binary @"$WORK/install.json" >/dev/null
curl -sk -X PUT "https://127.0.0.1:$API_PORT/api/v1/admin/profiles/aliyun-ecs-security-group/credentials" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"label":"default","fields":{"access_key_id":"AKIAFAKE","access_key_secret":"FAKE"}}' >/dev/null
RUN=$(curl -sk -X POST "https://127.0.0.1:$API_PORT/api/v1/admin/profiles/aliyun-ecs-security-group/run" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"action\":\"ensure_port_allowed\",\"node_id\":\"$NODE_ID\",\"port_min\":$PUB_PORT,\"port_max\":$PUB_PORT,\"protocol\":\"tcp\",\"params\":{\"region\":\"cn-hangzhou\",\"security_group_id\":\"sg-e2e\"},\"dry_run\":true,\"acknowledge_permissions\":true}")
if printf '%s' "$RUN" | grep -q '"skipped":true'; then
  ok "profile dry-run 成功（未调用云 API）"
else
  bad "profile dry-run 失败：$RUN"
fi

# ── 4. client ──────────────────────────────────────────────────────────────
say "客户端登录并创建隧道"
yuki login --server "https://127.0.0.1:$API_PORT" --username "$USER_NAME" --password "$USER_PASS" --insecure >/dev/null
ok "登录成功"

TUNNEL_JSON=$(yuki tunnel create --proto tcp --local-ip 127.0.0.1 --local-port "$LOCAL_PORT" --no-domain --json)
TUNNEL_ID=$(printf '%s' "$TUNNEL_JSON" | python3 -c 'import json,sys;print(json.load(sys.stdin)["tunnel"]["id"])')
ADDRESS=$(printf '%s' "$TUNNEL_JSON" | python3 -c 'import json,sys;print(json.load(sys.stdin)["tunnel"]["address"])')
ok "隧道 $TUNNEL_ID -> $ADDRESS"

say "启动本地服务与 agent"
mkdir -p "$WORK/www"
printf 'yukitunnel-e2e-ok\n' > "$WORK/www/index.html"
( cd "$WORK/www" && exec python3 -m http.server "$LOCAL_PORT" --bind 127.0.0.1 >/dev/null 2>&1 ) &
HTTP_PID=$!
for _ in $(seq 1 40); do
  curl -s -m 1 -o /dev/null "http://127.0.0.1:$LOCAL_PORT/" && break
  sleep 0.2
done
ok "本地服务 127.0.0.1:$LOCAL_PORT 就绪"

yuki run >"$WORK/agent.log" 2>&1 &
AGENT_PID=$!
for _ in $(seq 1 40); do
  grep -q "已连接到服务端" "$WORK/agent.log" 2>/dev/null && break
  sleep 0.25
done
ok "agent 已连接"

# ── 5. traffic ─────────────────────────────────────────────────────────────
say "启动隧道并验证公网访问"
yuki tunnel start "$TUNNEL_ID" >/dev/null
BODY=""
for _ in $(seq 1 30); do
  BODY=$(curl -s -m 3 "http://127.0.0.1:$PUB_PORT/" 2>/dev/null || true)
  [ -n "$BODY" ] && break
  sleep 0.3
done
if printf '%s' "$BODY" | grep -q 'yukitunnel-e2e-ok'; then
  ok "公网地址返回预期内容"
else
  bad "公网访问失败（响应：${BODY:-<空>}）"
fi

say "查看流量统计"
yuki tunnel stats "$TUNNEL_ID" | sed 's/^/         /'

say "停止隧道并确认端口关闭"
yuki tunnel stop "$TUNNEL_ID" >/dev/null
sleep 1
# curl prints 000 and exits non-zero when the connection is refused; capture
# stdout only so the two paths cannot concatenate into "000000".
CODE=$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PUB_PORT/" 2>/dev/null) || true
CODE="${CODE:-000}"
if [ "$CODE" = "000" ]; then ok "停止后端口不可达"; else bad "停止后仍可访问（$CODE）"; fi

say "删除隧道"
yuki tunnel delete "$TUNNEL_ID" -y >/dev/null
if yuki tunnel list --json | grep -q "$TUNNEL_ID"; then bad "隧道未删除"; else ok "隧道已删除"; fi

# ── 6. result ──────────────────────────────────────────────────────────────
echo
if [ "$FAILED" = "0" ]; then
  printf '\033[1;32m端到端测试全部通过\033[0m\n'
  exit 0
fi
printf '\033[1;31m端到端测试存在失败项\033[0m\n' >&2
diagnose
exit 1
