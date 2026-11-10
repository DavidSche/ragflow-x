# Changelog

本项目所有显著变更记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

> 引用约定：文中 `doc/NNN`、`doc/<目录>` 为工程内部设计/性能记录编号，**不随本仓库发布**，仅用于追证；
> 随仓库发布的用户文档在 [`doc/user`](./doc/user/README.md)（用户手册、权限矩阵、运维 Runbook、审计链 SOP）。

## [Unreleased]

### 修复

- **并发扫描会互相踩掉对方的运行租约**：手动扫描与手动重建在插入
  `scanning`/`running` 状态的 run 时没有写 `lease_expires_at`，而下一次扫描开头的
  陈旧恢复把「无租约」视为历史遗留行直接判失败，于是第二个扫描既拿到了自己的 run，
  又没能被「同一数据源同时只有一个 run」的闸门拦住（P0 `SC-SYNC-001` 在 CI 上以
  `success=2 conflict=0` 偶发暴露）。现在两条创建路径在插入时即持租约
  （`ragflow-sync-worker`，30min），并发闸门恢复确定性；`SC-SYNC-001` 用例改为用
  上游列举闸门固定交错点，不再依赖调度运气。
- **sqlite 默认没有开 WAL（文档说开、代码没开）**：`db` 包注释宣称 WAL + NORMAL，
  但默认 pragma 只有 `busy_timeout`/`synchronous`，于是每次提交都要 fsync 回滚日志——
  Windows CI runner 上单次写约 200ms，迁移种子行就要几分钟，`go-build` 的 windows 分片
  在 50min 超时里仍跑不完全量用例。现在默认补上 `journal_mode(WAL)`（内存库自动跳过，
  调用方显式指定则仍以调用方为准），并加三条 db 包测试把「文档里写的」和「实际生效的」钉住。
- **「最新一条」查询在时钟同刻时不成立**：`time.Now()` 在同一时钟刻度内会返回相同值
  （WAL 之后写变快，连续插入落在同刻的概率大幅上升），而排序只按 `created_at DESC`：
  路由评估运行、跨端最近会话等「取最新」的读取会随机返回并列行中的任意一条。表现为
  P0 `SC-ROUTE` 门禁在 `passed` 与 `insufficient_samples` 之间跳变、最近会话列表顺序漂移。
  现在评估运行排序用物理插入序做次级键（sqlite `rowid` / postgres `ctid`，这些表只追加），
  最近会话排序补齐 `last_turn_at` + 应用类型 + 会话 ID 的完整次序；`UpsertKnowledgeOpsEvent`
  不再用写库时刻覆盖调用方给的 `updated_at`。
- **陈旧窗口边界判定依赖时钟刻度**：审批操作只按严格 `updated_at < cutoff` 判陈旧，
  当两次 `time.Now()` 落在同一刻度时「恰好在窗口边界」的判定翻转为不陈旧；改为边界含等
  （`<=`），与执行器侧「`elapsed < window` 才算仍在进行」严格互补。网关配额陈旧预留的用例
  也不再依赖「刚写入的行一定早于下一次 `time.Now()`」这一时钟假设。

下一版本（v0.6.0）已登记、尚未开工：

- **RBAC `contract` 半步（R-27）**：把 `tenant.audit.read` / `tenant.usage.read` /
  `tenant.resource.manage` / `tenant.resource.delete` 从「与 `governance.*` 并行的宽权闸门」收窄为唯一闸门，
  需先发 deprecation 通知 + 自定义角色预检（`doc/248` §8）。
- **知识编译 `structure` 产物独立建模（B′）**：v1.0.0 把 `page_index` 重分类为结构变体，若要支持
  tree/graph/mindmap/timeline 产物，应另立 artifact kind（`doc/262` §7.1 D3 边界）。
- **数据源连接器**（注册/授权/定时同步调度）仍是数据管道闭环的主要缺口（`doc/41` §20.1）；
  知识图谱 / MCP 全功能写路径为登记先行（运行期显式 501）。

## [0.5.0] - 2026-11-10

> **本版是 v0.2.0 之后的第一个正式发布**：把 v0.3.0（137 知识编译 + 企业 Memory / 214 统一对话体验 /
> M-B 治理与精确 MVP 六主题）、v0.4.0（WS7-min 模板单一事实源 + 试跑）、v0.5.0（RBAC 读权收口 +
> 上游 RAGFlow v1.0.0 兼容）三条版本线合并交付。这三条线此前只以草稿形式存在于 `[Unreleased]`，
> 从未打过 tag；本条目按「合入当前发布」重排，能力清单与证据引用保持原样。

### 发布重点

- **上游 RAGFlow v1.0.0 正式版兼容**：97 条端点 `MaxVerified` 抬到 `v1.0.0`，修复 5 类真实漂移
  （建库 schema、会话端点、产物面、画布列表面、文档级解析绑定），并把「版本带 → 产物 kind」
  做成显式声明 + 入口 fail-closed（`doc/262` §7.1 D1–D5）。
- **安全与权限收口**：自定义角色提权修复、"写蕴含读"语义单点强制、`viewer` 读面收敛为
  `business_user` 的只读切片（读权倒挂 19 → 0）、平台细粒度动作真正接线。
- **治理与证据**：Release Gate 11 门、文档级治理投影、引用精确定位、多维配额、通知事件流；
  「试跑与 D1 冻结 bundle 不得进入发布门」由测试钉死。
- **单一事实源**：场景模板由后端 seed 为唯一内容源，向导/空态完全由 API 驱动。
- **质量证据**：`scripts/upstream-verify.sh --full` 在 v1.0.0 上 **5 passed / 0 failed / 0 skipped**。

### Added

#### 上游引擎兼容（RAGFlow v1.0.0）

- **端点注册表 stable 抬版钉**：97 条端点 `MaxVerified` 由 `v1.0.0-rc3` 统一抬至 `v1.0.0`（生产面 + live contract drill 重验）；能力注册表 `MinVersion` 仍为 `0.27.0`，`MaxVerified` 带写作 `0.27.*|1.0.0`
  （`internal/service/capabilities.go` 共 12 行，本版新增 `knowledge_compilation`）。
- **建库 schema 漂移 D1**：v1.0.0 建库必须显式给 `embedding_model`（缺省落 `"0"` →
  `500 model configuration unavailable`）；`parse_type` 为 **int**（1=BuiltIn，2=Pipeline）；
  `chunk_method`/`description`/`version` 等报 `extra inputs are not permitted`。适配层按版本分派
  请求形状，旧版本行为字节不变。
- **会话端点漂移 D2**：`POST /chats/{id}/completions` 在 v1.0.0 → 404，改用全局
  `POST /chat/completions`（`chat_id` + `session_id` + `question` + `stream:false`），
  provider 侧按版本选择路径并保留旧版本回退。
- **产物面漂移 D3 + 声明式映射**：v1.0.0 的 artifacts API 只暴露 `wiki_page`、检索只认
  `compile_kwd=wiki`，`page_index` 变体不可读。新增
  `internal/service/knowledge_compilation_engine_kind.go`——把「引擎版本带 → 可绑定 artifact kind」
  做成单点映射表（三态：`supported` / `unverified` / `unsupported`）：`1.0+` 上只有 `wiki` 是
  `supported`（探针证实 2–3 个 wiki_page 产物带 `content_md_rendered` + `source_chunk_ids`），
  `page_index` 为 `unsupported` + `alternative=wiki`（结构变体产物是 entity/relation 行，artifacts API 与
  检索都不暴露），其余 kind 记 `unverified`（允许跑，但 run detail 不得声称 payload-hash 绑定证据）。
  `TriggerCompilationRun` 在入口 **fail-closed**（`422/42261` + `use kind wiki instead`，不建 run 行），
  live drill 一律先过 `requireLegacyPageIndexBand` 判定（skip 而非虚假 FAIL）。
- **画布列表面漂移 D4（阻断级）**：v1.0.0 `GET /agents` 混入 `type:"compilation_template_group"` 行，
  把它当 canvas 用会拿到 `103 permission to access the agent`。`ListAgents` 现按 `Agent.Type`
  过滤模板组并按过滤后条数修正 `total`（单 agent 查询与分页语义不变）。
- **文档级解析绑定漂移 D5**：v1.0.0 `PATCH /datasets/{id}/documents/{doc}` 必须带
  `pipeline_version_id`（版本行只能由 `POST /agents/{id}/publish` 产生）；provider 新增
  `DocumentParseConfigUpdate.PipelineVersionID`（旧引擎忽略，向后兼容），Pipeline 库的首次解析与
  文档 re-bind（含改绑到另一个 canvas）都携带目标 canvas 的版本 id。
- **ingest 与编译解耦**：v1.0.0 每次 ingest 都会编译 wiki 产物，故首解析改走 **parse-only canvas**
  （无 Compiler：TokenChunker → Tokenizer），解析完成后再回绑 compiler pipeline——彻底解决
  「replacement drill 初次解析必失败」的历史缺陷。
- **默认模型解析**：v1.0.0 `/models/default` 只返回 chat 默认，embedding 默认由平台侧 best-effort
  解析（5 min TTL 缓存），不再因缺省 embedding 误判为引擎不可用。
- **live drill 预算对齐真实耗时**：上游共享 AMD 端点单次 map/reduce ~40s、排队可把编译拖过 20m，
  故编译相关 drill 预算改为「长于上游编译」——ctx 40m、文档等待 25m、binding 轮询 25m、
  服务组 `-timeout 75m`，**断言不放宽**；`--full` 全量串行复验
  **5 passed / 0 failed / 0 skipped**（binding 367.51s / replacement 57.27s / memory 54.99s /
  release 4.03s / P1 契约 4/4）。

#### 知识编译与企业记忆（137）

- **Knowledge Compilation**：知识编译全生命周期（Profile → Readiness Gate → Run → dispatch →
  artifact 绑定 → compiled-first 检索模式 → Improvement Gate A/B → 预算与成本归因 → recovery/零重摄取）；
  编译产物作为独立 artifact 参与检索排序（compiled-first，默认关闭、失败关闭）。
- **Enterprise Memory**：企业记忆策略（可写可查询范围）、Sealed Envelope 持久化、
  Context Pre-Model 读取授权、冲突/Tombstone、成员退出失效与对账补偿、隐私四权（同意/豁免/删除/审计）、
  Memory Policy 与豁免面板。

#### 治理与精确引用（M-B）

- **Release Governance & Observability（137/211/213）**：Release Gate 11 门自动校验、
  Governance Metrics 生产落位、Alertmanager 告警部署与送达演练、运营归因与通知 Hub。
- **升级版本带 preflight（223 T6.1–T6.3）**：Capability Registry 每条端点登记 `MinVersion/MaxVerified`；
  `VersionKnownIssue` 观测表 7 条外部版本差异；`VersionBandGapReport()` + 能力级 preflight
  `RunVersionPreflight`（fail-closed：探测失败/不可解析 → Block，新于已验证带 → VerificationRequired，
  低于带 → Degrade）；服务层 `capabilityUpgradeGate` 接入（`BLOCKED_CONTRACT_DRILL_REQUIRED` /
  `BLOCKED_VERSION_UNPARSEABLE`）。
  **v0.3.0 发版闭环接线（2026-10-22）【已实现】**：`CreateRelease` production 前置版本带 preflight——
  VerificationRequired/Block → HTTP `403/40076` fail-closed 拒绝发版；Degrade/Pass 放行并记录；
  引擎不可达容忍放行不卡死离线；观测版本/决策/原因落 `rgx_release` 行（migration 152
  `release_version_preflight`）；relcheck REL-003 并入版本带 gap 扫描（`relcheck/version_band.go`
  `CollectVersionBandGap`）；`release_version_preflight_test.go` 4 项 + version_band gate 断言全绿。
- **文档级治理与自动投影（223 T1/T2）**：治理字典 + Schema 绑定 + 自动投影（权威度连续数值走 schema
  门槛、auto 缺省 internal/published、Authority=50）、空集 fail-safe + `projection_degraded` 可观测、
  Lightweight 文档审批（轻量内联 + 文件级人工双通道）、文档级 ACL / governance read 路由
  （`SupportsGovernanceRead`）、migration 150 字典 seed 幂等；错误码 40131/40132/40133/40141/40142。
- **引用精确定位与表格投影（223 T3）**：eval v4 协议 citations 版本 + references 附件；表格投影专项
  （xlsx 族含扩展名识别、cell 值断言、表格对偶双边声明）；归因防火墙（off=allowed 不计数 /
  strict=block）；gate 条件输出 + WAIT 两条文案；537 场景总部回归 452/456s 全绿。
- **多维配额与失败分类（223 T5）**：Ledger 多维键（bucket 维度 tenant/model/assistant，migration 151
  `rgx_quota_bucket` + reservation，行级防超卖 guarded UPDATE + 防负 CASE + stale reap）；组合门
  `Preauthorize/Finalize/ReleaseGatewayQuotaWithBucket`（无桶退化为原 key 语义字节级不变）；
  **model 维度已接线**；bucket 维度 `430/42931`（HTTP 430 Would-Block），key 维度 `429/42930` 保持兼容；
  标准头三件套 `X-Request-Id`/`X-Trace-Id`/`X-Quota-Remaining`；失败分类表 `ClassifyGatewayFailure` 五类枚举
  （upstream/timeout/disconnect/quota/approval）。
- **通知事件流统一 + 五组导航（223 T8）**：notify Event 统一 header（tenant/object_type/action/approval_id/
  trace_id/deadline，零值省略保 legacy 字节兼容），审批 pending/decision/canceled/overdue/due_soon 全事件填充；
  前端五角色默认折叠导航（`NAVIGATION_VIEW_DEFAULT_EXPANDED`，身份角色解析后才播种、不覆盖用户手动折叠）；
  五角色走查 e2e 10 用例（desktop/mobile 双端钉死 `aria-expanded` 契约）。
- **高对比度 emulation 走查（219，SC-A11Y-001）【已实现】**：`web/e2e/high-contrast.spec.ts`
  forced-colors + dark emulation 下登录/侧栏/工作台 desktop+mobile 可用、无 page error（2 passed）；
  真机视觉与固定序列录屏仍为人工项。

#### 对话体验与知识运营（214 / 253 / 252）

- **统一对话中心体验（214）**：长会话滚动固定、输入框自动增长、命令面板（Palette）全键盘、
  顶部渐隐、双主题焦点环与可访问性增强；前端业务操作全量收敛于 RAGFlow-X UI（API-first，`doc/221` §5）。
- **检索与知识管理（C2/C5/C6/C1）**：检索测试面板（阈值/top_k/rerank 直通）、MCP Tool 治理化登记先行
  （运行时显式 501）、知识图谱只读视图、文档 chunk 编辑/新增（含跨租户审批治理）。
- **逐轮诚实度声明（P0-B，`doc/253` §13.2）**：每轮对话生成结构化 answerability 声明
  （answerable / confidence / source / abstain_reason / citation_count / distinct_docs），
  只注释不改写引擎答案；指标落到 `knowledge_ops` 事件与日志。
- **知识运营观测 levers（P2）**：`knowledge_ops` 事件新增 `top_similarity`（最高检索相似度）；
  看板新增 `avg_top_similarity` 与 `abstain_rate_warn`/`citation_rate_baseline`/`top_similarity_floor`
  三档运营阈值（`config.knowledge_ops` 可调，越线打标渲染）。
- **引用抽屉定位（P2，`doc/253`）**：`citation_reference` 新增 ref_index/positions 定位列；
  会话消息引用再水合——rc3/v1.0.0 sessions GET 不再回显 references，Conversation Center 按
  question+answer 哈希从完成时刻 answer snapshot 还原 Citations（T6/S14）。
- **搜索应用数据集绑定防呆（§13.3 P1，`doc/252`）**：`dataset_ids` 支持
  `link_id | link_name | engine_id` 解析并校验归租户（`validateOwnedDatasets`），错误提示给出近邻库名
  建议而不泄漏引擎原始报错；新增错误契约 **40092 / 40093 / 40095**（契约即文档）；
  `GET /search-apps/dataset-options` 向导数据面 + 前端创建/编辑下拉接线。
- **chatdrill（cmd/chatdrill）**：基于适配器自身代码路径对真实引擎做单轮聊天诊断（复用补齐平台栈即可执行），
  重验 chat/session/reference 契约；单轮/无并发/临时会话清理，解析到的 chat 模型非预期池时 fail-closed。

#### 模板单一事实源与试跑（WS7-min / v0.4.0 版本线）

- **模板单一事实源（T7.0）**：9 个内置场景模板由后端 seed 为唯一内容源（`rgx_scenario_template`，
  `source=builtin`，payload schema v2 含 zh 原文 + en 全量翻译），建租户与启动回填幂等；
  新增只读 `GET /scenario-template-catalog`（GET 不写库）；前端静态模板表下线，向导与空态完全由 API 驱动。
- **模板单一事实源修复（T7.0 复评）**：catalog 新增服务端解析后的 `prompt` 与 `parameters`；
  `mergeTemplateAuthoring` 改为「模板默认 + 仅覆盖显式字段」；向导删除本地 `PROMPT_PRESETS`/
  `PARAMETER_PROFILES` 与 `PromptPresetPicker`，chat 只提交 `llm_id`。`GET` 响应为增量字段，
  旧客户端不受影响（`doc/232` §4.1）。
- **模板 catalog 作用域修复（T7.0 复评二）**：删除 `scopeAll` 形参，强制按调用方工作区列出模板——
  修复「平台管理员在向导看到 29 个模板但 `instantiate-chat` 一律 404」的脱节；补 catalog 空态（zh/en）
  与跨租户隔离测试（`doc/234` §4.2）。
- **助手溯源**：`rgx_chat` 新增 `scenario_template_id` / `scenario_template_version`（定向写入，
  通用 chat 编辑不清空）；`POST /scenario-template-assets/:id/instantiate-chat` 创建助手并保留模板版本。
- **试跑（T7.3′）**：新增 `rgx_trial_run` / `rgx_trial_case_result` 与 `GET/POST /trial-runs`、
  `GET /trial-runs/:id`、`POST /trial-runs/:id/cancel`——retrieval-only、配置快照 + policy hash 可复现、
  `(tenant_id,idempotency_key)` 幂等复用、`gate_eligible=false` 永不计入发布门禁；无期望引用时命中率为
  `null`（不渲染 0%）；启动时把遗留 `QUEUED/RUNNING` 标记为中断失败。
- **试跑只读视图**：新增 `trial-runs` 前端资源（列表 + 详情：配置快照、逐用例结果、P95、未定义用例计数）。
- **上手就绪（T7.1′）**：`GET /onboarding/readiness` 输出四个全派生里程碑（知识库就绪 / 助手就绪 /
  完成试跑 / 首次带引用回答），0 新表；对话中心空态改为四步深链 + 起始问题；
  **T7.2 权限感知 CTA**——按 `chat:manage`/`scenario-template:read` 过滤里程碑，无权限步骤隐藏而非渲染
  必然 403 的死链，business_user 得到「查看我能用的助手」+ 三态申请入口。
- **i18n 收口（T7.7′）**：新增 `scenarioTemplates` 命名空间（zh/en 全同步），守护测试钉死
  zh/en 键集合一致、英文命名空间 CJK 扫描为 0、`%{placeholder}` 两端一致。
- **发版门出口补口（2026-10-27）**：①**迁移 157 PG 重入实测**——在内网共享开发库真跑
  `TestMigrationV157TrialRunPostgreSQLReentry`（两次 `Migrate` + 两表 + 两列 + 两个唯一索引）通过，
  此前该用例在无 DSN 时长期 `SKIP`；②**试跑快照 diff=0 守护**——反射式字段级 diff（新增字段自动纳入，
  不靠白名单）+ `policy_hash` 由快照纯函数重算（改阈值即失配），独立重建路径比对真跑快照断言 0 差异，
  11 个表驱动用例钉死「一个字段一处差异、数据集换序不算漂移」。

#### 质量、契约与回归机制

- **D1 冻结回归机制（WS4 C4.1）**：新增 `internal/testquality`——D1 manifest（`sample_size=51`、
  `passes=2`、`top_k=8`、`primary_metric=citation_precision`）与可复核判定器；bundle 必须引用 manifest
  文件 SHA-256 并复述语料/判分器/构建指纹，聚合指标一律从 per-case 行重算，未定义比率返回 `nil` 而非 0%，
  15 个违规码覆盖未冻结/篡改/调参/两遍不一致/未知字段。出厂 manifest 保持 `status=draft`，
  真实 D1 运行回填语料 hash 与判分模型前，任何 bundle 都不可能 `gate_eligible`；质量阈值只进 findings，
  接发布门属 C4.5（`doc/234` §2）。
- **端到端质量对比（D1，`doc/222` §12.7 / `doc/41` §20）**：KnowEval 端到端基准（build bd-272595、n=51、
  判分模型 DeepSeek-V4-Flash）三臂实测——ragflow-x accuracy 62.7%（双遍一致）、
  **引用可核对率 90.6–96.9%**（dify / RAGFlow 原生均为 0%）、**过度拒答 17.6%**（dify 33.3–39.2%、
  原生 29.4–33.3%）；对照 dify 56.9/58.8%、RAGFlow 原生 56.9/52.9%，基线不劣且引用/诚实度维度显著占优。
- **质量门负向防火墙（WS4 C4.5 部分）**：把「试跑与 D1 冻结 bundle 不得进入 Release Gate」（231 D7/D11）
  从口头约定变成测试——门禁源码出现 `trial_run`/`testquality`/`gate_eligible` 即红，并带防空转对照用例
  （`doc/237`）。
- **网关 1.0 契约（WS3 C3.1/C3.2）**：新增 `internal/gateway/contract`（契约版本 `gateway.v1`）——
  `/v1/*` 每条 API-key 路由标注 `stable`/`experimental`/`deprecated` 并锚定上游端点，23 条网关错误码登记
  状态、分类、可重试性、审计动作与运维提示；唯一「未分类」是上游 `>=300` 原样透传条款。网关路由注册收敛为
  `gatewayRouteSpecs` 单一入口，契约测试双向守护（路由 ↔ 契约、契约 ↔ provider 端点注册表、handler
  错误字面量全覆盖）。契约与矩阵见 `doc/233`；网关行为未变（流式语义修复见 Fixed）。
- **AGENTS / 治理文档同步**：`AGENTS.local.md` 红线与 `doc/00` 环境知识库同步最新结论
  （含引擎 v1.0.0 升级面与 v1.0.0 建库/绑定约束）。
- **权限矩阵文档同步与 RBAC 启动 reconcile 契约（2026-10-22）【已实现】**：`BuiltinPermissionMatrix()`
  与 `doc/user/zh-CN|en/permission-matrix.md` 逐格对齐——补齐 11 个治理/记忆/编译资源行，修正
  `release-governance`（平台/工作区/运营/团队补 `execute`）与 `evidence-snapshot`（团队补 `read`）；
  新增守护测试 `internal/db.TestPermissionMatrixDocSync`（解析用户文档 §3 逐格比对矩阵，新增资源漏同步
  即红灯）；修复「已最新 schema 启动不 reconcile RBAC」缺口——`migrateTo` 启动路径尾部无条件
  `seedDefaultRBAC` 幂等整体替换，兑现既有文档「启动时按矩阵整体 reconcile」声明，新增
  `TestLatestSchemaStartupReconcilesBuiltinRBAC` 守卫（删除 canonical grant + 植入 stale 后二次启动自动恢复）。
- **41 脚本走查完成度落档（2026-10-22）**：`doc/41` 新增 §20 汇总——S01–S24 全场景按
  `PASS / FAIL / BLOCKED / NOT_IMPLEMENTED` 纪律落档；可自动面全部收口（第四轮真实环境 + S21–S24 复核 +
  Memory live + 自动化 e2e/定向 go test）；剩余 3 项均为外部/人工面（S15 连接器同步 NOT_IMPLEMENTED、
  S24 §19 场景 A–D 实机演示 BLOCKED-外部、真机视觉/录屏人工未执行）。§20.1 登记数据管道闭环缺口：
  主干（建库/上传/解析/chunk/检索/图谱/编译）已在前端闭环，**剩余主要缺口 = 数据源连接器注册/授权/
  定时同步调度**。

### Security

- **自定义角色提权修复（`doc/247` R-24）**：`PUT /roles/:id/permissions` 过去只校验「动作在权限目录内」，
  而目录是内置矩阵的并集——持有 `role:manage` 的 `tenant_admin` 可把 `governance.read/tenant`
  （只有平台管理员持有的动作）写进自己的租户级角色并赋给自己，随后 `GET /api/v1/tenants` 会返回**全平台
  租户分页**。现新增「不能授予自己没有的权限」守卫（`assertCanDelegate`，`403/40302`），deny 规格不受限
  （只会收窄）；`platform_admin` 的授权集合恰等于整个目录，委派链不受影响。
- **权限模型语义收口（`doc/247` R-25/R-25b）**：①「写动作蕴含 read」此前只在内置矩阵上守护，`evaluate()`
  是精确匹配，自定义角色可被存成「有 manage 无 read」并因此 403——现由 `db.ReadImpliedByAction` 在求值侧
  单点强制（`delete:own` 一并入表；deny 仍保持字面语义）；②`route_registry.go` 补齐 `tenant` 的 13 个动作并
  新增反向断言 `TestRegistryDeclaresEveryGrantedAction`。内置角色有效权限零变化（矩阵 424 行 / 59 资源 /
  倒挂 0）。
- **平台细粒度授权接线（`doc/248` R-26；角色裁决见 `doc/249`）**：`tenant.audit.read` / `tenant.usage.read` /
  `tenant.resource.manage` / `tenant.resource.delete` 过去只写在矩阵里、**全代码库没有任何检查点**
  （授予了但什么都不发生）。现按 expand/contract 纪律只接 `expand` 半步，做成 `governance.*` 之外的
  **并行闸门**：新增 `ResolveTenantScopeFor(capability)` 让委派的审计/用量角色可跨工作区读取；
  `UpdateTenant`/`BatchUpdateTenantStatus` 接受 `tenant.resource.manage`，`DeleteTenant` 只接受
  `tenant.resource.delete`（「可改」不再蕴含「可删」）。**内置角色授权集合、矩阵行数（424/59 资源/倒挂 0）
  与审计归因零变化，0 迁移**。守护：`governance_gate_test.go` 4 项、`audit_governance_gate_test.go` 2 项、
  `router/delegation_e2e_test.go` 2 项（真实 HTTP 栈：委派审计员全链路 200 + 归因
  `tenant.audit.read:tenant`；`manage` 与 `delete` 在路由+服务两层的确切分工）。实施后代码审查另修 3 处缺陷
  （`doc/248` §8）：**R-26a（P1）** RBAC 中间件仍用宽权限解析治理 scope，使细粒度委派在真实请求中不可达；
  **R-26b** `POST /tenants` 缺细粒度检查，只持 `tenant.resource.delete` 的运营岗仍能建工作区；
  **R-26c** 审计/用量 capability 未覆盖 `audit-anchor`/`audit-export`/`usage-export`。
- **`viewer` 读权收敛（`doc/245`）**：内置 `viewer` 此前可读 33 个资源，其中 **19 个 `business_user` 读不到**
  （`task`/`usage`/`dashboard`/`knowledge-ops`/`approval`/`compilation`/`knowledge-lifecycle`/
  `document-governance`/`eval-set`/`evidence-snapshot`/`release-governance` + 6 个策略阈值配置 +
  `team`/`project` 组织面）——这是 `doc/242` 判定为缺陷的读权倒挂的**镜像**。按同一判据修复
  （「工作区产出了什么」= 业务读面，「工作区怎么运转/怎么被调优」= 岗位读面）：收敛后 `viewer` 只读 14 个
  业务资源，且永久不得持有 `manage`/`execute`/`append`/`session:create`；矩阵 **443 → 424** 行、资源 59 不变、
  重复 0、**读权倒挂 19 → 0**；`role_lattice.go` 新增超集边 `business_user ⊇ viewer`；新增 **migration 161**
  （`rbac_viewer_read_convergence`，纯数据、幂等、无 DDL）。守护升级：`TestViewerOnlyReadsAreGone` 升级为
  「不允许存在」、`TestViewerIsTheReadOnlySliceOfBusinessUser`（精确 14 读 + 只读 + 被 `business_user` 覆盖）、
  e2e 的「viewer 读 `/tasks/jobs` 应 200」反转为 **403**、前端只读导航不再包含已收回入口。
- **内置角色格声明与 `team_admin` 成员面修复（`doc/243`）**：对 6 个内置角色两两求差集后修两处同类缺陷——
  `team_admin` **比只读访客还弱**（`chat`/`search-app`/`agent`/`assistant`/`memory` 一条 grant 都没有，
  角色内部还自相矛盾）；`operator` 缺 `memory-forget:execute`（**刻意保留**：运营员不得代替用户遗忘记忆，
  `doc/137` §10.2/§10.3）。修复方式不是逐行补权，而是把「应该的层级」写成可执行声明：新增
  `internal/db/role_lattice.go`（**8 条超集关系** + **2 组显式不可比**，各带可反驳理由）+ **8 个守护测试**
  （全格声明、超集逐资源逐动作成立、不可比声明不得陈旧、`manage`/`execute`/`append`/`test`/`session:create`
  必须伴随 `read`、`team_admin` 可用性钉死、`viewer` 观察者边界防回退）；矩阵 422 → 433 grant；
  新增 **migration 159**（纯数据、幂等、无 DDL）。两处刻意不补均被既有钉死测试否决后回滚（后果明确接受：
  `team_admin` 不能发起试跑，改由独立 `trial-run` 资源表达）。
- **试跑权限单开：`trial-run` 资源（`doc/244`）**：把试跑从 `eval-set:execute` 拆成**独立资源**
  `trial-run`（`read`/`execute`），从此「练一次」与「改证据」解耦。矩阵 433 → 443 grant、资源 58 → 59，
  新增 **migration 160**（纯数据、幂等、无 DDL）；4 条路由改挂（`GET /trial-runs*` → `trial-run:read`，
  `POST /trial-runs` 与 `/:id/cancel` → `trial-run:execute`）。三条硬约束由测试钉死：`business_user`/`viewer`
  读取不被收回、`operator` 执行权不被收回、`team_admin` 仍不获得 `eval-set:execute`。刻意**不设**
  `trial-run:manage`（试跑没有独立生命周期可管理）。
- **审计与凭证**：审计哈希链 + `GET /audit/verify`/`POST /audit/repair` 保持全绿；`config.runtime.secrets.json`
  与 RSA setup 密钥走只读挂载（`RGX_SETUP_SECRETS_FILE`/`RGX_SETUP_RSA_KEY`），登录会话 `no-store`。

### Changed

- **产品定位（`doc/221` §2）**：不 Fork RAGFlow（引擎/后端 API-first），前端业务操作全量收敛在 RAGFlow-X UI；
  RAGFlow 原生界面仅保留给引擎管理员做底层运维。
- **A/B 证据台账收紧**：`relcheck.CollectGateStats` 只纳入声明 `gate_status` 的文件（此前仅声明
  `contract_version` 的构建日志/质量 bundle 也会进入发布证据台账）。
- **评测统计诚实增强（223 T4/T3）**：scoped evaluation v4 协议新增 test-retest 双遍一致性门禁
  （`min_repeat_consistency_pp`，双遍 <95% → EvaluationGateWait）与拒答校准三档策略 + 预算门禁
  （`abstention_policy` / `min_abstain_precision_pp` / `max_confident_wrong_pp`）；阈值/档位均预注册，
  回放调参须换协议 hash 防 p-hacking（`doc/223` §8）。
- **判分与嵌入模型规范**：`glm-5.3-flash` 停用登记（`doc/00` 226–227）；`deepseek-v4-flash-0731` 作为可用判分模型。
- **生成题集范围**：`GenerateMissingTemplateEvalSets` 跳过内置模板，避免每个工作区被批量灌入 9 个题集；
  内置题集按需在试跑/向导时发布；内置模板不要求 per-tenant release candidate（平台统一评审）。
- **live 契约 drill 姿势**：drill 客户端超时 15s → 5min（AMD radeon-external 单轮 60–90s）；chat 类模型固定
  `radeon-external` 池（绝不 fallback 到 ~15 tok/s 的慢池），嵌入/重排仍走引擎自有 GPU；单轮串行零并发。
- **前端依赖与工具链（2026-11）**：react/react-dom 19.3、@radix-ui 全系、ra-core/ra-i18n 5.15.5、
  @tanstack/react-query 5.104 等；query-string 10.0.0 说明性变更经 **patch-package** 就地修补
  （`web/patches/`，postinstall 自动应用）。CI 工具链：actions `upload/download-artifact` v4→v8/v7、
  docker set qemu / build-push 升级；dependabot `open-pull-requests-limit: 0`（常规依赖升级不再自动开 PR，
  高危安全修复仍走 Security Updates 通道）。

### Performance

- **受控性能基线（WS3 C3.4，单并发、非压测）**：7 场景 P50/P95/P99 与错误率已归档到工程内部性能记录
  （2026-10-26 C3.4 受控组）；网关非流式/流式自有开销 P50 约 10–15 ms（受控 in-process 上游），不代表
  生产推理延迟。真实部署的 RAGFlow 检索单次 60 s 级，已单列为环境限制（`doc/234` §3）。

### Fixed

- **流式失败语义与双拼专项（WS3 C3.3）**：修复「上游在已提交 `200` 的 SSE 中途报错、但投放台账记为成功」
  的缺陷——chat 与 search 两条流式路径此前从不消费采集器的错误标记，导致半包回答被计为 ANSWERED 并计费；
  现统一为失败投放（`SYSTEM_FAILED` + 保留上游原因）、释放配额预占、不计费、审计 `gateway.stream_error`。
  同一流**只保留一条终态错误信号**；agent 流不再在 in-band 失败时返回 nil。契约登记 `502/50231` 并新增
  `contract.StreamFailure()` 条款；Mock 故障注入可确定性重放（`provider/ragflow.StreamFault`）（`doc/235`）。
- **SSE answer 双拼（RAGFlow search completion 兼容层重复拼接）**：`parseSearchAppSSE` 对同帧
  content/answer 去叠印（回归测试 `TestSearchAppSSE_DeduplicateSameFrameAnswer`）。
- **迁移 155 的 PostgreSQL 兼容缺陷**：`CASE WHEN status = ? THEN ?` 绑定 bool 触发 `SQLSTATE 42804`，
  会阻断 PG 上的基线采集——已修。
- **业务角色读权断链（`doc/242`）**：补 `business_user` 的
  `scenario-template:read`（对话中心空态唯一的非 403 出口），其余 19 条改为**书面拒绝**并配
  `internal/db/permission_intent.go`（58 个资源的业务角色读意图 + 6 个守护测试）；删除 2 条重复 `system`
  平台行；`GET /governance/overview` 资源归属从 `scenario-template` 收紧为 `knowledge-ops`；
  新增 **migration 158**（纯数据、幂等、无 DDL）。
- **v1.0.0 画布列表导致的 `103 permission to access the agent`**：见「上游引擎兼容 D4」。
- **replacement drill 初次解析必失败**：见「ingest 与编译解耦」。

- **v0.5.0 发布流水线的 docker 环节失败（发版后补齐）**：`docker/Dockerfile` 的 web 阶段在 `npm ci`
  之前没有复制 `web/patches`，而 `npm ci` 会触发 `postinstall: patch-package`——补丁目录缺失时该钩子
  「无补丁可应用」仍以 0 退出，于是 `query-string@10.0.0`（v9 起改为仅默认导出）保持未打补丁，
  三步之后才以 `[MISSING_EXPORT] "parse"/"stringify" is not exported by node_modules/query-string/index.js`
  （由 `ra-core` 抛出）的形式炸在 `vite build`。现改为先 `COPY web/patches` 再 `npm ci`，并加
  `patches/*.patch` 存在性断言，缺补丁时立即失败而不是延后报编译错；同一轮把 `release.yml` 的镜像
  标签在非预发布版本上补 `:latest`（预发布永不上位），Release 步骤改为幂等（已存在则刷新说明 + `--clobber`
  覆盖资产），使 `workflow_dispatch` 重跑可以补齐发布页与镜像而不报 `release already exists`。
- **P0 质量门禁 `TestP0_QUALITY_000_ScenarioCatalogueIntegrity` 在 CI 红（发版后补齐）**：场景目录有 27 条
  `contractSource` 指向工程内部文档（`doc/develop/*`），而 `doc/` 除 `doc/user` 外不随仓库发布，本地有、
  CI 检出后 stat 失败。现改为随仓库发布的锚点（`doc/user/**` 用户文档，或实现该不变量的代码路径），
  并新增 fail-closed 守卫 `contractSourceShipped()`：`contractSource` 指向 `doc/` 下非 `doc/user/**` 时直接
  拒绝，配套单测钉死该边界，避免「本地绿、CI 红」的同类漂移再次发生。
- **PDF 导出在 Linux 上必然失败（业务导出能力实际不可用）**：`renderPDF` 把字体路径交给
  `fpdf.AddUTF8Font`，而该函数内部执行 `path.Join(fontpath, fileStr)`，默认 `fontpath` 为 `"."`——绝对路径
  的前导分隔符被吃掉（`/usr/share/...` 变成 `usr/share/...`），于是任何 Linux 部署只要配置了
  `RGX_EXPORT_PDF_FONT=/usr/share/fonts/...` 就报 `load pdf font: stat usr/share/...: no such file or directory`。
  Windows 上因为 `os.Stat` 容忍混合分隔符而「看起来正常」，这正是缺陷长期未被发现的原因。现改为先
  `os.ReadFile` 再 `AddUTF8FontFromBytes`，绝对/相对/Windows 路径统一可用，错误信息带出真正尝试的路径。
  fpdf 还有两层字体限制，本轮一并收口：其一，Linux 上最常见的 CJK 字体是**集合**（`.ttc`），fpdf 的 UTF8
  解析器读不了集合——只打印 `get metrics Error` 并把字体丢掉，最终表现为 `undefined font: cjk`；新增
  `extractFirstTTCFace()` 把集合首个 face 重排为独立 sfnt（重写表目录偏移、4 字节对齐、表数据原样搬运）。
  其二，fpdf 只能嵌入 TrueType（`glyf`）轮廓，OpenType/CFF 字体（`NotoSansCJK-Regular.ttc` 即 CFF）一律解析为空；
  现在候选字体探测只读表目录即按 `glyf` 过滤（`usableFontFile`），显式配置的 CFF 字体会得到
  `font has no TrueType (glyf) outlines` 而不是渲染到一半的 `undefined font`；候选表补入 `wqy-zenhei` /
  `wqy-microhei` / `uming`，CI 同时装 `fonts-noto-cjk`（CFF，用来验证被拒绝）与 `fonts-wqy-zenhei`（真跑通）。
  注册字体后立即 `SetFont` 一次，字体不可用时当场报错而不是渲染到一半才失败；
  `scripts/s24_readiness_check.sh` 的候选表同步改为只认 TrueType 轮廓字体，只有 Noto CFF 时报 FAIL 并给出装
  `fonts-wqy-zenhei` 的处置，不再把不可嵌入的字体报成 PASS。
  测试侧把硬编码的 `C:\Windows\Fonts\Deng.ttf` 换成生产同源查找（`testPDFFont`），使 Linux CI
  （`fonts-noto-cjk`）真正覆盖绝对路径 + 集合字体这两条链路，而不是静默 skip。
- **依赖工程内部文档的测试在发布仓为显式 skip（发版后补齐）**：`doc/develop`、`doc/runbooks` 不随仓库发布
  （只有 `doc/user/**` 发布），但 `TestREL3ExternalReviewGate`、`TestCompilationRecoveryRunbookLocksTenantSafetyGates`
  与 Prometheus 告警 runbook 存在性检查会去 stat 这些文件，导致检出发布仓后必红。现新增 `internal/engdocs`
  （模块根探测 + 工程文档树探测 + 统一的 skip 原因文案）：`relcheck` 的根目录探测改为只认 `go.mod`，三个门禁
  在工程文档树缺失时输出**带原因的 SKIP**（告警规则仍校验 runbook 路径形状与其他全部规则），不再把「未发布」
  误报成「不达标」。
- **`go-build` 的 `internal/service` 包在 CI 上撞 20m 包级超时**：该包在 2 vCPU runner 上约 20min+
  （本地约 13min），旧超时导致整轮被 `panic: test timed out after 20m0s` 收尾、另外两个平台被取消。
  单元测试与 `make test-coverage` 的包级超时统一放宽到 30m，`go-build` job 上限提到 60m。
- **`ghcr.io` 镜像推送被拒（`denied: permission_denied: write_package`）**：镜像包 `ragflow-x` 在 Actions
  接入前就已由手工推送创建（现存 `v0.2.0`/`v0.2.1`/`latest`），它没有把写权限授予本仓库的 `GITHUB_TOKEN`。
  流水线改为优先使用可选的 `GHCR_TOKEN`（`write:packages` 的 classic PAT），并把原始拒绝翻译成两条可执行
  处置（包设置里 "Manage Actions access" 加本仓库 Write，或配置 `GHCR_TOKEN`）；镜像构建本身已验证通过。

### Migration Notes

- **迁移上新至 162**：`150`（治理字典 seed）、`151`（多维配额桶，bucket 维度
  tenant/model/assistant）、`152`（发版版本带 preflight）、`153`–`156`（数据源连接器基础表：
  `connector_binding`/`connector_run`/`connector_actor_enabled`/`connector_source_identity`；连接器本身
  仍在补齐，见 `doc/41` §20.1）、`157`（`trial_run`：两表 + `rgx_chat` 两列）、
  `158`（`rbac_business_user_read_intent`）、`159`（`rbac_team_admin_member_surfaces`）、
  `160`（`rbac_trial_run_resource`）、`161`（`rbac_viewer_read_convergence`）、
  `162`（`p2_citation_positions_knowledge_ops_similarity`：引用定位列 `ref_index`/`positions` + 每轮
  top 相似度）。158–161 为纯数据权限同步（幂等、无 DDL），与 157 分列以便独立评审与独立回滚
  （例外已获产品负责人批准，理由见 `doc/242` §2.3 / `doc/244`）；162 为加列型 DDL，两种方言均容忍。
- **157 回滚步骤**（`doc/232` §3）：备份 → drop 两表 → drop 两列；回滚后 catalog 仍可用，试跑入口随表缺失关闭。
- **v1.0.0 引擎升级须按版本升级 SOP 执行**（工程内部记录 `doc/262`；面向运维的检查表见
  `doc/user` 运维 Runbook §16.5）：探测版本 → preflight 门禁 → live 契约集
  （P1 + `--full`）→ 留档；已知 5 类漂移（D1–D5）已建台账；升级后若版本带外推，须重跑
  live drill 再抬钉，禁止凭版本号放行。
- **RBAC 矩阵在启动时按 `BuiltinPermissionMatrix()` 整体 reconcile（幂等）**：存量库升级后自动对齐
  424 行 / 59 资源，无需手工 SQL。

### Supported engine versions

| RAGFlow 引擎 | 状态 | 语义 |
|---|---|---|
| `0.27.x` | 已验证带（legacy） | `page_index` 等旧编译产物契约可用；`ListAgents` 无 `type` 字段时按 agent 处理 |
| `1.0.0` | **已验证（2026-11-10）** | 97 条端点 `MaxVerified` 抬钉 `v1.0.0`；`--full` 5 passed / 0 failed / 0 skipped；编译产物仅 `wiki` |
| `> 1.0.0` | `verification_required` | fail-closed：建发布版本与能力启用都要求先重跑 live 契约集并留档 |
| 不可解析 / 不可达 | `blocked` | 建发布版本 fail-closed；运行期降级到最近已验证带 |

编译 kind 面在 `1.0+` 由声明式映射收口：`wiki` = `supported`，`page_index`/`timeline`/`knowledge_graph`/`session_*`
= `unsupported`（触发即 `422` / `42261`，附 `alternative=wiki`），未列出的 kind = `unverified`。运行时自查：
`GET /api/v1/system/capabilities`（`capabilities[]` 带 `min_version`/`max_verified`，`version_issues` 带逐条台账）。

### Known upstream issues (RAGFlow v1.0.0)

下列问题在 v0.5.0 验证轮（2026-11-10）的 live 环境实测确认，**上游仍未修复**。本版本不做静默降级：
每条要么被平台行为显式吸收，要么在入口 fail-closed 拒绝；台账同时以机器可读形式由
`GET /api/v1/system/capabilities` 的 `version_issues` 暴露（`observed` / `regression` / `untested`）。

| # | 上游缺陷 | 状态 | 本产品处置 |
|---|---|---|---|
| U1 | 结构化编译产物无读取面：`GET /datasets/{id}/artifacts` 固定 `type_kwd=wiki_page`，`POST /retrieval?include_knowledge_compilation=true` 只回 `compile_kwd=wiki`；Compiler 仍在写 `page_index`/`timeline`/`knowledge_graph` 行 | observed | band→kind 声明式映射 + 触发入口 fail-closed（`422` / `42261`），不产出不可达产物；`structure` 产物独立建模（B′）未立项 |
| U2 | 上游 web bundle 的 `POST /auth/login` 发送 `RSA(base64(密码))`，后端解密 `RSA(明文)` → 浏览器登录恒 `109` | observed | 不代理该页面：业务登录走 RAGFlow-X，引擎侧用 API Key；确需用上游 UI 时对 dist 打补丁（部署侧，不入库） |
| U3 | 建库必须显式 `embedding_model`；缺失则回落字面量 `"0"`，解析报 `500 model configuration unavailable` | observed | `Service.defaultEmbeddingModel` 平台侧解析（`ListDefaultModels` → `ListModels`，5 分钟 TTL）并恒定下发 |
| U4 | `GET /agents` 把 `type=compilation_template_group` 行混进 `data.canvas` 且计入 `total`；对其调用版本面返回 `103 permission to access the agent` | observed | provider `ListAgents` 按 `type` 过滤（无 `type` 的旧行保留），`total` 扣减被过滤条数 |
| U5 | 文档改绑到另一画布时，未带该画布已发布版本报 `102 pipeline version does not exist / does not belong to canvas X`；版本行只能由 `POST /agents/{id}/publish` 产生 | observed | 先 publish 版本再带 `pipeline_version_id` 下发；旧引擎忽略该字段 |
| U6 | [`#21095`](https://github.com/infiniflow/ragflow/issues/21095)：检索 `_source` 投影丢 `source_doc_ids`（社区修复 PR #21106 无人评审） | observed | 答案落库时持久化 `ref_index`/`positions`，引用从完成态快照复核，不依赖被丢字段 |
| U7 | [`#21097`](https://github.com/infiniflow/ragflow/issues/21097)：`ResponseHeaderTimeout`（20 分钟）与流式超时（60 秒）硬编码；上游 `main` 已由 PR #21434 合入，**尚未进任何 release tag** | observed | 长任务由 RAGFlow-X 调度器带显式 deadline 与单轮恢复驱动，不依赖引擎响应头超时 |
| U8 | [`#21096`](https://github.com/infiniflow/ragflow/issues/21096)：agent sandbox `statusFromExitCode` 致 Windows 构建破损（PR #21099 无人评审） | observed | 不打补丁：sandbox 只在 Linux 运行，由本产品治理/审计，不重写执行器 |
| U9 | 图检索与文档描述形式不一致（K 类版本冲突打分） | untested | 只在 dry run 触达，不作为发版门禁 |
| U10 | Memory `team-me` 权限模型跨版本不一致 | untested | 同上；记忆面按平台侧 scope 复核 |
| U11 | agent `sandbox`/`skills` 面跨版本漂移 | untested | 同上；治理与审计在平台侧，不依赖执行器细节 |
| U12 | search app 单数 `/searches/{id}/completion` 回归 `50265` | regression（上游已修） | 固定用复数 `/completions`；契约测试锁死路径形态 |

### Known Limitations

- **知识编译产物面**：v1.0.0 只暴露 `wiki` 产物（`page_index` 被引擎重分类为结构变体），`1.0+` 上
  `tree/graph/mindmap/timeline` 能力行标记 `unverified`；`structure` 产物独立建模（B′）未立项。
- **compiled-first 检索模式默认关闭、Gate WAIT 失败关闭**；上游关联 issue（#21095–#21098 等）跟踪中。
- **表格类文档引用精确率偏弱**（xlsx citation_precision 12.5%、表格/图片模态 over_abstain 偏高）→
  表格引用投影专项持续跟进。
- **试跑固定 retrieval-only、`top_n=3`**，不含答案质量与过度拒答指标（`doc/228` 附录 B 口径）；
  T7.4/T7.5/T7.6 未开工。
- **连接器**（数据源注册/授权/定时同步调度）仍是数据管道闭环的主要缺口（`doc/41` §20.1）；
  图形化管道画布属可选增强不阻塞闭环。
- **知识图谱 / MCP 全功能写路径为登记先行**（运行时 MCP 显式 501，`doc/221` §5.4）。
- **C3/C4 前端操作收敛的「有意取舍」项**与 glossary/knowledge-graph 写路径差异：见 `doc/221` §5.2 表。
- **RAGFlow 原生 UI 中对话画布入口由统一对话中心承接**（单一入口原则），不复刻原生 Chat/Agent 独立入口。
- **RBAC `contract` 半步（R-27）留待 v0.6.0**：细粒度动作目前仍与 `governance.*` 并行，收窄为唯一闸门
  需 deprecation 通知 + 自定义角色预检。

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

[Unreleased]: https://github.com/DavidSche/ragflow-x/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/DavidSche/ragflow-x/compare/v0.2.0...v0.5.0
[0.2.0]: https://github.com/DavidSche/ragflow-x/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/DavidSche/ragflow-x/releases/tag/v0.1.0
