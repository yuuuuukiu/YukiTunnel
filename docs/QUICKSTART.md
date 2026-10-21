# 5 分钟快速开始

从零到「公网可访问的内网服务」，只需五步。

---

## 你需要

- 一台有公网 IP 的服务器（Linux，1 核 1G 足够）
- 一个要暴露的内网服务（例如 `http://127.0.0.1:8080`）
- 一台客户端机器（Windows / macOS / Linux）

---

## 第 1 步：部署服务端

### A. Docker（推荐）

```bash
git clone https://github.com/yuuuuukiu/yukitunnel.git
cd yukitunnel
cp .env.example .env
```

编辑 `.env`，至少改这三项：

```ini
YUKI_PUBLIC_HOST=203.0.113.10          # 你的公网 IP 或域名
YUKI_ADMIN_PASSWORD=ChangeMe-Str0ng!   # 管理员密码，务必修改
YUKI_PORT_MIN=20000
YUKI_PORT_MAX=20100                    # 与 PORT_RANGE_START/END 保持一致
```

启动：

```bash
docker compose up -d --build
docker compose ps          # 等到 STATUS 显示 healthy
docker compose logs -f yuki-server
```

看到这一行即成功：

```
level=INFO msg="YukiTunnel server started" api=0.0.0.0:8443 control=0.0.0.0:9443
```

### B. 二进制

```bash
sudo ./yuki-server init --data-dir /var/lib/yukitunnel \
  --public-host 203.0.113.10 \
  --admin-user admin --admin-password 'ChangeMe-Str0ng!' \
  --port-min 20000 --port-max 20100

sudo ./yuki-server start --data-dir /var/lib/yukitunnel
sudo ./yuki-server doctor --data-dir /var/lib/yukitunnel   # 全部 ✓
```

### C. 桌面端向导

打开 YukiTunnel 桌面端 →「部署服务端」→ 一路默认 →「开始部署」→ 看到「部署完成」。

---

## 第 2 步：安装客户端

### 桌面端

从 [Releases](https://github.com/yuuuuukiu/yukitunnel/releases) 下载：

| 平台 | 文件 |
| --- | --- |
| Windows x64 | `YukiTunnel_x64-setup.exe` |
| Windows x86 | `YukiTunnel_x86-setup.exe` |
| macOS（Intel / Apple Silicon） | `YukiTunnel_x64.dmg` / `YukiTunnel_aarch64.dmg` |
| Linux x64 | `YukiTunnel-x86_64.AppImage` |

Linux AppImage：

```bash
chmod +x YukiTunnel-x86_64.AppImage
./YukiTunnel-x86_64.AppImage
```

### CLI

```bash
# 从 release 下载对应平台的 yuki
chmod +x yuki-linux-amd64
sudo mv yuki-linux-amd64 /usr/local/bin/yuki
yuki --version
```

---

## 第 3 步：首次登录

### 桌面端

1. 启动后进入「连接你的穿透服务端」
2. 服务端地址填 `https://203.0.113.10:8443`
3. 用户名 `admin`，密码为第 1 步设置的密码
4. 自签名证书时勾选「信任自签名证书」
5. 点「连接并登录」

### CLI

```bash
yuki login --server https://203.0.113.10:8443 --username admin --insecure
```

```
✓ 登录成功：admin（管理员）
  服务端：https://203.0.113.10:8443
  登录有效期至：2026-10-27 21:00
```

> `--insecure` 只在服务端使用自签名证书时需要。生产环境建议配置真实证书后去掉。

---

## 第 4 步：创建第一条隧道

### 先建一个普通用户（管理员操作）

```bash
yuki-server user create alice --password 'alice-pass-123' --data-dir /var/lib/yukitunnel
```

或在 WebUI「用户管理 → 新建用户」。

### 桌面端

1. 用 `alice` 登录客户端
2. 点右上角「+」
3. 选择节点（卡片上显示延迟，选最低的）
4. 协议选 `TCP`
5. 内网 IP 填 `127.0.0.1`，内网端口填 `8080`
6. 点「创建隧道」

成功后显示公网地址：

```
203.0.113.10:20001
```

### CLI

```bash
yuki server list          # 查看节点与延迟
yuki tunnel create --proto tcp --local-ip 127.0.0.1 --local-port 8080
```

```
✓ 隧道创建成功：tcp-8080
  隧道 ID ：tun_ab12cd34
  公网地址：203.0.113.10:20001
```

---

## 第 5 步：启动并验证

### 桌面端

点「启动穿透」→ 状态变为「运行中」→ 复制公网地址。

### CLI

```bash
yuki run
```

保持这个进程运行（断线会自动重连）。

### 验证

从另一台机器（不要在服务端本机）：

```bash
curl http://203.0.113.10:20001/
```

返回你的内网服务内容即成功。

查看流量：

```bash
yuki tunnel stats tun_ab12cd34
```

```
状态：● 运行中
上行：1.2 KB
下行：4.5 KB
累计连接：2
当前连接：0
```

停止：

```bash
yuki tunnel stop tun_ab12cd34
curl -m 3 http://203.0.113.10:20001/     # 应立即连接失败
```

---

## 常见错误排查

### 服务端地址不可达

**现象**：客户端提示「服务端地址不可达」或「连接超时」。

```bash
# 1. 服务端是否在跑
docker compose ps                      # 或 systemctl status yuki-server

# 2. 端口是否监听
ss -lntp | grep -E '8443|9443'

# 3. 防火墙是否放行
sudo ufw status | grep 8443
sudo firewall-cmd --list-ports

# 4. 云安全组是否放行（控制台）
# 5. 从外部测试
curl -k https://203.0.113.10:8443/api/v1/health
```

| 原因 | 处理 |
| --- | --- |
| 服务端未启动 | `docker compose up -d --build` |
| 安全组未放行 8443 | 控制台添加规则，或用云 Profile 自动放行 |
| 用了 `http://` | 必须 `https://` |
| DNS 解析失败 | 用 IP 测试，确认域名解析 |

### 认证失败

**现象**：「用户名或密码错误」「登录已过期」。

```bash
# 重置密码（管理员）
yuki-server user passwd alice --data-dir /var/lib/yukitunnel

# 检查是否被锁定（连续失败 10 次锁定 15 分钟）
yuki-server audit list --action auth. --limit 20
```

| 原因 | 处理 |
| --- | --- |
| 密码错误 | 重置密码 |
| 账号被禁用 | `yuki-server user enable alice` |
| 令牌过期 | 重新 `yuki login` |
| 时间偏差过大 | 校准系统时间 |

### 端口已被占用

**现象**：创建或启动隧道时提示「公网端口 20001 已被占用」。

```bash
# 查看端口占用
yuki-server port-range list --data-dir /var/lib/yukitunnel

# 找出占用者
ss -lntp | grep 20001
```

处理：

- 换一个端口：`yuki tunnel create ... --public-port 20002`
- 删除不用的隧道：`yuki-server tunnel delete <id>`
- 扩大端口段：`yuki-server port-range set --min 20000 --max 20200`（同时放行防火墙）

### 节点离线

**现象**：「节点离线，无法启动隧道」或节点列表延迟显示 `—`。

```bash
# 检测节点
yuki-server node check default --data-dir /var/lib/yukitunnel

# 看节点配置
yuki-server node list --data-dir /var/lib/yukitunnel
```

| 原因 | 处理 |
| --- | --- |
| 控制端口 9443 未放行 | 放行 TCP 9443 |
| `control_host` 填错 | 编辑节点，改成可达地址 |
| 节点被停用 | `yuki-server node enable <name>` |

### 隧道启动但访问超时

```bash
# 1. 公网端口段是否放行
sudo ufw status | grep -E '2000[0-9]'

# 2. 内网服务是否在监听
curl http://127.0.0.1:8080/          # 在客户端机器上执行

# 3. 内网 IP 是否写对
# 127.0.0.1 只对客户端本机有效；其他机器要用实际内网 IP

# 4. 系统防火墙适配是否生效
yuki-server doctor --data-dir /var/lib/yukitunnel | grep 防火墙
```

---

## 下一步

- 用域名访问：[docs/DEPLOYMENT.md](DEPLOYMENT.md) 的「域名绑定」
- 自动放行云安全组：[docs/CLOUD-PROFILES.md](CLOUD-PROFILES.md)
- 完整命令参考：[docs/CLI.md](CLI.md)
- 安全加固：[SECURITY.md](../SECURITY.md)
