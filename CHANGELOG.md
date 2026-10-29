# Changelog

本项目所有显著变更记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### v0.5.0（进行中，未提交）

#### Security

- **自定义角色提权修复（`doc/247` R-24）**：`PUT /roles/:id/permissions` 过去只校验「动作在权限目录内」，而目录是内置矩阵的并集——因此持有 `role:manage` 的 `tenant_admin` 可以把 `governance.read/tenant`（只有平台管理员持有的动作）写进自己的租户级角色并赋给自己，随后 `GET /api/v1/tenants` 会返回**全平台租户分页**（`Service.ListTenants` 只认该权限、不看平台身份标志）。现新增「不能授予自己没有的权限」守卫（`assertCanDelegate`，403/40302），deny 规格不受限（只会收窄）；`platform_admin` 的授权集合恰等于整个目录，委派链不受影响。
- **权限模型语义收口（`doc/247` R-25/R-25b）**：①「写动作蕴含 read」过去只在内置矩阵上守护，`evaluate()` 是精确匹配，自定义角色可被存成「有 manage 无 read」并因此 403——现由 `db.ReadImpliedByAction` 在求值侧单点强制（`delete:own` 一并入表；deny 仍保持字面语义，`deny execute` 不会连坐 read）；②`route_registry.go` 自称「权威策略形状」却把 `tenant` 的动作声明少了 11 个（服务层实际使用 `governance.*`/`tenant.connection.*`），现补齐 13 个动作并新增反向断言 `TestRegistryDeclaresEveryGrantedAction`（矩阵与路由表的动作必须都被 registry 声明）。内置角色有效权限零变化（矩阵仍 424 行 / 59 资源 / 倒挂 0）。

#### Changed

- **`viewer` 读权收敛：只读访客 = 业务使用者的只读切片（`doc/245`）**：内置 `viewer` 此前可读 33 个资源，其中 **19 个 `business_user` 读不到**——`task`（全租户任务队列）、`usage`（成本用量）、`dashboard`（经营聚合）、`knowledge-ops`、`approval`（含申请人/理由/金额）、`compilation`、`knowledge-lifecycle`、`document-governance`、`eval-set`、`evidence-snapshot`、`release-governance`，以及 `prompt-policy`/`parser-policy`/`quality-profile`/`knowledge-strategy`/`knowledge-profile`/`source-routing-rule` 六个策略与阈值配置，另加 `team`/`project` 组织面。这是 `doc/242` 判定为缺陷的读权倒挂的**镜像**（只读的人看得到干活的人看不到的租户级运营面），按同一判据修复：**「工作区产出了什么」= 业务读面，「工作区怎么运转/怎么被调优」= 岗位读面**。收敛后 `viewer` 只读 14 个业务资源（`assistant`/`chat`/`search-app`/`agent`/`dataset`/`document`/`logical-document`/`memory`/`memory-policy`/`memory-operation`/`context-decision`/`scenario-template`/`trial-run`/`user`），且永久不得持有 `manage`/`execute`/`append`/`session:create`；矩阵 **443 → 424** 行、资源 59 不变、重复 0、**读权倒挂 19 → 0**；`role_lattice.go` 新增超集边 `business_user ⊇ viewer`（8 条超集 + 2 组不可比），删除 `viewer` 两组不可比声明；新增 **migration 161**（`rbac_viewer_read_convergence`，纯数据、幂等、无 DDL）。守护升级：`TestViewerOnlyReadsAreGone` 把「须声明理由」升级为「不允许存在」，另加 `TestViewerIsTheReadOnlySliceOfBusinessUser`（精确 14 读 + 只读 + 被 `business_user` 覆盖）、`TestViewerConvergenceRemovedReadsIsCurrent`、migration 161 的 SQLite/PG ×2 重入、`TestP0CRoutingPermissions…`/`TestKnowledgeStrategy…`/`TestEvidenceSnapshot…`/`TestEvalSetExecute…` 路由零授予断言、`e2e_test` 的「viewer 读 `/tasks/jobs` 应 200」反转为 **403**、前端 `authProvider.navigation.test.ts` 只读导航不再包含已收回入口。zh/en 权限矩阵文档同步（19 行 `R → -`，`### viewer` 段重写）。**未动**：其它任何角色、任何路由挂载、`business_user` 的 15 个读意图。

> v0.3.0 草案（2026-10-21，随 221 发版门定稿；证据指向 doc/221、doc/222、doc/41）。

#### Added

- **Knowledge Compilation（137）**：知识编译全生命周期（Profile → Readiness Gate → Run → dispatch → artifact 绑定 → compiled-first 检索模式 → Improvement Gate A/B → 预算与成本归因 → recovery/零重摄取）；编译产物作为独立 artifact 参与检索排序（compiled-first，默认关闭、失败关闭）。
- **Enterprise Memory（137）**：企业记忆策略（可写可查询范围）、Sealed Envelope 持久化、Context Pre-Model 读取授权、冲突/Tombstone、成员退出失效与对账补偿、隐私四权（同意/豁免/删除/审计）、Memory Policy 与豁免面板。
- **Release Governance & Observability（137/211/213）**：Release Gate 11 门自动校验、Governance Metrics 生产落位、Alertmanager 告警部署与送达演练、运营归因与通知 Hub。
- **统一对话中心体验（214）**：长会话滚动固定、输入框自动增长、命令面板（Palette）全键盘、顶部渐隐、双主题焦点环与可访问性增强；前端业务操作全量收敛于 RAGFlow-X UI（API-first + 前端全收敛，见 doc/221 §5）。
- **检索与知识管理（C2/C5/C6/C1）**：检索测试面板（阈值/top_k/rerank 直通）、MCP Tool 治理化登记先行（运行时显式 501）、知识图谱只读视图、文档 chunk 编辑/新增（含跨租户审批治理）。
- **端到端质量对比（D1，doc/222 §12.7 / doc/41 §20）**：KnowEval 端到端基准（build bd-272595，n=51，判分 :18082 DeepSeek-V4-Flash）三臂实测——ragflow-x accuracy 62.7%（双遍一致）、**引用可核对率 90.6–96.9%**（dify/RAGFlow 原生均为 0%）、**过度拒答 17.6%**（dify 33.3–39.2%、原生 29.4–33.3%）；对照 dify 56.9/58.8%、RAGFlow 原生 56.9/52.9%，基线不劣且引用/诚实度维度显著占优。
- **升级版本带 preflight（223 T6.1–T6.3，M-B）**：Capability Registry 每条端点登记 `MinVersion/MaxVerified`（T6.2 全量落位 v1.0.0-rc1，证据=151 rc1 生产面 + live contract drill + D1）；`VersionKnownIssue` 观测表 6+1 条外部版本差异（含 search_app `/completions` 复数回归 50265 已修复）；`VersionBandGapReport()` 暴露 T6.2 backlog＋版本比较器＋能力级 preflight `RunVersionPreflight`（fail-closed：探测失败/不可解析 → Block，新于已验证带 → VerificationRequired，低于带 → Degrade）；服务层 `capabilityUpgradeGate` 接入——引擎新于 rc1 升级等待 live contract re-drill（`BLOCKED_CONTRACT_DRILL_REQUIRED`），不可解析版本 fail-closed（`BLOCKED_VERSION_UNPARSEABLE`），低版本保既有 READY/trial 语义（doc/224 §2.4）。
  **v0.3.0 发版闭环接线（M-B 并入 v0.3.0 版本线后提前的 T6.3 遗留，2026-10-22）【已实现】**：`CreateRelease` production 前置版本带 preflight——引擎新于已验证带（VerificationRequired）/不可解析（Block）→ HTTP 403/40076 fail-closed 拒绝发版；低版本 Degrade / 带内 Pass 放行并记录；引擎不可达容忍放行不卡死离线；preflight 观测版本/决策/原因落 `rgx_release` 行（migration 152 `release_version_preflight`，审计证据）；relcheck REL-003 发布评审自动门并入版本带 gap 扫描（`relcheck/version_band.go` `CollectVersionBandGap`，gap 非空自动门红灯）；新增 `release_version_preflight_test.go` 4 项 + relcheck version_band gate 断言全绿（doc/223 §3 T6.3，doc/224 §2.4）。
- **文档级治理与自动投影（223 T1/T2，M-B）**：治理字典 + Schema 绑定 + 自动投影（权威度连续数值走 schema 门槛、auto 缺省 internal/published、Authority=50）、空集 fail-safe + `projection_degraded` 可观测、Lightweight 文档审批（轻量内联 + 文件级人工双通道，reject 占位保留审计痕迹）、文档级 ACL / governance read 路由（`SupportsGovernanceRead`）、migration 150 字典 seed 幂等；错误码 40131/40132/40133/40141/40142、审计 + 观测 metric 对齐（doc/223 §3，doc/224 §2）。
- **引用精确定位与表格投影（223 T3，M-B）**：eval v4 协议 citations 版本 + references 附件；表格投影专项（xlsx 族含扩展名识别、cell 值断言、表格对偶双边声明）；归因防火墙（off=allowed 不计数 / strict=block）；gate 条件输出 + WAIT 两条文案；locator 样本框口径（分母=表格候选、gate 仅双边表格对偶聚合）；537 场景总部回归 452/456s 全绿（doc/223 §3，doc/224 §2.3）。
- **多维配额与失败分类（223 T5，M-B）**：Ledger 多维键（bucket 维度 tenant/model/assistant，migration 151 `rgx_quota_bucket`+reservation，行级防超卖 guarded UPDATE + 防负 CASE + stale reap）；组合门 `Preauthorize/Finalize/ReleaseGatewayQuotaWithBucket`（无桶退化为原 key 语义字节级不变；桶拒时 key 预留回滚）；**model 维度已接线**（组合门按请求 body 解析 model 名，存在 model 桶则按 model 桶预扣/结算、否则回落 tenant 桶）；key 维度 429/42930 保持向后兼容、bucket 维度 430/42931（HTTP 430 Would-Block）；标准头三件套 `X-Request-Id`/`X-Trace-Id`/`X-Quota-Remaining`（最严维余额）；失败分类表 `ClassifyGatewayFailure` 五类枚举（upstream/timeout/disconnect/quota/approval，disconnect 短路不可重试、429/430 quota retryable、5xx upstream retryable、40996/业务 4xx terminal）（doc/223 §3 T5，doc/224 §2.5）。
- **通知事件流统一 + 五组导航（223 T8，M-B）**：notify Event 统一 header（tenant/object_type/action/approval_id/trace_id/deadline，对齐审计列；零值省略保 legacy 字节兼容），审批 pending/decision/canceled/overdue/due_soon 全事件填充；前端五角色默认折叠导航（平台管理员/业务管理员/数据工程师/业务用户/审计员/只读，`NAVIGATION_VIEW_DEFAULT_EXPANDED`，身份 role 解析后才播种的 guard-once 修复，不覆盖用户手动折叠）；五角色走查 e2e 10 用例（desktop/mobile 双端钉死 `aria-expanded` 契约 + 展开组可见/折叠组为 0 + 手动折叠展开可用）（doc/223 §3 T8，doc/224 §3 ★ 决议）。
- **高对比度 emulation 走查（219，SC-A11Y-001）【已实现】**：`web/e2e/high-contrast.spec.ts` forced-colors+dark emulation 下登录/侧栏/工作台 desktop+mobile 渲染可用、无 page error（2 passed）；真机视觉与固定序列录屏仍为人工项（219）。
- **权限矩阵文档同步与 RBAC 启动 reconcile 契约（2026-10-22）【已实现】**：`BuiltinPermissionMatrix()`（10-22 快照 419 条 grant；2026-10-25 起新增 `connector` 资源 4 条 grant 后为 423，与代码实测一致）与 `doc/user/zh-CN/permission-matrix.md` / `doc/user/en/permission-matrix.md` 逐格对齐——文档补齐 11 个治理/记忆/编译资源行（`compilation`/`context-decision`/`document-governance`/`knowledge-profile`/`memory-content`/`memory-forget`/`memory-operation`/`memory-policy`/`memory-write`/`query-template`/`user-clearance`），修正 `release-governance`（平台/工作区/运营/团队补 `execute`）与 `evidence-snapshot`（团队补 `read`）；新增守护测试 `internal/db.TestPermissionMatrixDocSync`（解析用户文档 §3 逐格比对矩阵，新增资源漏同步即红灯）；修复「已最新 schema 启动不 reconcile RBAC」缺口——`migrateTo` 启动路径（Migrate）尾部无条件 `seedDefaultRBAC` 幂等整体替换，兑现既有文档「启动时按矩阵整体 reconcile」声明；新增 `TestLatestSchemaStartupReconcilesBuiltinRBAC` 守卫（删除 canonical grant + 植入 stale 后二次启动自动恢复/清除，全部 db 回归 82.2s 绿 + build/vet clean）。
- **41 脚本走查完成度落档（2026-10-22）**：41 脚本新增 §20 汇总——S01–S24 全场景按 `PASS / FAIL / BLOCKED / NOT_IMPLEMENTED` 纪律落档；可自动面全部收口（第四轮真实环境 + S21–S24 复核 + Memory live + 自动化 e2e/定向 go test）；剩余 3 项均为外部/人工面（S15 连接器同步 NOT_IMPLEMENTED、S24 §19 场景 A–D 实机演示 BLOCKED-外部、真机视觉/录屏人工未执行）。§20.1 登记数据管道闭环缺口：主干（建库/上传/解析/chunk/检索/图谱/编译）已在 RAGFlow-X 前端闭环（不回原生 UI，符合 221 §2「Single Pane of Glass」），**剩余主要缺口 = 数据源连接器注册/授权/定时同步调度**（前端无连接器 UI、route_registry 无 connector 资源；`/datasets/:id/incremental/scan` 幂等落地侧已存在）——建议 v0.3.0 feature-freeze 后立项补齐，图形化管道画布属可选增强不阻塞闭环。

#### Changed

- 产品定位：不 Fork RAGFlow（引擎/后端 API-first），前端业务操作全量收敛在 RAGFlow-X UI；见 doc/221 §2。
- 评测统计诚实增强（223 T4/T3，M-A）：scoped evaluation v4 协议新增 test-retest 双遍一致性门禁（`min_repeat_consistency_pp`，P2 双遍 <95% → EvaluationGateWait）与拒答校准三档策略 + 预算门禁（`abstention_policy` / `min_abstain_precision_pp` / `max_confident_wrong_pp`，判分侧注入 `expected_evidence_present`、报告段 `abstention_calibration`）；阈值/档位均预注册，回放调参须换协议 hash 防 p-hacking（doc/223 §8）。
- 判分与嵌入模型规范：`glm-5.3-flash` 停用登记（doc/00 226–227）；`deepseek-v4-flash-0731` 可用的判分模型。

#### Known Limitations

- compiled-first 检索模式默认关闭、Gate WAIT 失败关闭；上游关联 issue（#21095–#21098 等）跟踪中，官方镜像升级后复验（doc/221 §7 B 项标准）。
- SSE answer 双拼（RAGFlow search completion 兼容层重复拼接，doc/222 §12.4 P3）已在本仓库修复：`parseSearchAppSSE` 对同帧 content/answer 去叠印（doc/223 T3.5，含回归测试 TestSearchAppSSE_DeduplicateSameFrameAnswer）；上游引擎侧复验待 v0.3.x 验证窗口（doc/221 §7）。
- 表格类文档引用精确率偏弱（xlsx citation_precision 12.5%、表格/图片模态 over_abstain 偏高）→ 表格引用投影专项登记 v0.3.x。
- C3/C4（前端操作收敛中「有意取舍」项）与 glossary/knowledge-graph 写路径差异：发布说明引用 doc/221 §5.2 表。
- 知识图谱 / MCP 全功能写路径为登记先行（运行时 MCP 显式 501，见 doc/221 §5.4）。
- RAGFlow 原生 UI 中对话画布入口由统一对话中心承接（单一入口原则），不复刻原生 Chat/Agent 独立入口。

## [Unreleased] v0.4.0 草案（WS7-min）

> 状态：**草案，未提交、未发布**。范围依据 doc/231 D9–D12（WS7 仅最小可交付、试跑为独立对象、试跑不进 Release Gate、模板注册表化），实施与证据见 doc/232。WS7-min 全项 P1，不参与 v0.3.0 发版门。

#### Added

- **RBAC 读权单调修复（2026-10-27，`doc/242`）**：内置矩阵里 `viewer`（只读访客）在 **20 个资源**上读权严格包含 `business_user`（业务使用者）——角色是后补的（migration v88 只重种子），而矩阵是逐资源手写，新增治理资源时只给 viewer/operator/admin 补了 `read`。本次改动：①新增 `internal/db/permission_intent.go`，为**全部 58 个资源**声明业务角色读意图（`read` / `none`，`none` 必须带可被反驳的理由），并配 6 个守护测试——**新增资源不登记、或出现 viewer-only 读而不写理由，构建立即失败**；②补 `business_user` 的 `scenario-template:read`（唯一真正断链的一条：模板目录与上手就绪度是对话中心空态唯一的非 403 出口）；其余 19 条改为**书面拒绝**（`task` 的全租户任务投影、`usage`/`dashboard` 的租户级成本与指标，给出去就是看他人数据与财务口径）；③删除 2 条重复的 `system` 平台行（423 → **422** grant）；④新增 **migration 158**（纯数据同步、无 DDL、幂等，复用既有 delete-then-insert 协调器）——否则修复只对新库生效；⑤`GET /governance/overview` 的资源归属从 `scenario-template` 收紧为 `knowledge-ops`（聚合计数属治理读面）；⑥zh/en 权限矩阵文档同步，`TestPermissionMatrixDocSync` 逐格通过。**纠错**：`doc/240 §3.1`「缺 `approval:read` 导致业务用户无法自助申请」不成立——`SubmitApproval` 要求调用方已持有目标对象 `manage`，业务用户没有可提交对象；自助申请需独立 `access-request` 特性（v0.5.0）。
- **模板单一事实源（T7.0）**：9 个内置场景模板由后端 seed 为唯一内容源（`rgx_scenario_template`，`source=builtin`，payload schema v2 含 zh 原文 + en 全量翻译），建租户与启动回填幂等；新增只读 `GET /scenario-template-catalog`（GET 不写库）；前端静态模板表下线，向导与空态完全由 API 驱动。
- **助手溯源**：`rgx_chat` 新增 `scenario_template_id` / `scenario_template_version`（定向写入，通用 chat 编辑不清空）；`POST /scenario-template-assets/:id/instantiate-chat` 创建助手并保留模板版本。
- **试跑（T7.3′）**：新增 `rgx_trial_run` / `rgx_trial_case_result` 与 `GET/POST /trial-runs`、`GET /trial-runs/:id`、`POST /trial-runs/:id/cancel`——retrieval-only、配置快照 + policy hash 可复现、`(tenant_id,idempotency_key)` 幂等复用、`gate_eligible=false` 永不计入发布门禁；无期望引用时命中率为 `null`（不渲染 0%）；启动时把遗留 `QUEUED/RUNNING` 标记为中断失败。
- **上手就绪（T7.1′）**：`GET /onboarding/readiness` 输出四个全派生里程碑（知识库就绪 / 助手就绪 / 完成试跑 / 首次带引用回答），0 新表；对话中心空态改为四步深链 + 起始问题。
- **试跑只读视图**：新增 `trial-runs` 前端资源（列表 + 详情：配置快照、逐用例结果、P95、未定义用例计数）。
- **网关 1.0 契约（WS3 C3.1/C3.2）**：新增 `internal/gateway/contract`（契约版本 `gateway.v1`）——`/v1/*` 每条 API-key 路由标注 `stable`/`experimental`/`deprecated` 并锚定上游端点，23 条网关错误码登记状态、分类、可重试性、审计动作与运维提示；唯一「未分类」是上游 `>=300` 原样透传条款。网关路由注册收敛为 `gatewayRouteSpecs` 单一入口，契约测试双向守护（路由 ↔ 契约、契约 ↔ provider 端点注册表、handler 错误字面量全覆盖）。契约与矩阵见 doc/233；网关行为未变（流式失败语义修复见下条 C3.3）。
- **模板单一事实源修复（T7.0 复评）**：`GET /scenario-template-catalog` 新增服务端解析后的 `prompt` 与 `parameters`；`mergeTemplateAuthoring` 改为「模板默认 + 仅覆盖显式字段」（此前任意一处覆盖会整体丢弃模板 payload）；向导删除本地 `PROMPT_PRESETS`/`PARAMETER_PROFILES` 与 `PromptPresetPicker`，chat 只提交 `llm_id`，search/agent 配置由 catalog 生成；新增前后端守护测试。`GET` 响应为增量字段，旧客户端不受影响。详见 doc/232 §4.1。
- **D1 冻结回归机制（WS4 C4.1）**：新增 `internal/testquality`——D1 manifest（`sample_size=51`、`passes=2`、`top_k=8`、`primary_metric=citation_precision`）与可复核判定器；bundle 必须引用 manifest 文件 SHA-256 并复述语料/判分器/构建指纹，聚合指标一律从 per-case 行重算，未定义比率返回 `nil` 而非 0%，15 个违规码覆盖未冻结/篡改/调参/两遍不一致/未知字段。出厂 manifest 保持 `status=draft`：真实 D1 运行回填语料 hash 与判分模型前，任何 bundle 都不可能 `gate_eligible`。质量阈值只进 findings，接发布门是 C4.5。详见 doc/234 §2。
- **模板 catalog 作用域修复（T7.0 复评二）**：`GET /scenario-template-catalog` 删除 `scopeAll` 形参，强制按调用方工作区列出模板——修复“平台管理员在向导看到 29 个模板但 `instantiate-chat` 一律 404”的脱节；前端补 catalog 空态（zh/en），新增跨租户隔离测试。详见 doc/234 §4.2。
- **质量门负向防火墙（WS4 C4.5 部分）**：把「试跑与 D1 冻结 bundle 不得进入 Release Gate」（231 D7/D11）从口头约定变成测试——门禁源码（`release*.go`/`assistant_release*.go` + `governance/relcheck`）出现 `trial_run`/`testquality`/`gate_eligible` 即红，并带防空转对照用例；正向接线仍被 holdout ≥100 挡住，未做。详见 doc/237。
- **流式失败语义与双拼专项（WS3 C3.3）**：修复「上游在已提交 `200` 的 SSE 中途报错、但投放台账记为成功」的缺陷——chat 与 search 两条流式路径此前从不消费采集器的错误标记，导致半包回答被计为 ANSWERED 并计费；现统一为失败投放（`SYSTEM_FAILED` + 保留上游原因）、释放配额预占、不计费、审计 `gateway.stream_error`。同一流**只保留一条终态错误信号**（上游 in-band 错误帧原样透传，否则网关写 `gateway_error` 帧），agent 流不再在 in-band 失败时返回 nil（幂等记录不再被误标成功）。契约登记 `502/50231` 并新增 `contract.StreamFailure()` 条款（终态唯一/in-band/传输断流/失败不计费/无续传）；守护测试把错误码扫描扩展到三个流式服务函数。Mock 故障注入可确定性重放（`provider/ragflow.StreamFault`），真实上游录制回放待 C1.1。详见 doc/235。
- **内置角色格声明与 `team_admin` 成员面修复（2026-10-27，`doc/243`）**：`doc/242` 只修了`viewer ⊃ business_user` 一条边，没有回答其余角色对之间的关系。对 6 个内置角色两两求差集后发现同一类缺陷还有两处：①`team_admin`（团队管理员）**比只读访客还弱**——`chat`/`search-app`/`agent`/`assistant`/`memory` 一条 grant 都没有，导致团队管理员进不去对话中心、看不到本团队的助手与记忆；且角色内部自相矛盾：持有 `memory-content:manage`/`memory-policy:manage`/`memory-forget:execute` 却不能 `read memory`，持有 `scenario-template:manage`+`eval-set:manage` 却因缺 `eval-set:execute` 无法发起试跑。团队 owner 会被自动授予该角色，因此实际效果是「必须额外持有 `business_user` 才能用产品」。②`operator` 缺 `memory-forget:execute`——该缺口**是刻意的**（`doc/137 §10.2/§10.3`：运营员不得代替用户遗忘记忆）；第一版实现照扫描结果补了这一行，被既有守护测试 `TestP0_DBMIG_131_MemoryOperationOutboxIndexes` 当场打红后回滚，改为把该角色对声明为不可比。修复方式不是逐行补权，而是把「应该的层级」写成可执行声明：新增 `internal/db/role_lattice.go`——**7 条超集关系**（`doc/245` 起为 8 条）（含 `team_admin ⊇ business_user`、`operator ⊇ viewer`）+ **4 组显式不可比**（`business_user`/`operator`（上述隐私边界）、`operator`/`team_admin`、`viewer`/`business_user`、`viewer`/`team_admin`，各带可反驳理由；`doc/245` 起后两组由超集关系取代），配合**8 个守护测试**：全格声明（30 个有序角色对不得「未决定」）、超集逐资源逐动作成立、不可比声明不得陈旧（双向都得有独占）、`manage`/`execute`/`append`/`test`/`session:create` **必须伴随 `read`**（例外仅`memory-write`/`memory-forget` 动作型资源，须逐条给理由）、`team_admin` 可用性钉死、`viewer` 观察者边界防回退。矩阵 **422 → 433** grant（`team_admin` 44→55、`operator` 保持 62，58 资源不变、无重复三元组）；新增 **migration 159**（`rbac_team_admin_member_surfaces`，纯数据、幂等、无 DDL）。**范围裁决（两处刻意不补，均被既有钉死测试打红后回滚）**：①`operator` 补 `memory-forget:execute` 被 `TestP0_DBMIG_131` 否决（`doc/137 §10.2/§10.3` 隐私边界，运营员不得代替用户遗忘记忆）；②`team_admin` 补 `eval-set:execute` 被 `TestEvalSetExecutePermissionsMatchRevalidationContract` 否决（`doc/129 §1178`：该动作同时驱动 `POST /eval-cases/:id/revalidate`，revalidate 在 service 层只有租户级作用域、无 team ABAC）。后果明确接受：`team_admin` **不能发起试跑**，试跑仍是知识运营职责；若要让团队管理员自评本团队助手，应为试跑单开动作而非借用 `eval-set:execute`（v0.5.0 待裁决）。另：`team_admin` 仍**不**持有 `task`/`usage`/`dashboard`/`knowledge-ops`/`source-routing-rule`/`approval` 的 `read`——这些端点无 team 维度收敛（全租户任务投影、租户级成本聚合），给出去是用 RBAC 交出租户级可见性（与 `doc/242 §3` 给 `business_user` 的 19 条拒绝同一判据）。新增行本身不扩大可见面：这些资源的列表投影本就是租户级，任何 `business_user` 已能看到同样数据。zh/en 权限矩阵文档同步。
- **试跑权限单开：`trial-run` 资源（2026-10-28，`doc/244`）**：`doc/243` 拒绝把 `eval-set:execute` 借给团队管理员，代价是「团队管理员不能练本团队模板」。本次按产品负责人点名的「第四条路」把试跑拆成**独立资源** `trial-run`（`read`/`execute`），从此「练一次」与「改证据」解耦。根因是一个动作两种语义：`eval-set:execute` 同时驱动 `POST /trial-runs`（一次性诊断，产物永不晋升发版证据）与 `POST /eval-cases/:id/revalidate`（租户级证据重算、无 team ABAC，`internal/service/evidence_snapshot.go:236`）。矩阵 **433 → 443** grant（`trial-run` 10 行：platform/tenant/operator/team_admin 读+执行，`business_user`/`viewer` 只读）、资源数 **58 → 59**，新增 **migration 160**（纯数据、幂等、无 DDL）；4 条路由改挂（`GET /trial-runs*` → `trial-run:read`，`POST /trial-runs` 与 `/:id/cancel` → `trial-run:execute`，不再双检 `scenario-template:read`+`eval-set:execute`）。三条硬约束：①`business_user`/`viewer` 的读取**不被收回**（改挂路由时最容易发生的静默可见性回退）；②`operator` 的执行权**不被收回**；③`team_admin` **仍不获得** `eval-set:execute`（`TestTrialRunExecutePermissionsMatchPracticeRunContract` 双向钉死）。前端同步：侧边栏映射改 `trial-run:read`、试跑详情「取消」按钮按 `trial-run:execute` 门控（不给必然 403 的死按钮，与 R-18 同规则）、自定义角色编辑器补该资源与 zh/en 资源名。刻意**不设** `trial-run:manage`：试跑没有独立生命周期可管理（模板创作仍由 `scenario-template:manage` 把守），加 `manage` 等于给历史清理开第二条路，与 231 D10「只有保留策略能清理」冲突。
- **v0.4.0 发版门出口补口（2026-10-27）**：①**迁移 157 PG 重入实测**——在 `192.168.4.153/ragflow-x` 真跑 `TestMigrationV157TrialRunPostgreSQLReentry`（两次 `Migrate` + 两表 + 两列 + 两个唯一索引）通过，此前该用例在无 DSN 时长期 `SKIP`；②**试跑快照 diff=0 守护**——新增反射式字段级 diff（新增字段自动纳入，不靠白名单）+ `policy_hash` 由快照纯函数重算（改阈值即失配），独立重建路径比对真跑快照断言 0 差异，11 个表驱动用例钉死"一个字段一处差异、数据集换序不算漂移"；③**T7.2 权限感知 CTA**——对话中心空态按 `chat:manage`/`scenario-template:read` 过滤里程碑，无权限的步骤**隐藏而非渲染成 403 链接**，并且不再发必然被拒的 `/onboarding/readiness` 请求；business_user 得到「查看我能用的助手」+ 三态申请入口（有 `approval:execute` → 「申请助手」进审批中心；仅审批启用 → 「复制申请说明」写剪贴板交知识运营线下发起；审批未启用 → 提示联系管理员）。经复核 `POST /api/v1/approvals` 需要 `approval:execute` 而 business_user 无该 grant，**若照旧链到审批中心就是新的 403 死链**，故改为三态；权益倒挂与补权方案另立 `doc/240`；**后续已按 `doc/242` 完成根因修复**（补 `scenario-template:read` 一条 + 读意图登记表 + migration 158，矩阵净 +1 行），详见 doc/238 §3、doc/240、doc/242。
- **i18n 收口（T7.7′）**：新增 `scenarioTemplates` 命名空间（zh/en 全同步），并加守护测试——zh/en 键集合一致、英文命名空间 CJK 扫描为 0、`%{placeholder}` 两端一致。

#### Changed

- **A/B 证据台账收紧**：`relcheck.CollectGateStats` 只纳入声明 `gate_status` 的文件（此前仅声明 `contract_version` 的构建日志/质量 bundle 也会进入发布证据台账）。副作用：历史 `rel3-external-review-validation.json` 的 `gate_stats.evidence` 少一行误纳记录。
- 迁移版本上新至 **157**（`trial_run`：两表 + `rgx_chat` 两列，可重入、带 `HasTable`/`HasColumn` 守卫），**并追加 158**（`rbac_business_user_read_intent`：纯数据权限同步，幂等、无 DDL）；153–156 冻结。158 是对「v0.4.0 只新增 157」纪律的显式例外，理由：不推进版本号则读权修复对存量部署不生效（`doc/242 §2.3`）；**该例外已获产品负责人批准（2026-10-27）**。**再追加 159**（`rbac_team_admin_member_surfaces`：同为纯数据权限同步，让 `doc/243` 的角色格在存量库成立）；**再追加 160**（`rbac_trial_run_resource`：让 `doc/244` 的新资源在存量库可用）。158/159/160 与 157 分列而非合并，以便独立评审与独立回滚。
- `GenerateMissingTemplateEvalSets` 跳过内置模板，避免每个工作区被批量灌入 9 个题集；内置题集按需在试跑/向导时发布。
- 内置模板不要求 per-tenant release candidate（平台统一评审），自定义模板保持原 PASS 门。

#### Performance

- **受控性能基线（WS3 C3.4，单并发、非压测）**：7 场景 P50/P95/P99 与错误率落 `doc/performance/20261026-c34-controlled/`；网关非流式/流式自有开销 P50 约 10–15 ms（受控 in-process 上游），不代表生产推理延迟。真实 151 RAGFlow 检索单次 60 s 级，已单列为环境限制。方法、噪声与 SLO 输入见 doc/234 §3。
- 顺带修复迁移 155 的 PostgreSQL 兼容缺陷（`CASE WHEN status = ? THEN ?` 绑定 bool 触发 `SQLSTATE 42804`），该缺陷会阻断 PG 上的基线采集。

#### Migration Notes

- 157 前向迁移，回滚步骤见 doc/232 §3（备份 → drop 两表 → drop 两列）；回滚后 catalog 仍可用，试跑入口随表缺失关闭。

#### Known Limitations

- 试跑固定 retrieval-only、`top_n=3`，不含答案质量与过度拒答指标（按 doc/228 附录 B 口径）。
- ~~试跑授权复用 `scenario-template:read` + `eval-set:execute`，未新增 `trial-run:*` 权限行~~ → **已由 `doc/244` 变更**（`trial-run:read`/`trial-run:execute`，migration 160）。
- T7.4/T7.5/T7.6 未开工；连接器（doc/231 D1/D2）、网关 1.0 契约、质量门 D7 均不在本次范围。

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
