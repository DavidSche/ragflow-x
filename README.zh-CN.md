# RAGFlow-X

> 面向私有数据中心的企业知识库 AI 基础设施补齐层（Control Plane + Gateway）。不 Fork RAGFlow，通过 API 消除企业生产需求与核心 RAG 引擎能力之间的 GAP。

[English](./README.md) | [简体中文](./README.zh-CN.md)

[![license](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](./LICENSE)
[![version](https://img.shields.io/badge/version-v0.2.0-green.svg)](./CHANGELOG.md)
[![Discord](https://img.shields.io/badge/Discord-社区讨论-blueviolet?logo=discord&logoColor=white)](https://discord.gg/fnc8Ds9nj)
![backend](https://img.shields.io/badge/backend-Go%20%2B%20Gin-informational.svg)
![frontend](https://img.shields.io/badge/frontend-React%20%2B%20shadcn--admin--kit-blueviolet.svg)

RAGFlow-X 面向私有数据中心和企业真实生产环境。RAGFlow 继续作为核心 RAG 执行面与数据面；RAGFlow-X 补齐其外围 GAP：安全的多租户治理、RBAC/ABAC、OpenAI 兼容网关、审计合规、审批、用量与配额、可观测性、告警、全生命周期知识运营和统一对话中心。

## 核心能力

- **不 Fork、API 优先**：RAGFlow 操作走官方 `/api/v1` HTTP API，运营数据保存在独立的 `rgx_*` PostgreSQL/SQLite 数据库中。
- **安全与治理**：租户治理、RBAC/ABAC、审批、审计、可观测、计量、数据保留和企业网关。
- **网关**：`POST /v1/chat/completions`，内置认证、RBAC、限流、配额预扣、计量、审计、429 处理与降级。
- **全生命周期知识体系**：覆盖接入、解析、质量、版本、发布、消费、反馈、评测和运营。
- **统一对话中心**：以一致体验承接 Chat、Search 和 Agent，包括角色感知路由、引用、富内容、工具摘要和导出。
- **生产就绪**：版本与能力探测、契约测试、Docker Compose、结构化日志、Prometheus 和审计哈希链校验。

## 快速开始

### 本地开发

```bash
make start-local
cd web && npm install && npm run dev
make status
make stop
```

本地模式使用 SQLite 和 mock RAGFlow。

### 连接真实 RAGFlow

1. 复制 `config/config.example.yaml` 为 `config/config.yaml`。
2. 配置 `ragflow.base_url`、`api_key`、PostgreSQL 和 Redis；敏感信息使用 `RGX_*` 环境变量注入。
3. 使用 `make start` 启动服务。

### Docker Compose

```bash
cd docker
cp .env.example .env
docker compose up -d --build
```

使用预构建镜像：

```bash
docker pull ghcr.io/davidsche/ragflow-x:v0.2.0
```

把 `docker-compose.yml` 中的 `build:` 段替换为：

```yaml
image: ghcr.io/davidsche/ragflow-x:v0.2.0
```

## 用户文档

| 文档 | 说明 |
|---|---|
| [用户手册](./doc/user/zh-CN/user-guide.md) | 管理员与业务用户的日常操作 |
| [内置角色与权限矩阵](./doc/user/zh-CN/permission-matrix.md) | 内置角色、资源、动作与权限边界 |
| [运维 Runbook 与故障演练 SOP](./doc/user/zh-CN/operations-runbook-and-failure-drill-sop.md) | 初始化、升级、回滚、降级、同步与配额运维 |
| [审计链备份与修复 SOP](./doc/user/zh-CN/audit-chain-backup-and-repair-sop.md) | 校验、备份、修复与证据归档 |

英文文档见[用户文档索引](./doc/user/README.md)。

## 产品视频

![RAGFlow-X 产品总览](https://github.com/user-attachments/assets/59752bf8-2423-491f-b366-1c5fb8e3279a)

## 开发检查

```bash
make build
make test
make vet
cd web && npm run typecheck && npm test
```

## 参与贡献

提交 Pull Request 前请阅读 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。标准流程：

1. Fork 并克隆仓库：
   ```bash
   git clone git@github.com:DavidSche/ragflow-x.git
   ```
2. 创建特性分支：`git checkout -b feat/my-feature`。
3. 执行本地质量门禁：`make check` 和 `cd web && npm test`。
4. 使用仓库 Pull Request 模板提交。

## 社区

- Discord：[加入社区](https://discord.gg/fnc8Ds9nj)
- Issues：[GitHub Issues](https://github.com/DavidSche/ragflow-x/issues)
- 安全：遵循 [`SECURITY.md`](./SECURITY.md)，请勿公开提交漏洞。

## 许可证

本项目采用 GPL-3.0。分发二进制、修改版源码或衍生作品时，必须按同一许可证提供对应源码。

RAGFlow 仍是独立项目，仅通过官方 HTTP API 集成。部署完整方案时请同时遵守上游许可条款。
