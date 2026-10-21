# 部署指南

三种部署方式：Docker Compose、二进制 + systemd、桌面端一键部署。
本文覆盖参数、持久化、TLS、反向代理、镜像站、代理与升级。

---

## 1. Docker Compose

### 1.1 快速启动

```bash
cp .env.example .env
# 编辑 .env（见下表）
docker compose up -d --build
docker compose ps        # STATUS 应为 healthy
docker compose logs -f yuki-server
```

首次运行会从源码构建镜像（需要 Docker 能拉取 golang/node 基础镜像），
之后再次 `docker compose up -d` 会复用本地镜像，秒级启动。

访问 `https://<服务器IP>:8443`。

### 1.2 `.env` 参数

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `YUKI_IMAGE` | `yukitunnel/server:local` | 默认从源码本地构建；生产建议改成已发布的固定版本，如 `ghcr.io/yuuuuukiu/yuki-server:0.1.0` |
| `API_PORT` | `8443` | WebUI + REST API 映射到宿主机的端口 |
| `CONTROL_PORT` | `9443` | 客户端控制端口（WSS） |
| `PORT_RANGE_START` | `20000` | 隧道公网端口段起点（**必须与下方 YUKI_PORT_MIN 一致**） |
| `PORT_RANGE_END` | `20100` | 隧道公网端口段终点 |
| `YUKI_PUBLIC_HOST` | 空 | 客户端看到的公网 IP/域名；留空自动探测 |
| `YUKI_ADMIN_USERNAME` | `admin` | 首次启动创建的管理员 |
| `YUKI_ADMIN_PASSWORD` | 空 | 留空则随机生成并写入 `/data/admin.txt` |
| `YUKI_DOMAIN_BINDING` | `false` | 是否返回「域名+端口」 |
| `YUKI_DOMAIN_SUFFIX` | 空 | 例如 `tunnel.example.com` |
| `YUKI_PORT_MIN` / `YUKI_PORT_MAX` | `20000` / `20100` | 服务端可分配的端口范围 |
| `YUKI_TOKEN_TTL_HOURS` | `168` | 登录令牌有效期 |
| `YUKI_LOG_LEVEL` | `info` | `debug` 可看到防火墙与 profile 细节 |
| `YUKI_FIREWALL` | 空 | 设为 `off` 关闭系统防火墙自动放行 |
| `YUKI_CREDENTIAL_KEY` | 空 | base64 的 32 字节密钥；不设则用 `/data/credential.key` |
| `TZ` | `Asia/Shanghai` | 容器时区 |

### 1.3 持久化

命名卷 `yuki-data` 挂载到 `/data`：

```
/data/config.yaml
/data/yukitunnel.db        (+ -wal / -shm)
/data/credential.key       (云凭证加密密钥，0600)
/data/tls/server.crt|key
/data/logs/
/data/admin.txt            (仅随机密码时存在)
/data/profiles-cache/      (验签后的 profile 索引缓存)
```

备份：

```bash
docker run --rm -v yuki-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/yuki-$(date +%F).tgz -C /data .
```

恢复：

```bash
docker compose down
docker run --rm -v yuki-data:/data -v "$PWD":/backup alpine \
  sh -c 'cd /data && tar xzf /backup/yuki-YYYY-MM-DD.tgz'
docker compose up -d
```

> `credential.key` 必须与数据库一起备份，否则云凭证无法解密。

### 1.4 升级

```bash
docker compose pull
docker compose up -d
```

数据库迁移是幂等的（`ALTER TABLE ... ADD COLUMN` 忽略重复）。

### 1.5 反向代理（可选）

Nginx：

```nginx
server {
  listen 443 ssl http2;
  server_name panel.example.com;

  ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

  client_max_body_size 2m;

  location / {
    proxy_pass https://127.0.0.1:8443;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
  }
}
```

Caddy：

```
panel.example.com {
  reverse_proxy 127.0.0.1:8443 {
    transport http {
      tls_insecure_skip_verify
    }
  }
}
```

控制端口 9443 **建议直接暴露**（WebSocket 长连接）。若必须走代理，需透传 `Upgrade`/`Connection` 并把超时设为 ≥ 1 小时。

---

## 2. 二进制 + systemd

```bash
sudo useradd -r -s /usr/sbin/nologin yuki
sudo mkdir -p /var/lib/yukitunnel
sudo chown yuki:yuki /var/lib/yukitunnel
sudo chmod 700 /var/lib/yukitunnel

sudo ./yuki-server init --data-dir /var/lib/yukitunnel \
  --public-host 203.0.113.10 \
  --admin-user admin --admin-password 'ChangeMe-Str0ng!' \
  --port-min 20000 --port-max 20100

sudo ./yuki-server doctor --data-dir /var/lib/yukitunnel
```

`/etc/systemd/system/yuki-server.service`：

```ini
[Unit]
Description=YukiTunnel Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
ExecStart=/usr/local/bin/yuki-server serve --data-dir /var/lib/yukitunnel
Restart=always
RestartSec=3
LimitNOFILE=65535
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/yukitunnel
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now yuki-server
sudo systemctl status yuki-server
sudo journalctl -u yuki-server -f
```

> 需要 `CAP_NET_ADMIN` 才能自动改防火墙。不想授权就设 `Environment=YUKI_FIREWALL=off`
> 并手工放行端口段。

---

## 3. 桌面端一键部署

1. 打开 YukiTunnel →「部署服务端」
2. 环境检测（组件、目录、端口）
3. 端口与数据目录
4. 管理员 + TLS + 域名开关
5. 「开始部署」→ 观察进度 → 健康检查

部署产物默认在系统应用数据目录（「设置」页可查看路径）。

---

## 4. TLS

- 默认自签名（10 年），生成到 `<data>/tls/`
- 客户端首次连接可勾选「信任自签名证书」，或 CLI 加 `--insecure`
- 生产用真实证书：

```bash
yuki-server init ... \
  --tls-cert /etc/letsencrypt/live/panel/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/panel/privkey.pem
```

- 域名绑定开启时，建议 `*.tunnel.example.com` 泛解析到节点公网 IP

---

## 5. 域名绑定

1. WebUI「域名与端口」→ 开启 → 填 `tunnel.example.com`
2. DNS：`*.tunnel.example.com A 203.0.113.10`
3. 新建隧道返回 `<slug>-<rand>.tunnel.example.com:<port>`
4. 关闭后新隧道返回 `IP:<port>`；已有隧道保持原地址，编辑后按新开关重算

---

## 6. Profile Registry 配置

### 6.1 镜像链

内置顺序：

```
1. https://cdn.jsdelivr.net/gh/yuuuuukiu/yukitunnel-profiles@main
2. https://raw.githubusercontent.com/yuuuuukiu/yukitunnel-profiles/main
3. https://tunnel.yuuuuukiu.top              (Cloudflare R2，需先配好 DNS)
```

自定义镜像（优先于内置）：

```bash
# CLI
yuki-server profile mirror set --mirrors "https://mirror.corp.local/profiles"

# 环境变量（完全替换内置链，适合内网/测试）
export YUKI_PROFILE_MIRRORS="https://mirror.corp.local/profiles"

# API
curl -X PUT https://panel:8443/api/v1/admin/profile-settings \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mirrors":"https://mirror.corp.local/profiles"}'
```

> 无论从哪个镜像取，**ed25519 验签都会执行**。镜像只是传输层。

### 6.2 代理

```bash
# HTTP 代理
yuki-server profile mirror set --proxy http://127.0.0.1:7890

# SOCKS5
yuki-server profile mirror set --proxy socks5://127.0.0.1:1080
```

代理同时作用于「下载索引」和「调用云 API」。

### 6.3 R2 secrets（仅发布方需要）

| Secret | 用途 |
| --- | --- |
| `R2_ACCESS_KEY_ID` | R2 API Token 的 Access Key |
| `R2_SECRET_ACCESS_KEY` | R2 API Token 的 Secret |
| `R2_ENDPOINT` | `https://<account-id>.r2.cloudflarestorage.com` |
| `PROFILE_SIGNING_KEY` | base64 的 ed25519 私钥 PEM |

Variable（可选）：`R2_BUCKET`，默认 `yukitunnel-profiles`。

fork PR 没有这些 secrets，workflow 会跳过上传并打印 warning，不会变红。

### 6.4 签名密钥

```bash
bash scripts/gen-profile-signing-key.sh
# 把输出的 base64 公钥写入 internal/server/profilekeys.go
# 把私钥 base64 后设为 PROFILE_SIGNING_KEY
```

发布构建若仍是占位公钥，`YUKI_RELEASE=1` 会拒绝启动，CI preflight 也会失败。

---

## 7. 防火墙

| 端口 | 协议 | 用途 | 需公网可达 |
| --- | --- | --- | --- |
| 8443 | TCP | WebUI + REST API | 是 |
| 9443 | TCP | 隧道控制（WSS） | 是 |
| 20000-20100 | TCP/UDP | 隧道公网端口 | 是 |

```bash
# ufw
sudo ufw allow 8443/tcp
sudo ufw allow 9443/tcp
sudo ufw allow 20000:20100/tcp
sudo ufw allow 20000:20100/udp

# firewalld
sudo firewall-cmd --permanent --add-port=8443/tcp
sudo firewall-cmd --permanent --add-port=9443/tcp
sudo firewall-cmd --permanent --add-port=20000-20100/tcp
sudo firewall-cmd --permanent --add-port=20000-20100/udp
sudo firewall-cmd --reload
```

或者交给 YukiTunnel 自动管理（需 root）：见 [FIREWALL.md](FIREWALL.md)。

云安全组可用 Profile 自动放行：见 [CLOUD-PROFILES.md](CLOUD-PROFILES.md)。

---

## 8. 排障

| 现象 | 排查 |
| --- | --- |
| WebUI 打不开 | `yuki-server doctor`；8443 是否被占用；防火墙 |
| 客户端「服务端地址不可达」 | 8443 可达性；自签名需勾选信任 |
| 隧道启动但访问超时 | 端口段是否放行；内网服务监听地址（`127.0.0.1` 只对客户端本机有效） |
| 「端口已被占用」 | `yuki-server port-range list`；换端口或删除旧隧道 |
| 「节点离线」 | `yuki-server node check <name>`；9443 可达性 |
| 域名无法访问 | 泛解析、端口放行、`domain_binding` 是否开启 |
| Profile 索引拉取失败 | `yuki-server profile index` 会逐个列出镜像的失败原因 |
| 云 API 签名失败 | 时间偏差 > 15 分钟；跑 `scripts/live-test-*.sh` 看具体错误码 |
| 部署失败 | `docker compose logs` 或 `<data>/logs/desktop-server.log` |

### 诊断命令

```bash
yuki-server doctor --data-dir /var/lib/yukitunnel
yuki-server status --data-dir /var/lib/yukitunnel
yuki-server audit list --limit 50 --data-dir /var/lib/yukitunnel
docker compose logs --tail 200 yuki-server
```
