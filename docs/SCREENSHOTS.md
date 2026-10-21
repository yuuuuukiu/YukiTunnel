# 界面截图说明

截图位于 [`docs/screenshots/`](screenshots/)，由真实运行的服务端 + 演示数据生成。

| 文件 | 界面 |
| --- | --- |
| `web-login.png` | WebUI 登录（密码 / RSA Key） |
| `web-dashboard.png` | 概览：节点、隧道、流量、管理摘要 |
| `web-tunnels.png` | 我的隧道（卡片式，含复制/启停/编辑/删除） |
| `web-create-tunnel.png` | 新建隧道弹窗（节点延迟 + 协议 + 内网地址） |
| `web-admin-users.png` | 用户管理 |
| `web-admin-nodes.png` | 节点管理 |
| `web-admin-settings.png` | 域名与端口 |
| `web-admin-audit.png` | 审计日志 |
| `web-admin-profiles.png` | 云 Profile：签名源状态、已安装列表、执行记录 |
| `desktop-connect.png` | 桌面客户端首次连接向导 |
| `desktop-home.png` | 桌面客户端主界面 |
| `desktop-deploy.png` | 一键部署向导（环境检测） |
| `desktop-deploy-admin.png` | 一键部署向导（TLS / 管理员） |
| `desktop-deploy-confirm.png` | 一键部署向导（确认配置 / 进度） |

以下为各界面应展示的关键信息，便于后续更新截图。

## 1. 登录页（WebUI）

- 左侧品牌区：产品名、定位文案、四条能力清单。
- 右侧卡片：密码 / RSA Key 切换、用户名、密码（或私钥粘贴 + 文件选择）、错误提示、登录按钮。
- 截图要点：深色渐变背景、聚焦态边框、错误态红色提示条。

## 2. 用户概览

- 四张统计卡：在线节点、运行中隧道、累计流量、域名绑定状态。
- 节点表：名称 / 公网入口 / 延迟（分档着色）/ 在线徽章。
- 最近隧道表：名称、地址、状态、流量。
- 管理员额外看到「管理概览」与最近审计。

## 3. 我的隧道

- 双列卡片：名称 + 协议徽章 + 状态徽章、内网地址、公网地址（带复制按钮）、流量与连接数、启动/停止/编辑/删除。
- 空状态：图标 + 「创建第一条隧道」按钮。

## 4. 新建隧道弹窗

- 节点卡片网格：名称、区域、公网入口、延迟（绿/黄/红）、在线徽章。
- 协议四选一：TCP / UDP / HTTP / HTTPS，各带一行用途说明。
- 内网 IP、内网端口、名称、公网端口（占位「自动」）。
- 地址形式：域名+端口 / IP+端口（服务端关闭域名时显示只读提示）。
- 成功页：大号公网地址 + 复制 + 「立即启动」。

## 5. 服务端管理端

- 用户管理：角色下拉、密钥数、隧道数、启用开关、公钥管理弹窗、重置密码。
- 节点管理：公网入口、控制地址、状态/延迟、隧道上限、启用开关、检测按钮。
- 隧道管理：全部用户隧道、搜索、禁用开关、删除。
- 域名与端口：域名绑定开关 + 后缀、端口范围、公网入口覆盖、泛解析提示。
- 审计日志：时间、操作人、动作徽章、对象、详情、IP、导出 JSON。

## 6. 桌面客户端

- 连接向导：服务端地址、用户名、密码/RSA Key、信任自签名。
- 主界面：顶部连接信息、「穿透运行状态」卡（启动/停止穿透、PID、运行日志）、节点延迟列表、隧道表格。
- 设置：服务端地址、配置目录、二进制位置、防火墙提示。

## 7. 一键部署向导

1. 环境检测：逐项 ✓/✗，致命项红色并禁用下一步。
2. 端口与目录：端口输入、目录选择、端口段。
3. 安全与管理员：管理员账号、密码、TLS 模式切换、域名开关。
4. 确认与进度：配置摘要 + 进度条 + 步骤列表 + 日志尾部。
5. 完成：WebUI 地址、管理员、TLS 指纹、打开/停止/重新检测。

## 截图脚本（手工）

```bash
# 1) 起一个演示服务端
./bin/yuki-server init --data-dir /tmp/demo --api-addr 127.0.0.1:18443 \
  --control-addr 127.0.0.1:19443 --admin-password demo12345 \
  --port-min 28000 --port-max 28050 --domain-suffix demo.local --domain-binding
./bin/yuki-server start --data-dir /tmp/demo

# 2) 造数据
./bin/yuki-server user create alice --password alice12345 --data-dir /tmp/demo
./bin/yuki-server user create bob --password bob12345 --data-dir /tmp/demo
./bin/yuki-server node create hk-1 --public-host 203.0.113.10 --region 香港 --data-dir /tmp/demo

# 3) 浏览器打开 https://127.0.0.1:18443，登录 admin/demo12345
```

建议尺寸：1440×900（WebUI）、1180×760（桌面端），深色模式。
