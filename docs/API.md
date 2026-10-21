# REST API 参考

Base URL：`https://<host>:8443/api/v1`

- 除 `/health`、`/meta`、`/auth/*` 外均需 `Authorization: Bearer <token>`。
- 请求/响应均为 `application/json; charset=utf-8`。
- 错误结构统一：

```json
{ "error": { "code": "PORT_IN_USE", "message": "公网端口 20001 已被占用，请更换", "details": {} } }
```

## 公共

### GET /health

```json
{ "status": "ok", "version": "0.1.0", "database": "ok", "time": "2026-10-20T11:00:00Z" }
```

数据库不可用时返回 `503` 且 `status: "degraded"`。

### GET /meta

```json
{
  "name": "YukiTunnel",
  "version": "0.1.0",
  "domain_binding": true,
  "domain_suffix": "tunnel.example.com",
  "public_host": "203.0.113.10",
  "port_range": { "min": 20000, "max": 20100 },
  "registration": false
}
```

## 认证

### POST /auth/login

```json
{ "username": "alice", "password": "S3cure-pass" }
```

`200`：

```json
{ "token": "yk_...", "expires_at": "2026-10-27T11:00:00Z",
  "user": { "id": "…", "username": "alice", "role": "user", "disabled": false } }
```

| 状态 | code | 含义 |
| --- | --- | --- |
| 400 | `INVALID_CREDENTIALS` | 用户名或密码为空 |
| 401 | `AUTH_FAILED` | 用户名或密码错误 |
| 403 | `USER_DISABLED` | 账号被禁用 |
| 429 | `ACCOUNT_LOCKED` | 失败次数过多，临时锁定 |
| 429 | `RATE_LIMITED` | 同 IP 请求过频 |

### POST /auth/key/challenge

```json
{ "username": "alice" }
```

```json
{ "nonce": "…", "expires_in": 120, "algorithm": "RSASSA-PKCS1-v1_5-SHA256" }
```

### POST /auth/key/login

```json
{ "username": "alice", "nonce": "…", "signature": "<base64>" }
```

签名内容：`"yukitunnel-key-auth:" + nonce`，SHA-256 + RSASSA-PKCS1-v1_5。

nonce 一次性；重复使用返回 `401 CHALLENGE_INVALID`。签名不匹配返回 `401 KEY_MISMATCH`。

### POST /auth/logout

撤销当前令牌，`{ "ok": true }`。

## 当前用户

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/me` | `{ user, domain_binding, domain_suffix }` |
| POST | `/me/password` | `{ current_password, new_password }` |
| GET | `/me/keys` | 自己的公钥 |
| POST | `/me/keys` | `{ name, public_key }` |
| DELETE | `/me/keys/{id}` | 删除公钥 |
| GET | `/me/tokens` | 令牌列表（不含明文） |
| POST | `/me/tokens` | `{ name, ttl_hours }` → `{ token, meta }`，明文只返回一次 |
| DELETE | `/me/tokens/{id}` | 撤销令牌 |

## 节点（客户端视角）

### GET /servers

```json
{
  "servers": [
    { "id": "92abdba5", "name": "hk-1", "region": "香港",
      "public_host": "203.0.113.10", "status": "online", "online": true,
      "latency_ms": 12, "domain": "tunnel.example.com",
      "port_range": [20000, 20100], "max_tunnels": 0, "tunnel_count": 3 }
  ],
  "domain_binding": true,
  "domain_suffix": "tunnel.example.com"
}
```

`latency_ms` 为 0 时服务端会实时 TCP 探测并回写。

## 隧道（用户）

### GET /tunnels

```json
{ "tunnels": [ {
  "id": "tun_ab12cd34", "user_id": "…", "node_id": "92abdba5", "node_name": "hk-1",
  "name": "web demo", "proto": "http", "local_ip": "127.0.0.1", "local_port": 8080,
  "public_port": 20001, "domain": "web-demo-pmtcs5.tunnel.example.com",
  "address": "web-demo-pmtcs5.tunnel.example.com:20001", "public_host": "203.0.113.10",
  "active": true, "enabled": true, "desired_running": true,
  "rx_bytes": 1024, "tx_bytes": 2048, "connections": 3, "active_connections": 1,
  "created_at": "2026-10-20T11:00:00Z"
} ] }
```

### POST /tunnels

```json
{ "node_id": "92abdba5", "proto": "tcp", "local_ip": "127.0.0.1", "local_port": 8080,
  "name": "可选", "public_port": 0, "use_domain": true }
```

- `proto`：`tcp` | `udp` | `http` | `https`（`tls` 视为 `https`）。
- `public_port` 省略或 0 → 在管理员配置的范围内自动分配。
- `use_domain` 省略 → 跟随服务端开关；显式 `true` 但开关关闭 → `409 DOMAIN_DISABLED`。
- 返回 `201 { "tunnel": {...} }`。

| 状态 | code | 场景 |
| --- | --- | --- |
| 400 | `INVALID_PROTOCOL` / `INVALID_LOCAL_IP` / `INVALID_LOCAL_PORT` | 参数不合法 |
| 400 | `PORT_OUT_OF_RANGE` / `PORT_NOT_ALLOWED` | 指定端口越界或不在白名单 |
| 404 | `NODE_NOT_FOUND` | 节点不存在 |
| 409 | `NODE_OFFLINE` | 节点已停用 |
| 409 | `NODE_FULL` | 节点隧道数达上限 |
| 409 | `PORT_IN_USE` | 端口已被占用 |
| 409 | `NO_PORT_AVAILABLE` | 范围内无可用端口 |
| 409 | `DOMAIN_DISABLED` / `DOMAIN_NOT_CONFIGURED` | 域名开关/后缀问题 |

### 其他

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/tunnels/{id}` | 详情 |
| PATCH | `/tunnels/{id}` | 可改 name/node_id/proto/local_ip/local_port/public_port/bandwidth_kbps/max_connections/enabled/desired_running；运行中会自动重启监听 |
| DELETE | `/tunnels/{id}` | 删除并关闭监听 |
| POST | `/tunnels/{id}/start` | 启动；返回 `{ tunnel, address }` |
| POST | `/tunnels/{id}/stop` | 停止；公网端口立即释放 |
| GET | `/tunnels/{id}/stats` | `{ active, rx_bytes, tx_bytes, connections, active_connections }` |

## 管理端

### GET /admin/overview

```json
{ "counts": { "users": 3, "nodes": 2, "nodes_online": 2, "tunnels": 5, "tunnels_active": 4 },
  "settings": { "domain_binding": true, "domain_suffix": "…", "public_host": "…",
                "port_min": 20000, "port_max": 20100 },
  "recent_audit": [ … ], "version": "0.1.0" }
```

### 用户

| 方法 | 路径 | 请求体 |
| --- | --- | --- |
| GET | `/admin/users` | — |
| POST | `/admin/users` | `{ username, display_name, password?, role?, public_key?, key_name? }` |
| PATCH | `/admin/users/{id}` | `{ display_name?, password?, role?, disabled? }` |
| DELETE | `/admin/users/{id}` | — |
| GET/POST | `/admin/users/{id}/keys` | `{ name, public_key }` |
| DELETE | `/admin/users/{id}/keys/{keyID}` | — |

约束：不能删除/禁用自己；不能取消自己最后一个管理员身份。

### 节点

| 方法 | 路径 | 请求体 |
| --- | --- | --- |
| GET | `/admin/nodes` | — |
| POST | `/admin/nodes` | `{ name, region, public_host, control_host?, control_port, max_tunnels, enabled? }` |
| PATCH | `/admin/nodes/{id}` | 同上（部分字段） |
| DELETE | `/admin/nodes/{id}` | 仍有隧道时 `409 NODE_IN_USE` |
| POST | `/admin/nodes/{id}/check` | 返回 `{ node_id, status, latency_ms }` |

### 隧道

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/admin/tunnels?user_id=` | 全部隧道 |
| POST | `/admin/tunnels/{id}/enable` / `disable` | 禁用会立即停止 |
| DELETE | `/admin/tunnels/{id}` | 删除 |

### 设置

`GET /admin/settings` → `{ settings: {...} }`

`PUT /admin/settings`：

```json
{ "domain_binding": true, "domain_suffix": "tunnel.example.com",
  "public_host": "203.0.113.10", "port_min": 20000, "port_max": 20100 }
```

开启域名绑定时必须提供非空后缀，否则 `400 DOMAIN_REQUIRED`。

### 审计与端口

- `GET /admin/audit?limit=100&offset=0` → `{ entries, limit, offset }`
- `GET /admin/ports` → `{ range: {min,max}, nodes: [{ node_id, name, used: [20001, ...] }] }`

## 云 Profile（管理员）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/admin/profiles/index` | 拉取并验签远端索引；所有镜像失败返回 502 + 逐条原因 |
| GET | `/admin/profiles` | 已安装列表 |
| POST | `/admin/profiles/install` | `{id}` 从镜像安装，或 `{document, sha256}` 本地导入 |
| GET | `/admin/profiles/{id}` | 详情 + YAML 原文 |
| POST | `/admin/profiles/{id}/enable|disable` | 启停 |
| DELETE | `/admin/profiles/{id}` | 移除 |
| GET | `/admin/profiles/{id}/credentials` | 凭证列表（值已脱敏） |
| PUT | `/admin/profiles/{id}/credentials` | `{label, fields}`，字段必须在 profile 的 `credential_fields` 内 |
| DELETE | `/admin/profiles/{id}/credentials/{credID}` | 删除凭证 |
| POST | `/admin/profiles/{id}/run` | 执行 ensure/revoke（见下） |
| GET | `/admin/profile-runs?limit=` | 执行记录 |
| GET/PUT | `/admin/profile-settings` | 镜像链、代理、默认来源网段 |

执行请求：

```json
{
  "action": "ensure_port_allowed",
  "node_id": "optional",
  "port_min": 20000,
  "port_max": 20000,
  "protocol": "tcp",
  "credential_label": "default",
  "params": { "region": "cn-hangzhou", "security_group_id": "sg-xxx" },
  "dry_run": false,
  "acknowledge_permissions": true
}
```

非 dry-run 必须 `acknowledge_permissions: true`，否则 `400 PERMISSIONS_NOT_ACKNOWLEDGED`。

错误码：`PROFILE_NOT_FOUND`、`PROFILE_DISABLED`、`PROFILE_INVALID`、`PROFILE_HASH_MISMATCH`、
`PROFILE_EXECUTION_FAILED`、`CREDENTIAL_MISSING`、`UNKNOWN_CREDENTIAL_FIELD`、
`REGISTRY_UNAVAILABLE`、`PERMISSIONS_NOT_ACKNOWLEDGED`、`INVALID_PROXY`。

## 数据面 WebSocket

```
wss://<host>:9443/v1/control
Authorization: Bearer <token>
```

协议见 [ARCHITECTURE.md](ARCHITECTURE.md#23-数据面协议)。
