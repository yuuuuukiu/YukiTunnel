# 当前状态

> 最后一次更新：2026-10-21

## 一句话

**代码已就绪，可以打 tag 发布 v0.1.0 (beta)。** 剩下的只有需要你本人凭证或硬件的验证。

---

## 已完成并验证

### 服务端

| 项 | 验证方式 |
| --- | --- |
| 单二进制 + 内嵌 WebUI | 容器内 `200 text/html`，`/Applications` 实机 |
| Docker Compose 自构建 | 干净目录 `up -d --build` → healthy |
| 三种部署方式 | Compose / 二进制 / 桌面 GUI 向导 |
| TCP / UDP / HTTP / HTTPS 隧道 | 各有 round-trip 测试，HTTPS 含真实 TLS 握手 |
| 多节点、延迟探测、域名开关 | API + CLI + WebUI 三端 |
| 审计日志、Argon2id、令牌哈希 | 单元测试 + smoke |
| 系统防火墙 | **Linux 容器内真实 ufw + nftables 验证** |

### 客户端

| 项 | 验证方式 |
| --- | --- |
| 桌面 GUI 全功能 | **DMG 实机安装并运行** |
| 一键部署向导 | 环境检测 → init → serve → 健康检查 → WebUI 200 → 登录成功 |
| 桌面管理端（7 个页面） | 复用 WebUI 组件，`cargo check` + 路由测试 |
| CLI 全命令 + `--json` | 错误也输出 JSON（stderr） |
| 密码 + RSA Key 认证 | 三端支持 |

### 云 Profile

| 项 | 验证方式 |
| --- | --- |
| 6 个官方 profile | 全部通过 `Validate()` |
| ed25519 签名链 | 内置公钥，无法关闭 |
| 三源镜像一致 | jsDelivr / raw / R2 均为 `cf24fe1f` |
| 客户端验签 | `profile index` 显示「已验签」 |
| 远端安装 | 来源显示 `.../profiles`，非 `builtin` |
| 离线兜底 | 传输失败回退内置副本；**验签失败硬拒绝** |
| 凭证加密 | AES-256-GCM，DB 内无明文，API 返回 `****` |

### 工程

```
gofmt / go vet        无输出
go test ./...         12 包全绿
go test -race         全绿
连续 3 次全量测试      稳定
CI                    连续 9 次 success
```

覆盖率（本轮从大量 0% 提升）：

| 包 | 覆盖率 |
| --- | --- |
| config | 89.6% |
| tlsutil | 80.6% |
| apierr | 79.2% |
| ids | 75.0% |
| cloudprofile/registry | 74.6% |
| cloudprofile | 73.6% |
| tunnel | 61.2% |
| firewall | 53.7% |
| server | 51.5% |
| auth | 44.9% |
| store | 36.4% |
| cli / client / servercli / wsnet | 0%（靠 e2e 覆盖） |

### 本轮发现并修复的真实缺陷

| 缺陷 | 影响 |
| --- | --- |
| CLI 用开发密钥验签 | `profile index` 拒绝同一二进制通过 HTTP 接受的索引 |
| 安装路径缺 `profiles/` 前缀 | 所有远端安装 404，静默回退内置副本 |
| 管理员无法禁用普通用户 | WebUI「禁用」按钮永远报 `LAST_ADMIN` |
| 桌面端无管理页 | 三端对等需求不成立 |
| 缺 `icon.icns` | `tauri build` 无法产出安装包 |
| 版本号取 commit hash | 开发构建看起来像已发布版本 |
| `build.sh` 删除 `.keep` | 全新 clone 无法 `go build` |
| Docker 只读命令触发初始化 | `docker run image --version` 污染数据卷 |
| `--json` 失败输出纯文本 | 脚本无法解析错误 |
| HTTPS 隧道零测试 | 四种协议里唯一无覆盖的 |
| `.gitignore` 漏 `secrets/` | 私钥可能被误提交 |

---

## 仍未完成（需要你）

### 1. 真实云联调（需要 AK/SK）

```bash
export YUKI_ALIYUN_ACCESS_KEY_ID=...
export YUKI_ALIYUN_ACCESS_KEY_SECRET=...
export YUKI_REGION=cn-hangzhou
export YUKI_SECURITY_GROUP_ID=sg-你的测试安全组
bash scripts/live-test-aliyun.sh
```

腾讯云同理。**当前 6 个厂商全部只做了 mock 验证**，release notes 已如实标注。

### 2. Windows / Linux 安装包实机验证

macOS DMG 已验证；Windows `.exe` 和 Linux AppImage 需要对应机器。

### 3. firewalld 真机验证

ufw 与 nftables 已在容器验证；firewalld 需要 systemd，容器里成本过高。

### 4. 打 tag 发布

```bash
git tag -a v0.1.0 -m "YukiTunnel v0.1.0 (beta)"
git push origin v0.1.0
```

会触发 release workflow：preflight（检查占位密钥）→ 测试 → 7 平台二进制 → Docker 多架构 → 桌面端 3 平台 → 创建 draft release。

---

## 已知限制

| 限制 | 影响 | 对策 |
| --- | --- | --- |
| Cloudflare 代理的 R2 域名拒绝 Go 客户端 | 强制只用 R2 会 403 | 默认链 jsDelivr 在前，不受影响；或关掉 Bot Fight Mode |
| AWS / 华为云 / Azure / GCP 未真实联调 | 可能因账号策略失败 | 按 `docs/CLOUD-LIVE-TEST.md` 自测 |
| 裸 iptables 不支持 | 需手工放行 | 改用 ufw/firewalld |
| firewalld 无规则注释 | 对账依赖端口段推断 | 不要手工在同段加规则 |
| 无 Prometheus 指标 | 无法接入监控 | 0.2.0 |

---

## 快速验证

```bash
cd /Users/yukiu/Downloads/YukiTunnel

make test                        # 109 个测试
bash scripts/smoke.sh            # 端到端
bash scripts/e2e-local.sh --no-docker
./bin/yuki-server profile index  # 应显示「已验签」
```

---

## 文档索引

| 文档 | 内容 |
| --- | --- |
| [docs/QUICKSTART.md](docs/QUICKSTART.md) | 5 分钟跑通 + 排错 |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | 部署参数、R2、镜像、代理 |
| [docs/PROFILE-GUIDE.md](docs/PROFILE-GUIDE.md) | Profile YAML 与 PR 流程 |
| [docs/FIREWALL.md](docs/FIREWALL.md) | 防火墙适配与验证记录 |
| [docs/SETUP-SECRETS.md](docs/SETUP-SECRETS.md) | Secrets 填什么、填哪里 |
| [docs/RELEASE-CHECKLIST.md](docs/RELEASE-CHECKLIST.md) | 发布清单（35/89 已勾） |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 贡献指南 |
