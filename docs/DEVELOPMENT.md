# 开发指南

## 环境

| 工具 | 版本 |
| --- | --- |
| Go | 1.24+ |
| Node.js | 20+ |
| pnpm | 9+ |
| Rust | 1.77+（仅桌面端） |
| Docker | 可选 |

## 目录速览

见 [ARCHITECTURE.md](ARCHITECTURE.md#12-项目目录结构)。

## 本地运行

### 服务端

```bash
go run ./cmd/yuki-server init --data-dir /tmp/yuki-dev \
  --api-addr 127.0.0.1:18443 --control-addr 127.0.0.1:19443 \
  --admin-password admin12345 --port-min 28000 --port-max 28100

go run ./cmd/yuki-server start --data-dir /tmp/yuki-dev
# 或前台：go run ./cmd/yuki-server serve --data-dir /tmp/yuki-dev
```

### WebUI 开发服务器

```bash
cd web && pnpm install && pnpm dev
```

Vite 将 `/api` 代理到 `YUKI_DEV_SERVER`（默认 `https://127.0.0.1:18443`，`secure: false`）：

```bash
YUKI_DEV_SERVER=https://127.0.0.1:18443 pnpm dev
```

### 客户端

```bash
export YUKI_CLIENT_HOME=/tmp/yuki-client
go run ./cmd/yuki login --server https://127.0.0.1:18443 --username alice --password alice12345 --insecure
go run ./cmd/yuki tunnel create --proto tcp --local-port 8080
go run ./cmd/yuki run
```

### 桌面端

```bash
bash scripts/prepare-sidecars.sh      # 生成 desktop/src-tauri/binaries/*
cd desktop && pnpm install && pnpm tauri:dev
```

> 桌面端复用 `web/src`（Vite alias `@web`），修改共享组件后两端同时生效。

## 测试

```bash
go test ./...                        # 单元 + API 集成 + 数据面集成
go test ./internal/tunnel -v         # TCP/UDP/HTTP 隧道端到端
go test ./internal/firewall -v       # ufw/firewalld/nftables 适配（fake runner）
go test ./internal/cloudprofile -v   # profile 引擎与签名
go vet ./...
cd web && pnpm build                 # TS 类型检查 + 产物构建
cd desktop && pnpm build && (cd src-tauri && cargo check)
```

### 端到端与联调

| 命令 | 依赖 | 覆盖 |
| --- | --- | --- |
| `bash scripts/smoke.sh` | 无 | 服务端 + 客户端 CLI + 真实隧道流量 |
| `bash scripts/e2e-local.sh` | Docker | 全链路：compose → 用户 → 隧道 → 公网访问 → 清理 |
| `bash scripts/e2e-local.sh --no-docker` | 无 | 同上，用本地二进制 |
| `bash scripts/live-test-aliyun.sh` | 阿里云 AK | 真实安全组放行/幂等/回收 |
| `bash scripts/live-test-tencent.sh` | 腾讯云 AK | 同上 |

联调凭证只从环境变量读取，不落盘、不进 CI。见 [CLOUD-LIVE-TEST.md](CLOUD-LIVE-TEST.md)。

### 防火墙测试

`internal/firewall` 用 `fakeRunner` 注入命令输出，因此可以在 macOS 上测 Linux 逻辑：

```go
f := newFake("ufw")
f.on("ufw status verbose", "Status: active\nDefault: deny (incoming)\n")
d := &Detector{Runner: f, GOOS: "linux", EUID: 0}
```

数据面测试使用真实的 WebSocket + yamux 连接，覆盖 TCP 回环、HTTP 反代、UDP 分帧与未授权拒绝。

## 代码约定

- 错误：内部用 `apierr.New(status, code, message)`；`message` 直接面向终端用户，用中文可读文案。
- 数据库：所有时间以 Unix 秒存储，读取时转 `time.Time`；查询集中在 `internal/store`。
- 数据面：监听器归服务端所有，客户端只转发；改动协议需同时升级 `ProtocolVersion`。
- 前端：共享组件放 `web/src`，桌面独有放 `desktop/src`；颜色/间距使用 `styles.css` 变量。
- 提交前：`gofmt`、`go vet`、`pnpm build`、`cargo check`。

## 发布

```bash
VERSION=0.2.0 bash scripts/build.sh
bash scripts/prepare-sidecars.sh
cd desktop && pnpm tauri:build
docker build -t ghcr.io/yuuuuukiu/yuki-server:0.2.0 .
```

CI 建议矩阵：`ubuntu-latest`（Go 测试 + Docker 构建）、`macos-latest` / `windows-latest`（Tauri 打包）。

### 桌面端与 WebUI 的代码共享

管理页面只有一份实现，位于 `web/src/pages/admin/`。桌面端通过
`desktop/src/pages/admin/*.tsx` 做一层再导出：

```tsx
export { default } from '@web/pages/admin/Users';
```

这样两端共用同一个组件、同一个 `@web/lib/api` 客户端，行为天然一致。
新增管理页时：

1. 在 `web/src/pages/admin/` 实现
2. 在 `web/src/App.tsx` 注册路由
3. 在 `desktop/src/pages/admin/` 加一行再导出
4. 在 `desktop/src/App.tsx` 与 `desktop/src/pages/Layout.tsx` 的 `adminNav` 注册

权限判断有两层：`RequireAdmin` 做 UI 重定向，服务端 `/api/v1/admin/*`
对非管理员返回 403。前者只是体验优化，后者才是真正的边界。
