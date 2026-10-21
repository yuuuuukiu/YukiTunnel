# 配置 Secrets（照着填）

本文只回答三件事：**在哪里填、填什么、怎么验证**。

> 结论先说：**主仓库一个 secret 都不需要。**
> 所有密钥只填在 **registry 仓库** `yuuuuukiu/yukitunnel-profiles`。

---

## 0. 为什么主仓库不需要

签名与发布已经搬到 registry 仓库。主仓库的 workflow 只有：

| workflow | 是否需要 secret |
| --- | --- |
| `ci.yml` | ❌ 不需要（只用自动注入的 `GITHUB_TOKEN`） |
| `profiles.yml` | ❌ 不需要（只做校验，不签名） |
| `release.yml` | ❌ 不需要（只校验内置副本） |

所以你现在**不用**在主仓库配任何东西。

---

## 1. 先建 registry 仓库

```bash
cd /path/to/YukiTunnel
bash scripts/init-profiles-repo.sh
```

脚本会自动：创建 `yuuuuukiu/yukitunnel-profiles` → 推送 `profiles/*.yaml` →
生成 `.github/workflows/sign.yml`。

---

## 2. 在哪里填

打开：

```
https://github.com/yuuuuukiu/yukitunnel-profiles/settings/secrets/actions
```

页面分两块，**别填错位置**：

```
┌─ Secrets ─────────────────────────────┐   ← 敏感值填这里
│  Repository secrets                    │
│  [+ New repository secret]             │
├─ Variables ───────────────────────────┤   ← 非敏感配置填这里
│  Repository variables                  │
│  [+ New repository variable]           │
└────────────────────────────────────────┘
```

命令行等价：

```bash
REPO=yuuuuukiu/yukitunnel-profiles
gh secret   set <名称> --repo $REPO     # Secrets
gh variable set <名称> --repo $REPO     # Variables
```

---

## 3. 填什么

### 3.1 `PROFILE_SIGNING_KEY`（Secret，必填）

**填什么**：私钥 PEM 文件的 **base64**（单行，无换行）。

**怎么生成**：

```bash
bash scripts/gen-profile-signing-key.sh
```

输出：

```
secrets/profile-signing.key   ← 私钥（0600），不要提交
secrets/profile-signing.pub   ← 公钥 base64，要写进代码
```

**怎么得到要填的值**：

```bash
# macOS
base64 -i secrets/profile-signing.key | tr -d '\n' | pbcopy
# Linux
base64 -w0 secrets/profile-signing.key
```

复制输出（形如 `LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0t...`），粘贴到：

```
Name:  PROFILE_SIGNING_KEY
Value: <上面那一长串>
```

或者一条命令：

```bash
gh secret set PROFILE_SIGNING_KEY \
  --repo yuuuuukiu/yukitunnel-profiles \
  < <(base64 -w0 secrets/profile-signing.key)
```

> ⚠️ 填的是 **base64 后的内容**，不是 PEM 原文。workflow 里会 `base64 -d` 还原。

**同时要做的事**：把公钥写进代码

```bash
cat secrets/profile-signing.pub
# 输出类似 Du+fe5Rau371VWCTGHBIt6/J0C51bMkShZyP7cAyK7Y=

# 替换 internal/server/profilekeys.go 第 27 行
const builtinProfileKeyB64 = "Du+fe5Rau371VWCTGHBIt6/J0C51bMkShZyP7cAyK7Y="
```

改完验证：

```bash
YUKI_RELEASE=1 go test ./internal/server -run TestBuiltinKeyIsNotPlaceholder -v
```

---

### 3.2 `R2_ACCESS_KEY_ID`（Secret）

**从哪里拿**：

```
Cloudflare Dashboard → R2 → Manage R2 API Tokens → Create API Token
```

配置：

| 项 | 值 |
| --- | --- |
| Token name | `yukitunnel-registry-publish` |
| Permissions | **Object Read & Write**（不要 Admin Read & Write） |
| Specify bucket | 只勾 **`yuki-tunnel-profile`** |
| TTL | 留空或按需 |

创建后页面**只显示一次**：

```
Access Key ID:     <32 位十六进制>
Secret Access Key: <64 位十六进制>
```

`Access Key ID` 填进 `R2_ACCESS_KEY_ID`。

> 这一页关掉就再也看不到 Secret，先复制到临时文本再慢慢填。

---

### 3.3 `R2_SECRET_ACCESS_KEY`（Secret）

同一个页面的 `Secret Access Key`，填进 `R2_SECRET_ACCESS_KEY`。

---

### 3.4 `R2_ENDPOINT`（Secret）

**填什么**：

```
https://<ACCOUNT_ID>.r2.cloudflarestorage.com
```

**ACCOUNT_ID 在哪**：

```
Cloudflare Dashboard → R2 → 右侧栏 "Account Details" → Account ID
```

或者在 URL 里：`https://dash.cloudflare.com/<ACCOUNT_ID>/r2`

**注意**：这是 API 端点，**不是** `tunnel.yuuuuukiu.top`。两者用途不同：

| 值 | 用途 |
| --- | --- |
| `https://<id>.r2.cloudflarestorage.com` | CI 上传用（S3 API） |
| `https://tunnel.yuuuuukiu.top` | 客户端下载用（自定义域） |

---

### 3.5 `R2_BUCKET`（Variable，可选）

```
Name:  R2_BUCKET
Value: yuki-tunnel-profile
```

不填则用默认值 `yuki-tunnel-profile`（脚本里已写成默认）。

---

### 3.6 `PROFILE_KEY_ID`（Variable，可选）

```
Name:  PROFILE_KEY_ID
Value: yuki-profiles-2026
```

不填则默认 `yuki-profiles-2026`。这个值必须和 `internal/server/profilekeys.go`
里的 `builtinProfileKeyID` 一致，否则客户端会报「签名密钥不受信任」。

---

## 4. 最终清单

在 **`yuuuuukiu/yukitunnel-profiles`** 仓库：

| 类型 | 名称 | 值 | 必填 |
| --- | --- | --- | --- |
| Secret | `PROFILE_SIGNING_KEY` | `base64 -w0 secrets/profile-signing.key` | ✅ |
| Secret | `R2_ACCESS_KEY_ID` | R2 Token 的 Access Key ID | ✅ |
| Secret | `R2_SECRET_ACCESS_KEY` | R2 Token 的 Secret Access Key | ✅ |
| Secret | `R2_ENDPOINT` | `https://<account-id>.r2.cloudflarestorage.com` | ✅ |
| Variable | `R2_BUCKET` | `yuki-tunnel-profile` | 可选 |
| Variable | `PROFILE_KEY_ID` | `yuki-profiles-2026` | 可选 |

在 **`yuuuuukiu/yukitunnel`** 仓库：**无**。

---

## 5. 验证

### 5.1 检查是否填对

```bash
gh secret list --repo yuuuuukiu/yukitunnel-profiles
gh variable list --repo yuuuuukiu/yukitunnel-profiles
```

应看到四个 secret + 两个 variable（值不会显示，这是正常的）。

### 5.2 手动跑一次

```
https://github.com/yuuuuukiu/yukitunnel-profiles/actions
  → "Sign and publish registry" → Run workflow
```

期望日志：

```
Sign index.json
  index: profiles/index.json
  signature: profiles/index.json.sig
Sync to Cloudflare R2
  upload: profiles/index.json to s3://yuki-tunnel-profile/profiles/index.json
  synced to s3://yuki-tunnel-profile/profiles/
Verify public mirrors agree
  OK    https://cdn.jsdelivr.net/...  a1b2c3d4
  OK    https://raw.githubusercontent.com/...  a1b2c3d4
  OK    https://tunnel.yuuuuukiu.top/...  a1b2c3d4
  all reachable mirrors agree
```

### 5.3 客户端验签

```bash
./bin/yuki-server profile index
```

期望：

```
来源：https://cdn.jsdelivr.net/gh/yuuuuukiu/yukitunnel-profiles@main
状态：已验签（key_id: yuki-profiles-2026）
```

如果显示 `builtin`，说明镜像还没生效，客户端退回了内置副本。

---

## 6. 常见错误

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `PROFILE_SIGNING_KEY is not set` | 填在了主仓库 | 改填 registry 仓库 |
| `bad decrypt` / `illegal base64` | 填了 PEM 原文而非 base64 | `base64 -w0` 后重填 |
| R2 同步被跳过（warning） | `R2_ENDPOINT` 为空 | 检查变量名是否拼错 |
| `AccessDenied` on R2 | Token 没勾这个 bucket | 重新建 token，勾 `yuki-tunnel-profile` |
| 客户端报「签名密钥不受信任」 | `PROFILE_KEY_ID` 与代码不一致 | 两边改成同一个 |
| `tunnel.yuuuuukiu.top` 404 | 自定义域没连或 DNS 未生效 | 见 DEPLOYMENT.md §6.4 |
| mirrors disagree | jsDelivr 缓存未刷新 | 等 5-10 分钟重跑 |

---

## 7. 安全提醒

- 私钥只存在两个地方：`secrets/profile-signing.key`（本地）和 GitHub Secret
- `secrets/` 已在 `.gitignore` 里，`git status` 不该看到它
- R2 Token 只授权单个 bucket，泄露也无法影响其他资源
- 不要在 PR 里粘贴任何密钥；fork PR 拿不到 secrets，但仍不要冒险
- 详见 [SECRETS.md](SECRETS.md)
