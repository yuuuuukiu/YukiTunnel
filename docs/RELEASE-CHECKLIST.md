# v0.1.0 发布检查清单

按顺序执行，任何一项失败都不得发布。

```bash
git checkout main && git pull
export VERSION=0.1.0
```

---

## 1. 签名密钥

- [ ] `bash scripts/gen-profile-signing-key.sh` 生成 Ed25519 密钥对
- [ ] 私钥已存入密码管理器，**未**提交（`git status` 无 `*.key`）
- [ ] `internal/server/profilekeys.go` 的 `builtinProfileKeyB64` 已替换为公钥
- [ ] `grep -n 'builtinProfileKeyB64 =' internal/server/profilekeys.go` 显示真实 base64
- [ ] GitHub secret `PROFILE_SIGNING_KEY` 已设置（base64 的 PEM）
- [ ] 发布守门生效：

```bash
# 占位值下必须失败
YUKI_RELEASE=1 go run ./cmd/yuki-server version   # 预期：报错退出
# 真实公钥下必须通过
YUKI_RELEASE=1 go test ./internal/server -run TestBuiltinKeyIsNotPlaceholder
```

---

## 2. Profile Registry

- [ ] `go run ./cmd/yuki-profile-sign -dir profiles -key <key> -key-id yuki-profiles-2026` 成功
- [ ] `profiles/index.json` 与 `index.json.sig` 已提交
- [ ] 6 个 profile 的 `sha256` 与文件一致
- [ ] R2 secrets 已配置：`R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` / `R2_ENDPOINT`
- [ ] R2 可访问：`curl -fsSL https://tunnel.yuuuuukiu.top/index.json | head -c 80`
- [ ] 三个镜像字节一致（官方 / jsDelivr / raw）
- [ ] 客户端验签通过：`./bin/yuki-server profile index` 显示「已验签」

---

## 2ter. 仓库与镜像地址

已统一为 `yuuuuukiu` 账号 + `tunnel.yuuuuukiu.top` 镜像域名。

- [ ] 主仓库已存在：`https://github.com/yuuuuukiu/yukitunnel`
- [ ] registry 仓库已建：`bash scripts/init-profiles-repo.sh`
- [ ] `tunnel.yuuuuukiu.top` 已添加 DNS 记录（当前 NXDOMAIN）
- [ ] jsDelivr / raw 能取到 `profiles/index.json`（404 说明仓库还没推送）
- [ ] `./bin/yuki-server profile index` 能显示来源与「已验签」
- [ ] 若镜像尚未就绪：确认离线兜底可用（会显示 `builtin` 警告）

> schema 标识 `yukitunnel.io/v1` 与数据目录 `/var/lib/yukitunnel/` 是稳定契约，
> 不随账号迁移改变。

## 2bis. 首次运行体验（发布前必测）

在**干净目录**里模拟一个新用户，不要复用你的构建缓存：

```bash
git clone <repo> /tmp/fresh && cd /tmp/fresh
cp .env.example .env      # 只改 YUKI_ADMIN_PASSWORD
docker compose up -d --build
docker compose ps          # 必须 healthy
curl -sk https://127.0.0.1:8443/api/v1/health
```

- [ ] `docker compose up -d --build` 能直接从源码构建（不依赖已发布的镜像）
- [ ] 构建 context 不含 `desktop/src-tauri/target`（否则会多传数 GB）
- [ ] 容器 `healthy`，健康检查返回 `"status":"ok"`
- [ ] WebUI 打开能看到登录页
- [ ] 断网环境下 `yuki-server profile install <id>` 回退到内置副本并给出警告
- [ ] 伪造签名的镜像**不会**触发回退（`PROFILE_INTEGRITY_FAILURE`）

## 3. 代码质量

```bash
gofmt -l .
go vet ./...
go test ./... -race
bash scripts/smoke.sh
bash scripts/e2e-local.sh
bash scripts/e2e-local.sh --no-docker
cd web && pnpm build && cd ..
cd desktop && pnpm build && cd ..
cd desktop/src-tauri && cargo check
```

- [ ] 以上全部通过

---

## 4. 云 Profile 联调

- [x] 远端镜像 `profile index` 显示「已验签」（key_id: yuki-profiles-2026）
- [x] 远端镜像 `profile install` 走网络（来源为 `.../profiles`，非 `builtin`）
- [x] 错误凭证返回 `InvalidAccessKeyId.NotFound`（已用假 AK 验证过一次真实调用）
- [ ] 阿里云 `profile run --dry-run --ack` 成功（需凭证）
- [ ] 阿里云 `bash scripts/live-test-aliyun.sh` 全 PASS（**需真实 AK/SK**）
- [ ] 腾讯云 `profile run --dry-run --ack` 成功（需凭证）
- [ ] 腾讯云 `bash scripts/live-test-tencent.sh` 全 PASS（**需真实 AK/SK**）
- [ ] 错误凭证返回可读错误（`InvalidAccessKeyId.NotFound` / `AuthFailure`）
- [ ] 联调记录已填入 `docs/ACCEPTANCE-PROFILES.md`

---

## 5. 端到端真机

- [ ] Linux 服务器 `docker compose up -d`，`healthy`
- [ ] WebUI 可登录，能创建用户 / 节点 / 隧道
- [ ] 客户端 GUI 首次向导完成登录
- [ ] 客户端 GUI 点「+」看到节点延迟并创建隧道
- [ ] 隧道启动后公网地址可从外部访问
- [ ] 停止隧道后端口立即不可达
- [ ] 客户端断网 30 秒恢复后隧道自动重连
- [x] macOS DMG 生成并实机启动（`YukiTunnel_0.1.0_aarch64.dmg`，15.45 MiB）
- [ ] Windows `.exe` / Linux AppImage 安装包实机验证
- [ ] 桌面端管理员登录后能看到「管理」分组（7 个页面）
- [ ] 桌面端「部署服务端」向导完整跑通一次

---

## 6. 系统防火墙

- [ ] ufw（`Default: deny (incoming)`）：启动隧道后 `ufw status` 出现 `20000/tcp ... # yukitunnel`
- [ ] ufw：停止隧道后规则消失
- [ ] firewalld（target=DROP）：`firewall-cmd --list-ports` 出现端口
- [ ] firewalld：停止后端口移除
- [ ] nftables：`nft list chain inet yukitunnel input` 出现规则
- [ ] `yuki-server doctor` 的「系统防火墙」一行状态正确
- [ ] 服务端重启后孤儿规则被清理（审计含 `firewall.reconciled`）

验证脚本见 [FIREWALL.md §8](FIREWALL.md#8-手工验证)。

---

## 7. 安全四项

```bash
go test ./internal/store  -run TestCredentialsEncryptedAtRest -v
go test ./internal/server -run TestCredentialsAPIResponseMasked -v
go test ./internal/server -run TestSignatureCannotBeBypassedByPolicy -v
go test ./internal/server -run TestAuditLogCoversProfileCalls -v
```

- [ ] 四项全绿
- [ ] 手工确认数据库无明文：

```bash
sqlite3 /var/lib/yukitunnel/yukitunnel.db \
  "select count(*) from profile_credentials where fields_json like '%AKIA%';"
# 预期：0
```

- [ ] 手工确认 API 返回脱敏（WebUI 凭证页显示 `****`）
- [ ] `SECURITY.md` 的报告邮箱可收信

---

## 8. 文档

- [ ] `README.md` 快速开始命令逐条可执行
- [ ] `docs/QUICKSTART.md` 完整（部署 → 登录 → 建隧道 → 验证 → 排错）
- [ ] `docs/PROFILE-GUIDE.md` 完整（YAML / 签名 / PR / dry-run）
- [ ] `docs/DEPLOYMENT.md` 含 Compose 参数、R2 secrets、镜像与代理
- [ ] `docs/FIREWALL.md` 完整
- [ ] `docs/ACCEPTANCE-PROFILES.md` 含检查表与记录模板
- [ ] `docs/CLOUD-LIVE-TEST.md` 含最小权限与解读
- [ ] `docs/RELEASE-NOTES-v0.1.0.md` 定稿
- [ ] `SECURITY.md` 定稿
- [ ] `.github/ISSUE_TEMPLATE/` 三个模板存在
- [ ] `docs/screenshots/quickstart.gif` 存在且为最新录屏
- [ ] 所有截图来自当前版本

---

## 9. 发布

```bash
git tag -a v0.1.0 -m "YukiTunnel v0.1.0 (beta)"
git push origin v0.1.0
```

- [ ] Release workflow 的 `preflight` 通过
- [ ] `test` / `profiles` / `binaries` / `docker` / `desktop` 全绿
- [ ] GitHub Release 为 draft + prerelease
- [ ] 下载 `yuki-server-linux-amd64` 到干净机器，`yuki-server --version` 输出 `0.1.0`
- [ ] 该二进制能 `profile index` 验签
- [ ] Release notes 标注 beta 与联调范围

---

## 回滚

```bash
gh release delete v0.1.0 --yes
git push --delete origin v0.1.0
git tag -d v0.1.0

# 若 R2 已同步错误索引：revert 后重新签名并同步
git revert <bad-commit>
go run ./cmd/yuki-profile-sign -dir profiles -key <key> -key-id yuki-profiles-2026
```
