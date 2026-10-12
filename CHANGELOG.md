# Changelog

本项目所有显著变更记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.2.0] - 2026-10-08

#### Added

- **Ingestion Governance**：新增 Parser Policy 与 Quality Profile 模型、租户隔离 CRUD、
  Active Policy 冲突保护、Fallback 链校验、解析质量门禁、Attempt/Report 追踪、
  RAGFlow Pipeline Provider 校验、RAGFlow 解析执行接线和前端质量追踪。Reference 阈值
  拥有 Hard-Fail 否决权。
- **Parser Fallback Execution**：质量门禁返回 RETRY 时自动切换 RAGFlow 文档级解析
  配置并触发备选 ParserPolicy 重解析；失败关闭并通过 Attempt 链与文档 marker 支持恢复。
- **Version Lifecycle & Outbox**：新增 LogicalDocument / DocumentVersion、Temporal
  Invariant、durable publish、补偿与 Reconciler、恢复流程和持久化 Outbox 事件链。
- **Query Control Plane**：新增 Typed Tool Registry、DB Connection、Query Template、
  SQL AST/Execution Policy、四层 ABAC、单/多 SQL、混合 Tool、fact_lookup、
  Knowledge Retrieval、LLM Planner 与 LLM Synthesis。SQL Runtime 对 PostgreSQL/MySQL
  使用显式只读事务，对 SQLite 使用 `query_only`。
- **Fact Guard**：新增 Fact Registry、Evidence Conflict、Claim Validation、
  strict/regenerate/warn/off 模式、Planned Synthesis Claim Gate、Answer Run 状态投影、
  运行时 Evidence API 和统一 Fact Guard 运维视图。
- **Answer Control Plane**：新增 Answer Run / Answer Delivery 快照、安全裁剪投影、
  幂等交付、状态事件和审计。
- **Knowledge Strategy**：新增五类语义策略、Capability Probe、fallback、Temporal
  Resolver、structured filters、keyword/rerank 透传和 Knowledge Retrieval Tool。
- **Evaluation Control Plane**：新增 Evidence Snapshot、canonical hash、dependency set、
  stale/revalidated 状态、dataset projection、team scope 和恢复 E2E。
- **权限与治理**：同步 Resource Type Registry、Builtin Permission Matrix、
  API `accessRules`、前端资源映射与 protectedRoutes；管理动作保留审计。
- **前端**：新增 Parser Policy / Quality Profile、版本生命周期、Tool / Query Template、
  Knowledge Strategy、Claim Status 和质量追踪管理界面，并接入统一错误与授权语义。

#### Changed

- 更新产品定位：RAGFlow-X 面向私有数据中心，通过安全治理、全生命周期知识体系
  和统一对话中心消除企业生产需求与 RAGFlow 核心 RAG 引擎能力之间的 GAP。
- 升级前端 `axios`、`dompurify`、`react-router-dom` 与相关传递依赖，消除当前
  依赖图中的高级别已知漏洞；非破坏性依赖审计通过，Vitest 385 用例与类型检查回归通过。

#### Known Limitations

- PostgreSQL、MySQL 8.0.36 和 MariaDB 10.6 的 SQL Tool 只读账号、截断和超时
  集成验证均已通过。
- 自动从 Knowledge chunks / Tool outputs 抽取 Fact 与 Claim 仍属后续增强；当前使用
  受控 Evidence API。
- `SearchDataset` scoped provider 已实现但尚未接入单数据集主链路，需产品契约确认。
- RAGFlow v1.0.0-rc1 Pipeline 契约、PostgreSQL/MySQL 8.0.36/MariaDB 10.6 只读 SQL
  集成、权限矩阵复核、场景 E 实机演示、全量后端与前端回归、安全裁剪和本地
  release drill 均已通过。正式发布仍需推送 tag 并复核云端 release workflow。

### 计划中（M5 生产化闭环）

- 多 RAGFlow 实例与容灾路由增强（见 `doc/105`）。
- 模板实例 API 族与灰度策略（见 `doc/124`）。
- 多渠道通知、OTel 深链、Timeline 排版与 Metric 看板（见 `doc/125`）。

> 状态与差距清单见 `doc/103_M5实施状态与场景Runbook.md`。

## [0.1.0] - 2026-09-28

首个公开版本。面向私有数据中心的企业知识库基础设施补齐层（Control Plane +
Gateway）：不 Fork RAGFlow，通过官方 HTTP API 集成，在其之上补齐多租户治理、
权限、审计、计量配额、审批与可观测能力。

### Added

- **多租户与组织治理**：Tenant / User / Team / Project / Role 五级模型，
  五级 RBAC + ABAC 权限矩阵，Repository 层强制租户过滤。
- **数据集 / 文档 / 任务**：数据集 CRUD、上传后自动解析、批量解析/停止/删除、
  Chunk 管理、异步 Worker（任务队列、重试/退避/心跳、自愈周期任务）。
- **统一模型管理**：Model Providers / Instances / Models / Model Routes，
  国产模型厂商可配。
- **OpenAI 兼容网关**：`POST /v1/chat/completions`，Bearer Key 限流（SHA-256）、
  流式 SSE、模型路由双路径、429/降级、计量与审计、配额事前预扣（原子预扣、
  `request_id` 幂等、finalize/release、`X-Quota-Remaining`）。
- **问答工作台**：Chat 影子表 + authoring 配置、Session 管理、SSE 流式问答、
  行内/侧栏引用、赞踩反馈、引用深链租户校验。
- **统一对话体验**：Chat / Search App / Agent 复用统一消息协议与对话组件；
  富内容白名单渲染（Markdown/GFM、Mermaid 安全模式）；会话导出 Markdown/HTML/JSON；
  9 类提示词预设、5 档参数策略、场景模板向导。
- **资产治理与知识运营**：场景模板导入/导出/版本留痕/跨租户复制、提示词默认值
  版本回滚、数据集 owner/expiry 生命周期、评测集生成与坏例处置、知识运营看板。
- **Search App / Memory / Agent**：Search Apps、Memories、Agent 配置管理 +
  版本/回滚 + 发布/下线 + 会话转发；前端 Agent 工作流画布（ReactFlow）。
- **审批工作流**：按 `object_type + action` 的策略门禁、多步骤审批、组织适配、
  职责分离、幂等键；批准后由 Worker 幂等执行；审批中心三类视图。
- **安全与合规**：JWT Secret 强校验、审计哈希链防篡改（`GET /audit/verify`、
  `POST /audit/repair`）、日志脱敏、数据保留策略（TTL 清理 + 审计链 re-anchor）、
  企业 OIDC 身份绑定。
- **可观测**：Prometheus `/metrics` 与告警规则、OpenTelemetry（可选）、
  结构化日志 + `trace_id` 全链路、`GET /dashboard`、`GET /system/health`。
- **Release Governance / Resource Sync**：能力探测 + 契约测试、企业连接与
  资源同步、性能基线工具、部署演练（`make deploy-drill`）。
- **前端**：React 19 + shadcn-admin-kit，22 个资源域 CRUD，统一对话中心，
  i18n（zh/en），Vitest + RTL 单测与 Playwright E2E。
- **工程化**：YAML + `RGX_*` env 覆盖、结构化日志轮转、Docker Compose、
  多阶段 Dockerfile、Makefile、CI/Integration/Nightly/Release 四条 workflow、
  ghcr.io 多架构镜像发布。

### Validated

- 与 RAGFlow `v0.27.x` 真实环境联调：数据集解析、Agent Workflow、审批与审计链
  （见 `doc/43_真实场景验证执行报告_20260902.md`）。

### Known Limitations

- M5 生产化闭环进行中（容灾路由、多实例治理等，见 `doc/103`）。
- Invoice / Payment 与组织树按规划红线不做。
- Agent 画布中"编译算子模板"能力开发中，创建入口已禁用。
- K8s / Helm 编排为后续演进方向，当前推荐 Docker Compose 部署。

[Unreleased]: https://github.com/DavidSche/ragflow-x/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/DavidSche/ragflow-x/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/DavidSche/ragflow-x/releases/tag/v0.1.0
