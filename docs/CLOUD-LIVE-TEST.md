# 真实云联调指南

本指南说明如何用**最小权限子账号**验证云 Profile 在真实账号上可用。
仓库不包含任何凭证，CI 也不运行这些测试。

---

## 1. 为什么需要真实联调

mock 端点能验证「请求形状 + 签名算法」，但验证不了：

- 云厂商是否接受我们的签名串（尤其阿里云 RFC3986 编码、腾讯云 TC3 派生）
- 端口格式是否被正确解析（`8000/8010` vs `8000-8010`）
- 幂等检查的 JSON 路径是否命中真实响应结构
- 错误码是否按预期返回（决定能否给出可读提示）

---

## 2. 准备：阿里云

### 2.1 创建最小权限子账号

1. 登录阿里云控制台 → **RAM 访问控制** → 用户 → 创建用户
2. 勾选「OpenAPI 调用访问」，保存 AccessKey ID / Secret（只显示一次）
3. 创建自定义策略（脚本化方式见下），绑定到该用户
4. **不要**给该用户 `AdministratorAccess`

策略 JSON 见 [ACCEPTANCE-PROFILES.md §3.1](ACCEPTANCE-PROFILES.md#31-阿里云-ram自定义策略)。
把 `sg-REPLACE_WITH_YOUR_SG_ID` 换成你准备测试的安全组 ID。

用 CLI 绑定：

```bash
# 需要 aliyun CLI 且有 RAM 管理权限
aliyun ram CreatePolicy \
  --PolicyName YukiTunnelLiveTest \
  --PolicyDocument "$(cat aliyun-policy.json)" \
  --Description "YukiTunnel live test: 3 ECS security group actions"

aliyun ram AttachPolicyToUser \
  --PolicyName YukiTunnelLiveTest \
  --PolicyType Custom \
  --UserName yuki-live-test
```

### 2.2 准备测试安全组

- 新建一个**专用**安全组（不要用生产安全组），例如 `sg-yukitunnel-test`
- 不绑定任何实例，避免误放行真实流量
- 记下安全组 ID 与地域（如 `cn-hangzhou`）

### 2.3 运行

```bash
export YUKI_ALIYUN_ACCESS_KEY_ID=LTAI...
export YUKI_ALIYUN_ACCESS_KEY_SECRET=...
export YUKI_REGION=cn-hangzhou
export YUKI_SECURITY_GROUP_ID=sg-yukitunnel-test
export YUKI_TEST_PORT=20000

bash scripts/live-test-aliyun.sh
```

---

## 3. 准备：腾讯云

### 3.1 创建最小权限子账号

1. 控制台 → **访问管理 CAM** → 用户 → 新建用户 → 自定义创建
2. 访问方式选「编程访问」，保存 SecretId / SecretKey
3. 创建自定义策略（见 [ACCEPTANCE-PROFILES.md §3.2](ACCEPTANCE-PROFILES.md#32-腾讯云-cam自定义策略)）
4. 关联到子用户；不要授予 `QcloudVPCFullAccess`

```bash
# 使用 tccli（腾讯云 CLI）
tccli cam CreatePolicy \
  --PolicyName YukiTunnelLiveTest \
  --PolicyDocument "$(cat tencent-policy.json)" \
  --Description "YukiTunnel live test"

tccli cam AttachUserPolicy \
  --PolicyId <返回的 PolicyId> \
  --AttachUin <子账号 UIN>
```

### 3.2 运行

```bash
export YUKI_TENCENT_SECRET_ID=AKID...
export YUKI_TENCENT_SECRET_KEY=...
export YUKI_REGION=ap-guangzhou
export YUKI_SECURITY_GROUP_ID=sg-yukitunnel-test
export YUKI_TEST_PORT=20000

bash scripts/live-test-tencent.sh
```

---

## 4. 可选环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `YUKI_TEST_PORT` | `20000` | 测试端口，必须不在现有规则中 |
| `YUKI_TEST_PROTO` | `tcp` | `tcp` 或 `udp` |
| `YUKI_SOURCE_CIDR` | `0.0.0.0/0` | 建议收敛到你的出口 IP `/32` |
| `YUKI_PROXY` | 空 | `http://` 或 `socks5://` 代理 |
| `YUKI_LIVE_BIN` | `bin/yuki-live-test` | 自定义二进制路径 |
| `YUKI_CLOCK_HOST` | Cloudflare trace | 用于校时的 HTTPS 端点 |

---

## 5. 如何解读结果

脚本输出形如：

```
[PASS] clock-skew                          120ms  时间偏差 0.4s（在 15 分钟内）
[PASS] profile-load                          0ms  aliyun-ecs-security-group@1.0.0 sha256=c69948bb2fd0
[PASS] ensure_port_allowed                 412ms  HTTP 200 | ecs.cn-hangzhou.aliyuncs.com | req=…
[PASS] ensure_idempotent                   205ms  already-present | HTTP 200 | req=…
[PASS] revoke_port_allowed                 388ms  HTTP 200 | req=…
[PASS] recreate_after_revoke               401ms  HTTP 200 | req=…

ALL PASS (6 steps)
```

| 步骤 | 含义 | 失败通常意味着 |
| --- | --- | --- |
| `clock-skew` | 本机与网络时间偏差 | 时间偏差 > 15 分钟，所有签名都会失败 |
| `profile-load` | YAML 解析 + 结构校验 + sha256 | profile 文件损坏或被改 |
| `ensure_port_allowed` | 首次放行 | 签名错误 / 权限不足 / 安全组不存在 |
| `ensure_idempotent` | 第二次放行应命中已有规则 | 幂等检查的 JSON 路径不对，或端口格式与真实响应不一致 |
| `revoke_port_allowed` | 回收 | 删除参数与创建不匹配 |
| `recreate_after_revoke` | 回收后能再次创建 | 回收实际没生效 |

### 常见错误对照

| 输出 | 原因 | 处理 |
| --- | --- | --- |
| `SignatureDoesNotMatch` | 阿里云时间偏差或 Secret 错误 | 校准时间；重新复制 Secret（注意首尾空格） |
| `InvalidAccessKeyId.NotFound` | AK 不存在或已禁用 | 检查 AK；确认子账号未停用 |
| `Forbidden.RAM` | 子账号缺权限 | 检查策略绑定的 Action 与 Resource ARN |
| `AuthFailure.SignatureFailure` | 腾讯云 SecretKey 错误或时间偏差 > 5 分钟 | 校准时间；重新复制密钥 |
| `UnauthorizedOperation` | 腾讯云子账号缺权限 | 检查 CAM 策略 |
| `InvalidSecurityGroupId.NotFound` | 安全组不属于该地域 | 检查 Region 与 SG ID |
| `Throttling` / `RequestLimitExceeded` | 触发限流 | 等待 1 分钟后重跑 |

---

## 6. 安全须知

- **只用专用测试安全组**，不要指向生产环境。
- 联调结束后删除子账号 AccessKey（或直接禁用子账号）。
- 不要把这些变量写进 `.env`、shell profile 或 CI secrets；联调是本地一次性操作。
- 脚本失败时会尝试回收端口；若仍有残留，在控制台搜索描述为
  `YukiTunnel managed rule` 的规则手动删除。
- 联调记录里的安全组 ID 请脱敏（`sg-****abcd`）。

---

## 7. 其他厂商

AWS / 华为云 / Azure / GCP 的 profile 已实现并通过 mock 验证，真实联调步骤相同：

```bash
# AWS
export YUKI_AWS_ACCESS_KEY_ID=...
export YUKI_AWS_SECRET_ACCESS_KEY=...
export YUKI_REGION=us-east-1
./bin/yuki-live-test -file profiles/aws-ec2-security-group.yaml -port 20000 \
  -param region=us-east-1 -param security_group_id=sg-xxx

# 华为云（需先换取 IAM Token）
export YUKI_HUAWEI_IAM_TOKEN=...
export YUKI_HUAWEI_PROJECT_ID=...
./bin/yuki-live-test -file profiles/huawei-vpc-security-group.yaml -port 20000 \
  -param region=cn-north-4 -param project_id=$YUKI_HUAWEI_PROJECT_ID \
  -param security_group_id=<sg-uuid>
```

这两条路径**尚未经过真实账号验证**，结果请记录到
[ACCEPTANCE-PROFILES.md](ACCEPTANCE-PROFILES.md#4-联调记录模板) 的模板中。
