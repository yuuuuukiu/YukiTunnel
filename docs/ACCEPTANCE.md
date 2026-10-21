# 验收清单与验证记录

## 1. 功能验收

| # | 验收标准 | 实现 | 验证方式 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | `docker compose up -d` 启动服务端与 WebUI | `docker-compose.yml`、`Dockerfile`、`deploy/docker-entrypoint.sh` | `bash scripts/e2e-local.sh`（Docker 模式） | ✅ |
| 2 | 桌面 GUI 一键部署后 WebUI 可访问 | `desktop/src-tauri/src/deploy.rs` | macOS DMG 实机：环境检测 → init → serve → 健康检查 → WebUI 200 → 登录成功 | ✅ 已验证 |
| 3 | 服务端 CLI 完成初始化/用户/节点/域名/健康检查 | `internal/servercli` | `scripts/smoke.sh`、`scripts/e2e-local.sh` | ✅ |
| 4 | 三端功能一致 | WebUI / 桌面端 / CLI | `TestAdminCreatesUserAndTunnel`、`TestDomainBindingToggle` | ✅ |
| 5 | 客户端首次配置 + 密码/Key 登录 | `Connect.tsx`、`yuki login` | `TestKeyAuthFlow` | ✅ |
| 6 | 点「+」看到延迟并创建隧道 | `CreateTunnelModal.tsx` | 截图 + `POST /tunnels` | ✅ |
| 7 | 返回正确的公网地址形式 | `Service.PublicAddress` | `TestAdminCreatesUserAndTunnel`（域名）、`TestDomainBindingToggle`（IP） | ✅ |
| 7b | TCP / UDP / HTTP / HTTPS 四种协议 | `internal/tunnel/hub.go` | `TestTCPTunnelRoundTrip`、`TestUDPTunnel`、`TestHTTPTunnel`、`TestHTTPSTunnelPassthrough`、`TestHTTPSTunnelCarriesRealTLS` | ✅ |
| 8 | 启动生效、停止失效 | `Hub.StartTunnelFor` / `Hub.StopTunnel` | `internal/tunnel` 集成测试 + e2e | ✅ |
| 9 | CLI 覆盖 GUI 核心流程并支持 `--json` | `internal/cli` | `scripts/e2e-local.sh` | ✅ |
| 10 | 云 Profile 安装/执行/回收 | `internal/cloudprofile` | 见 [ACCEPTANCE-PROFILES.md](ACCEPTANCE-PROFILES.md) | ✅ |
| 11 | 系统防火墙自动放行 | `internal/firewall` | fake runner 测试 + Linux 容器内真实 ufw/nftables | ✅ 已验证 |

## 2. 自动化验证

```text
$ gofmt -l .                       # 无输出
$ go vet ./...                     # 无输出
$ go test ./... -race
ok  internal/cloudprofile
ok  internal/cloudprofile/registry
ok  internal/firewall
ok  internal/server
ok  internal/store
ok  internal/tunnel

$ bash scripts/smoke.sh            # 通过
$ bash scripts/e2e-local.sh        # 通过（Docker）
$ bash scripts/e2e-local.sh --no-docker   # 通过
$ (cd web && pnpm build)           # 通过
$ (cd desktop && pnpm build)       # 通过
$ (cd desktop/src-tauri && cargo check)   # 通过
```

### 端到端实测输出

```text
==> 启动服务端
    OK   容器已启动（yukitunnel-e2e）
==> 等待健康检查
    OK   健康检查通过
==> 创建用户与节点
    OK   用户 e2e-user 已通过 API 创建
    OK   节点 ade07c4b3138c121 可用
==> 安装云 Profile 并 dry-run
    OK   profile dry-run 成功（未调用云 API）
==> 客户端登录并创建隧道
    OK   登录成功
    OK   隧道 tun_4l9had76 -> 127.0.0.1:28555
==> 启动本地服务与 agent
    OK   本地服务 127.0.0.1:18080 就绪
    OK   agent 已连接
==> 启动隧道并验证公网访问
    OK   公网地址返回预期内容
==> 停止隧道并确认端口关闭
    OK   停止后端口不可达
==> 删除隧道
    OK   隧道已删除
端到端测试全部通过
```

## 3. 手工验收（发布前）

见 [RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md)。

## 4. 未覆盖项

| 项 | 原因 | 状态 |
| --- | --- | --- |
| 真实云账号联调（阿里云/腾讯云） | 需要用户凭证 | 脚本就绪，待执行 |
| AWS / 华为云 / Azure / GCP 真实联调 | 需要用户凭证 | mock 验证完成 |
| R2 实际推送 | 需要仓库 secrets | workflow 就绪 |
| ufw / firewalld 真机验证 | 需要对应 Linux 主机 | 文档提供步骤 |
| 桌面安装包 | 需要各平台打包 | CI 矩阵就绪 |
