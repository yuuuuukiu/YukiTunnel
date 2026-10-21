# YukiTunnel 完整设计方案

> 本文是交付说明的主文档，按需求顺序组织；实现细节分散在
> [ARCHITECTURE.md](ARCHITECTURE.md)、[API.md](API.md)、[CLI.md](CLI.md)、
> [DEPLOYMENT.md](DEPLOYMENT.md)。

## 1. 产品需求摘要与范围边界

**定位**：开源、开箱即用的内网穿透平台。服务端 Docker 或桌面 GUI 一键部署；管理侧 WebUI / 桌面端 / CLI 三端对等；客户端全 GUI + 对等 CLI；普通用户不接触任何配置文件。

**范围内（已实现）**

- 服务端单二进制 + 内嵌 WebUI + SQLite + 强制 TLS
- Docker Compose 一键部署（`.env.example`、命名卷、健康检查）
- 桌面端一键部署向导（环境检测 / 端口 / TLS / 管理员 / 进度 / 健康检查 / 失败诊断）
- 服务端 CLI：init / start / stop / restart / status / doctor / user / node / tunnel / domain / port-range / audit / config / token，本地与远程双模式，全命令 `--json`
- WebUI 与桌面管理端：用户、节点、隧道、域名开关、端口范围、审计，功能对等且共用组件
- 客户端 GUI：首次向导、节点延迟、`+` 建隧道、启停、复制地址、编辑、删除、日志、托盘、自动重连
- 客户端 CLI：login/logout/server list/tunnel create|list|start|stop|delete|stats/run/config/doctor/status，`--json`
- TCP / UDP / HTTP / HTTPS 隧道；多节点；域名绑定开关；流量统计；审计

**范围外（预留）**：自助注册、套餐计费、多租户组织、P2P 打洞、多入口共享控制面、移动端。

**边界原则**：服务端是唯一事实来源（地址、开关、权限），客户端只呈现服务端返回的结果。

## 2. 系统架构

```
浏览器 ──HTTPS:8443──▶ ┌──────────────────────────────┐
                       │ yuki-server (Go 单二进制)      │
客户端 GUI/CLI ───────▶│  /api/v1  REST + WebUI        │
   │  WSS:9443        │  /v1/control  yamux 数据面     │
   │                  │  SQLite · auth · tunnel.Hub    │
   ▼                  └───────────────┬──────────────┘
yuki agent ──▶ 内网服务         公网端口 20000+ ──▶ 公网访问者
```

数据面：客户端 WSS 连接 → yamux → 首条流为控制流（hello/register/stats/stop）→ 每条公网连接一个流，首行 `StreamHeader{tunnel_id, local_ip, local_port, proto}` → 字节双向转发；UDP 用 2 字节长度前缀分帧。

关键取舍：**公网监听器在服务端**，客户端只转发。因此启停由 API 直接生效，客户端离线时隧道标记待恢复，重连自动拉起。

选型：Go（交叉编译、单文件、embed）、SQLite/modernc（免 CGO）、REST+WSS（浏览器与脚本通用）、yamux（单连接多路复用）、React+Vite（两端共享）、Tauri 2（小体积 + 子进程/托盘）、Cobra（一致的 CLI 体验）。

## 3. 服务端模块、数据库与 API

模块：`config` `store` `auth` `tlsutil` `tunnel` `wsnet` `server` `servercli` `client` `cli` `cliutil`。

表：`users` `user_keys` `api_tokens` `nodes` `tunnels` `settings` `audit_logs` `login_attempts` `schema_migrations`。

完整字段、索引与 API 清单见 [ARCHITECTURE.md §3](ARCHITECTURE.md#3-服务端模块数据库与-api) 与 [API.md](API.md)。

## 4. Docker Compose

```bash
cp .env.example .env && docker compose up -d
```

入口脚本幂等：首次 `init`（生成配置/TLS/管理员），之后 `exec serve`；数据在 `yuki-data` 卷；健康检查 `/api/v1/health`。详见 [DEPLOYMENT.md](DEPLOYMENT.md)。

## 5. 服务端 GUI 一键部署

五步向导 + 实时进度事件：

1. 环境检测：二进制存在与版本、目录可写、8443/9443 空闲、Docker 可选、已有部署
2. 网络与端口：API/控制端口、公网入口、数据目录、端口段
3. 安全与管理员：管理员账号密码（留空随机）、TLS（自签名/已有证书）、域名开关
4. 确认与进度：`validate → init → tls → admin → start → health → finish`，每步 running/done/error，日志尾部实时推送
5. 完成：WebUI 地址、管理员、TLS 指纹、打开/停止/重新检测

失败诊断：进程提前退出检测 + 日志尾部回传 + 常见修复提示。

## 6. 服务端 CLI 与三端映射

| 功能 | WebUI | 桌面端 | `yuki-server` |
| --- | --- | --- | --- |
| 初始化 | 部署向导 | 一键部署 | `init` |
| 生命周期 | — | 部署页 | `start/stop/restart/status` |
| 健康检查 | 概览 | 环境检测 | `doctor` |
| 用户/密钥 | 用户管理 | 同左 | `user ...` / `user key ...` |
| 节点 | 节点管理 | 同左 | `node ...` |
| 隧道 | 隧道管理 | 同左 | `tunnel ...` |
| 域名开关 | 域名与端口 | 同左 | `domain binding on|off` |
| 端口范围 | 域名与端口 | 同左 | `port-range set/list` |
| 审计 | 审计页 | 同左 | `audit list` |
| 配置 | 服务端信息 | 同左 | `config show/set` |

本地模式直连 SQLite，远程模式走同一套 REST；全部支持 `--json`。详见 [CLI.md](CLI.md)。

## 7. 页面结构与组件共用

WebUI：`Login / Dashboard / Servers / Tunnels / TunnelDetail / Account / Layout / admin/*`。
桌面端通过 `@web` alias 复用 `lib/api.ts`、`lib/types.ts`、`lib/format.ts`、`state/auth.tsx`、`state/toast.tsx`、`components/ui.tsx`、`components/CreateTunnelModal.tsx`、`styles.css`；独有 `Connect / ClientHome / Deploy / Settings / Shell`。

`AuthProvider` 增加 `serverUrl` 与 `setServerUrl`，同一套页面既能同源（WebUI）也能跨源（桌面连任意服务端）。桌面端通过 Rust `api_request` 代理请求，规避 webview 对自签名证书的限制。

## 8. 客户端 GUI

连接向导（地址 → 用户名 → 密码/私钥 → 信任自签名）→ 主界面（运行状态卡 + 节点延迟 + 隧道表）→ 创建弹窗（节点延迟/协议/内网地址/端口/地址形式）→ 成功页（公网地址 + 复制 + 立即启动）→ 托盘常驻 + 自动重连。

## 9. 客户端 CLI

`login/logout/whoami/server list/tunnel create|list|start|stop|delete|stats/run/status/config/doctor`，`--json` 全支持。详见 [CLI.md](CLI.md)。

## 10. 认证与安全

- 密码 Argon2id（64MiB/t=2/p=2），PHC 存储
- 会话令牌 32B 随机，仅存 SHA-256，7 天，登出撤销
- RSA 挑战-响应：一次性 nonce（2 分钟），`SHA256("yukitunnel-key-auth:"+nonce)` + PKCS#1 v1.5；支持 PKCS#1/PKCS#8/OpenSSH 私钥
- 登录失败锁定 + IP 节流；全链路 TLS；安全响应头；审计日志
- 两级角色与资源归属校验

## 11. 域名开关与节点延迟

域名：`settings.domain_binding` + `domain_suffix`，运行时以 DB 为准；创建隧道时决定 `domain`，地址由 `Service.PublicAddress` 统一计算；关闭时回退 `node.public_host`；用户无法绕过（显式请求域名但开关关闭 → 409）。

延迟：节点表存 `latency_ms/status/last_seen_at`；`GET /servers` 对 0 值实时 TCP 探测并回写；管理员可强制 `POST /admin/nodes/{id}/check`；前端 15s 刷新并按 80/200ms 分档着色。

## 12. 目录结构

见 [ARCHITECTURE.md §12](ARCHITECTURE.md#12-项目目录结构)。

## 13. 分阶段计划与验收

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| P0 | 服务端 + WebUI + 双 CLI + 桌面壳 + Docker | ✅ 本次交付 |
| P1 | 限速 UI、IP 白名单、节点自动注册、日志流 | 规划 |
| P2 | 指标、备份恢复、systemd、升级 | 规划 |
| P3 | 套餐配额、OIDC、自动更新 | 规划 |
| P4 | 多入口调度 | 规划 |

验收对照表见 [ARCHITECTURE.md §13](ARCHITECTURE.md#13-分阶段开发计划与验收)。

## 14. 云 Profile Registry（端口放行）

服务端可在创建节点/隧道时自动放行云安全组端口。实现方式是**声明式 YAML + 内置签名引擎**，
新增厂商不需要写 Go 代码。详见 [CLOUD-PROFILES.md](CLOUD-PROFILES.md)。

### 14.1 六厂商参数差异（已核对官方文档）

| 维度 | 阿里云 | 腾讯云 | AWS | 华为云 | Azure | GCP |
| --- | --- | --- | --- | --- | --- | --- |
| 端口写法 | `8000/8010` | `8000-8010` | `FromPort`/`ToPort` | `port_range_min/max` | `"8000-8010"` | `["8000-8010"]` |
| 协议大小写 | `tcp` | `TCP` | `tcp` | `tcp` | `Tcp` | `tcp` |
| 签名 | HMAC-SHA1 | TC3-HMAC-SHA256 | SigV4 | IAM Token | OAuth2 Bearer | OAuth2 Bearer |
| 幂等 | ClientToken | ClientToken | 重复错误码 | 409 | PUT 覆盖 | 同名 alreadyExists |
| 撤销 | 同参数 | 同参数/PolicyIndex | 同参数 | rule_id | 规则名 | 规则名 |

### 14.2 Registry 与多源回退

```
GitHub: yuuuuukiu/yukitunnel-profiles
  ├── profiles/*.yaml
  ├── index.json          # CI 生成
  └── index.json.sig      # ed25519 签名

拉取顺序：官方镜像 → jsDelivr → GitHub raw → 自定义镜像 → 本地缓存
每个源都必须验签；任一环节失败即跳过。代理（http/socks5）不影响验签。
```

- 内置公钥编译进二进制（`YUKI_PROFILE_PUBKEY` 可覆盖，仅用于测试）。
- `index.json` 里每个 profile 带 `sha256`，下载后逐字节校验。
- 缓存文件同样验签，因此篡改磁盘缓存也会被拒绝。

### 14.3 执行安全

| 控制 | 实现 |
| --- | --- |
| 域名白名单 | `allowed_hosts`，引擎强制校验，仅允许 https |
| 权限明示 | `permissions` 展示给管理员，API 要求 `acknowledge_permissions` |
| 幂等优先 | `idempotent_check` 先读后写；已存在则跳过 |
| 凭证加密 | AES-256-GCM，密钥在 `<data>/credential.key`（0600） |
| 凭证不落日志 | 审计只记录 `access_key_id` 前 4 位 |
| dry-run | 渲染并签名但不发送请求 |
| 执行记录 | `profile_runs` 表记录 profile 版本、sha256、端口、状态、耗时 |

### 14.4 三端一致

| 功能 | WebUI | CLI | 桌面端 |
| --- | --- | --- | --- |
| 查看签名索引 | 云 Profile 页 | `yuki-server profile index` | 复用 WebUI 页面 |
| 安装 / 移除 / 启停 | 同上 | `profile install/verify` | 同上 |
| 凭证管理 | 同上 | `profile credential set` | 同上 |
| 放行 / 回收端口 | 同上 | `profile run --action ... --ack` | 同上 |
| 执行记录 | 同上 | `profile runs` | 同上 |
| 镜像与代理 | 同上 | `profile mirror set/show` | 同上 |

## 15. 系统防火墙与凭证安全

### 15.1 防火墙适配

| 后端 | 检测 | 放行 | 标记 |
| --- | --- | --- | --- |
| ufw | `ufw status verbose` | `ufw allow p/proto comment yukitunnel` | 注释 |
| firewalld | `firewall-cmd --state` + zone target | `--permanent --add-port` + `--add-port` | 端口段推断 |
| nftables | `nft list chains` + input policy | 独立表 `inet yukitunnel` | 独立表 |

仅在「已启用 + 入站默认 deny + 有权限」时修改；否则 `Skipped` 并说明原因。
隧道启停触发放行/回收，服务端启动执行一次对账清理孤儿规则。

### 15.2 凭证加密

```
AK/SK → AES-256-GCM → "v1:<base64>" → SQLite
密钥：YUKI_CREDENTIAL_KEY > <data>/credential.key(0600) > 拒绝写入
API ：值一律 "****"，access_key_id 保留 4 位前缀
轮换：yuki-server profile rekey（YUKI_CREDENTIAL_KEY_OLD → YUKI_CREDENTIAL_KEY）
```

## 16. MVP 代码骨架

真实可运行代码，非伪代码：

```
cmd/yuki-server/main.go            服务端 + 管理 CLI 入口
cmd/yuki/main.go                   客户端入口
internal/tunnel/hub.go             数据面（TCP/UDP/HTTP、限速、统计、恢复）
internal/tunnel/protocol.go        控制流与流头协议
internal/server/http.go            路由、中间件、CORS、SPA
internal/server/api_*.go           认证 / 客户端 / 管理 API
internal/servercli/*.go            服务端 CLI（本地 + 远程后端）
internal/client/agent.go           客户端 agent（重连、转发）
internal/cli/*.go                  客户端 CLI
internal/auth/*.go                 Argon2id / 令牌 / RSA / OpenSSH
internal/store/*.go                SQLite 模型与查询（含 cloud_profiles / profile_runs）
internal/cloudprofile/             声明式 profile：schema、模板、执行引擎
internal/cloudprofile/registry/    多源回退、ed25519 验签、缓存
internal/cloudprofile/signers.go   4 个内置签名引擎 + 2 个 OAuth2
internal/firewall/                 ufw/firewalld/nftables 适配 + 对账
internal/store/credentials.go      AES-256-GCM 凭证加密与轮换
profiles/*.yaml                    官方维护的 6 个厂商 profile
cmd/yuki-profile-sign/             CI 签名工具
web/src/**                         WebUI
desktop/src/** + src-tauri/src/**  桌面端 + 一键部署
```

验证命令：`make test && make test-smoke`，以及 `make build` 后 `./bin/yuki-server init ... && ./bin/yuki-server start`。
