# 云 Profile 验收与联调记录

本文分三部分：**已自动化的测试覆盖**、**真实云联调检查表**、**联调记录模板**。

---

## 1. 测试覆盖清单

以下全部由 `go test ./...` 自动执行，无需凭证。

### 1.1 Profile 结构与执行引擎（`internal/cloudprofile`）

| 测试 | 覆盖点 |
| --- | --- |
| `TestOfficialProfilesAreValid` | 6 个官方 profile 全部可解析、通过 `Validate()`，两个 action 齐备，`allowed_hosts` / `permissions` 非空 |
| `TestPortFormatDiffersPerVendor` | 阿里云 `20000/20100`、腾讯云 `20000-20100` 等端口方言 |
| `TestPortRangeFormats` | `slash` / `dash` / `string` 三种渲染 |
| `TestProtocolCasing` | `lower` / `upper` / `title`，以及 `all → -1` / `*` |
| `TestAliyunSignatureIsStable` | HMAC-SHA1、`SignatureMethod`、RFC3986 百分号编码 |
| `TestTencentSignatureHeaders` | TC3 凭据范围、`X-TC-Action`、`X-TC-Region` |
| `TestAWSSigV4Headers` | `AWS4-HMAC-SHA256`、`/region/ec2/aws4_request` 作用域 |
| `TestHuaweiRequiresToken` | 无 IAM Token 报错，有则写入 `X-Auth-Token` / `X-Project-Id` |
| `TestHostAllowlistBlocksExfiltration` | 恶意 endpoint 被 `allowed_hosts` 拒绝 |
| `TestHTTPEndpointRejected` | 非 https 端点拒绝 |
| `TestMatchHostWildcards` | `ecs.*.aliyuncs.com` 等通配规则，含 `amazonaws.com.evil.io` 反例 |
| `TestIdempotentCheckSkipsWrite` | 规则已存在时只发 1 次探测请求，不发写请求 |
| `TestDryRunDoesNotCallProvider` | dry-run 不触网 |
| `TestMissingRequiredParameter` | 必填参数缺失报可读错误 |
| `TestUnknownActionRejected` | 未声明的 action 拒绝 |
| `TestUnsupportedEngineRejected` | 未知签名引擎在参数校验前报错 |
| `TestValidateRejectsMissingFields` | 缺 `vendor` / `allowed_hosts` / `permissions` / action 时一次性列出全部问题 |
| `TestRPCProvidersIncludeActionParam` | 阿里云 RPC 请求体包含 `Action`（曾漏发导致真实调用失败） |
| `TestTencentBodyEnvelope` | `SecurityGroupPolicySet.Ingress[]` 包装 + 大写协议 + 连字符端口 |
| `TestGCPPortsArray` | `allowed[].ports[]` 字符串数组 |

### 1.2 Registry 与签名（`internal/cloudprofile/registry`）

| 测试 | 覆盖点 |
| --- | --- |
| `TestRefreshVerifiesSignature` | 正常索引验签通过并记录来源 |
| `TestTamperedSignatureRejected` | 签名被篡改 → 拒绝 |
| `TestUntrustedKeyRejected` | key_id 不在信任列表 → 拒绝 |
| `TestMirrorFallback` | 坏镜像 → 自动回退到下一个，`OnFallback` 被调用 |
| `TestProfileHashMismatchRejected` | index 的 `sha256` 与文档不符 → 拒绝 |
| `TestLoadVerifiedProfile` | 加载成功并回填 `Source` / `SHA256` |
| `TestCacheStillVerified` | 镜像全挂时用缓存，且缓存仍需验签 |
| `TestCacheTamperingRejected` | 磁盘缓存被改 → 拒绝 |
| `TestNoTrustedKeysIsFatal` | 没有信任密钥时拒绝启动 |
| `TestProxyURLValidation` | `http`/`https`/`socks5` 通过，`ftp` 拒绝 |
| `TestVerifyIndexRejectsBadSchema` | schema ≠ 1 拒绝 |
| `TestVerifyIndexRejectsNonEd25519` | 非 ed25519 算法拒绝 |
| `TestLoadUnknownProfile` | 不存在的 id 报 not found |

### 1.3 API 与安全（`internal/server`）

| 测试 | 覆盖点 |
| --- | --- |
| `TestBuiltinKeyIsNotPlaceholder` | 占位密钥在 `YUKI_RELEASE=1` 下拒绝启动；真实密钥可解析 |
| `TestProfileIndexRejectsUnsignedMirror` | 无 `.sig` 的镜像不产生「已验证」索引 |
| `TestProfileInstallRequiresHash` | 本地导入必须显式提供 sha256 |
| `TestProfileInstallAndList` | 安装后出现在列表，哈希一致 |
| `TestProfileCredentialFieldValidation` | 未声明字段拒绝；响应中值为 `****` |
| `TestProfileRunRequiresAcknowledgement` | 未确认权限 → 400 |
| `TestProfileRunRequiresCredentials` | 无凭证 → 400 |
| `TestProfileDryRunRecordsRun` | dry-run 记录到 `profile_runs` |
| `TestProfileSettingsProxyValidation` | 非法代理 → 400 |
| `TestProfileDisabledBlocksExecution` | 禁用后执行 → 403 |
| `TestCredentialsAPIResponseMasked` | **AK/SK 在响应中为 `****` / `AKIA****`** |
| `TestSignatureCannotBeBypassedByPolicy` | profile 自称 `profile_policy: allow` 但签名无效 → 仍拒绝 |
| `TestAuditLogCoversProfileCalls` | 审计含 actor / target / action / timestamp；run 表含端口/协议/哈希 |
| `TestCredentialAuditOmitsSecrets` | 审计不含明文 |

### 1.4 凭证静态加密（`internal/store`）

| 测试 | 覆盖点 |
| --- | --- |
| `TestCredentialsEncryptedAtRest` | 直接读 DB 列与 DB 文件均无明文；解密可还原 |
| `TestCredentialKeyFromEnv` | `YUKI_CREDENTIAL_KEY` 优先于文件 |
| `TestCredentialKeyRejectsShortEnvKey` | 过短密钥拒绝 |
| `TestCredentialKeyFilePermissions` | 密钥文件 0600 |
| `TestOpenCredentialsRejectsGarbage` | 损坏密文报错而非 panic |
| `TestRotateCredentialKey` | 旧密钥 → 新密钥迁移后仍可解密 |
| `TestMaskCredentials` / `TestMaskSecret` | 脱敏规则 |

### 1.5 系统防火墙（`internal/firewall`）

| 测试 | 覆盖点 |
| --- | --- |
| `TestDetectUFWDeny` / `TestDetectUFWAllowDefault` / `TestDetectUFWInactive` | 三种 ufw 状态 |
| `TestDetectUFWWithoutRoot` | 非 root 时报告不可管理 |
| `TestDetectFirewalldRunning` / `TestDetectFirewalldStopped` | firewalld 状态（`not running` 不被误判） |
| `TestDetectNftables` / `TestDetectNone` | nft 与无防火墙 |
| `TestDetectUnsupportedPlatforms` | Windows/macOS 返回 Unsupported 且操作 Skipped |
| `TestSkipWhenDisabled` / `TestSkipWhenDefaultAllow` / `TestSkipWhenPolicyUnknown` | 三种跳过条件 |
| `TestUFWAllowAndRevoke` / `TestUFWAlreadyExistsIsSkipped` / `TestUFWPermissionDeniedIsError` | ufw 放行/回收/幂等/权限错误 |
| `TestFirewalldAllowAndRevoke` | permanent + runtime 双写 |
| `TestNftablesAllowIsIdempotent` / `TestNftablesCreatesChainWhenMissing` | nft 幂等与建链 |
| `TestReconcileRemovesOrphans` | 只删带 `yukitunnel` 标记的规则，绝不碰 SSH |
| `TestReconcileSkippedWhenNotManaged` | 不满足条件时不做任何枚举 |
| `TestParseUFWRule` / `TestFindNFTHandle` / `TestRuleKeyNormalisation` | 解析与归一化 |

---

## 2. 真实云联调检查表

> 真实联调需要云账号。**本仓库不包含任何凭证**，也不在 CI 中运行。
> 执行方式见 [CLOUD-LIVE-TEST.md](CLOUD-LIVE-TEST.md)。

### 2.1 阿里云 ECS

| # | 检查项 | 命令 / 观察点 | 通过标准 |
| --- | --- | --- | --- |
| A1 | 签名被接受 | `bash scripts/live-test-aliyun.sh` | 无 `SignatureDoesNotMatch` |
| A2 | 规则出现 | 脚本 `ensure_port_allowed` 步 | `[PASS]`，控制台可见 `20000/20000` |
| A3 | 幂等 | 脚本 `ensure_idempotent` 步 | `already-present`，规则数不增加 |
| A4 | 回收 | 脚本 `revoke_port_allowed` 步 | 控制台规则消失 |
| A5 | 回收后重建 | 脚本 `recreate_after_revoke` 步 | `[PASS]`，随后自动清理 |
| A6 | 错误凭证可读 | 用假 AK 跑脚本 | 输出 `InvalidAccessKeyId.NotFound` + 排查提示 |
| A7 | 权限不足可读 | 用无 ECS 权限的子账号 | 输出 `Forbidden.RAM` |

### 2.2 腾讯云 VPC

| # | 检查项 | 命令 / 观察点 | 通过标准 |
| --- | --- | --- | --- |
| T1 | 签名被接受 | `bash scripts/live-test-tencent.sh` | 无 `AuthFailure.SignatureFailure` |
| T2 | 规则出现 | `ensure_port_allowed` 步 | `[PASS]`，控制台可见 `TCP:20000` |
| T3 | 幂等 | `ensure_idempotent` 步 | `already-present`，`PolicyIndex` 不增加 |
| T4 | 回收 | `revoke_port_allowed` 步 | 控制台规则消失 |
| T5 | 不影响其他规则 | 回收前后对比 `DescribeSecurityGroupPolicies` | 仅目标规则变化 |
| T6 | 错误凭证可读 | 假 SecretKey | `AuthFailure.SignatureFailure` |
| T7 | 权限不足可读 | 无 VPC 权限子账号 | `UnauthorizedOperation` |

### 2.3 错误路径通用检查

| 场景 | 期望行为 |
| --- | --- |
| 系统时间偏差 > 15 分钟 | 脚本在第一步就 FAIL，并提示校准时间 |
| 安全组 ID 不属于该 Region | `InvalidSecurityGroupId` / `ResourceNotFound`，脚本打印排查清单 |
| 端口已被同五元组规则占用 | 幂等检查命中，返回 `already-present`，不报错 |
| 网络中断 | `request to <host> failed`，不含凭证 |
| 代理配置错误 | `invalid proxy` / 连接失败，不影响签名逻辑 |

---

## 3. 最小权限策略

### 3.1 阿里云 RAM（自定义策略）

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecs:DescribeSecurityGroups",
        "ecs:AuthorizeSecurityGroup",
        "ecs:RevokeSecurityGroup"
      ],
      "Resource": [
        "acs:ecs:*:*:securitygroup/sg-REPLACE_WITH_YOUR_SG_ID"
      ]
    }
  ]
}
```

要点：

- 只授予三个 Action，不授予 `ecs:*`。
- `Resource` 收敛到单个安全组 ARN，避免影响同账号其他安全组。
- 需要跨地域时把 `*` 展开成明确的地域列表，不要用全通配。
- 建议额外加一条 `Deny` 阻止删除安全组：`ecs:DeleteSecurityGroup`。

### 3.2 腾讯云 CAM（自定义策略）

```json
{
  "version": "2.0",
  "statement": [
    {
      "effect": "allow",
      "action": [
        "vpc:DescribeSecurityGroupPolicies",
        "vpc:CreateSecurityGroupPolicies",
        "vpc:DeleteSecurityGroupPolicies"
      ],
      "resource": [
        "qcs::vpc:ap-guangzhou:uin/REPLACE_WITH_YOUR_UIN:securitygroup/sg-REPLACE_WITH_YOUR_SG_ID"
      ]
    }
  ]
}
```

要点：

- `resource` 使用六段式 ARN，地域与安全组都要写死。
- 不授予 `vpc:*`；特别不要授予 `vpc:DeleteSecurityGroup`。
- 子账号建议开启 MFA，AK 仅用于联调，联调后立即禁用。

### 3.3 其他厂商（参考，当前仅 mock 验证）

| 厂商 | 最小权限 |
| --- | --- |
| AWS | `ec2:DescribeSecurityGroups`, `ec2:AuthorizeSecurityGroupIngress`, `ec2:RevokeSecurityGroupIngress`，Resource 限定 `arn:aws:ec2:region:account:security-group/sg-xxx` |
| 华为云 | `vpc:securityGroupRules:create/delete/list`，项目级授权 |
| Azure | `Microsoft.Network/networkSecurityGroups/securityRules/read|write|delete`，作用域限定到单个 NSG |
| GCP | `compute.firewalls.create/delete/get`，建议自定义 Role 并绑定到项目 |

---

## 4. 联调记录模板

每次联调复制一份填写，提交到 `docs/live-test-records/`（该目录可选，含账号信息时不要提交）。

```markdown
### 联调记录

- 厂商：阿里云 / 腾讯云
- 日期：2026-__-__
- 执行人：
- 客户端版本：`yuki-server --version` → 
- profile 版本 / sha256：`aliyun-ecs-security-group@1.0.0` / `c69948bb2fd0…`
- 地域：cn-hangzhou
- 安全组 ID（脱敏）：`sg-****abcd`
- AK 权限范围：仅 ecs:DescribeSecurityGroups / AuthorizeSecurityGroup / RevokeSecurityGroup
- AK 类型：RAM 子账号（MFA 已开启）
- 测试端口：20000/tcp

| 步骤 | 结果 | 耗时 | 备注 |
| --- | --- | --- | --- |
| clock-skew | PASS | 120ms | 偏差 0.4s |
| profile-load | PASS | 0ms | sha256 校验通过 |
| ensure_port_allowed | PASS | 412ms | HTTP 200, req=… |
| ensure_idempotent | PASS | 205ms | already-present |
| revoke_port_allowed | PASS | 388ms | HTTP 200 |
| recreate_after_revoke | PASS | 401ms | 已自动清理 |

- 遗留规则：无
- 异常与处理：
- 结论：通过 / 不通过
```

---

## 5. 已知限制

| 限制 | 说明 |
| --- | --- |
| AWS / 华为云 / Azure / GCP 未做真实联调 | 仅通过 mock 端点验证请求形状、签名头与 body 结构；真实账号验证待补 |
| Azure / GCP 需要预先获取 OAuth2 Token | 引擎不实现 OAuth2 授权码流程，Token 由外部获取后传入 |
| 华为云需要预先换取 IAM Token | 引擎只接受 `iam_token`，不代做 AK/SK → Token 交换 |
| 腾讯云删除依赖五元组精确匹配 | 若控制台手工改过规则（如改描述），删除可能不命中，需人工清理 |
| 阿里云 `NicType` 固定 `intranet` | 经典网络需要改成 `internet`，当前 profile 未参数化 |
| 云 API 限流 | profile 声明 `rate_limit_per_minute: 60`，但引擎尚未强制；高频创建需自行退避 |
| R2 同步需要仓库 secrets | fork PR 不会执行上传（workflow 已做条件跳过） |
