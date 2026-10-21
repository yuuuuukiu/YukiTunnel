# Profile 开发指南

Profile 是一份纯声明式 YAML，描述「如何在某个云平台上放行/回收端口」。
新增一个云厂商**不需要改 Go 代码**，只需要提交一份通过校验的 YAML。

---

## 1. 文件位置与命名

```
profiles/
├── aliyun-ecs-security-group.yaml
├── tencent-vpc-security-group.yaml
├── aws-ec2-security-group.yaml
├── huawei-vpc-security-group.yaml
├── azure-nsg.yaml
├── gcp-firewall.yaml
├── index.json          # CI 生成，勿手工编辑
└── index.json.sig      # CI 生成，勿手工编辑
```

命名规则：`<vendor>-<product>-<resource>.yaml`，`metadata.id` 与文件名一致（不含 `.yaml`）。

---

## 2. YAML 结构

```yaml
apiVersion: yukitunnel.io/v1     # 固定（schema 标识，不随仓库迁移改变）
kind: CloudProfile               # 固定
metadata:
  id: aliyun-ecs-security-group  # ^[a-z0-9][a-z0-9-]{2,62}$
  name: 阿里云 ECS 安全组
  vendor: aliyun                 # aliyun|tencent|aws|huawei|azure|gcp
  version: 1.0.0                 # 语义化版本
  maintainer: yukitunnel
  min_engine: 0.2.0              # 需要的最低引擎版本
  description: |
    多行说明，会显示在 WebUI。
  docs:
    - https://help.aliyun.com/...

spec:
  auth:
    engine: aliyun-hmac-sha1     # 内置签名引擎，见下表
    credential_fields:           # 用户需要填写的凭证字段
      - access_key_id
      - access_key_secret
    service: ecs                 # 部分引擎需要
    version: "2014-05-26"

  endpoint:
    template: "https://ecs.{{ .region }}.aliyuncs.com/"
    method: POST
    content_type: application/x-www-form-urlencoded
    query:                       # 可选，固定查询参数
      Version: "2014-05-26"
    headers:                     # 可选，固定请求头
      X-Custom: "{{ .region }}"

  allowed_hosts:                 # 必填，安全边界
    - "ecs.*.aliyuncs.com"

  permissions:                   # 必填，执行前展示给管理员
    - "ecs:AuthorizeSecurityGroup"

  parameters:                    # 用户填写的参数
    - name: region
      label: 地域
      required: true
      example: cn-hangzhou
    - name: source_cidr
      label: 来源网段
      default: "0.0.0.0/0"

  revoke_policy:
    by: same_params              # same_params|rule_id|rule_name|policy_index
    name_template: "{{ .rule_name }}"   # by=rule_name 时使用

  rate_limit_per_minute: 60

  actions:
    ensure_port_allowed:         # 必填
      operation: AuthorizeSecurityGroup
      params: { ... }
      idempotent_check:          # 必填
        operation: DescribeSecurityGroups
        params: { ... }
        match:
          path: "SecurityGroups.SecurityGroup.0.Permissions.Permission"
          compare: port_range    # port_range|exact
          fields:
            IpProtocol: '{{ proto "lower" .protocol }}'
            PortRange: '{{ port_range "slash" .port_min .port_max }}'
      success_when:
        status: 200
        duplicate_codes: ["AlreadyExists"]
      body:                      # 可选，JSON 包一层
        envelope: SecurityGroupPolicySet

    revoke_port_allowed:         # 必填
      operation: RevokeSecurityGroup
      params: { ... }
      success_when:
        status: 200
```

---

## 3. 内置签名引擎

| 引擎 | 适用 | 凭证字段 | 说明 |
| --- | --- | --- | --- |
| `aliyun-hmac-sha1` | 阿里云 RPC | `access_key_id`, `access_key_secret` | RFC3986 编码 + HMAC-SHA1 |
| `tencent-hmac-sha256` | 腾讯云 | `secret_id`, `secret_key`, `region` | TC3-HMAC-SHA256 |
| `aws-sigv4` | AWS | `access_key_id`, `secret_access_key`, `region`, `session_token` | SigV4，服务名 ec2 |
| `huawei-aksk` | 华为云 | `iam_token`, `project_id` | 需先换取 IAM Token |
| `azure-oauth2` | Azure | `access_token` | Bearer Token |
| `gcp-oauth2` | GCP | `access_token` | Bearer Token |

引擎决定「怎么签名」，profile 只声明用哪个。

---

## 4. 模板函数

模板语法刻意保持极简，不允许任意逻辑。

| 函数 | 示例 | 结果 |
| --- | --- | --- |
| `port_range` | `{{ port_range "slash" .port_min .port_max }}` | `8000/8010` |
| | `{{ port_range "dash" ... }}` | `8000-8010` |
| | `{{ port_range "string" ... }}` | `"8000-8010"` |
| `proto` | `{{ proto "upper" .protocol }}` | `TCP` |
| | `{{ proto "lower" ... }}` | `tcp`（`all` → `-1`） |
| | `{{ proto "title" ... }}` | `Tcp`（`all` → `*`） |
| `default` | `{{ default .source_cidr "0.0.0.0/0" }}` | 回退值 |
| `json` | `{{ json .payload }}` | JSON 字符串 |
| `upper/lower/title` | `{{ upper .region }}` | 大小写 |
| `join` | `{{ join .ports "," }}` | 拼接 |
| `trim` / `replace` | `{{ replace .name " " "-" }}` | 字符串处理 |

**没有** `exec`、`env`、文件读取等函数——这是安全边界的一部分。

---

## 5. 校验规则

`Validate()` 会强制检查：

- `apiVersion == yukitunnel.io/v1`、`kind == CloudProfile`
- `metadata.id` 匹配 `^[a-z0-9][a-z0-9-]{2,62}$`
- `metadata.vendor` 非空
- `spec.auth.engine` 非空、`credential_fields` 非空
- `spec.endpoint.template` 非空
- **`spec.allowed_hosts` 非空**（防止凭证外发）
- **`spec.permissions` 非空**（执行前展示）
- 两个 action 都存在且各有 `operation`
- `ensure_port_allowed` 必须有 `idempotent_check`
- `revoke_policy.by` 是四个合法值之一

本地校验：

```bash
./bin/yuki-server profile verify profiles/your-profile.yaml
```

```
✓ your-profile 校验通过
  vendor   ：aliyun
  版本     ：1.0.0
  sha256   ：abc123...
  签名引擎 ：aliyun-hmac-sha1
  允许域名 ：ecs.*.aliyuncs.com
  权限     ：ecs:AuthorizeSecurityGroup
```

---

## 6. 测试你的 profile

### 6.1 dry-run（不触网）

```bash
./bin/yuki-server profile install --file profiles/your-profile.yaml --data-dir /tmp/yt
./bin/yuki-server profile credential set your-profile \
  --field access_key_id=AK --field access_key_secret=SK --data-dir /tmp/yt
./bin/yuki-server profile run your-profile \
  --action ensure_port_allowed --port-min 20000 --port-max 20000 \
  --param region=cn-hangzhou --param security_group_id=sg-test \
  --dry-run --ack --data-dir /tmp/yt
```

dry-run 会渲染并签名，但**不发送请求**。它能发现：模板语法错误、参数缺失、host 不在白名单。

### 6.2 mock 端点

写一个 Go 测试（参考 `internal/cloudprofile/profile_test.go` 的 `TestRPCProvidersIncludeActionParam`）：

```go
srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // 断言请求形状：Action、端口格式、协议大小写、签名头
    w.Header().Set("Content-Type", "application/json")
    _, _ = w.Write([]byte(`{"RequestId":"r1"}`))
}))
p.Spec.Endpoint.Template = srv.URL + "/"
p.Spec.AllowedHosts = []string{u.Hostname()}
```

### 6.3 真实联调

见 [CLOUD-LIVE-TEST.md](CLOUD-LIVE-TEST.md)。

---

## 7. 提交流程

```bash
# 1. 分支
git checkout -b profile/your-vendor

# 2. 添加文件
$EDITOR profiles/your-vendor.yaml

# 3. 本地校验
./bin/yuki-server profile verify profiles/your-vendor.yaml

# 4. 跑全部测试
go test ./internal/cloudprofile/... -v

# 5. 确认 index 能被重新生成（不提交 index，CI 会做）
go run ./cmd/yuki-profile-sign -dir profiles -key /tmp/test.key \
  -key-id test -generate-key

# 6. 提交
git add profiles/your-vendor.yaml
git commit -m "feat(profiles): add <vendor> <resource> profile"
git push origin profile/your-vendor
```

CI（`.github/workflows/profiles.yml`）会：

1. 校验所有 profile
2. 用 `PROFILE_SIGNING_KEY` 重新签名 `index.json`
3. 提交签名后的 index
4. 同步到 R2
5. 校验三个镜像字节一致

PR 合并后，客户端下次刷新索引即可看到新 profile。

---

## 8. 签名是怎么发生的

```
维护者提交 YAML
    │
    ▼
CI: go run ./cmd/yuki-profile-sign -dir profiles -key <私钥> -key-id <id>
    │  1. 扫描 profiles/*.yaml
    │  2. 逐个 Validate()
    │  3. 计算 sha256
    │  4. 写 index.json
    │  5. ed25519 签名 → index.json.sig
    ▼
提交 index.json + index.json.sig
    │
    ▼
同步到 R2 / jsDelivr / raw
    │
    ▼
客户端：内置公钥验签 → 按 sha256 校验文档 → 执行
```

私钥在 CI secrets（`PROFILE_SIGNING_KEY`），永不进仓库。
生成方式见 `scripts/gen-profile-signing-key.sh`。

---

## 8bis. 内置副本

`internal/cloudprofile/bundled/` 存放着官方 profile 的副本，被编译进二进制，
用于离线安装。提交新 profile 时同步复制一份：

```bash
cp profiles/*.yaml internal/cloudprofile/bundled/
go test ./internal/cloudprofile -run TestBundledProfilesMatchRepository
```

注意：内置副本只在**网络不可达**时启用。签名校验失败会直接报错，不会回退。

## 9. 常见问题

| 问题 | 原因 | 解决 |
| --- | --- | --- |
| `allowed_hosts is required` | 忘了写 | 加上实际端点域名，支持 `*` 通配 |
| `host "..." is not in allowed_hosts` | 模板渲染出的 host 不匹配 | 检查通配规则，注意 `*.example.com` 不匹配 `example.com` |
| `endpoint must use https` | 用了 http | 云 API 都支持 https |
| `缺少必填参数 X` | 没传 `--param X=...` | 补参数 |
| `signing engine "..." is not available` | 引擎名拼错 | 对照第 3 节 |
| 签名被云厂商拒绝 | 时间偏差 / 端口格式 / 编码 | 先跑 `live-test` 脚本，看具体错误码 |
| 幂等检查不命中 | `match.path` 与真实响应结构不符 | 用真实响应调试，或先用 `compare: exact` 只比对关键字段 |

---

## 10. 参考实现

| 场景 | 参考文件 | 关键点 |
| --- | --- | --- |
| RPC + 斜杠端口 + HMAC-SHA1 | `aliyun-ecs-security-group.yaml` | `Action` 自动注入、`PortRange: 8000/8010` |
| JSON-RPC + envelope + 大写协议 | `tencent-vpc-security-group.yaml` | `body.envelope`、`proto "upper"` |
| Query API + 双端口字段 | `aws-ec2-security-group.yaml` | `FromPort`/`ToPort`、`InvalidPermission.Duplicate` |
| REST + 嵌套 body | `huawei-vpc-security-group.yaml` | `body.envelope: security_group_rule` |
| PUT 天然幂等 | `azure-nsg.yaml` | `revoke_policy.by: rule_name` |
| 数组端口 | `gcp-firewall.yaml` | `ports: ["8000-8010"]` |
