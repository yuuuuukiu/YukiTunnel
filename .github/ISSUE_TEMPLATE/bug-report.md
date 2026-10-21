---
name: Bug 报告
about: 报告可复现的问题（安全漏洞请走 SECURITY.md）
title: '[Bug] '
labels: ['bug']
assignees: ''
---

> ⚠️ 安全漏洞**不要**用这个模板。请阅读 [SECURITY.md](../../SECURITY.md)。

## 环境

```bash
# 请粘贴以下命令的完整输出
yuki-server --version
yuki-server doctor --data-dir <你的数据目录>
```

<details>
<summary>doctor 输出</summary>

```
（粘贴在这里）
```

</details>

| 项目 | 值 |
| --- | --- |
| 操作系统 | <!-- Ubuntu 24.04 / macOS 15 / Windows 11 等 --> |
| 架构 | <!-- amd64 / arm64 / 386 --> |
| 部署方式 | <!-- Docker Compose / 二进制 / 桌面端向导 --> |
| 客户端类型 | <!-- 桌面端 / CLI / 两者 --> |
| 客户端版本 | `yuki --version` |

## 问题描述

<!-- 发生了什么，期望发生什么 -->

## 复现步骤

1.
2.
3.

## 日志

```bash
# 服务端
docker compose logs --tail 200 yuki-server
# 或
journalctl -u yuki-server --no-pager -n 200

# 客户端
# 桌面端：设置 → 运行日志 → 复制
# CLI：加 -v 重新执行
yuki -v tunnel start <id>
```

<details>
<summary>日志</summary>

```
（粘贴在这里，注意去掉令牌与密码）
```

</details>

## 附加信息

- 是否使用了域名绑定：
- 是否使用了云 Profile：
- 系统防火墙类型（`ufw` / `firewalld` / `nftables` / 无）：
- 网络环境（是否在 NAT / 代理后）：
