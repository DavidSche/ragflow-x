# 内置角色与权限矩阵（RAGFlow-X）

> 版本：v1.18 | 日期：2026-11-13\
> 依据：`internal/db/migrate.go` 的 `BuiltinPermissionMatrix()`。该函数是内置角色权限的唯一权威来源；服务启动时按矩阵整体 reconcile，不使用平台通配权限。本版同步资源同步、Answer Delivery 与知识任务路由落地后的矩阵增量：新增 `ragflow-sync` 资源与 `model-default` 平台级资源（doc/269，迁移 163），收敛 Answer Delivery 与知识任务路由的资源归属。迁移 128 专门修复“数据库已到最新 schema 但内置权限矩阵新增条目未回填”的存量库场景。

## 1. 模型与术语

RAGFlow-X 是企业私有环境的 AI/RAG 控制面。产品语义使用 **工作区（Workspace）**；数据库、API 参数和兼容层继续使用 `tenant` / `tenant_id` 技术标识。`Platform` 是控制面租户，不承载业务资源。

- 权限按 **资源 × 操作** 授权，常用操作为 `read`、`manage`、`execute`；文档新增/追加使用 `append`，文档贡献者删除本人上传使用 `delete:own`，智能体会话使用 `session:create`，企业连接连通性测试使用 `test`，跨工作区治理使用 `governance.read`、`governance.manage`、`governance.read_sensitive`、`tenant.resource.*`、`tenant.connection.*`、`tenant.audit.read`、`tenant.usage.read` 等细粒度治理动作。
- 判定规则：显式 `deny` 优先于 `allow`；未显式配置的 `(资源, 操作)` 默认拒绝。
- 蕴含规则：`manage`/`execute`/`append`/`test`/`session:create`/`delete:own` 在授权求值时**蕴含同资源 `read`**——有写权一定有列表/详情视图，内置角色与自定义角色同一套语义；`deny` 不做蕴含（`deny execute` 只禁执行，不连坐查看）。
- 委派规则：自定义角色只能授予**授予者自己已持有**的权限（否则返回 403/40302）——权限目录是内置矩阵的并集，包含只有平台管理员才持有的动作，因此「动作在目录内」不等于「可以委派」。
- 父角色继承沿 `parent_id` 聚合；内置角色不设置父角色。
- `platform_admin` 权限已显式枚举，不再是 `* / *` 超级用户。跨工作区治理还必须具备 `governance.read/manage tenant`。
- ABAC / Governance Scope 在 RBAC 之上继续收敛边界：普通工作区用户默认 `CURRENT`；跨工作区读需要平台治理权限并使用服务端解析的 `SPECIFIC` / `ALL_AUTHORIZED`。

## 2. 角色概览

| 角色标识 | 显示名称 | 作用域 | 定位 |
|---|---|---|---|
| `platform_admin` | 平台管理员 | platform | 平台治理、跨工作区只读治理、系统配置与企业连接管理 |
| `tenant_admin` | 工作区管理员 | workspace | 管理本工作区组织、内容、模型、审批与审计 |
| `operator` | 内容运营员 | workspace | 日常内容、任务、对话和应用运营，可管理授权资产，不做工作区级治理 |
| `business_user` | 业务使用者 | workspace | 使用统一对话中心助手并浏览场景模板目录，可向既有数据集追加文档并删除本人上传文档，不管理应用、提示词和工作区 |
| `viewer` | 只读访客 | workspace | 授权业务资源只读 |
| `team_admin` | 团队管理员 | workspace（owner 边界） | 业务使用者能力 + 管理本人负责团队及绑定项目/数据集 |

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
| user-clearance | R M | R M | - | - | - | R |
| team | R M | R M | R | - | - | R M（own） |
| project | R M | R M | R | - | - | R M（own） |
| dataset | R M | R M | R | R | R | R M（own） |
| dataset-export | R | R | - | - | - | - |
| document | R A D E | R A D E | R A D E | R A D | R | R A D E（own） |
| task | R M E | R M E | R M E | - | - | - |
| compilation | R M E | R M E | R M E | - | - | R M E |
| model-provider | R M E | R M E | - | - | - | - |
| model-default | R M | R | - | - | - | - |
| enterprise-connection | R M T | R T | - | - | - | - |
| connector | R M | R M | - | - | - | - |
| db-connection | R M T | R M T | - | - | - | - |
| model-route | R M | R M | - | - | - | - |
| api-key | R M | R M | - | - | - | - |
| usage | R | R | R | - | - | - |
| usage-export | R | R | - | - | - | - |
| audit | R M | R | - | - | - | - |
| audit-anchor | R | R | - | - | - | - |
| audit-export | R | R | - | - | - | - |
| dashboard | R | R | R | - | - | - |
| system-health | R | R | - | - | - | - |
| assistant | R M E | R M E | R E | R E | R | R E |
| knowledge-ops | R M | R M | R M | - | - | - |
| knowledge-profile | R M | R M | R M | - | - | R M |
| chat | R M E | R M E | R E | R E | R | R E |
| search-app | R M E | R M E | R E | R E | R | R E |
| query-template | R M | R M | - | - | - | - |
| memory | R M E | R M E | R M E | R E | R | R E |
| memory-content | R M E | R M E | R E | R E | - | R M E |
| memory-forget | M E | M E | - | E | - | E |
| memory-operation | R | R | R | R | R | R |
| memory-policy | R M E | R M E | R M E | R E | R | R E |
| memory-write | E | E | E | E | - | E |
| agent | R M E S | R M E S | R M E S | R E S | R | R E S |
| ragflow-sync | R M | - | - | - | - | - |
| scenario-template | R M | R M | R | R | R | R M（own） |
| scenario-template-baseline | R M | - | - | - | - | - |
| trial-run | R E | R E | R E | R | R | R E |
| prompt-policy | R M | R M | R M | - | - | R |
| parser-policy | R M | R M | R | - | - | R |
| quality-profile | R M | R M | R | - | - | R |
| logical-document | R M E | R M E | R | R | R | R M E（own） |
| document-governance | R M E | R M E | R | - | - | R |
| tool-registry | R M E | R M E | - | - | - | - |
| source-routing-rule | R M | R M | R | - | - | - |
| context-decision | R | R | R | R | R | R |
| knowledge-strategy | R M E | R M E | R E | - | - | R M E（own） |
| evidence-snapshot | R | R | R | - | - | R |
| outbox-event | R E | R E | R | - | - | - |
| knowledge-lifecycle | R M | R M | R | - | - | R |
| eval-set | R M E | R M E | R M E | - | - | R M（own） |
| release-governance | R M E | R M E | R E | - | - | R E |
| alert | R M | R M | R | - | - | - |
| approval | R M E | R M E | R E | - | - | - |
| approval-policy | R M | R M | - | - | - | - |

> `memory` 的操作为 `R M E`：`manage` 仅用于记忆配置增删改，消息读取/检索/写入分别由 `read`/`execute` 覆盖，与 `agent` 一样不含 `session:create`。

> `team_admin` 的 `own` 表示仅在调用者是团队 owner 时生效。跨工作区写不因角色自动放行；仍需治理授权、目标工作区校验和审批通道。

> `knowledge-strategy` 的 `team_admin own` 已通过 dataset→project→owned team ABAC 生效：对象路由按 dataset 解析 project，创建/更新/列表在服务层收敛到 owned team 绑定项目；成员关系本身不放行，无 owned project 时列表为空且创建拒绝。

> `evidence-snapshot` 的 `team_admin own` 通过 Evidence Snapshot Dataset → DatasetLink.Project → Owned Team 解析链生效；快照任一 DatasetID 越界或投影不完整时拒绝，列表在 SQL 分页前收敛。成员关系本身不放行，无可用 Dataset 时列表为空。

> `outbox-event` 只读状态给运营排查；手动 `execute` 重试仅授予平台/工作区管理员。事件投影不返回 Payload 或 LastError，已发布事件不可重试。

> `eval-set` 的 `execute` 仅用于 Stale Evidence 的重新验证，授予平台/工作区管理员和运营员；Evidence Set 编辑仍走 `manage`。

> `memory-content` / `memory-forget` / `memory-operation` / `memory-policy` / `memory-write` 是记忆子系统的细分资源：`memory`（记忆配置与总体读）、`memory-content`（消息内容的读/写/管理）、`memory-write`（记忆写入 `execute`，全员除 viewer）、`memory-forget`（遗忘执行，无 `read`）、`memory-operation`（运行状态只读）、`memory-policy`（记忆策略治理）。`business_user` 可写/遗忘与本人相关记忆内容，但不可管理记忆策略。

> `document-governance` 承载文档治理（归属投影、归因、审批与恢复运行；`execute` 仅授予平台/工作区管理员）；`compilation` 承载知识编译运行；`knowledge-profile` 承载知识画像；`context-decision` 为上下文决策只读（全体业务角色）；`query-template` 为查询模板（平台/工作区管理员）；`user-clearance` 为用户数据许可（敏感内容最小授权，`read` 含团队管理员）。

## 4. 特殊动作与边界

### platform_admin

- 平台治理资源显式授权，包括 `system`、`tenant`、`role`、`branding`、审计修复、平台配置和 `ragflow-sync`（RAGFlow 资源同步的设置、扫描、导入、分配、Reconcile、Relink 与冲突解决）。
- `governance.read tenant` 允许服务端解析 `SPECIFIC` / `ALL_AUTHORIZED` 治理视图；`governance.manage tenant` 是受控跨工作区操作的前置授权；`governance.read_sensitive tenant` 控制敏感治理字段读取。
- 四条细粒度平台动作自 `doc/248` 起真正生效，且都保留 `governance.*` 作为并行入口（老角色行为不变）：`tenant.audit.read` 可跨工作区读取审计与审计链锚点，`tenant.usage.read` 可跨工作区读取用量与用量导出，`tenant.resource.manage` 可修改其他工作区设置，`tenant.resource.delete` 可删除工作区（与“可改”分离，只改不删的平台运营岗不会误删）。这四条只解决“同一能力更细的委派”，不放大权限：`tenant.audit.read` 不会带来 usage 或通用治理读。
- 工作区写操作分两层：**管理面**（路由级 `manage tenant`，决定能否进入工作区管理接口）与**操作面**（细粒度动作，决定能做什么）。`tenant.resource.manage` 覆盖新建与修改工作区，`tenant.resource.delete` 只覆盖删除；委派平台运营岗时两者需按需组合，缺哪个就做不了哪个。审计面按“一个证据能力”处理：持 `tenant.audit.read` 即可跨工作区读取审计日志、审计链锚点与审计导出。
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
- 可读场景模板目录（`scenario-template:read`）：这是对话中心空态唯一的非 403 出口，也是了解“本工作区能提供什么”的入口；创建仍需 `chat:manage` / `scenario-template:manage`，不在本角色范围内。
- 可读试跑结果（`trial-run:read`）：助手“练过没练过”是可消费信号，故保留读取；但**发起/取消试跑需要 `trial-run:execute`，本角色不持有**（试跑属于创作动作）。
- 不管理提示词策略、Agent Workflow、知识运营、评测集、任务、模型、API Key、角色和工作区。
- **读权不扩张原则（v0.5.0 收紧为硬约束）**：`viewer` 的读集合是本角色读集合的**真子集**（`TestViewerOnlyReadsAreGone` 守护）。`doc/245` 之前还留了一条“显式声明 `none` 即可豁免”的口子，现在没有了——新资源必须先是业务角色可读，才谈得上只读角色可读。

### viewer

- **业务使用者的只读切片**（`doc/245`）：只在 14 个业务读面上持有 `read`——助手、对话、检索应用、Agent、数据集、文档、逻辑文档、记忆、记忆策略、记忆运行状态、上下文决策、场景模板、试跑、成员目录；无 `manage`、`execute`、`append`、`session:create`。
- **不再持有租户级运营/治理/配置读面**：任务队列、用量、经营看板、`knowledge-ops`、审批单、评测集、发布治理、证据快照、知识生命周期、`document-governance`、以及 `prompt-policy`/`parser-policy`/`quality-profile`/`knowledge-strategy`/`knowledge-profile`/`source-routing-rule` 这些阈值与策略配置，均已在 migration 161 中收回。理由：这些都是“工作区怎么运转/怎么被调优”，不是“工作区产出了什么”；只读访客能看而付费成员看不到，是 `doc/242` 读权倒挂的镜像缺陷。
- 需要看治理台账的岗位是 `team_admin`（own 范围）与 `operator`/`tenant_admin`，不是 `viewer`。
- Answer Delivery 导出路由按 `chat:read` 授权，`viewer` 因此可导出其可见回答的快照；但每次下载仍有请求者、归属与 Retention 校验，无会话创建与执行能力。

### team_admin

- **业务使用者 + 团队范围管理**（`doc/243`）：完整持有 `business_user` 的全部权限（对话、检索、助手、记忆的 `read`/`execute`，含 `agent:session:create`），再叠加本人负责团队及其绑定项目/数据集的 `own` 管理面。
- 因此团队管理员可以进入对话中心，并能对自己团队的模板发起试跑：`scenario-template:read`（目录）+ **`trial-run:execute`**（`doc/244` 起试跑是独立资源，不再借用 `eval-set:execute`）。`manage` 仍收敛到 owned team。
- 不持有 `eval-set:execute`：该动作同时驱动 `POST /eval-cases/:id/revalidate`（租户级证据重算、无 team ABAC），`doc/129` 明确只授予 platform/tenant/operator；试跑不因此受限，因为试跑已改用 `trial-run:execute`。
- 无模型、API Key、审计、审批和平台治理权限；也不持有租户级观察者读面（`task`/`usage`/`dashboard`/`knowledge-ops`/`approval` 等无 team 维度收敛，给出去等于用 RBAC 换租户级可见性）。
- 该角色与 `business_user` 的关系由 `internal/db/role_lattice.go` 声明并由 `TestRoleSupersetRelationsHoldOnMatrix` 守护：`team_admin ⊇ business_user`，不允许回退。

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
- `conversation-center` 是前端导航与权限矩阵 UI 的聚合别名，不是后端 RBAC 资源。其聚合规则为“任一 `chat:execute`、`search-app:execute` 或 `agent:execute` 允许”；对话运行、会话创建、附件、路由和导出仍由底层资源按各自动作授权。因此 `platform_admin`、`tenant_admin`、`operator`、`business_user` 可见并完整使用对话中心；`viewer` 只有三类资源的 `read`；`team_admin` 自 `doc/243` 起持有三类资源的 `read`+`execute`，对话中心可见。
- `workbench` 是旧问答工作台的兼容权限别名，仅在权限矩阵 UI 中把 `workbench:execute` 映射为 `chat:execute`；它同样不是后端 RBAC 资源，也不再作为侧边栏导航项或 `/workbench` 页面资源。`/workbench` 仅保留 URL 兼容重定向。
- Fact Guard 运行时 API 不新增 RBAC 资源：`POST /tool-routing/answer-runs`、`POST /tool-routing/answer-runs/:answerRunId/evidence-facts` 复用 `tool-registry:execute`；`GET /tool-routing/answer-runs/:answerRunId/evidence-facts` 与 `GET /tool-routing/answer-runs/:answerRunId/fact-guard` 复用 `tool-registry:read`。该组路由仍受保护组默认拒绝约束，读响应会裁剪 LLM 原文、声明值和 FactIDs 数组。
- 新增资源需要同步六处：`internal/router/route_registry.go`（注册表形状）、Handler 授权检查、`BuiltinPermissionMatrix()`（`PermissionCatalog()` 由它派生）、`internal/db/permission_intent.go`（`business_user` 读意图）、本表与 en 版、前端 `authProvider.ts` 的 `RESOURCE_ACCESS` 与侧边栏分组；并为存量库新增一条**纯数据幂等迁移**。`trial-run`（`doc/244`）是该流程的完整样例。

## 7. 校验基线

- 权威矩阵：`internal/db/migrate.go: BuiltinPermissionMatrix()`。
- 回归：启动后内置角色权限被整体替换，禁止历史 `* / *` 残留；幂等回归见 `internal/db/migrate_p0_test.go: TestP0_DBMIG_001_MigrationAndBuiltinRoleReconciliationAreIdempotent`。
- 内置角色目录（六个角色、名称、作用域、描述）由 `internal/service/role_test.go: TestBuiltinRoleCatalogMatchesDoc30` 守护；权限目录由 `PermissionCatalog()` 聚合，前端角色权限 UI 只展示目录内的 `(资源, 动作)`。
- 文档同步状态（v0.5.2 / 迁移 166 口径）：矩阵收敛后共 **429 行 / 61 个资源 / 读权倒挂 0**；`viewer` 只保留 14 个业务资源只读，`trial-run` 自迁移 160 起是独立资源（`read`/`execute`），`business_user ⊇ viewer` 与 `team_admin ⊇ business_user` 两条超集边由 `role_lattice.go` 与测试守护。本表同时已同步 `ragflow-sync` 拆分、Answer Delivery 权限映射、Fact Guard 运行时 API 复用授权、最新最小权限矩阵，以及 M-B 治理/记忆/编译新增资源（`compilation`/`context-decision`/`document-governance`/`knowledge-profile`/`memory-content`/`memory-forget`/`memory-operation`/`memory-policy`/`memory-write`/`query-template`/`user-clearance`）与版本评审运行 `execute` 权限、`evidence-snapshot` 团队只读（2026-10-22）。新增 `model-default` 平台级资源：`platform_admin` 可读可写，`tenant_admin` 只读（doc/269）。新增 `scenario-template-baseline` 平台级资源：仅 `platform_admin` 读/管，工作区经 catalog 消费基线、经 `fork` 生成自己的 `scenario-template` 行（doc/276）。
