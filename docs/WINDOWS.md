# Windows 构建与验证

## 产物

`dist/windows/`（构建于 Windows 11 22H2，Go 1.24.3，`CGO_ENABLED=0`）：

| 文件 | 架构 | PE | 大小 |
| --- | --- | --- | --- |
| `yuki-server-windows-amd64.exe` | x86-64 | PE32+ | 13 MB |
| `yuki-windows-amd64.exe` | x86-64 | PE32+ | 11 MB |
| `yuki-server-windows-386.exe` | **x86 (32 位)** | PE32 | 12 MB |
| `yuki-windows-386.exe` | **x86 (32 位)** | PE32 | 10 MB |
| `yuki-server-windows-arm64.exe` | ARM64 | PE32+ | 11 MB |
| `yuki-windows-arm64.exe` | ARM64 | PE32+ | 10 MB |

校验和见 `dist/windows/SHA256SUMS.txt`。

> 32 位（386）产物面向老旧的 Windows 7/10 x86 机器与 32 位 Wine/PE 环境；64 位系统优先用 amd64。

## 构建方式

### 方式 A：Windows 主机本地构建（推荐，已验证）

```powershell
# 需要 Go 1.24+ 与源码
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Arch all -Version 0.1.0

# Go 不在 PATH 时（新装机器常见）显式指定
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Arch 386 -GoExe C:\Go\bin\go.exe
```

脚本会为 amd64 / 386 / arm64 各生成 server 与 client，输出到 `dist\windows\`。

### 方式 B：任意平台交叉编译

```bash
GOOS=windows GOARCH=386   CGO_ENABLED=0 go build -trimpath -o dist/windows/yuki-server-windows-386.exe   ./cmd/yuki-server
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/windows/yuki-server-windows-amd64.exe ./cmd/yuki-server
GOOS=windows GOARCH=386   CGO_ENABLED=0 go build -trimpath -o dist/windows/yuki-windows-386.exe          ./cmd/yuki
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/windows/yuki-windows-amd64.exe        ./cmd/yuki
```

## 自动化冒烟测试

仓库内置 `scripts/smoke-windows.ps1`，在 Windows 上跑完整的「初始化 → 登录 → 建隧道 → 真实流量 → 停止 → 删除 → CLI 配置 → doctor」流程：

```powershell
# 先构建
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Arch all

# amd64
powershell -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1 -Arch amd64

# x86 (32 位)
powershell -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1 -Arch 386
```

Go 不在 PATH 时加 `-GoExe C:\path\to\go.exe`（脚本用它编译一个本地 echo 服务）。

实测输出（两个架构均通过）：

```text
==> init                        OK   init
==> start                       OK   health
==> create user + login         OK   user create / login / server list
==> local service               OK   echo service built / local service
==> tunnel                      OK   tunnel create / traffic through tunnel
==> stop                        OK   port closed after stop
==> delete                      OK   tunnel deleted
==> server CLI                  OK   domain on / doctor

All Windows smoke tests passed.
```

> 脚本以 UTF-8 BOM 保存：Windows PowerShell 5.1 对无 BOM 的 UTF-8 会按本地代码页解码，中文注释会导致解析错误。

## 本次验证记录

在 Parallels 的 Windows 11（22H2，ARM64 宿主，x64 模拟）中：

```text
# 1) 源码与工具链
robocopy Z:\Downloads\YukiTunnel C:\build\YukiTunnel /E /XD node_modules target .git
tar -xf go1.24.3.windows-amd64.zip -C C:\build

# 2) 编译（虚拟机内执行，非交叉编译）
> scripts\build-windows.ps1 -Arch all
==> windows/amd64 (server)  ==> windows/amd64 (client)
==> windows/386  (server)   ==> windows/386  (client)

# 3) 运行
> yuki-server-windows-386.exe version      → yuki-server 0.1.0-win
> yuki-windows-386.exe --version           → yuki version 0.1.0-win

# 4) 端到端（amd64）
> yuki-server init --api-addr 127.0.0.1:18443 --control-addr 127.0.0.1:19443 \
    --admin-password admin12345 --port-min 28000 --port-max 28010
> yuki-server serve &
> curl -sk https://127.0.0.1:18443/api/v1/health
  {"database":"ok","status":"ok","version":"0.1.0-win"}
> yuki login --server https://127.0.0.1:18443 --username alice --password alice12345 --insecure
  ✓ 登录成功
> yuki server list
  cac7cd4825c87491  default  local  127.0.0.1  在线  1 ms  28000-28010
> yuki tunnel create --proto tcp --local-port 18080 --no-domain
  tun_ysq1atf9 → 127.0.0.1:28001
> yuki run &
> yuki tunnel start tun_ysq1atf9
> curl http://127.0.0.1:28001/        → hello-from-windows-vm
> yuki tunnel stop tun_ysq1atf9
> curl http://127.0.0.1:28001/        → 连接被拒绝（000）

# 5) 端到端（x86 / 386 全套）
> yuki-server-windows-386.exe init --api-addr 127.0.0.1:18444 --control-addr 127.0.0.1:19444 \
    --port-min 28100 --port-max 28110
> yuki-server-windows-386.exe serve &
> curl -sk https://127.0.0.1:18444/api/v1/health      → ok
> yuki-windows-386.exe login --server https://127.0.0.1:18444 --username admin --password admin12345 --insecure
  ✓ 登录成功
> yuki-windows-386.exe tunnel create --proto tcp --local-port 18080 --no-domain
  tun_6lxjpqjv → 127.0.0.1:28100
> yuki-windows-386.exe run &
> yuki-windows-386.exe tunnel start tun_6lxjpqjv
> curl http://127.0.0.1:28100/        → hello-from-windows-vm
```

PE 头校验（主机侧独立确认，非仅凭文件名）：

```text
yuki-server-windows-386.exe   machine=i386   magic=0x10b   (PE32)
yuki-windows-386.exe          machine=i386   magic=0x10b   (PE32)
yuki-server-windows-amd64.exe machine=x86-64 magic=0x20b   (PE32+)
yuki-windows-amd64.exe        machine=x86-64 magic=0x20b   (PE32+)
```

## Windows 使用提示

- 首次启动 `yuki-server serve` 时 Windows 防火墙会弹窗，允许「专用网络」即可；或手动放行 8443/tcp、9443/tcp 与端口段。
- 数据目录默认在 `%AppData%\yukitunnel`；服务端建议显式指定，例如 `--data-dir D:\yukitunnel`。
- 后台运行：`yuki-server start`（自动脱离终端）或用 `nssm`/计划任务把 `yuki-server serve` 注册为服务。
- 客户端配置文件：`%AppData%\yukitunnel\client.yaml`；可用 `set YUKI_CLIENT_HOME=D:\yuki-client` 覆盖。
- 控制台中文乱码：`chcp 65001` 切到 UTF-8；或用 `--json` 输出。
- 32 位系统请使用 `*-386.exe`；若在 64 位系统误用 386 版本也能运行，但内存与并发能力受限。
