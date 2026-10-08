# 内置角色与权限矩阵（RAGFlow-X）

> 版本：v1.14 | 日期：2026-10-08\
> 依据：`internal/db/migrate.go` 的 `BuiltinPermissionMatrix()`。该函数是内置角色权限的唯一权威来源；服务启动时按矩阵整体 reconcile，不使用平台通配权限。本版同步资源同步、Answer Delivery 与知识任务路由落地后的矩阵增量：新增 `ragflow-sync` 资源，收敛 Answer Delivery 与知识任务路由的资源归属。迁移 128 专门修复“数据库已到最新 schema 但内置权限矩阵新增条目未回填”的存量库场景。

## 1. 模型与术语

RAGFlow-X 是企业私有环境的 AI/RAG 控制面。产品语义使用 **工作区（Workspace）**；数据库、API 参数和兼容层继续使用 `tenant` / `tenant_id` 技术标识。`Platform` 是控制面租户，不承载业务资源。

- 权限按 **资源 × 操作** 授权，常用操作为 `read`、`manage`、`execute`；文档新增/追加使用 `append`，文档贡献者删除本人上传使用 `delete:own`，智能体会话使用 `session:create`，企业连接连通性测试使用 `test`，跨工作区治理使用 `governance.read`、`governance.manage`、`governance.read_sensitive`、`tenant.resource.*`、`tenant.connection.*`、`tenant.audit.read`、`tenant.usage.read` 等细粒度治理动作。
- 判定规则：显式 `deny` 优先于 `allow`；未显式配置的 `(资源, 操作)` 默认拒绝。
- 父角色继承沿 `parent_id` 聚合；内置角色不设置父角色。
- `platform_admin` 权限已显式枚举，不再是 `* / *` 超级用户。跨工作区治理还必须具备 `governance.read/manage tenant`。
- ABAC / Governance Scope 在 RBAC 之上继续收敛边界：普通工作区用户默认 `CURRENT`；跨工作区读需要平台治理权限并使用服务端解析的 `SPECIFIC` / `ALL_AUTHORIZED`。

## 2. 角色概览

| 角色标识 | 显示名称 | 作用域 | 定位 |
|---|---|---|---|
| `platform_admin` | 平台管理员 | platform | 平台治理、跨工作区只读治理、系统配置与企业连接管理 |
| `tenant_admin` | 工作区管理员 | workspace | 管理本工作区组织、内容、模型、审批与审计 |
| `operator` | 内容运营员 | workspace | 日常内容、任务、对话和应用运营，可管理授权资产，不做工作区级治理 |
| `business_user` | 业务使用者 | workspace | 使用统一对话中心助手，可向既有数据集追加文档并删除本人上传文档，不管理应用、提示词和工作区 |
| `viewer` | 只读访客 | workspace | 授权业务资源只读 |
| `team_admin` | 团队管理员 | workspace（owner 边界） | 管理本人负责团队及绑定项目/数据集 |

> 前端侧边栏的“视图”不是可分配角色，也不是新的 RBAC 对象。导航视图必须由上表内置角色或自定义角色的生效权限推导。内置角色到导航视图的映射如下：

| 内置角色 / 权限来源 | 前端导航视图 |
|---|---|
| `platform_admin` | 平台管理员视图 |
| `tenant_admin`、`team_admin` | 工作区管理员视图 |
| `operator` | 内容运营员视图 |
| `business_user` | 业务使用者视图 |
| `viewer` | 只读视图 |
| 自定义角色且显式拥有 `audit:read` | 审计视图 |

`user` 是用户表的历史默认角色标记；没有生效权限时也落入只读视图。自定义角色不按角色名推断视图，只按生效权限推断，审计视图必须由 `audit:read` 显式放行触发。

## 3. 详细权限矩阵

图例：`R=read` 查看 · `M=manage` 管理 · `E=execute` 执行 · `A=append` 新增/追加文档 · `D=delete:own` 删除本人上传 · `S=session:create` · `T=test` 连通性测试 · `-`=无权限。

| 资源 | `platform_admin` | `tenant_admin` | `operator` | `business_user` | `viewer` | `team_admin` |
|---|---|---|---|---|---|---|
| governance-tenant | R M | - | - | - | - | - |
| tenant / workspace | R M | R | - | - | - | - |
| system | R M | - | - | - | - | - |
| role | R M | R M | - | - | - | - |
| branding | R M | R M | - | - | - | - |
| user | R M | R M | R | R | R | R |
| team | R M | R M | R | - | R | R M（own） |
| project | R M | R M | R | - | R | R M（own） |
| dataset | R M | R M | R | R | R | R M（own） |
| dataset-export | R | R | - | - | - | - |
| document | R A D E | R A D E | R A D E | R A D | R | R A D E（own） |
| task | R M E | R M E | R M E | - | R | - |
| model-provider | R M E | R M E | - | - | - | - |
| enterprise-connection | R M T | R T | - | - | - | - |
| db-connection | R M T | R M T | - | - | - | - |
| model-route | R M | R M | - | - | - | - |
| api-key | R M | R M | - | - | - | - |
| usage | R | R | R | - | R | - |
| usage-export | R | R | - | - | - | - |
| audit | R M | R | - | - | - | - |
| audit-anchor | R | R | - | - | - | - |
| audit-export | R | R | - | - | - | - |
| dashboard | R | R | R | - | R | - |
| system-health | R | R | - | - | - | - |
| assistant | R M E | R M E | R E | R E | R | - |
| knowledge-ops | R M | R M | R M | - | R | - |
| chat | R M E | R M E | R E | R E | R | - |
| search-app | R M E | R M E | R E | R E | R | - |
| memory | R M E | R M E | R M E | R E | R | - |
| agent | R M E S | R M E S | R M E S | R E S | R | - |
| ragflow-sync | R M | - | - | - | - | - |
| scenario-template | R M | R M | R | - | R | R M（own） |
| prompt-policy | R M | R M | R M | - | R | R |
| parser-policy | R M | R M | R | - | R | R |
| quality-profile | R M | R M | R | - | R | R |
| logical-document | R M E | R M E | R | R | R | R M E（own） |
| tool-registry | R M E | R M E | - | - | - | - |
| source-routing-rule | R M | R M | R | - | R | - |
| knowledge-strategy | R M E | R M E | R E | - | R | R M E（own） |
| evidence-snapshot | R | R | R | - | R（own） | - |
| outbox-event | R E | R E | R | - | - | - |
| knowledge-lifecycle | R M | R M | R | - | R | R |
| eval-set | R M E | R M E | R M E | - | R | R M（own） |
| release-governance | R M | R M | R | - | R | - |
| alert | R M | R M | R | - | - | - |
| approval | R M E | R M E | R E | - | R | - |
| approval-policy | R M | R M | - | - | - | - |

> `memory` 的操作为 `R M E`：`manage` 仅用于记忆配置增删改，消息读取/检索/写入分别由 `read`/`execute` 覆盖，与 `agent` 一样不含 `session:create`。

> `team_admin` 的 `own` 表示仅在调用者是团队 owner 时生效。跨工作区写不因角色自动放行；仍需治理授权、目标工作区校验和审批通道。

> `knowledge-strategy` 的 `team_admin own` 已通过 dataset→project→owned team ABAC 生效：对象路由按 dataset 解析 project，创建/更新/列表在服务层收敛到 owned team 绑定项目；成员关系本身不放行，无 owned project 时列表为空且创建拒绝。

> `evidence-snapshot` 的 `team_admin own` 通过 Evidence Snapshot Dataset → DatasetLink.Project → Owned Team 解析链生效；快照任一 DatasetID 越界或投影不完整时拒绝，列表在 SQL 分页前收敛。成员关系本身不放行，无可用 Dataset 时列表为空。

> `outbox-event` 只读状态给运营排查；手动 `execute` 重试仅授予平台/工作区管理员。事件投影不返回 Payload 或 LastError，已发布事件不可重试。

> `eval-set` 的 `execute` 仅用于 Stale Evidence 的重新验证，授予平台/工作区管理员和运营员；Evidence Set 编辑仍走 `manage`。

## 4. 特殊动作与边界

### platform_admin

- 平台治理资源显式授权，包括 `system`、`tenant`、`role`、`branding`、审计修复、平台配置和 `ragflow-sync`（RAGFlow 资源同步的设置、扫描、导入、分配、Reconcile、Relink 与冲突解决）。
- `governance.read tenant` 允许服务端解析 `SPECIFIC` / `ALL_AUTHORIZED` 治理视图；`governance.manage tenant` 是受控跨工作区操作的前置授权；`governance.read_sensitive tenant` 控制敏感治理字段读取。
- 企业连接可创建、修订、绑定治理、生命周期管理和连通性测试。

### tenant_admin

- 管理本工作区用户、角色、团队、项目、数据集、任务、模型、路由、API Key 和审批。
- 企业连接仅可读取与测试；创建、修订和绑定企业连接仍按审批与平台/归属工作区规则处理。
- 可读本工作区审计与锚点，但不能修复审计链；审计修复由 `platform_admin` 完成。

### operator / 内容运营员

- 可执行文档、任务、对话、检索应用、助手和 Agent；名称中的“内容”强调业务内容运营，不表示普通只读用户。
- 可管理任务、记忆、Agent、知识运营、提示词策略和评测集。
- 不可管理系统、工作区、角色、品牌、模型、API Key 和审计。

### business_user / 业务使用者

- 使用统一对话工作台、检索应用和 Agent；可读取助手、应用、记忆和既有数据集。
- 只能向既有数据集新增/追加文档（`document append`），并删除本人上传且归属记录完整的文档（`document delete:own`）；不能创建/修改数据集配置、解析/停止文档、删除他人文档或改写分块。
- 可按 `chat:read` 读取并导出自己的回答快照（Answer Delivery），但无知识运营管理权限。
- 不管理提示词策略、Agent Workflow、知识运营、评测集、任务、模型、API Key、角色和工作区。

### viewer

- 只保留授权业务资源和审批记录的 `read`，无 `manage`、`execute`、`session:create`。
- Answer Delivery 导出路由按 `chat:read` 授权，`viewer` 因此可导出其可见回答的快照；但每次下载仍有请求者、归属与 Retention 校验，无会话创建与执行能力。

### team_admin

- 仅能管理本人负责的团队和其绑定项目/数据集；成员只能按普通权限查看。
- 无模型、API Key、审计、审批和平台治理权限。

## 5. 权限配置三态与继承

权限分配页每个 `(资源, 操作)` 支持继承、允许和拒绝：

- **inherit**：未显式配置；自定义角色沿 `parent_id` 查找最近显式规则，整链未命中则拒绝。
- **allow**：显式放行。
- **deny**：显式拒绝；链路上任一拒绝优先。

内置角色无父角色，不向上继承。存量库中的历史通配权限会在启动 reconcile 时删除。

## 6. 导航与权限联动

- 前端通过 `/auth/me` 返回的生效权限集动态显隐菜单，未映射或未授权资源默认不可见。
- 前端导航 Profile 是视图收敛层；`platform_admin`、`tenant_admin`、`operator`、`business_user`、`viewer`、`team_admin` 六个内置角色与 `BuiltinPermissionMatrix()` 保持一一映射，审计视图仅由权限推导。文档归属删除（`rgx_document_ownership`）由 ABAC 归属校验承载，历史文档没有可靠上传者归属时仍由 `execute document` 治理角色管理。
- 系统设置与 RAGFlow 资源同步使用独立资源 `ragflow-sync`，不从 `system` 权限隐式继承；仅 `platform_admin` 持有，工作区管理员无该权限。
- Answer Delivery 不新增资源：`/answer-snapshots/*`（Snapshot 读取、ExportJob、下载）按导出语义归入 `chat:read`；知识任务、Impact Report、Duplicate Candidate 及其决策按治理语义归入 `knowledge-ops:read/manage`；知识资产地图沿用 `knowledge-lifecycle:read`。因此 `business_user` 与 `viewer` 都能导出各自可见回答；但只有持有 `knowledge-ops:manage` 的角色能创建影响面报告或处置重复候选。
- `/template-instances` API 族与 `/assistants/:id/rollout-policies` 不新增资源：按导出语义复用 `release-governance:read/manage` 与 `scenario-template:manage`，故本表不新增行。审计级导出新增独立资源 `audit-export`（动作仅 `read`，仅 `platform_admin`/`tenant_admin` 持有，对应 `GET /answer-snapshots/:snapshotId/audit-export`），与业务导出（`chat:read`）分离；幂等键前缀 `audit:` 保证与业务导出通道不冲突。
- 侧边栏按使用流程组织，不按后端表名简单罗列：知识准备、助手与应用、对话与体验分别成组；生命周期、审批、网关、审计和系统配置也保持独立入口。`system` 不再混入网关与模型分组。
- `enterprise-connection`、`asset-governance`、`scenario-templates`、`release-governance`、`alerts` 等组合页必须分别映射其子资源权限，不能默认沿用前端资源名。
- `conversation-center` 是前端导航与权限矩阵 UI 的聚合别名，不是后端 RBAC 资源。其聚合规则为“任一 `chat:execute`、`search-app:execute` 或 `agent:execute` 允许”；对话运行、会话创建、附件、路由和导出仍由底层资源按各自动作授权。因此 `platform_admin`、`tenant_admin`、`operator`、`business_user` 可见并完整使用对话中心；`viewer` 只有三类资源的 `read`，`team_admin` 无这三类权限，二者的对话中心菜单均不可见。
- `workbench` 是旧问答工作台的兼容权限别名，仅在权限矩阵 UI 中把 `workbench:execute` 映射为 `chat:execute`；它同样不是后端 RBAC 资源，也不再作为侧边栏导航项或 `/workbench` 页面资源。`/workbench` 仅保留 URL 兼容重定向。
- Fact Guard 运行时 API 不新增 RBAC 资源：`POST /tool-routing/answer-runs`、`POST /tool-routing/answer-runs/:answerRunId/evidence-facts` 复用 `tool-registry:execute`；`GET /tool-routing/answer-runs/:answerRunId/evidence-facts` 与 `GET /tool-routing/answer-runs/:answerRunId/fact-guard` 复用 `tool-registry:read`。该组路由仍受保护组默认拒绝约束，读响应会裁剪 LLM 原文、声明值和 FactIDs 数组。
- 新增资源需要同步：模型/Handler 授权检查、`BuiltinPermissionMatrix()`、`PermissionCatalog()`、前端 `RESOURCE_ACCESS` 映射和侧边栏分组。

## 7. 校验基线

- 权威矩阵：`internal/db/migrate.go: BuiltinPermissionMatrix()`。
- 回归：启动后内置角色权限被整体替换，禁止历史 `* / *` 残留；幂等回归见 `internal/db/migrate_p0_test.go: TestP0_DBMIG_001_MigrationAndBuiltinRoleReconciliationAreIdempotent`。
- 内置角色目录（六个角色、名称、作用域、描述）由 `internal/service/role_test.go: TestBuiltinRoleCatalogMatchesDoc30` 守护；权限目录由 `PermissionCatalog()` 聚合，前端角色权限 UI 只展示目录内的 `(资源, 动作)`。
- 文档同步状态：本表已同步 `ragflow-sync` 拆分、Answer Delivery 权限映射、Fact Guard 运行时 API 复用授权与最新最小权限矩阵。
