# 系统防火墙适配

YukiTunnel 在隧道启动时自动放行公网端口，停止时回收，启动时对账清理孤儿规则。
本页说明支持范围、权限要求、行为细节与已知限制。

> 云厂商安全组由 [云 Profile](CLOUD-PROFILES.md) 处理，两者互补：
> 系统防火墙管「主机」，「云 Profile」管「云平台」。

---

## 1. 支持范围

| 平台 / 后端 | 检测方式 | 放行方式 | 状态 |
| --- | --- | --- | --- |
| Ubuntu / Debian（ufw） | `ufw status verbose` | `ufw allow <port>/<proto> comment yukitunnel` | ✅ 支持 |
| RHEL / CentOS / Fedora（firewalld） | `firewall-cmd --state` + 默认 zone target | `firewall-cmd --permanent --add-port` + `--add-port` | ✅ 支持 |
| 裸 nftables | `nft list chains` + `inet filter input` 的 policy | 独立表 `inet yukitunnel` | ✅ 支持 |
| 裸 iptables（无 ufw/firewalld） | — | — | ❌ 不处理（见限制） |
| Windows | — | — | ❌ Unsupported（用系统防火墙界面或组策略） |
| macOS | — | — | ❌ Unsupported（用「系统设置 → 网络 → 防火墙」） |

### 检测结果字段

```json
{
  "type": "ufw",
  "available": true,
  "enabled": true,
  "default_inbound_policy": "deny",
  "managed": true,
  "detail": "入站默认策略 deny"
}
```

`yuki-server doctor` 会输出一行：

```
✓ 系统防火墙          ufw 已启用，入站默认 deny，自动放行；入站默认策略 deny
```

---

## 2. 何时会放行（以及何时跳过）

只有同时满足以下条件才会修改防火墙：

1. 检测到受支持的后端（ufw / firewalld / nftables）
2. 后端处于启用状态
3. **入站默认策略为 `deny`（或 firewalld target 为 `DROP`/`REJECT`/`default`）**
4. 进程有权限（root，或具备相应 capabilities）

任一条件不满足就返回 `Skipped`，并在原因里说明。这是刻意设计：

- 入站默认 `allow` 时，端口本来就通，加规则只是噪音
- 策略未知时（例如非 root 读不到 ufw 状态），不猜测、不改动
- 不支持平台直接跳过，不报错打断隧道

关闭整个适配层：

```bash
export YUKI_FIREWALL=off
```

---

## 3. 权限要求

| 后端 | 需要 | 说明 |
| --- | --- | --- |
| ufw | root 或 `CAP_NET_ADMIN` | 非 root 时 `ufw status` 也会失败，检测会报告「需要 root」 |
| firewalld | root 或 polkit 授权 | 通过 D-Bus 调用，非 root 需要 `firewall-cmd` 的 polkit 规则 |
| nftables | root 或 `CAP_NET_ADMIN` | 直接操作 netlink |

推荐以 root 运行服务端（Docker 默认就是 root，二进制部署可用 systemd `User=root`）。
若坚持非 root，请预先手工放行端口段，并把 `YUKI_FIREWALL=off`：

```bash
sudo ufw allow 20000:20100/tcp
sudo ufw allow 20000:20100/udp
```

---

## 4. 规则标记与对账

### 4.1 标记

- **ufw**：规则注释为 `yukitunnel`（`# yukitunnel`）
- **nftables**：独立表 `inet yukitunnel`，所有规则都在里面
- **firewalld**：firewalld 的 port 规则没有 comment 字段，因此无法按标记区分；
  对账时会把「端口在 YukiTunnel 端口段内、但没有任何隧道在用」的端口移除

### 4.2 对账（reconcile）

服务端启动时执行一次：

1. 读取所有 `enabled && desired_running && public_port > 0` 的隧道，得到期望端口集合
2. 列出后端中「由 YukiTunnel 管理」的规则
3. 期望集合内的保留；其余回收
4. 结果写审计：`firewall.reconciled`，detail 里列出被移除的端口

对账解决这些场景：

- 服务端崩溃后隧道已删除，但规则残留
- 手工在数据库删了隧道
- 端口段调整后旧端口不再需要

### 4.3 绝不触碰的规则

- ufw 中不带 `# yukitunnel` 注释的规则（例如 SSH）
- nftables 中 `inet yukitunnel` 之外的任何表/链
- firewalld 中端口段之外的端口

测试 `TestReconcileRemovesOrphans` 专门断言 `22/tcp` 不会被删除。

---

## 5. 与隧道生命周期的衔接

```
POST /tunnels/{id}/start
  ├─ firewall.AllowPort(public_port, proto)     ← 先放行
  ├─ Hub.StartTunnelFor(...)                    ← 再监听
  └─ audit: firewall.allow / tunnel.started

POST /tunnels/{id}/stop
  ├─ Hub.StopTunnel(id)
  ├─ firewall.RevokePort(public_port, proto)
  └─ audit: firewall.revoke / tunnel.stopped
```

放行失败**不会**阻止隧道启动：很多运维自己管理防火墙，隧道本身仍然可用。
失败会写入服务端日志与审计（`firewall.allow` 带 `error=`）。

---

## 6. 审计事件

| action | 触发 | detail |
| --- | --- | --- |
| `firewall.allow` | 隧道启动放行成功 | `backend=ufw port=20000 proto=tcp applied=true` |
| `firewall.revoke` | 隧道停止回收成功 | 同上 |
| `firewall.reconciled` | 启动对账移除了孤儿规则 | `removed=[20001/tcp 20002/udp]` |

跳过（Skipped）不写审计，避免在默认 allow 的主机上刷屏。

---

## 7. 已知限制

| 限制 | 说明 | 规避 |
| --- | --- | --- |
| 裸 iptables 不支持 | 没有可靠的方式识别「我们的规则」而不误删 | 改用 ufw/firewalld，或手工维护 |
| firewalld 无规则注释 | 无法精确标记；对账依赖端口段推断 | 不要手工在同一端口段内加规则 |
| 仅处理 IPv4 端口规则 | ufw 的 `allow` 默认同时加 v4/v6；nft 表为 `inet`（双栈）；firewalld 只加 v4 | 纯 IPv6 环境需手工放行 |
| 不处理 NAT/端口转发 | 只放行本机监听端口 | 前置负载均衡需另行配置 |
| 容器内检测的是容器网络 | Docker 部署时容器内的 ufw 通常未启用 | 在宿主机放行端口段（compose 已映射） |
| 不持久化「哪些端口是 YukiTunnel 开的」 | 依赖后端自身的标记 | nft 用独立表，ufw 用注释 |

---

## 8. 手工验证

### Ubuntu (ufw)

```bash
sudo ufw default deny incoming
sudo ufw enable
yuki-server start --data-dir /var/lib/yukitunnel &
# 创建并启动一条隧道后：
sudo ufw status numbered | grep yukitunnel
# 预期：[ 1] 20000/tcp  ALLOW IN  Anywhere  # yukitunnel

# 停止隧道后：
sudo ufw status numbered | grep -c yukitunnel
# 预期：0
```

### CentOS (firewalld)

```bash
sudo systemctl start firewalld
sudo firewall-cmd --permanent --zone=public --set-target=DROP
sudo firewall-cmd --reload
# 启动隧道后：
sudo firewall-cmd --list-ports
# 预期：20000/tcp

sudo firewall-cmd --permanent --list-ports
# 预期同样包含 20000/tcp（重启后仍生效）
```

### nftables

```bash
sudo nft list chain inet yukitunnel input
# 预期：tcp dport 20000 accept comment "yukitunnel"
```

### 对账

```bash
# 手工制造孤儿规则
sudo ufw allow 20001/tcp comment yukitunnel
# 重启服务端
yuki-server restart --data-dir /var/lib/yukitunnel
sudo ufw status | grep 20001
# 预期：不存在
yuki-server audit list --action firewall. | head -3
# 预期：firewall.reconciled
```
