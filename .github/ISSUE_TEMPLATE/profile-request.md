---
name: Profile 请求
about: 请求支持新的云厂商 / 产品或修正现有 profile
title: '[Profile] '
labels: ['profile', 'enhancement']
assignees: ''
---

## 目标平台

- 厂商：<!-- 阿里云 / 腾讯云 / AWS / 华为云 / Azure / GCP / 其他 -->
- 产品与接口：<!-- 例如 ECS AuthorizeSecurityGroup -->
- API 文档链接：

## 需求类型

- [ ] 新增厂商
- [ ] 现有 profile 的参数/字段缺失
- [ ] 现有 profile 的端口或协议格式不对
- [ ] 幂等检查不命中
- [ ] 其他

## 关键参数（用于写 profile）

| 项目 | 值 |
| --- | --- |
| 端点（含地域占位） | `https://...` |
| 入方向字段名 | |
| 端口写法 | <!-- 8000/8010 或 8000-8010 或两个字段 --> |
| 协议大小写 | <!-- tcp / TCP / Tcp --> |
| 签名算法 | |
| 撤销方式 | <!-- 同参数 / rule_id / 规则名 / PolicyIndex --> |
| 幂等特性 | <!-- ClientToken / 重复错误码 / PUT 覆盖 / 同名报错 --> |

## 最小权限

请贴出该操作需要的 IAM Action 列表：

```json
["vendor:Action1", "vendor:Action2"]
```

## 你能提供联调支持吗？

- [ ] 我可以提供测试账号的临时凭证（**不要贴在 issue 里**，我们会私信联系）
- [ ] 我可以提供真实响应样例（已脱敏）
- [ ] 只能提供文档链接

## 附加信息

<!-- 控制台截图、curl 请求、错误码等 -->
