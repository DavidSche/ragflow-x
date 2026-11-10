# RAGFlow-X

> 面向私有数据中心的企业知识库 AI 基础设施补齐层（Control Plane + Gateway）。RAGFlow 仍是核心 RAG 执行面与数据面——仅通过官方 HTTP API 集成，不 Fork、不旁路。RAGFlow-X 是唯一的业务操作入口：企业全部业务操作（知识库、解析、chunk 治理、检索调优、对话、Agent、审批、审计）均在 RAGFlow-X 界面完成；RAGFlow 原生界面仅保留给引擎管理员做底层运维。

[English](./README.md) | [简体中文](./README.zh-CN.md)

[![license](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](./LICENSE)
[![version](https://img.shields.io/badge/version-v0.5.0-green.svg)](./CHANGELOG.md)
[![Discord](https://img.shields.io/badge/Discord-社区讨论-blueviolet?logo=discord&logoColor=white)](https://discord.gg/fnc8Ds9nj)
![backend](https://img.shields.io/badge/backend-Go%20%2B%20Gin-informational.svg)
![frontend](https://img.shields.io/badge/frontend-React%20%2B%20shadcn--admin--kit-blueviolet.svg)

RAGFlow-X 面向私有数据中心和企业真实生产环境。「不 Fork」是数据面架构决策，不是功能范围声明：RAGFlow 始终是唯一的执行面与数据面（解析、切片、检索、模型推理、workflow 执行），仅通过官方 `/api/v1` HTTP API 访问。而在业务操作层则相反——RAGFlow-X 是单一操作入口（Single Pane of Glass)：业务用户、知识运营与管理员的全部日常工作都在 RAGFlow-X 界面完成，无需接触 RAGFlow 原生 Web 界面（后者仅保留给引擎管理员）。在这个统一操作面之上，RAGFlow-X 补齐安全的多租户治理、RBAC/ABAC、OpenAI 兼容网关、审计合规、审批、用量与配额、可观测性、告警、全生命周期知识运营和统一对话中心。

## 核心能力

- **不 Fork、API 优先**：RAGFlow 操作走官方 `/api/v1` HTTP API，运营数据保存在独立的 `rgx_*` PostgreSQL/SQLite 数据库中。
- **单一业务操作入口**：全部业务操作在 RAGFlow-X 界面完成——包括 RAGFlow 自有业务功能的等效操作（数据集、解析、chunk、检索测试、对话、Agent）；RAGFlow 原生界面仅限引擎管理员使用。
- **安全与治理**：租户治理、RBAC/ABAC、审批、审计、可观测、计量、数据保留和企业网关。
- **网关**：`POST /v1/chat/completions`，内置认证、RBAC、限流、配额预扣、计量、审计、429 处理与降级。
- **全生命周期知识体系**：覆盖接入、解析、质量、版本、发布、消费、反馈、评测和运营。
- **统一对话中心**：以一致体验承接 Chat、Search 和 Agent，包括角色感知路由、引用、富内容、工具摘要和导出。
- **生产就绪**：版本与能力探测、契约测试、Docker Compose、结构化日志、Prometheus 和审计哈希链校验。
- **基于 RAGFlow v1.0.0 正式版复验**：能力注册表 97 条端点全部在 v1.0.0 上重验并抬钉至 stable；上游真实漂移 5 类（建库 schema、会话端点、产物面、画布列表面、文档级解析绑定）全部修复，并以「版本带 → 产物 kind」声明式映射 + 入口 fail-closed 取代静默降级。
- **引用透明**：引用携带 ref_index/positions 精确定位到文档坐标；会话中心按完成时刻快照再水合引用（v1.0.0 sessions 读回不再回显 references）。
- **诚实度纪律**：每轮对话在引擎答案之上记录结构化 answerability 声明（是否可答/置信度/依据来源/拒答理由）——只注释、不改写引擎答案。
- **搜索应用绑定防呆**：dataset_ids 支持 link_id | link_name | engine_id 解析并校验归租户，给近邻库建议而非泄漏引擎报错，错误契约即文档（40092/40093/40095）。

## v0.5.0 更新要点

v0.2.0 之后的第一个正式发布，合并三条版本线（v0.3.0 知识编译 + 企业记忆 + 治理/体验、v0.4.0 模板单一事实源 + 试跑、v0.5.0 RBAC 收口 + 上游 RAGFlow v1.0.0 兼容）。完整清单见 [CHANGELOG.md](./CHANGELOG.md)。

- **兼容 RAGFlow v1.0.0**：端点抬钉到 `v1.0.0`，修复 5 类上游漂移（建库 schema、`/chat/completions` 路由、产物面、`/agents` 混入 `compilation_template_group`、文档 re-bind 必须带 `pipeline_version_id`），并以声明式「版本带 → 产物 kind」映射 + `422/42261` fail-closed 兜底（`page_index` 已被上游重分类为结构变体）。
- **知识编译与企业记忆**端到端交付（Profile → Readiness → Run → dispatch → artifact 绑定 → compiled-first 检索 → Improvement Gate → recovery）。
- **安全收口**：自定义角色提权洞已堵、求值侧强制「写蕴含读」、`viewer` 收敛为 `business_user` 的只读切片（读权倒挂 19 → 0）、此前「授予了但什么都不发生」的平台细粒度动作真正接线。
- **治理与证据**：Release Gate（11 门）、文档级治理投影、引用精确定位、多维配额桶、统一通知事件流、网关 v1 契约，以及「试跑/D1 冻结 bundle 不得进入发布门」的冻结判分器。
- **模板单一事实源**，且试跑拥有独立权限资源 `trial-run`——可以练，永远不会成为发布证据。
- **v1.0.0 上的证据**：`scripts/upstream-verify.sh --full` → 5 passed / 0 failed / 0 skipped。

## 引擎兼容矩阵

| RAGFlow 引擎 | 状态 | 说明 |
|---|---|---|
| `0.27.x` | 已验证带 | 历史基线；支持 `page_index` 编译产物 |
| `1.0.0` | **已验证**（2026-11-10） | 抬钉 + live drill 全量重跑；编译产物仅 `wiki` |
| `> 1.0.0` | `verification_required` | fail-closed：发版与能力使用需重跑 live 契约集后再抬钉 |
| 不可解析 / 不可达 | `blocked` | 发版 fail-closed；运行期降级到最近已验证带 |

复验流程：`./scripts/upstream-verify.sh --full`（P1 provider 缝契约测试 + 串行 `service` E2E 套件，按 `RGX_ALLOW_*` 显式开启），跑完留档证据。面向运维的升级检查表（含五类已验证的上游行为：建库 `embedding_model`、对话端点迁移、仅 `wiki` 产物、`compilation_template_group` 行、`pipeline_version_id` 绑定）见
[运维 Runbook](./doc/user/zh-CN/operations-runbook-and-failure-drill-sop.md)。

### v1.0.0 仍未修复的上游缺陷

以下为 v0.5.0 验证轮（2026-11-10）在已验证 `1.0.0` 带上实测、**上游仍未修复**的缺陷与未公告的契约变化。本产品不静默降级：每条都由明确行为吸收，或在入口 fail-closed 拒绝。运行期可自查 `GET /api/v1/system/capabilities` 的 `version_issues` / `version_issues_summary` 字段。

| 上游缺陷 | 不做兜底的后果 | RAGFlow-X 处置 |
|---|---|---|
| 结构化编译产物没有读取面：`GET /datasets/{id}/artifacts` 固定 `type_kwd=wiki_page`，检索只回 `compile_kwd=wiki`，而 Compiler 仍在写 `page_index` / `timeline` / `knowledge_graph` 行 | 跑成功也永远列不出、绑不上、引不到 | 声明式「引擎版本 → 产物 kind」映射；非 `wiki` 在触发入口即拒（`422` / `42261`，「改用 kind `wiki`」），不产出不可达产物 |
| 上游 web bundle 的 `POST /auth/login` 发送 `RSA(base64(密码))`，后端解密的是 `RSA(明文)` → **浏览器登录恒 `109`** | 引擎自带 Web UI 无法登录 | RAGFlow-X 不代理该页面：业务登录走 RAGFlow-X，引擎侧用 API Key；确需用上游 UI 时对 dist 打本地补丁 |
| 建库必须显式给 `embedding_model`，不给则引擎回落到字面量 `"0"`，解析报 `500 model configuration unavailable` | 新库首次解析必失败 | 平台侧解析租户默认 embedding 模型（`ListDefaultModels` → `ListModels`，5 分钟 TTL）并恒定下发 |
| `GET /agents` 把 `type=compilation_template_group` 行混进 `data.canvas`，**且计入 `total`** | 影子同步把模板组当可运行 agent 导入；对其调 `GET /agents/{id}/versions` 返回 `103 permission to access the agent` | `ListAgents` 按 `type` 过滤（无 `type` 的旧引擎行保留），`total` 扣减被过滤条数 |
| 文档改绑到另一画布时，不给该画布已发布版本就报 `102 pipeline version does not exist / does not belong to canvas X` | Pipeline 库重建绑、parse-only 演练直接断 | 先 `POST /agents/{id}/publish` 发布版本再带 `pipeline_version_id` 下发；旧引擎忽略该字段 |
| [#21095](https://github.com/infiniflow/ragflow/issues/21095)：检索 `_source` 投影丢 `source_doc_ids`（社区修复 PR [#21106](https://github.com/infiniflow/ragflow/pull/21106) 仍无人评审） | 引用 → 切片映射在回读时丢失 | 答案落库时持久化 `ref_index` / `positions`，引用从完成态快照复核，不依赖被丢字段 |
| [#21097](https://github.com/infiniflow/ragflow/issues/21097)：`ResponseHeaderTimeout`（20 分钟）与流式超时（60 秒）硬编码；上游 `main` 已由 [#21434](https://github.com/infiniflow/ragflow/pull/21434) 合入，但**尚未进任何 release tag** | 长编译与慢模型撞上游超时且无开关 | 长任务由 RAGFlow-X 调度器带显式 deadline 驱动，并带单轮恢复，不依赖引擎的响应头超时 |
| [#21096](https://github.com/infiniflow/ragflow/issues/21096)：agent sandbox 的 `statusFromExitCode` 导致 Windows 构建破损；社区修复 PR [#21099](https://github.com/infiniflow/ragflow/pull/21099) 无人评审 | 上游 sandbox 无法在 Windows 构建 | 不打补丁：sandbox 只在 Linux 运行，由 RAGFlow-X 治理与审计，不重写执行器 |
| 图检索、Memory `team-me` 权限模型、agent `sandbox`/`skills` 面在不同 1.0.x 构建间与文档不一致 | 补丁版本之间行为会漂移 | 版本缺陷台账记为 `untested`：只在 dry run 中触达，未在未验证带上作为发版门禁 |

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
docker pull ghcr.io/davidsche/ragflow-x:v0.5.0
```

把 `docker-compose.yml` 中的 `build:` 段替换为：

```yaml
image: ghcr.io/davidsche/ragflow-x:v0.5.0
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
