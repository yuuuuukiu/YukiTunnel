# Profile Registry 与云厂商参数对照

YukiTunnel 的「一键放行端口」不是把 API 密钥塞进服务端，而是执行一份**纯声明式 YAML profile**：
profile 描述「调用哪个 API、传什么参数、如何签名」，由内置签名引擎在本地完成签名，服务端只负责编排与审计。

## 1. 六家厂商官方参数差异（已核对官方文档）

| 维度 | 阿里云 ECS `AuthorizeSecurityGroup` | 腾讯云 VPC `CreateSecurityGroupPolicies` | AWS EC2 `AuthorizeSecurityGroupIngress` | 华为云 VPC `CreateSecurityGroupRule` | Azure NSG `SecurityRules_CreateOrUpdate` | GCP `firewalls.insert` |
| --- | --- | --- | --- | --- | --- | --- |
| 协议风格 | RPC（Query/Form） | JSON-RPC（TC3） | Query API（SigV4） | REST（AK/SK） | REST（OAuth2 / Entra） | REST（OAuth2 SA） |
| 签名算法 | `HMAC-SHA1`，`SignatureVersion=1.0`，RFC3986 编码 + `StringToSign` = `POST&%2F&percentEncode(canonicalizedQuery)` | `TC3-HMAC-SHA256`：`CanonicalRequest` → `StringToSign` → 派生签名密钥 `TC3+SecretKey` | `AWS4-HMAC-SHA256`，`Credential=AK/date/region/ec2/aws4_request` | `AK/SK` → 先取 IAM token，再 `X-Auth-Token`；请求体 `{"security_group_rule":{...}}` | `Authorization: Bearer <entra_token>`，无请求级签名 | `Authorization: Bearer <sa_access_token>`，无请求级签名 |
| 入方向标识 | `IpProtocol=tcp` + `PortRange=8000/8010` + `SourceCidrIp=0.0.0.0/0` + `Policy=accept` | `SecurityGroupPolicySet.Ingress[]` 中 `Protocol=TCP`、`Port="8000-8010"`、`CidrBlock`、`Action=ACCEPT` | `IpPermissions.N.IpProtocol=tcp`、`FromPort`/`ToPort`、`IpRanges.N.CidrIp` | `direction=ingress`、`protocol=tcp`、`port_range_min`/`port_range_max`、`remote_ip_prefix` | `properties.direction=Inbound`、`protocol=Tcp`、`destinationPortRange="8000-8010"`、`sourceAddressPrefix`、`access=Allow` | `direction=INGRESS`、`allowed[].IPProtocol=tcp`、`allowed[].ports=["8000-8010"]`、`sourceRanges` |
| 端口写法 | **`起始/结束`**（斜杠分隔，单端口也写 `80/80`） | **`起始-结束`**（连字符；单端口写 `80`） | 两个整数字段 `FromPort`/`ToPort` | 两个整数字段 `port_range_min`/`port_range_max` | 字符串 `"80"` 或 `"8000-8010"` | 字符串数组 `["80"]` 或 `["8000-8010"]` |
| 协议大小写 | 小写 `tcp`/`udp`/`icmp` | **大写** `TCP`/`UDP`/`ICMP`/`ALL` | 小写 `tcp`/`udp`/`icmp`/`-1` | 小写 | **首字母大写** `Tcp`/`Udp`/`Icmp`/`*` | 小写 `tcp`/`udp`/`icmp` |
| 规则优先级 | `Priority`（1-100，**越小越高**） | `PolicyIndex`（插入位置，非优先级） | 无（全部 allow，无优先级） | 无显式优先级 | `priority`（100-4096，**越小越高**，必填且唯一） | `priority`（0-65535，越小越高，默认 1000） |
| 撤销方式 | `RevokeSecurityGroup`（同参数），或按 `SecurityGroupRuleId` | `DeleteSecurityGroupPolicies`（按 `PolicyIndex` 或完整规则） | `RevokeSecurityGroupIngress`（同参数） | `DeleteSecurityGroupRule`（按 rule_id） | `SecurityRules_Delete`（按规则名） | `firewalls.delete`（按规则名） |
| 幂等能力 | `ClientToken` | `ClientToken`（部分接口） | **天然幂等失败**：重复规则报 `InvalidPermission.Duplicate` | 重复创建返回 409 | `PUT` 同名即更新（**天然幂等**） | 同名 `insert` 报 `alreadyExists` |
| 规则唯一键 | 五元组（方向/协议/端口/源/策略） | 五元组 + `PolicyIndex` | 五元组 | 五元组 | **规则名** | **规则名** |
| 必填上下文 | `RegionId`、`SecurityGroupId` | `Region`、`SecurityGroupId` | `Region`、`GroupId` | `project_id`、`security_group_id` | `subscriptionId`、`resourceGroupName`、`nsgName`、`ruleName` | `project`、`network`、`name` |
| 端点示例 | `ecs.{region}.aliyuncs.com` | `vpc.tencentcloudapi.com` | `ec2.{region}.amazonaws.com` | `vpc.{region}.myhuaweicloud.com` | `management.azure.com` | `compute.googleapis.com` |

### 由差异推导出的引擎抽象

| 抽象 | 说明 |
| --- | --- |
| `port_format` | `slash` / `dash` / `split_fields` / `string` / `string_array` |
| `protocol_case` | `lower` / `upper` / `title` |
| `direction_field` | `ingress_flag` / `direction_enum` / `implicit` |
| `idempotency` | `client_token` / `duplicate_error` / `put_upsert` / `named_lookup` |
| `revoke_by` | `same_params` / `policy_index` / `rule_id` / `rule_name` |

profile 用这些字段声明差异，引擎按声明渲染请求，**不写 if-else 分支到代码里**。

## 2. Profile 结构

```yaml
apiVersion: yukitunnel.io/v1
kind: CloudProfile
metadata:
  id: aliyun-ecs-security-group
  name: 阿里云 ECS 安全组
  vendor: aliyun
  version: 1.0.0
  maintainer: yukitunnel
spec:
  auth:
    engine: aliyun-hmac-sha1          # 内置签名引擎
    credential_fields: [access_key_id, access_key_secret]
  endpoint:
    template: "https://ecs.{{region}}.aliyuncs.com/"
    method: POST
    content_type: application/x-www-form-urlencoded
  allowed_hosts:                       # 白名单：引擎拒绝访问其他域名
    - "ecs.*.aliyuncs.com"
  permissions:                         # 最小权限声明，UI 展示给用户确认
    - "ecs:AuthorizeSecurityGroup"
    - "ecs:RevokeSecurityGroup"
    - "ecs:DescribeSecurityGroups"
  parameters:                          # 用户需填写的上下文
    - { name: region, required: true, example: cn-hangzhou }
    - { name: security_group_id, required: true }
    - { name: source_cidr, default: "0.0.0.0/0" }
  actions:
    ensure_port_allowed:
      operation: AuthorizeSecurityGroup
      params: { ... 模板 ... }
      idempotent_check:
        operation: DescribeSecurityGroups
        match: { ... }
      success_when: { status: 200 }
    revoke_port_allowed:
      operation: RevokeSecurityGroup
      params: { ... }
```

### 模板语法（刻意保持极简，避免图灵完备）

| 语法 | 含义 |
| --- | --- |
| `{{ .region }}` | 引用参数 |
| `{{ port_range "slash" .port_min .port_max }}` | 按厂商格式渲染端口 |
| `{{ proto "lower" .protocol }}` | 按厂商大小写渲染协议 |
| `{{ default .source_cidr "0.0.0.0/0" }}` | 默认值 |
| `{{ json .payload }}` | 序列化为 JSON 字符串 |

## 3. Registry 与多源回退

```
GitHub repo: yuuuuukiu/yukitunnel-profiles
  profiles/
    aliyun-ecs-security-group.yaml
    tencent-vpc-security-group.yaml
    aws-ec2-security-group.yaml
    huawei-vpc-security-group.yaml
    azure-nsg.yaml
    gcp-firewall.yaml
  index.json          # CI 生成
  index.json.sig      # CI 用 ed25519 私钥签名
```

`index.json`：

```json
{
  "schema": 1,
  "generated_at": "2026-10-20T12:00:00Z",
  "key_id": "yuki-profiles-2026",
  "profiles": [
    {
      "id": "aliyun-ecs-security-group",
      "vendor": "aliyun",
      "version": "1.0.0",
      "sha256": "…",
      "path": "profiles/aliyun-ecs-security-group.yaml"
    }
  ]
}
```

拉取顺序（每个源都必须通过签名校验，失败即跳过并记录）：

1. jsDelivr `https://cdn.jsdelivr.net/gh/yuuuuukiu/yukitunnel-profiles@main/`
2. GitHub raw `https://raw.githubusercontent.com/yuuuuukiu/yukitunnel-profiles/main/`
3. 品牌镜像 `https://tunnel.yuuuuukiu.top/`（Cloudflare R2，需要 DNS 就绪）
4. 用户自定义镜像（`--profile-mirror` / 设置页）
5. 二进制内置副本（仅在**传输失败**时启用，见 §4bis）
6. 本地缓存（仅在签名仍有效时使用，用于离线）

> 顺序只影响延迟，不影响信任：每个源都要过 ed25519 验签。
> 把 GitHub 系放在前面，是为了让「仓库刚公开、R2 还没配好」时也能立即使用。

代理：`--proxy http://…` 或 `socks5://…`，通过 `http.Transport.Proxy` 统一生效，**签名校验在解包前、代理之后仍然执行**。

## 4. 实现映射（代码 → 设计）

| 设计元素 | 代码位置 |
| --- | --- |
| profile schema 与校验 | `internal/cloudprofile/profile.go`（`Validate()` 强制 `allowed_hosts` / `permissions` / 两个 action / `idempotent_check`） |
| 模板与端口/协议渲染 | `internal/cloudprofile/template.go`（`port_range`、`proto`、`default`、`json`） |
| 执行引擎、幂等探测、host 白名单 | `internal/cloudprofile/engine.go` |
| 四个签名引擎 | `internal/cloudprofile/signers.go`（`aliyun-hmac-sha1`、`tencent-hmac-sha256`、`aws-sigv4`、`huawei-aksk`，另含 `azure-oauth2`、`gcp-oauth2`） |
| 多源回退 + 验签 + 缓存 | `internal/cloudprofile/registry/` |
| CI 签名器 | `cmd/yuki-profile-sign` |
| 服务端 API | `internal/server/api_profiles.go` |
| 服务端 CLI | `internal/servercli/profile.go` + `profile_local.go` |
| WebUI | `web/src/pages/admin/Profiles.tsx` |
| 官方 profile | `profiles/*.yaml` |

### 一个容易踩的坑（已修复）

阿里云与 AWS 走 RPC/Query 协议，`Action` 必须作为**表单参数**出现在请求体里，
而不是 URL 路径或签名之外。引擎最初只把 `Action` 用在签名串上，导致真实请求被
阿里云拒绝（`MissingParameter`）。现在 `buildRequest` 会在 `x-www-form-urlencoded`
分支自动注入 `Action=<operation>`，profile 无需重复声明。
回归测试：`TestRPCProvidersIncludeActionParam`。

## 4bis. 离线回退与它的边界

二进制内置了 6 个官方 profile（`internal/cloudprofile/bundled/`），因此
内网隔离环境也能开箱使用。回退条件是**严格区分**的：

| 情况 | 行为 | 理由 |
| --- | --- | --- |
| 镜像 DNS 失败 / 连接拒绝 / 超时 | 回退到内置副本，标注 `source: builtin`、`offline: true` | 纯传输问题，内容本身没有可疑之处 |
| 镜像返回 index.json 但无 `.sig` | **拒绝**，`PROFILE_INTEGRITY_FAILURE` | 能发索引却拿不出签名，不能证明发布者身份 |
| 签名不匹配 / key 不可信 / schema 错 | **拒绝**，不回退缓存也不回退内置 | 唯一安全解释是有人在篡改 |
| 索引验签通过但文档 sha256 不符 | **拒绝** | 同上 |

这条边界很关键：如果「验签失败」也允许回退，攻击者只要阻断镜像就能把客户端
降级到未验证内容。回归测试：`TestOfflineFallbackOnlyOnTransportFailure`。

内置副本与 `profiles/*.yaml` 必须逐字节一致，由 `TestBundledProfilesMatchRepository`
守护；更新 profile 后请执行：

```bash
cp profiles/*.yaml internal/cloudprofile/bundled/
go test ./internal/cloudprofile -run Bundled
```

## 5. 安全设计

- **签名永远校验**：先取 `index.json` + `index.json.sig`，用内置公钥验签；再按 index 里的 `sha256` 校验每个 profile。任一环节失败 → 该源作废。
- **密钥不落盘**：云厂商 AK/SK 只存在于内存或系统钥匙串，审计日志只记录 `access_key_id` 前缀。
- **allowed_hosts 强制**：签名引擎在发起请求前校验目标 host 命中白名单，防止恶意 profile 把凭证发到外部。
- **permissions 明示**：执行前 UI 展示 profile 声明的权限集合，用户确认后才运行。
- **只读探测优先**：`idempotent_check` 先跑 Describe/List，已放行则跳过写操作。
- **审计**：每次执行记录 profile id/version/sha256、动作、端口、结果、耗时，不记录凭证。
