# RAGFlow-X User Documentation

> Applies to RAGFlow-X v0.5.3 | Updated: 2026-11-13 | These documents ship with the repository.

This directory contains administrator and business-user documentation. Chinese
documents are under [`zh-CN`](./zh-CN); English equivalents are under
[`en`](./en). Shared screenshots live under [`images`](./images).

RAGFlow-X closes the gap between enterprise production requirements and
RAGFlow's core RAG engine. Its primary value comes from governed security, a
full-lifecycle knowledge system, and a unified conversation experience for
private data centers.

## Documents

| Topic | Chinese | English |
|---|---|---|
| User guide | [zh-CN/user-guide.md](./zh-CN/user-guide.md) | [en/user-guide.md](./en/user-guide.md) |
| Built-in permission matrix | [zh-CN/permission-matrix.md](./zh-CN/permission-matrix.md) | [en/permission-matrix.md](./en/permission-matrix.md) |
| Operations and failure drill SOP | [zh-CN/operations-runbook-and-failure-drill-sop.md](./zh-CN/operations-runbook-and-failure-drill-sop.md) | [en/operations-runbook-and-failure-drill-sop.md](./en/operations-runbook-and-failure-drill-sop.md) |
| Audit-chain backup and repair SOP | [zh-CN/audit-chain-backup-and-repair-sop.md](./zh-CN/audit-chain-backup-and-repair-sop.md) | [en/audit-chain-backup-and-repair-sop.md](./en/audit-chain-backup-and-repair-sop.md) |

**Install path for v0.5.3:** release binary archives only (the frontend is
embedded). No container image is published for this version — `docker/` still
builds one from source. See the user guide's install section and the repository
README.

The release operation manual and design records (engineering-internal
`doc/NNN` structure) are maintained by the engineering team and are **not**
part of this repository; only the documents above ship with a release. Demo
videos are distributed as release assets, not as repository content.

---

本目录面向管理员和业务用户。中文文档位于 [`zh-CN`](./zh-CN)，英文文档位于
[`en`](./en)，共用截图位于 [`images`](./images)。以上文档随仓库发布，适用于
RAGFlow-X v0.5.3（迁移 v166 起）。

**v0.5.3 安装方式**：只提供 release 二进制包（前端已内嵌），本版本不发布容器镜像；`docker/` 目录仍可从源码构建镜像与 Compose 栈。

发版操作手册与工程内部设计记录（`doc/NNN` 编号）由工程团队单独维护，**不随本仓库发布**；演示视频以 release 资产分发，不作为仓库内容。

RAGFlow-X 的定位是消除企业生产需求与 RAGFlow 核心 RAG 引擎之间的 GAP。其核心价值来自安全治理、全生命周期知识体系和面向私有数据中心的统一对话体验。
