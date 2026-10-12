# 安全政策

## 支持的版本

| 版本 | 支持情况 |
| --- | --- |
| 0.1.x | ✅ 安全修复 |
| < 0.1.0 | ❌ |

## 报告漏洞

**请勿通过公开 Issue 报告安全漏洞。**

请使用 GitHub 的 [私有安全公告（Private Vulnerability Reporting）](
https://github.com/DavidSche/ragflow-x/security/advisories/new) 提交报告，
这是默认且首选的渠道。

报告时请尽量包含：

- 受影响组件与版本（如：网关配额预扣、审批执行器、审计链、OIDC 绑定）；
- 复现步骤或概念验证（PoC），包括所需的最低权限前提；
- 影响评估（越权、租户隔离、数据泄露、审计链完整性等）；
- 若已知，附上缓解措施。

### 响应时限

- **48 小时内**确认收到；
- **7 天内**给出初步评估（影响等级与修复计划）；
- 高危（可跨租户读写、认证绕过、审计链破坏）以安全补丁版本（patch）优先发布。

### 披露政策

- 修复发布前，我们将与报告者协调披露时间线；
- 修复发布后，在安全公告中致谢报告者（除非要求匿名）；
- 请在修复发布前不要公开披露细节。

## 范围界定

**属于本项目范围**：

- `ragflow-x` 服务端（`cmd/server`、`internal/...`）：认证/会话、RBAC/ABAC、
  租户隔离、配额与计量、审批、审计哈希链、Secret Store、OIDC 绑定；
- 管理前端（`web/`）中的权限绕过与 XSS（富内容白名单渲染）；
- 官方交付物：`release.yml` 产物、`ghcr.io` 镜像、`docker/` 编排模板。

**不属于本项目范围**（请报告给上游 RAGFlow 项目）：

- RAGFlow 引擎本身（数据面）的漏洞——本项目不包含、不修改 RAGFlow 源代码；
- 用户自行修改的配置（如弱 `RGX_JWT_SECRET`、将管理端口暴露到公网）；
- 对 RAGFlow 官方 API 的滥用。

## 安全设计基线（供审计参考）

- Secret 只通过 `RGX_*` 环境变量或首启向导的加密 Secret Store 注入；
  `config/config.yaml`（含真实凭据）不提交、不打包；
- JWT Secret 拒绝空值、已知占位值且要求 ≥32 字节（`internal/pkg/jwt`）；
- 运行库默认要求迁移/运行时权限分离与 Platform 行级 RLS
  （`enforce_runtime_privileges`，见 `doc/08`）；
- 审计日志带哈希链，支持 `GET /audit/verify` 校验与受控修复（SOP 见 `doc/44`）；
- 发布产物提供 SHA256SUMS 校验，镜像按 tag 发布于 ghcr.io。
