#!/usr/bin/env bash
# End-to-end smoke test: init a server, create a user, login from the client
# CLI, create/start a tunnel and verify real traffic, then clean up.
#
#   bash scripts/smoke.sh
#
# Requires: go, curl, python3 (for the local HTTP server).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
pick_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
API_PORT="$(pick_port)"
CTL_PORT="$(pick_port)"
LOCAL_PORT="$(pick_port)"
PUB_PORT="$(pick_port)"
SERVER_PID=""
AGENT_PID=""
HTTP_PID=""

cleanup() {
  set +e
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  [ -n "$HTTP_PID" ] && kill "$HTTP_PID" 2>/dev/null
  wait 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

say() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

say "Building binaries"
mkdir -p "$TMP/bin"
go build -o "$TMP/bin/yuki-server" ./cmd/yuki-server
go build -o "$TMP/bin/yuki" ./cmd/yuki
export PATH="$TMP/bin:$PATH"
export YUKI_CLIENT_HOME="$TMP/client"

say "Initialising server"
yuki-server init --data-dir "$TMP/data" \
  --api-addr "127.0.0.1:$API_PORT" --control-addr "127.0.0.1:$CTL_PORT" \
  --admin-password admin12345 --port-min "$PUB_PORT" --port-max "$((PUB_PORT + 5))" \
  --public-host 127.0.0.1 >/dev/null

yuki-server start --data-dir "$TMP/data" >/dev/null
SERVER_PID="$(cat "$TMP/data/yuki-server.pid")"
for _ in $(seq 1 40); do
  curl -sk "https://127.0.0.1:$API_PORT/api/v1/health" >/dev/null 2>&1 && break
  sleep 0.25
done
curl -sk "https://127.0.0.1:$API_PORT/api/v1/health" | grep -q '"status":"ok"'
echo "    health OK"

say "Creating user"
yuki-server user create alice --password alice12345 --data-dir "$TMP/data" >/dev/null
yuki-server user list --data-dir "$TMP/data" | grep -q alice
echo "    user OK"

say "Client login"
yuki login --server "https://127.0.0.1:$API_PORT" --username alice --password alice12345 --insecure >/dev/null
yuki server list --json | grep -q '"online": true'
echo "    login + node list OK"

say "Creating tunnel"
CREATE_JSON="$(yuki tunnel create --proto tcp --local-ip 127.0.0.1 --local-port "$LOCAL_PORT" --no-domain --json)"
TUNNEL_ID="$(printf '%s' "$CREATE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["tunnel"]["id"])')"
ADDRESS="$(printf '%s' "$CREATE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["tunnel"]["address"])')"
echo "    tunnel $TUNNEL_ID -> $ADDRESS"

say "Starting local service and agent"
( cd "$TMP" && exec python3 -m http.server "$LOCAL_PORT" --bind 127.0.0.1 >/dev/null 2>&1 ) &
HTTP_PID=$!
for _ in $(seq 1 40); do
  curl -s -m 1 -o /dev/null "http://127.0.0.1:$LOCAL_PORT/" && break
  sleep 0.2
done
yuki run >"$TMP/agent.log" 2>&1 &
AGENT_PID=$!
for _ in $(seq 1 40); do
  grep -q "已连接到服务端" "$TMP/agent.log" 2>/dev/null && break
  sleep 0.25
done
sleep 0.5

say "Starting tunnel and checking traffic"
yuki tunnel start "$TUNNEL_ID" >/dev/null
sleep 1.5
BODY="$(curl -s -m 5 "http://127.0.0.1:$PUB_PORT/")"
printf '%s' "$BODY" | grep -q "Directory listing" || { echo "FAILED: no response through tunnel"; cat "$TMP/agent.log"; exit 1; }
echo "    traffic OK"

say "Stopping tunnel"
yuki tunnel stop "$TUNNEL_ID" >/dev/null
sleep 1
if curl -s -m 2 -o /dev/null "http://127.0.0.1:$PUB_PORT/"; then
  echo "FAILED: port still reachable after stop"; exit 1
fi
echo "    stop OK"

say "Deleting tunnel"
yuki tunnel delete "$TUNNEL_ID" -y >/dev/null
yuki tunnel list --json | grep -q "$TUNNEL_ID" && { echo "FAILED: tunnel still listed"; exit 1; }
echo "    delete OK"

say "Server CLI: domain + port range + audit"
yuki-server domain binding on --suffix smoke.local --data-dir "$TMP/data" | grep -q "已开启"
yuki-server domain binding off --data-dir "$TMP/data" | grep -q "已关闭"
yuki-server port-range set --min 28600 --max 28610 --data-dir "$TMP/data" | grep -q "28600-28610"
yuki-server audit list --limit 5 --data-dir "$TMP/data" | grep -q tunnel.created
echo "    server CLI OK"

say "Doctor"
yuki-server doctor --data-dir "$TMP/data" >/dev/null && echo "    doctor OK"

say "All smoke tests passed ✅"
