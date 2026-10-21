# 命令行参考

## `yuki`（客户端）

全局参数：

| 参数 | 说明 |
| --- | --- |
| `--json` | 以 JSON 输出，便于脚本 |
| `-s, --server URL` | 覆盖服务端地址 |
| `--token TOKEN` | 使用 API 令牌 |
| `--key PATH` | RSA 私钥（Key 登录） |
| `-u, --username NAME` | 用户名 |
| `--insecure` | 跳过 TLS 校验（自签名） |
| `-v, --verbose` | 详细日志 |

### 登录

```bash
yuki login --server https://panel.example.com:8443 --username alice
yuki login --server https://1.2.3.4:8443 --username alice --key ~/.ssh/id_rsa --insecure
yuki logout
yuki whoami
```

未提供 `--server` / `--username` 时进入交互式提问；密码通过隐藏输入读取。

### 节点

```bash
yuki server list
yuki server list --json | jq -r '.servers[] | "\(.name)\t\(.latency_ms)ms"'
```

输出列：ID、名称、区域、公网入口、状态、延迟、端口范围。

### 隧道

```bash
yuki tunnel create --proto tcp --local-ip 127.0.0.1 --local-port 8080
yuki tunnel create --proto http --local-port 80 --node hk-1 --name "博客"
yuki tunnel create --proto udp --local-port 19132 --public-port 20100 --no-domain
yuki tunnel list [-w]
yuki tunnel start tun_ab12cd34
yuki tunnel stop tun_ab12cd34
yuki tunnel stats tun_ab12cd34
yuki tunnel delete tun_ab12cd34 -y
```

- 不指定 `--node` 时自动选择延迟最低的在线节点。
- `--domain` 强制域名地址；`--no-domain` 强制 IP 地址；都不加则跟随服务端开关。
- `--json` 下 `create` 输出 `{ tunnel, address }`。

### 常驻转发

```bash
yuki run                       # 启动全部隧道
yuki run --tunnel tun_xxx      # 只启动指定隧道（可重复）
yuki run --json                # 逐行输出统计，适合 journald
```

断线按 1s→30s 指数退避自动重连；`Ctrl+C` 优雅退出。

### 其他

```bash
yuki status                    # 登录态 + 连通性 + 隧道计数
yuki config show
yuki config set server https://new-host:8443
yuki config set insecure true
yuki config path
yuki doctor                    # 配置、地址、凭证、健康检查
```

配置文件：`~/.config/yukitunnel/client.yaml`（macOS: `~/Library/Application Support/yukitunnel/`），权限 0600。可用 `YUKI_CLIENT_HOME` 覆盖目录。

---

## `yuki-server`（服务端）

全局参数：

| 参数 | 说明 |
| --- | --- |
| `--json` | JSON 输出 |
| `--data-dir DIR` | 数据目录 |
| `--config FILE` | 配置文件路径（默认 `<data-dir>/config.yaml`） |
| `--server URL` | 远程模式：管理运行中的服务端 |
| `--token` / `--username` / `--password` | 远程模式凭证 |
| `--insecure` | 跳过 TLS 校验 |
| `-y, --yes` | 跳过确认 |

### 初始化与生命周期

```bash
yuki-server init --data-dir /var/lib/yukitunnel \
  --api-addr 0.0.0.0:8443 --control-addr 0.0.0.0:9443 \
  --public-host 203.0.113.10 \
  --admin-user admin --admin-password 'ChangeMe-123' \
  --port-min 20000 --port-max 30000 \
  [--domain-suffix tunnel.example.com --domain-binding] \
  [--tls-cert /path/fullchain.pem --tls-key /path/privkey.pem]

yuki-server start [--foreground]
yuki-server stop
yuki-server restart
yuki-server status
yuki-server doctor
yuki-server serve          # 容器入口，前台运行
```

`init` 可重复执行；已有配置不会被覆盖（`--force` 强制）。未指定密码时随机生成并写入 `<data-dir>/admin.txt`（0600）。

### 用户

```bash
yuki-server user list
yuki-server user create alice --password 'S3cure-pass' [--role admin] [--display-name Alice]
yuki-server user create bob --key-file ~/.ssh/id_rsa.pub --key-name bob-mac
yuki-server user disable alice
yuki-server user enable alice
yuki-server user passwd alice
yuki-server user delete alice
yuki-server user key add alice --key-file ~/.ssh/id_ed25519.pub
yuki-server user key list alice
yuki-server user key remove alice <keyID>
```

### 节点

```bash
yuki-server node create hk-1 --public-host 203.0.113.10 --control-port 9443 --region hk [--max-tunnels 100]
yuki-server node list
yuki-server node enable hk-1
yuki-server node disable hk-1
yuki-server node check hk-1
yuki-server node delete hk-1
```

### 隧道

```bash
yuki-server tunnel list [--user alice]
yuki-server tunnel disable tun_xxx
yuki-server tunnel enable tun_xxx
yuki-server tunnel delete tun_xxx
```

### 域名与端口

```bash
yuki-server domain binding on --suffix tunnel.example.com
yuki-server domain binding off
yuki-server domain show

yuki-server port-range set --min 20000 --max 20100
yuki-server port-range list
```

### 云 Profile

```bash
yuki-server profile list                      # 已安装
yuki-server profile index [--vendor aliyun]   # 远端签名索引（已验签）
yuki-server profile install <id>              # 从镜像安装
yuki-server profile install --file x.yaml     # 从本地安装（仍需校验结构）
yuki-server profile verify x.yaml [--sha256]  # 本地校验

yuki-server profile credential set <id> \
  --field access_key_id=... --field access_key_secret=... --label prod

yuki-server profile run <id> \
  --action ensure_port_allowed --port-min 20000 --port-max 20000 \
  --param region=cn-hangzhou --param security_group_id=sg-xxx \
  --credential prod --dry-run --ack

yuki-server profile runs --limit 20           # 执行记录
yuki-server profile mirror show               # 镜像链与代理
yuki-server profile mirror set --mirrors "https://mirror.local" --proxy socks5://127.0.0.1:1080
yuki-server profile rekey                     # 轮换凭证加密密钥
```

`--ack` 表示已知晓 profile 声明的云权限；`--dry-run` 不要求 `--ack`（不触网）。

### 审计与配置

```bash
yuki-server audit list --limit 50 --action tunnel.
yuki-server config show
yuki-server config set public-host 203.0.113.10
yuki-server config set port-max 20200
```

### 远程模式

```bash
yuki-server --server https://panel.example.com:8443 \
  --username admin --password '***' user list --json

# 先签发长期令牌，之后脚本用 --token
yuki-server --server https://panel.example.com:8443 --username admin --password '***' token create --ttl-hours 720
yuki-server --server https://panel.example.com:8443 --token yk_xxx node list
```

### 退出码

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 业务错误（参数、网络、权限、健康检查失败等），错误信息写入 stderr |
