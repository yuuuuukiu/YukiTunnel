# 贡献指南

欢迎 Issue 与 PR。本文说明环境搭建、提交前的自检，以及几处容易踩的坑。

---

## 快速开始

```bash
git clone git@github.com:yuuuuukiu/yukitunnel.git
cd yukitunnel

# 依赖：Go 1.24+、Node 20+ 与 pnpm、Rust 1.77+（仅桌面端）
make build          # WebUI + yuki + yuki-server -> bin/
make test           # 单元 + 集成测试
bash scripts/smoke.sh
```

跑通本地服务端：

```bash
./bin/yuki-server init --data-dir /tmp/yt \
  --api-addr 127.0.0.1:18443 --control-addr 127.0.0.1:19443 \
  --admin-password dev12345 --port-min 20000 --port-max 20010
./bin/yuki-server serve --data-dir /tmp/yt
```

---

## 提交前必须通过

```bash
gofmt -l .                                  # 必须无输出
go vet ./...                                # 必须无输出
go test ./... -race -timeout 300s
bash scripts/smoke.sh
bash scripts/e2e-local.sh --no-docker
cd web && pnpm build
cd desktop && pnpm build && (cd src-tauri && cargo check)
```

CI 会跑同样的检查，本地先过可以省一轮等待。

---

## 代码约定

| 项 | 约定 |
| --- | --- |
| 注释 | 英文，解释**为什么**而非**做了什么** |
| 用户可见文案 | 中文 |
| 错误信息 | 中文，带可操作的下一步 |
| 提交信息 | 英文，首行 ≤ 72 字符，正文说明动机 |
| 测试命名 | `TestXxxDoesYyy`，覆盖行为而非实现 |

### 不要做的事

- 不要在两个地方实现同一份逻辑。`internal/servercli/profile_keys.go` 曾经复制了服务端的密钥校验，结果 CLI 用开发密钥拒绝了同一个二进制通过 HTTP 接受的索引。
- 不要把 `secrets` 用在 workflow 的 `if:` 里，GitHub 会在解析阶段判定整个 workflow 无效（0 个 job + failure）。改用 step 或 job 级 `env` 中转。
- 不要在 TypeScript 里从 `web/src` 引用 `react` 却不装 web 的依赖。`desktop` 通过 `@web` 别名编译 `web/src`，而 TS 从**导入方文件**向上查找模块，永远不会进 `desktop/node_modules`。
- 不要提交 `internal/server/webdist/` 的内容，只保留 `.keep`。

---

## 新增云厂商 Profile

不需要改 Go 代码，只需要一份 YAML。完整格式见 [docs/PROFILE-GUIDE.md](docs/PROFILE-GUIDE.md)。

```bash
# 1. 写 profile
$EDITOR profiles/your-vendor.yaml

# 2. 校验
./bin/yuki-server profile verify profiles/your-vendor.yaml

# 3. 同步内置副本（必须，否则 CI 会失败）
cp profiles/*.yaml internal/cloudprofile/bundled/
go test ./internal/cloudprofile -run TestBundledProfilesMatchRepository

# 4. 提交
git add profiles/your-vendor.yaml internal/cloudprofile/bundled/
git commit -m "feat(profiles): add <vendor> security group profile"
```

用 [Profile 请求模板](.github/ISSUE_TEMPLATE/profile-request.md) 提 Issue 也可以，附上端口写法、协议大小写和撤销方式即可。

**没有真实账号也能贡献**：mock 端点可以验证请求形状、签名头与 body 结构。真实联调见 [docs/CLOUD-LIVE-TEST.md](docs/CLOUD-LIVE-TEST.md)，需要你自己的最小权限子账号。

---

## 改动敏感区域

这些地方改动需要额外说明：

| 区域 | 为什么 |
| --- | --- |
| `internal/cloudprofile/registry/sign.go` | 签名校验是信任链的根，任何放宽都必须有对应测试 |
| `internal/store/credentials.go` | 云凭证静态加密；改算法要考虑已有数据的迁移 |
| `internal/firewall/` | 会改主机防火墙，`Reconcile` 必须永不碰非 `yukitunnel` 标记的规则 |
| `internal/server/profilekeys.go` | 内置公钥，改动等于更换信任根 |
| `internal/tunnel/` | 数据面，影响所有隧道 |

---

## 测试要求

| 改动 | 需要 |
| --- | --- |
| 修 bug | 一个先失败、修好后通过的回归测试 |
| 新功能 | 正常路径 + 至少一个错误路径 |
| 安全相关 | 证明防护**会**拒绝恶意输入的测试 |
| Profile | mock 端点测试，断言端口格式与协议大小写 |

验证回归测试有效的方法：临时把修复注释掉，确认测试失败，再恢复。

---

## 分支与提交

```
main                  始终可发布
feat/<short-name>     新功能
fix/<short-name>      bug 修复
profile/<vendor>      profile 相关
```

提交信息用祈使句，说明为什么：

```
fix(registry): resolve profile paths relative to the index

The registry repo keeps index.json under profiles/ but records bare
filenames, so resolving against the mirror root 404'd and every install
silently fell back to the bundled copy.
```

---

## 安全问题

**不要**用公开 Issue 报告漏洞。见 [SECURITY.md](SECURITY.md)。

---

## 行为准则

参与本项目即表示同意 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
