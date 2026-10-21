# YukiTunnel 架构设计

## 1. 产品需求摘要与范围边界

### 1.1 一句话定位

YukiTunnel 是一个**开箱即用的开源内网穿透平台**：服务端可 Docker 一键部署，也可用桌面 GUI 向导一键部署；管理侧提供 WebUI、桌面管理端、服务端 CLI 三种功能对等的入口；客户端提供全图形化桌面端与功能对等的 CLI。

### 1.2 用户角色

| 角色 | 入口 | 能做什么 |
| --- | --- | --- |
| 服务端管理员 | WebUI / 桌面管理端 / `yuki-server` | 初始化部署、用户、节点、隧道、域名开关、端口范围、审计 |
| 普通用户 | 桌面客户端 / `yuki` CLI | 登录、查看节点延迟、创建/启动/停止/删除隧道、查看流量 |
| 运维 | Docker Compose / systemd | 部署、升级、备份、观测 |

### 1.3 MVP 范围内（已实现）

- 服务端单二进制（内嵌 WebUI），SQLite 持久化，TLS 强制。
- Docker / Docker Compose 一键部署（含 `.env.example`、健康检查、命名卷）。
- 桌面端「一键部署」向导：环境检测 → 端口/目录 → TLS/管理员 → 进度 → 健康检查 → 失败诊断。
- REST API（`/api/v1`）+ WebSocket 数据面（`/v1/control`）。
- TCP / UDP / HTTP / HTTPS 四种隧道；端口自动分配与占用校验。
- 域名绑定全局开关：开启返回「域名+端口」，关闭返回「IP+端口」。
- 多节点，节点在线状态 + 延迟（TCP 探测）。
- 密码（Argon2id）与 RSA / SSH Key（挑战-响应签名）双认证。
- 审计日志、登录失败锁定、会话令牌、API 令牌。
- 客户端 GUI（Tauri + React，复用 WebUI 组件）与 `yuki` CLI 功能对等。
- 自动重连、托盘常驻、流量统计、人类可读错误。

### 1.4 明确不在 MVP 内（预留扩展点）

- 自助注册、计费套餐、配额中心（数据表已预留 `bandwidth_kbps` / `max_connections`）。
- 多租户组织隔离（当前是「管理员 / 用户」两级）。
- P2P 打洞（当前是标准的服务端中转模型）。
- 集群化调度多个节点共享同一数据库（当前每节点一套服务端，节点表记录入口）。
- 原生移动端。

### 1.5 关键约束

- 普通用户**永远不需要**接触 frp/TOML/YAML。
- 所有对外流量必须 TLS；HTTP 隧道由服务端反向代理，HTTPS 隧道为 TLS 透传（端到端加密）。
- 密码只存 Argon2id 哈希；令牌只存 SHA-256；私钥永不离开客户端。

---

## 2. 系统架构

### 2.1 部署拓扑

```
                    ┌──────────────────────────────────────────────┐
   浏览器 ──HTTPS──▶ │  yuki-server  (单二进制, 内嵌 WebUI)          │
                    │                                              │
   客户端 GUI/CLI ──▶│  :8443  HTTPS  REST /api/v1 + WebUI 静态资源  │
        │           │  :9443  WSS    /v1/control  (数据面)         │
        │           │                                              │
        │           │  ┌────────────┐  ┌─────────────┐             │
        │           │  │ store      │  │ tunnel.Hub  │             │
        │           │  │ (SQLite)   │  │ 会话/监听器 │             │
        │           │  └────────────┘  └──────┬──────┘             │
        │           │  ┌────────────┐         │                    │
        │           │  │ auth       │         │ 公网端口 20000+    │
        │           │  │ Argon2/Key │         ▼                    │
        │           │  └────────────┘   ┌──────────────┐           │
        │           └───────────────────│ 公网访问者    │           │
        │  WSS + yamux 多路复用           └──────────────┘           │
        ▼                               
   ┌──────────────────────┐   每条隧道一个 yamux stream
   │ yuki (客户端 agent)   │──────────────▶ 内网服务 127.0.0.1:8080
   │ 自动重连 / 本地转发    │
   └──────────────────────┘
```

### 2.2 组件职责

| 组件 | 语言/框架 | 职责 |
| --- | --- | --- |
| `yuki-server` | Go | API、WebUI 托管、认证、隧道数据面、SQLite、TLS、审计 |
| `yuki-server`（CLI 模式） | Go + Cobra | `init/start/stop/status/doctor/user/node/tunnel/domain/port-range/audit/config/token` |
| `yuki` | Go + Cobra | 客户端 CLI：登录、节点、隧道、`run` 常驻转发 |
| WebUI | React 19 + TS + Vite + TanStack Query | 管理员与用户网页控制台 |
| 桌面端 | Tauri 2 + 同一套 React 组件 | 客户端 GUI + 服务端管理 + 一键部署向导 |
| 数据面 | gorilla/websocket + hashicorp/yamux | 单条 WSS 承载任意数量隧道流 |

### 2.3 数据面协议

1. 客户端以 `Authorization: Bearer <token>` 连接 `wss://host:9443/v1/control`。
2. WebSocket 之上跑 yamux；客户端打开的第一条 stream 是 **控制流**（逐行 JSON）。
3. `hello → hello_ok`，随后 `register` 携带要启动的隧道列表；服务端为每条隧道在**服务端**绑定公网监听器（TCP/UDP/HTTP），并回 `registered`（含最终公网地址）。
4. 每个公网连接 → 服务端 `OpenStream()` → 首行写入 `StreamHeader{tunnel_id, local_ip, local_port, proto}` → 其余字节原样双向转发。
5. UDP 在 stream 内以 2 字节长度前缀分帧，按来源地址维护会话，60s 空闲回收。
6. 服务端每 5s 通过控制流下发 `stats`，客户端据此更新 UI 流量。
7. 断线后客户端指数退避重连（1s→30s）；服务端在会话建立时按 `desired_running` 自动恢复监听器。

> 关键设计：**监听器在服务端**，客户端只负责转发。这样「启动/停止」由 API 直接控制，不依赖客户端在线；客户端离线时隧道标记为待恢复，重连即生效。

### 2.4 技术选型理由

| 选择 | 理由 |
| --- | --- |
| Go 单二进制 | 交叉编译 Windows/macOS/Linux；无运行时依赖；`embed` 内嵌 WebUI；运维只需一个文件 |
| SQLite（modernc 纯 Go） | 免 CGO，交叉编译简单；单机管理规模足够；WAL 并发读 |
| REST + WebSocket | 比 gRPC 更易被浏览器/脚本/桌面端复用；`curl` 即可排障；OpenAPI 友好 |
| yamux over WSS | 单连接多路复用，穿透 80/443 友好；比自研帧协议可靠 |
| React + Vite | 生态成熟、构建快；WebUI 与桌面端共享组件与 API 客户端 |
| Tauri 2 | 包体远小于 Electron；Rust 侧可直接管理子进程与托盘；复用 WebView |
| Cobra | 与 `kubectl`/`gh` 一致的子命令体验，自动补全 |

---

## 3. 服务端模块、数据库与 API

### 3.1 Go 包结构

```
cmd/yuki-server        服务端入口（serve + 管理 CLI）
cmd/yuki               客户端入口
internal/config        配置加载：flag > env > yaml > 默认
internal/store         SQLite：用户/密钥/令牌/节点/隧道/设置/审计/登录尝试
internal/auth          Argon2id、令牌、RSA 挑战签名、OpenSSH 私钥解析
internal/tlsutil       自签名证书生成、指纹、客户端 pinning
internal/tunnel        数据面：Hub、会话、TCP/UDP/HTTP 代理、限速
internal/wsnet         WebSocket ↔ net.Conn 适配
internal/server        Service、HTTP 路由、API 处理器、内嵌部署
internal/servercli     yuki-server 的本地/远程双后端
internal/firewall      ufw / firewalld / nftables 适配（检测、放行、对账）
internal/client        客户端 agent（重连、转发）
internal/cli           yuki 命令树
internal/cliutil       客户端配置、API 客户端、输出格式
```

### 3.2 数据库表

| 表 | 关键列 | 说明 |
| --- | --- | --- |
| `users` | id, username(unique), password_hash, role, disabled | 管理员预创建，无注册 |
| `user_keys` | user_id, fingerprint(unique), public_key | SSH/RSA 公钥 |
| `api_tokens` | user_id, token_hash(unique), scope, expires_at, revoked_at | 会话与 API 令牌，只存哈希 |
| `nodes` | name(unique), public_host, control_host/port, enabled, status, latency_ms, max_tunnels | 公网入口 |
| `tunnels` | user_id, node_id, proto, local_ip/port, public_port, domain, enabled, **desired_running**, bandwidth_kbps, max_connections | 唯一约束 `(node_id, public_port)` |
| `settings` | key, value | 运行时开关：domain_binding / domain_suffix / public_host / port_min / port_max / profile_mirrors / profile_proxy |
| `cloud_profiles` | id, vendor, version, source, sha256, document, enabled | 已验签安装的 profile |
| `profile_credentials` | profile_id, label, fields_json | AES-256-GCM 密文（`v1:` 前缀） |
| `profile_runs` | profile_id, action, port_min/max, protocol, status, duration_ms, actor | 执行记录 |
| `audit_logs` | actor, action, target, detail, ip, created_at | 追加写 |
| `login_attempts` | username, ip, success, created_at | 暴力破解防护 |
| `schema_migrations` | version, applied_at | 迁移版本 |

### 3.3 API 一览（`/api/v1`）

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/health` | 公开 | 健康检查（DB ping） |
| GET | `/meta` | 公开 | 版本、域名开关、端口范围 |
| POST | `/auth/login` | 公开 | 密码登录 → 令牌 |
| POST | `/auth/key/challenge` | 公开 | 获取一次性 nonce |
| POST | `/auth/key/login` | 公开 | 签名换令牌 |
| POST | `/auth/logout` | 登录 | 撤销当前令牌 |
| GET | `/me` | 登录 | 当前用户 + 域名开关 |
| POST | `/me/password` | 登录 | 修改密码 |
| GET/POST/DELETE | `/me/keys`, `/me/keys/{id}` | 登录 | 自己的公钥 |
| GET/POST/DELETE | `/me/tokens`, `/me/tokens/{id}` | 登录 | API 令牌 |
| GET | `/servers` | 登录 | 节点列表（含实时延迟） |
| GET/POST | `/tunnels` | 登录 | 列表 / 创建 |
| GET/PATCH/DELETE | `/tunnels/{id}` | 所有者 | 详情 / 编辑 / 删除 |
| POST | `/tunnels/{id}/start` `/stop` | 所有者 | 启停 |
| GET | `/tunnels/{id}/stats` | 所有者 | 流量与连接数 |
| GET | `/admin/overview` | 管理员 | 汇总 |
| GET/POST/PATCH/DELETE | `/admin/users...` | 管理员 | 用户与密钥 |
| GET/POST/PATCH/DELETE | `/admin/nodes...` | 管理员 | 节点；`/check` 探测 |
| GET/DELETE/POST | `/admin/tunnels...` | 管理员 | 全部隧道；`enable`/`disable` |
| GET/PUT | `/admin/settings` | 管理员 | 域名开关、后缀、端口范围 |
| GET | `/admin/audit` | 管理员 | 分页审计 |
| GET | `/admin/ports` | 管理员 | 端口占用 |
| WS | `/v1/control` | Bearer | 数据面 |

错误统一为：

```json
{ "error": { "code": "PORT_IN_USE", "message": "公网端口 20001 已被占用，请更换", "details": {} } }
```

稳定错误码：`AUTH_FAILED`、`ACCOUNT_LOCKED`、`USER_DISABLED`、`TOKEN_EXPIRED`、`CHALLENGE_INVALID`、`KEY_MISMATCH`、`NODE_OFFLINE`、`NODE_FULL`、`PORT_IN_USE`、`PORT_OUT_OF_RANGE`、`DOMAIN_DISABLED`、`DOMAIN_NOT_CONFIGURED`、`NO_PORT_AVAILABLE`、`TUNNEL_NOT_FOUND`、`FORBIDDEN`。

---

## 4. Docker Compose 一键部署

```bash
cp .env.example .env      # 改 YUKI_PUBLIC_HOST / 管理员密码 / 端口段
docker compose up -d
docker compose logs -f yuki-server
```

要点：

- `deploy/docker-entrypoint.sh` 幂等：首次 `yuki-server init`，之后 `exec yuki-server serve`。
- 命名卷 `yuki-data:/data` 保存数据库、TLS 证书、日志；升级容器不丢数据。
- 端口：`8443`（API/WebUI）、`9443`（控制面）、`20000-20100`（隧道公网端口，与 `YUKI_PORT_MIN/MAX` 必须一致）。
- 健康检查直接打 `/api/v1/health`；`restart: unless-stopped` + 日志轮转。
- 生产建议在 8443 前放 Nginx/Caddy 终止 443，并把 `YUKI_PUBLIC_HOST` 设为公网域名。

---

## 5. 服务端 GUI 一键部署（Tauri）

### 5.1 向导步骤

| 步骤 | 内容 | 校验 |
| --- | --- | --- |
| 1 环境检测 | 内置 `yuki-server` 是否存在、版本、数据目录可写、8443/9443 是否空闲、Docker 是否可用、是否已有部署 | 致命项不通过则不能下一步 |
| 2 网络与端口 | API 端口、控制端口、公网入口、数据目录、端口段 | 端口合法且互不冲突、端口段合法 |
| 3 安全与管理员 | 管理员账号/密码（留空随机）、TLS（自签名/已有证书）、域名开关+后缀 | 密码 ≥8 或留空、证书两项齐全、开启域名必须填后缀 |
| 4 确认与进度 | 配置摘要 + 实时进度列表 + 服务端日志尾部 | — |
| 5 完成 | WebUI 地址、管理员、TLS 指纹、打开/停止/重新检测 | — |

### 5.2 Rust 侧实现（`desktop/src-tauri/src/deploy.rs`）

- `env_check`：`std::net::TcpListener::bind` 探测端口，写入探针文件验证目录可写，`yuki-server version` 取版本。
- `deploy_server`：
  1. 再次校验端口 → 发 `deploy://progress{step:"validate"}`；
  2. 执行 `yuki-server init --json`（幂等，已有配置不覆盖账号）；
  3. 以分离子进程启动 `yuki-server serve`，stdout/stderr 写入 `<data>/logs/desktop-server.log`；
  4. 每 500ms 轮询 `https://127.0.0.1:<port>/api/v1/health`，同时把日志增量通过 `deploy://log` 推给前端；
  5. 检测子进程提前退出（`try_wait`），失败时回传日志尾部，前端展示「失败诊断」。
- `server_status` / `stop_server` / `server_logs` 提供后续运维。
- 进度事件结构：`{step, status, message, detail, percent}`，前端按 step 合并渲染。

---

## 6. 服务端 CLI（`yuki-server`）与功能映射

| 功能 | WebUI | 桌面管理端 | `yuki-server` |
| --- | --- | --- | --- |
| 初始化 | —（部署时完成） | 一键部署向导 | `init` |
| 启动/停止/状态 | — | 部署页 | `start` / `stop` / `restart` / `status` |
| 环境与健康 | 概览卡片 | 部署页环境检测 | `doctor` |
| 用户管理 | 用户管理页 | 同左（复用组件） | `user create/list/disable/enable/delete/passwd`、`user key add/list/remove` |
| 节点管理 | 节点管理页 | 同左 | `node create/list/enable/disable/delete/check` |
| 隧道管理 | 隧道管理页 | 同左 | `tunnel list/enable/disable/delete` |
| 域名绑定 | 域名与端口页 | 同左 | `domain binding on --suffix / off / show` |
| 端口范围 | 域名与端口页 | 同左 | `port-range set --min --max / list` |
| 审计日志 | 审计页 | 同左 | `audit list --limit --action` |
| 配置查看 | 服务端信息卡 | 同左 | `config show / config set` |
| API 令牌 | 账号页 | 同左 | `token create`（远程模式） |

CLI 支持两种模式：

- **本地模式（默认）**：直接读写 `<data-dir>/yukitunnel.db`，用于 `init` 与无 HTTP 服务的救援场景。
- **远程模式**：`--server https://host:8443 --token <管理员令牌>`（或 `--username/--password`），走同一套 REST API。
- 所有命令支持 `--json`；错误走 stderr 且退出码非 0。

---

## 7. WebUI 与桌面管理端页面结构（共用组件）

```
web/src
├── lib/api.ts            REST 客户端（含错误码 → 人类文案）
├── lib/types.ts          与 Go JSON 结构一一对应
├── lib/format.ts         humanBytes / timeAgo / copyText
├── state/auth.tsx        会话、令牌持久化、RSA 登录（WebCrypto 签名）
├── state/toast.tsx       全局提示
├── components/ui.tsx     Card / Badge / Button / Field / Modal / Switch / EmptyState
├── components/CreateTunnelModal.tsx   节点选择（显示延迟）+ 协议 + 内网地址
└── pages
    ├── Login.tsx  Dashboard.tsx  Servers.tsx  Tunnels.tsx
    ├── TunnelDetail.tsx  Account.tsx  Layout.tsx
    └── admin/Overview Users Nodes Tunnels Settings Audit
```

桌面端通过 Vite alias `@web -> ../web/src` **直接复用**：

- `lib/api.ts`、`lib/types.ts`、`lib/format.ts`
- `state/auth.tsx`、`state/toast.tsx`
- `components/ui.tsx`、`components/CreateTunnelModal.tsx`
- `styles.css`

桌面端独有：`pages/Connect.tsx`（首次向导）、`pages/ClientHome.tsx`（含 `AgentControl`）、`pages/Deploy.tsx`（部署向导）、`pages/Settings.tsx`、`lib/tauri.ts`。

差异处理方式：通过 `AuthProvider` 的 `initialServerUrl` / `setServerUrl` 让同一套页面既能同源（WebUI）也能跨源（桌面端连接任意服务端）。

---

## 8. 客户端 GUI 页面结构与关键交互

```
连接向导 (Connect)
  ├─ 服务端地址（唯一必填）
  ├─ 用户名
  ├─ 密码 / RSA 私钥（文件选择或粘贴）
  ├─ 信任自签名证书
  └─ 登录 → 保存会话到本地

主界面 (ClientHome)
  ├─ 顶部：服务端、用户、刷新、+ 新建隧道
  ├─ 运行状态卡（AgentControl）：启动穿透 / 停止穿透 / 运行日志 / PID
  ├─ 可用节点：名称、区域、公网入口、延迟、在线状态
  ├─ 账号卡：用户名、角色、连接设置、退出
  └─ 隧道表：名称、节点、内网地址、公网地址(+复制)、流量、状态、启停/日志/编辑

创建隧道 (CreateTunnelModal，WebUI 与桌面共用)
  节点（卡片显示延迟，离线不可选）→ 协议（TCP/UDP/HTTP/HTTPS）
  → 内网 IP → 内网端口 → 名称(可选) → 公网端口(可选/自动)
  → 地址形式（域名/IP，由服务端开关决定）→ 创建
  → 成功页展示公网地址 + 复制 + 立即启动

设置 (Settings)：服务端地址、配置目录、二进制位置、防火墙提示
部署 (Deploy)：见第 5 节
```

交互细节：

- 隧道状态每 3s 轮询，节点延迟每 15s 刷新。
- 「启动穿透」拉起 `yuki run` 子进程；关闭窗口 → 托盘常驻；异常退出通过 `agent://state` 提示。
- 所有错误来自后端 `message` 字段，天然是人类可读中文（如「公网端口 20001 已被占用，请更换」）。

---

## 9. 客户端 CLI（`yuki`）

```text
yuki login    --server URL [--username U] [--password P | --key PATH] [--insecure]
yuki logout
yuki whoami
yuki server list
yuki tunnel create --node ID --proto tcp|udp|http|https --local-ip IP --local-port N
                   [--public-port N] [--name NAME] [--domain|--no-domain]
yuki tunnel list [-w]
yuki tunnel start <id>
yuki tunnel stop <id>
yuki tunnel delete <id> [-y]
yuki tunnel stats <id>
yuki run [--all] [--tunnel ID ...] [--headless]
yuki status
yuki config show | config set <k> <v> | config path
yuki doctor
```

- 全部命令支持 `--json`；`yuki run --json` 逐行输出统计事件，适合 systemd/journald。
- 配置文件 `~/.config/yukitunnel/client.yaml`（0600），含服务端、用户名、令牌、密钥路径、自签名信任。
- `--server/--token/--key/--insecure` 可临时覆盖配置文件，便于 CI。

---

## 10. 认证与安全

### 10.1 密码

- Argon2id：`t=2, m=64MiB, p=2, keyLen=32, salt=16B`，PHC 字符串存储。
- 登录失败按 `(username, ip)` 计数，窗口内超过 `max_failed_logins`（默认 10）锁定 `lockout_minutes`（默认 15）。
- 登录接口另有 IP 级 30 次/分钟节流。
- 会话令牌：32 字节随机，`yk_` 前缀，仅存 SHA-256；默认 7 天；登出即撤销。

### 10.2 RSA / SSH Key（挑战-响应）

```
客户端                         服务端
  │  POST /auth/key/challenge   │
  │  {username}                 │ 生成 32B nonce，2 分钟有效、一次性
  │◀──────────── {nonce} ───────│
  │ RSA-SHA256("yukitunnel-key-auth:"+nonce)
  │  POST /auth/key/login       │
  │  {username, nonce, signature}
  │                             │ 遍历该用户公钥逐一验签
  │                             │ 命中 → 签发会话令牌 + 审计
  │◀──────────── {token} ───────│
```

- 服务端从不接触私钥；公钥支持 `ssh-rsa` / `ssh-ed25519`（Ed25519 可登记，挑战签名当前用 RSA）。
- 私钥格式兼容 PKCS#1、PKCS#8、OpenSSH（`ssh-keygen` 默认），受密码保护的私钥会提示先解密。
- WebUI 的 Key 登录用 WebCrypto 在浏览器内签名，私钥不上传。

### 10.3 传输与数据面

- API 与控制面均强制 TLS 1.2+；无证书时自动生成 10 年自签名证书并输出 SHA-256 指纹。
- 客户端可用 `--insecure`（自签名）或指纹固定（`tlsutil.ServerTLSConfigForClient`）。
- 数据面 WebSocket 需 Bearer 令牌；Hub 内再次校验隧道归属与 `enabled`。
- 安全响应头：HSTS、X-Frame-Options、nosniff、Referrer-Policy。

### 10.4 授权与审计

- 两级角色：`admin` / `user`；管理端点统一中间件校验。
- 用户只能操作自己的隧道；管理员可管理全部。
- 关键动作（登录、登录失败、Key 登录、用户/节点/隧道增删改、设置变更、令牌）全部写审计，含操作者、对象、IP、时间。

### 10.5 可选增强（已预留字段/入口）

带宽限制（`bandwidth_kbps`，令牌桶已实现）、连接数上限（`max_connections`）、端口白名单（`ports.allowlist`）、IP 白名单（后续在节点维度加）。

---

## 11. 域名绑定开关与节点延迟

### 11.1 域名绑定

- 存储：`settings.domain_binding` + `settings.domain_suffix`，与 `config.yaml` 互为默认值，运行时以 DB 为准（改完立即生效，无需重启）。
- 创建隧道时：
  - `useDomain = binding`（用户显式请求域名但开关关闭 → `409 DOMAIN_DISABLED`）；
  - 域名 = `<slug(name)>-<rand6>.<suffix>`，写入 `tunnels.domain`；
  - 展示地址 = `domain:public_port`。
- 关闭后：新隧道 `domain=""`，展示 `node.public_host:public_port`（`public_host` 设置可覆盖）。
- 已有隧道保持原地址，用户编辑隧道时会按当前开关重新计算。
- 客户端与 WebUI 均只读取服务端返回的 `address`，不自行拼接，保证「开关」是唯一事实来源。
- HTTP 隧道按「每隧道独立端口」路由，因此无需依赖 Host 头，但原始 Host 会透传给内网服务。

### 11.2 节点延迟

- 节点表存最近一次探测结果 `latency_ms` / `status` / `last_seen_at`。
- `GET /servers` 时，若 `latency_ms == 0` 则实时 `net.DialTimeout(node.control_host:port, 2s)` 测 RTT 并回写；管理员「检测」按钮走 `POST /admin/nodes/{id}/check` 强制探测。
- 前端每 15s 刷新；延迟分档着色：≤80ms 绿、≤200ms 黄、其余红。
- 节点离线（TCP 不可达）→ `status=offline`，创建隧道时返回 `NODE_OFFLINE`，启动时拒绝。

---

## 12. 项目目录结构

```
YukiTunnel/
├── cmd/
│   ├── yuki/                 # 客户端 CLI 入口
│   └── yuki-server/          # 服务端入口（serve + 管理 CLI）
├── internal/
│   ├── apierr/               # 统一错误码
│   ├── auth/                 # Argon2id / 令牌 / RSA 挑战 / OpenSSH 解析
│   ├── cli/                  # yuki 命令树
│   ├── client/               # 客户端 agent（重连、TCP/UDP/HTTP 转发）
│   ├── cliutil/              # 客户端配置与 API 客户端
│   ├── config/               # 服务端配置
│   ├── ids/  version/
│   ├── server/               # Service、HTTP 路由、API、内嵌部署
│   ├── servercli/            # yuki-server 命令树 + 本地/远程后端
│   ├── store/                # SQLite 模型与查询
│   ├── tlsutil/              # 自签名、指纹、pinning
│   ├── tunnel/               # 数据面 Hub 与协议
│   └── wsnet/                # WebSocket ↔ net.Conn
├── web/                      # WebUI（React + Vite），构建产物嵌入服务端
├── desktop/                  # Tauri 桌面端（客户端 GUI + 管理端 + 部署向导）
│   ├── src/                  # 复用 @web 组件 + 桌面独有页面
│   └── src-tauri/            # Rust：sidecar、部署、agent 守护、托盘
├── deploy/                   # 容器入口脚本
├── docker-compose.yml  docker-compose.dev.yml  Dockerfile  .env.example
├── scripts/                  # build.sh / prepare-sidecars.sh
├── docs/                     # 架构、API、CLI、部署、截图说明
└── README.md
```

---

## 13. 分阶段开发计划与验收

| 阶段 | 内容 | 验收 |
| --- | --- | --- |
| **P0 骨架（本次交付）** | 配置/存储/认证/数据面/API/CLI/WebUI/桌面壳/Docker | `go test ./...` 通过；`docker compose config` 合法；CLI 全流程可跑通 |
| P1 打磨 | 令牌桶限速 UI、IP 白名单、节点自动注册、WebSocket 日志流 | 带宽限制生效；节点无需手填 |
| P2 运维 | Prometheus 指标、备份/恢复命令、systemd unit、升级脚本 | `yuki-server backup` 可恢复 |
| P3 生态 | 套餐/配额、OIDC、多语言、自动更新 | 第三方 IdP 登录 |
| P4 规模化 | 多节点共享控制面、调度器 | 单控制面管理 N 个入口 |

### 验收对照

| 验收项 | 实现位置 | 自动化验证 |
| --- | --- | --- |
| `docker compose up -d` 启动服务端 + WebUI | `docker-compose.yml`、`Dockerfile`、`deploy/docker-entrypoint.sh` | `docker compose config`；镜像 HEALTHCHECK |
| 桌面 GUI 一键部署 | `desktop/src-tauri/src/deploy.rs`、`desktop/src/pages/Deploy.tsx` | 向导 5 步 + 健康检查 + 失败诊断 |
| 管理员三端功能一致 | WebUI 页面 / 桌面复用组件 / `internal/servercli` | `TestAdminCreatesUserAndTunnel`、CLI 冒烟 |
| 创建用户/节点/域名开关 | `/admin/*` API | `TestDomainBindingToggle`、`TestAuditTrail` |
| 客户端首次配置 + 密码/Key 登录 | `Connect.tsx`、`yuki login`、`/auth/key/*` | `TestKeyAuthFlow` |
| “+” 选节点看延迟、填内网地址创建 | `CreateTunnelModal.tsx` | 组件复用 + API 测试 |
| 返回正确形式公网地址 | `Service.PublicAddress` | `TestAdminCreatesUserAndTunnel`、`TestDomainBindingToggle` |
| 启动生效、停止失效 | `Hub.StartTunnelFor` / `Hub.StopTunnel` | `internal/tunnel` 集成测试 + 手工 curl |
| CLI 核心流程 + `--json` | `internal/cli` | 冒烟脚本 `scripts/smoke.sh` |
