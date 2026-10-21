# YukiTunnel v0.1.0 (beta)

> ⚠️ **这是 beta 版本。** 接口与配置格式在 1.0 之前可能变化，不建议直接用于关键生产环境。

首个可发布版本：服务端一键部署、三端对等管理、全 GUI 客户端、声明式云 Profile。

---

## 亮点

### 服务端

- **单二进制**（内嵌 WebUI），SQLite 持久化，无外部依赖
- **三种部署**：Docker Compose、二进制 + systemd、桌面端图形向导
- **强制 TLS**：默认自签名，支持自有证书与指纹固定
- **多节点**：每节点独立公网入口，实时延迟探测
- **TCP / UDP / HTTP / HTTPS** 四种隧道；HTTPS 为 TLS 透传
- **域名绑定开关**：开启返回「域名+端口」，关闭返回「IP+端口」

### 三端对等管理

| 能力 | WebUI | 桌面端 | CLI |
| --- | --- | --- | --- |
| 用户管理 | ✅ | ✅ | ✅ |
| 节点管理 | ✅ | ✅ | ✅ |
| 隧道管理 | ✅ | ✅ | ✅ |
| 域名绑定开关 | ✅ | ✅ | ✅ |
| 端口范围 | ✅ | ✅ | ✅ |
| 审计日志 | ✅ | ✅ | ✅ |
| 云 Profile | ✅ | ✅ | ✅ |
| 一键部署 | — | ✅ | ✅ |

### 客户端

- 全图形化：首次向导只需服务端地址 + 账号
- 点击「+」选节点（显示延迟）、填内网地址、创建即返回公网地址
- 一键启动/停止、复制地址、编辑、删除、自动重连、托盘常驻
- CLI 功能对等，全部命令支持 `--json`

### 云 Profile（新）

- **声明式 YAML** 描述云安全组端口放行，新增厂商无需改代码
- **6 个官方 profile**：阿里云、腾讯云、AWS、华为云、Azure、GCP
- **4 个内置签名引擎** + 2 个 OAuth2 引擎
- **签名强制校验**：ed25519，内置公钥，无法关闭
- **多源回退**：官方镜像 → jsDelivr → GitHub raw → 自定义 → 缓存
- **HTTP / SOCKS5 代理**支持，代理不影响验签
- **dry-run**、幂等探测、`allowed_hosts` 白名单、权限确认

### 系统防火墙

- 自动检测 ufw / firewalld / nftables
- 仅在「已启用 + 入站默认 deny」时放行，否则跳过
- 隧道启停自动放行/回收，服务端启动对账清理孤儿规则
- Windows / macOS 明确返回 Unsupported

---

## 真实联调范围

| 厂商 | 状态 | 说明 |
| --- | --- | --- |
| 阿里云 | ⚠️ 仅 mock 验证 | 请求形状、`Action` 参数、HMAC-SHA1 签名、斜杠端口格式已验证；**未跑真实账号** |
| 腾讯云 | ⚠️ 仅 mock 验证 | TC3 签名、`SecurityGroupPolicySet` envelope、大写协议已验证；**未跑真实账号** |
| AWS | ⚠️ 仅 mock 验证 | 请求形状与 SigV4 头已验证 |
| 华为云 | ⚠️ 仅 mock 验证 | 请求体与 IAM Token 头已验证 |
| Azure | ⚠️ 仅 mock 验证 | PUT 语义与端口格式已验证 |
| GCP | ⚠️ 仅 mock 验证 | `ports` 数组与 `INGRESS` 已验证 |

> **没有任何厂商经过真实账号联调。** 全部 6 个 profile 只通过 mock 端点验证了
> 请求结构、签名算法、端口方言与协议大小写。
>
> 这能保证「请求长得对」，但**不能保证云厂商一定接受**——限流、子账号策略、
> 账号状态、地域差异都可能导致真实调用失败。
>
> 发布前请自行联调：
>
> ```bash
> export YUKI_ALIYUN_ACCESS_KEY_ID=...   # 最小权限子账号
> bash scripts/live-test-aliyun.sh
> ```
>
> 步骤与最小权限策略见 [CLOUD-LIVE-TEST.md](CLOUD-LIVE-TEST.md)。

---

## 安装

### 服务端（Docker）

```bash
docker run -d --name yuki-server \
  -p 8443:8443 -p 9443:9443 -p 20000-20100:20000-20100 \
  -e YUKI_PUBLIC_HOST=203.0.113.10 \
  -e YUKI_ADMIN_PASSWORD='ChangeMe-Str0ng!' \
  -v yuki-data:/data \
  ghcr.io/yuuuuukiu/yuki-server:0.1.0
```

### 客户端

| 平台 | 文件 |
| --- | --- |
| Windows x64 | `yuki-server-windows-amd64.exe` / `yuki-windows-amd64.exe` |
| Windows x86 | `yuki-server-windows-386.exe` / `yuki-windows-386.exe` |
| macOS Intel / ARM | `yuki-server-darwin-amd64` / `yuki-server-darwin-arm64` |
| Linux x64 / ARM | `yuki-server-linux-amd64` / `yuki-server-linux-arm64` |
| Linux 桌面 | `YukiTunnel-x86_64.AppImage` |

所有二进制附带 `SHA256SUMS.txt`。

---

## 升级方式

### 从 0.0.x 升级

首次正式发布，无历史版本需要迁移。

### Docker

```bash
docker compose pull && docker compose up -d
```

数据库迁移幂等，配置自动补齐新字段。

### 二进制

```bash
sudo systemctl stop yuki-server
sudo cp yuki-server /usr/local/bin/
sudo systemctl start yuki-server
sudo yuki-server doctor --data-dir /var/lib/yukitunnel
```

---

## 已知限制

| 限制 | 影响 | 计划 |
| --- | --- | --- |
| AWS / 华为云 / Azure / GCP 未真实联调 | 可能因账号策略失败 | 0.2.0 |
| Azure / GCP 需外部获取 OAuth2 Token | 不能一条命令跑通 | 0.2.0 增加 token 获取 |
| 华为云需外部换取 IAM Token | 同上 | 0.2.0 |
| 裸 iptables 不支持 | 需手工放行或改用 ufw/firewalld | 评估中 |
| firewalld 无规则注释 | 对账依赖端口段推断 | 上游限制 |
| 云 API 限流未强制 | 高频创建可能触发 Throttling | 0.2.0 |
| 无自助注册 | 用户必须由管理员创建（设计如此） | 不计划 |
| 无计费/配额 | 单租户部署 | 0.3.0 |
| 无 P2P 打洞 | 流量始终经服务端中转 | 不计划 |

---

## 安全

- 密码：Argon2id（64MiB / t=2 / p=2）
- 令牌：仅存 SHA-256，默认 7 天
- 云凭证：AES-256-GCM 静态加密，API 返回 `****`
- Profile：ed25519 签名强制校验，`allowed_hosts` 防凭证外发
- 审计：登录、用户、节点、隧道、设置、Profile 执行、防火墙变更

漏洞报告：见 [SECURITY.md](../SECURITY.md)。

---

## 致谢

感谢所有提交 issue、测试 profile、提供云账号联调环境的贡献者。

---

## 下一步（0.2.0 预告）

- AWS / 华为云 / Azure / GCP 真实联调
- Profile 限流与重试
- Prometheus 指标
- 备份/恢复命令
- 节点自动注册
