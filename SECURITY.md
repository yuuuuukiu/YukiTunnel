# 安全策略

## 支持的版本

| 版本 | 安全更新 |
| --- | --- |
| 0.1.x（beta） | ✅ |
| < 0.1 | ❌ |

---

## 报告漏洞

**不要**用公开 issue 报告安全问题。

- 邮件：`security@yuuuuukiu.top`（PGP 指纹见文末）
- 或使用 GitHub 的 [Private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)

请在报告中包含：

1. 受影响版本（`yuki-server --version`）与平台
2. 复现步骤或最小 PoC
3. 影响评估（能否读取他人隧道、能否越权调用云 API 等）
4. 是否已公开披露

**响应时限**：48 小时内确认，7 天内给出初步评估，30 天内发布修复（严重问题更快）。
修复发布后会在 release notes 中致谢（除非你要求匿名）。

---

## 威胁模型

### 资产

| 资产 | 位置 | 泄露后果 |
| --- | --- | --- |
| 用户密码 | SQLite `users.password_hash`（Argon2id） | 撞库、账号接管 |
| 会话 / API 令牌 | SQLite `api_tokens.token_hash`（SHA-256） | 冒用身份、操作隧道 |
| 云厂商 AK/SK | SQLite `profile_credentials.fields_json`（AES-256-GCM） | 攻击者可在你的云账号开端口、改安全组 |
| RSA 私钥 | 仅在客户端 | 冒充用户登录 |
| 隧道流量 | 传输中 | 内网数据泄露 |
| 审计日志 | SQLite `audit_logs` | 攻击痕迹被抹除 |

### 信任边界

```
[不可信] 公网访问者
    │  仅能访问被显式放行的公网端口
    ▼
[半可信] 客户端 agent        持有用户令牌，只能操作自己的隧道
    │  WSS + TLS + Bearer
    ▼
[可信]   服务端              持有数据库、云凭证、TLS 私钥
    │  HTTPS
    ▼
[不可信] Profile 镜像         内容必须通过 ed25519 验签才被使用
```

### 主要威胁与缓解

| 威胁 | 缓解措施 | 代码位置 |
| --- | --- | --- |
| 暴力破解密码 | Argon2id（64MiB/t=2/p=2）+ 失败锁定 + IP 节流 | `internal/auth` |
| 令牌泄露 | 只存 SHA-256；7 天过期；登出即撤销 | `internal/store/tokens.go` |
| 越权访问他人隧道 | 每个 handler 校验 `tunnel.UserID == user.ID` 或 admin | `internal/server/api_client.go` |
| 中间人攻击 | 强制 TLS 1.2+；支持证书指纹固定 | `internal/tlsutil` |
| 恶意 Profile 外发凭证 | `allowed_hosts` 白名单 + 仅 https | `internal/cloudprofile/engine.go` |
| 镜像投毒 | ed25519 签名，内置公钥，无法关闭 | `internal/cloudprofile/registry` |
| 云凭证落盘泄露 | AES-256-GCM；密钥 0600；API 返回 `****` | `internal/store/credentials.go` |
| 端口被他人抢占 | 端口分配 + 唯一约束 + 归属校验 | `internal/server/api_client.go` |
| 审计被绕过 | 关键操作统一走 `Service.audit` | `internal/server/service.go` |
| 本地提权改防火墙 | 仅在 root 且 deny 策略时操作，规则带标记 | `internal/firewall` |

### 明确不在范围内

- 拥有服务器 root 权限的攻击者（可直接读数据库与内存）
- 客户端机器被完全控制（可读私钥与令牌）
- 云厂商自身的凭证泄露
- 用户主动把公网地址分享给不可信第三方
- 拒绝服务（带宽打满）——请在上游加限流

---

## 凭证保护

### 云 AK/SK

```
写入：POST /admin/profiles/{id}/credentials
      → AES-256-GCM 加密
      → SQLite 只存密文（v1:<base64>）

读取：GET  /admin/profiles/{id}/credentials
      → {"fields": {"access_key_id": "AKIA****", "access_key_secret": "****"}}

执行：POST /admin/profiles/{id}/run
      → 内存解密 → 签名 → 发请求 → 立即丢弃
```

密钥来源优先级：

1. `YUKI_CREDENTIAL_KEY`（base64，≥16 字节）——容器/密钥管理系统
2. `<data-dir>/credential.key`（随机 32 字节，0600）——单机默认
3. 无（拒绝写入凭证）

轮换：

```bash
export YUKI_CREDENTIAL_KEY_OLD=$(base64 -w0 < /var/lib/yukitunnel/credential.key)
export YUKI_CREDENTIAL_KEY=$(head -c 32 /dev/urandom | base64 -w0)
yuki-server profile rekey --data-dir /var/lib/yukitunnel
```

### 密码

- Argon2id，PHC 字符串，每条记录独立 salt
- 最小长度 8，由 `HashPassword` 强制
- 默认管理员密码若未设置会使用 `admin12345` 并在日志中告警——**生产必须设置**

### 令牌

- 32 字节随机，`yk_` 前缀，仅存 SHA-256
- 会话令牌 7 天（`auth.token_ttl_hours` 可调）
- API 令牌最长 30 天，可单独撤销

---

## 签名信任链

```
GitHub 仓库 yuuuuukiu/yukitunnel-profiles
    │  CI 用 ed25519 私钥签名 index.json
    ▼
index.json + index.json.sig
    │  同步到 R2 / jsDelivr / raw
    ▼
客户端（内置公钥，编译期固定）
    │  1. 用内置公钥验 index.json.sig
    │  2. 按 index 里的 sha256 校验每个 profile
    │  3. 校验 YAML 结构与 allowed_hosts/permissions
    ▼
执行（仅在以上全部通过后）
```

关键性质：

- **签名校验无法关闭**，没有 `--insecure-profiles` 之类的开关
- 内置公钥在编译期固定；镜像被攻破也无法伪造（没有私钥）
- 缓存文件同样验签，篡改本地磁盘也会被拒
- 每个 profile 单独哈希，索引被替换但文档没换也会被发现
- 私有密钥只在 CI secrets（`PROFILE_SIGNING_KEY`），从不进仓库

轮换签名密钥：

1. 生成新密钥对
2. 新公钥写入 `internal/server/profilekeys.go`，`builtinProfileKeyID` 改名（如 `yuki-profiles-2027`）
3. 旧公钥保留一个版本，允许过渡期
4. 发布新版本后，旧索引失效

---

## 部署安全基线

```bash
# 1. 必须设置强管理员密码
export YUKI_ADMIN_PASSWORD='<32+ 随机字符>'

# 2. 使用真实证书
--tls-cert /etc/letsencrypt/live/panel/fullchain.pem \
--tls-key  /etc/letsencrypt/live/panel/privkey.pem

# 3. 数据目录权限
chmod 700 /var/lib/yukitunnel
chmod 600 /var/lib/yukitunnel/credential.key

# 4. 收敛端口段与来源网段
--port-min 20000 --port-max 20050
# profile 的 source_cidr 建议 /32，不要 0.0.0.0/0

# 5. 反向代理加认证与限流
# 6. 定期备份并验证恢复
```

### 检查清单

- [ ] 管理员密码已改，非默认值
- [ ] TLS 证书有效期 > 30 天
- [ ] `/data` 权限 0700，数据库 0600
- [ ] 8443/9443 未直接暴露到不可信网络（或已加代理认证）
- [ ] 云子账号为最小权限，AK 可随时吊销
- [ ] 审计日志有外部备份
- [ ] 已阅读并接受 `docs/ACCEPTANCE-PROFILES.md` 的已知限制

---

## PGP

```
Fingerprint: (发布前由维护者填入)
```

在指纹公布前，请使用 GitHub Private vulnerability reporting。
