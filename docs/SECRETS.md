# 密钥与凭证管理

本文说明 YukiTunnel 仓库需要的所有密钥、它们存放的位置，以及为什么不会泄露。

---

## 1. 谁会拿到什么

| 密钥 | 存放位置 | 谁能读 |
| --- | --- | --- |
| `PROFILE_SIGNING_KEY`（ed25519 私钥） | 主仓库 + registry 仓库的 Actions Secret | 仅有写权限的 workflow；fork PR 拿不到 |
| `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` | registry 仓库 Actions Secret | 同上 |
| `R2_ENDPOINT` | registry 仓库 Actions Secret | 同上（不是敏感值，但一起放） |
| 云厂商 AK/SK（阿里云/腾讯云） | **不进 CI**，只在你的 shell 里 | 只有你 |
| 服务端管理员密码 | 部署时的 `.env` 或 secret manager | 只有你 |
| 云凭证加密密钥 `YUKI_CREDENTIAL_KEY` | 服务器本地文件 / 容器 secret | 只有你 |

---

## 2. GitHub Secrets 为什么安全

### 2.1 只写不读

```
Settings → Secrets and variables → Actions → New repository secret
```

设置后：

- UI 只显示名称，值永远不回显
- REST / GraphQL API 只返回名称，不返回值
- 无法"下载"已设置的 secret

### 2.2 日志自动打码

GitHub 会把 secret 值从日志中替换成 `***`。即使你写了：

```yaml
run: echo "${{ secrets.R2_SECRET_ACCESS_KEY }}"
```

日志里也只会出现 `***`。

> 注意：**派生值不会自动打码**。例如 `echo "$SECRET" | base64` 输出的是新字符串，
> GitHub 认不出来。所以不要把 secret 转码后回显。

### 2.3 fork PR 拿不到 secrets

这是 GitHub 的硬隔离：

| 事件 | secrets 是否可用 |
| --- | --- |
| `push`（同仓库） | ✅ |
| `workflow_dispatch` | ✅ |
| `pull_request`（来自 fork） | ❌ 全部为空 |
| `pull_request_target` | ✅ **危险**，我们不用它 |

YukiTunnel 的两个 workflow 都只监听 `push` / `workflow_dispatch`：

```yaml
on:
  push:
    paths: ['profiles/**']
  workflow_dispatch:
```

没有 `pull_request_target`，所以不存在"外部 PR 改 workflow 文件偷 secret"的路径。

### 2.4 条件跳过不会泄漏

缺 secret 时的行为：

```yaml
if: ${{ env.R2_ENDPOINT != '' }}   # 为空则跳过
```

跳过的是整个 step，不会打印任何值，也不会让 workflow 变红。

---

## 3. 最小权限清单

### 3.1 GitHub 仓库权限

`Settings → Actions → General → Workflow permissions`：

- 选 **Read repository contents and packages permissions**
- 只有需要提交签名的 registry 仓库才需要 **Read and write**（workflow 里已用
  `permissions: contents: write` 显式声明，作用域仅限该 job）

### 3.2 Cloudflare R2 Token

```
R2 → Manage R2 API Tokens → Create API Token
```

| 项 | 建议值 |
| --- | --- |
| Permissions | **Object Read & Write**（不要 Admin） |
| Specify bucket | **只勾 `yuki-tunnel-profile`** |
| TTL | 按需，或永久 + 定期轮换 |
| Client IP filtering | 可选，留空即可（GitHub runner IP 不固定） |

即使 token 泄露，攻击者也只能写这一个 bucket 的对象，不能删 bucket、不能碰你的
其他 R2 资源、不能访问账户 API。

### 3.3 Cloudflare 账户 API Token（不需要）

我们不用它。R2 的 S3 兼容 API 只认 R2 Token，与账户级 API Token 是两套东西。

---

## 4. 云厂商 AK/SK

**永远不要**把阿里云 / 腾讯云的 AK 放进 GitHub Secrets 或仓库。

它们只在两处出现：

1. **你的本地 shell**，跑 `scripts/live-test-*.sh` 时临时 export
2. **服务端数据库**，经 AES-256-GCM 加密后存储（用户通过 WebUI/CLI 录入）

联调结束立刻禁用子账号的 AccessKey：

```bash
# 阿里云
aliyun ram DeleteAccessKey --UserName yuki-live-test --UserAccessKeyId LTAI...

# 腾讯云
tccli cam DeleteAccessKey --TargetUin <uin> --AccessKeyId AKID...
```

---

## 5. 轮换

| 密钥 | 频率 | 步骤 |
| --- | --- | --- |
| `PROFILE_SIGNING_KEY` | 每年，或泄露时立即 | 生成新密钥 → 改 `builtinProfileKeyB64` + `builtinProfileKeyID` → 重签 index → 发版 |
| R2 Token | 每 6-12 个月 | R2 控制台 roll → 更新两个 secret |
| `YUKI_CREDENTIAL_KEY` | 泄露时 | `yuki-server profile rekey`（见 SECURITY.md） |
| 云厂商 AK | 联调后立即 | 删除子账号 AccessKey |

签名密钥轮换是**破坏性**的：旧二进制只认旧公钥，新 index 用新密钥签会导致旧客户端
验签失败。所以必须：

1. 新公钥写进新版本二进制
2. 发布新版本
3. 等用户升级后，再用新密钥签 index

过渡期可让 `builtinProfileKeys()` 同时返回新旧两把公钥（已支持多 key 映射）。

---

## 6. 自检

```bash
# 仓库里没有签名材料
git ls-files | grep -E '(\.pem$|\.key$|\.p12$)' && echo "LEAK" || echo "clean"

# 没有硬编码的云凭证
git ls-files | xargs grep -lE 'LTAI[0-9A-Za-z]{12,}|AKID[0-9A-Za-z]{20,}' 2>/dev/null || echo "clean"

# workflow 没有在 if 里直接用 secrets（会让整个 workflow 失效）
grep -rn 'if:.*secrets\.' .github/workflows/ && echo "INVALID" || echo "clean"

# registry 仓库的 R2 变量在 job 级（step 级 env 对同级 if 不可见）
grep -n 'R2_ENDPOINT' /path/to/yukitunnel-profiles/.github/workflows/sign.yml
```

CI 里的 `Profiles` workflow 会自动跑前两项检查。
