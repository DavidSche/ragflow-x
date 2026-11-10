# 运维 Runbook 与故障演练 SOP

> 日期：2026-11-10\
> 适用版本：RAGFlow-X v0.5.0（迁移 v161 起；v0.2.0 起的升级路径见第 16 节）
> 状态：SOP 已发布；真实环境演练记录仍为空，必须按第 13 节证据模板归档后才可宣布运维验收完成。
> 说明：文中 `doc/NNN` 为工程内部设计记录编号，不随本仓库发布；本文件自身随仓库发布。

## 1. 目标与边界

本 Runbook 面向平台管理员、值班运维和审计人员，覆盖日常健康检查、初始化、升级、回滚、RAGFlow 降级、资源同步失败、网关配额异常、通知失败和审计链修复。它不替代数据库厂商的 PITR 手册，也不允许绕过审批直接修改生产数据。

所有生产修复必须满足三个条件：

1. 先记录现场证据，再执行修复。
2. 修改设置、审计链或发布基线前获得对应审批。
3. 修复后复核健康、权限、审计和关键业务路径。

## 2. 环境与账号准备

| 准备项 | 要求 |
| --- | --- |
| 管理入口 | 前端 URL、`/api/v1` 地址、平台管理员账号、租户管理员账号、具备 `audit:read` 的审计视图账号 |
| 服务器访问 | 可查看 RAGFlow-X 容器/进程日志，可读取配置与数据库备份，不要求直接改库 |
| RAGFlow | 至少两个环境实例，例如 production 与 staging；记录 base URL、API Key、版本、健康入口 |
| SMTP | STARTTLS 可用（推荐 587），发件地址、收件地址、凭据注入方式 |
| Webhook | generic Webhook 可接收 JSON 并配置 HMAC secret；企微/钉钉机器人返回 2xx 且 `errcode=0`；故障演练时可切到不可用端点 |
| 证据目录 | 每次演练一个目录，保存命令输出、截图、trace_id、时间线、审批链接 |
| 密钥 | `RGX_JWT_SECRET`、`RGX_ENCRYPTION_KEY`、`RGX_MASTER_KEY`、SMTP/Webhook secret 不写入代码库 |
| OIDC IdP | 企业 IdP discovery、RS256/JWKS、client_id/client_secret、回调 URL；用户邮箱必须已预置到目标工作区，且默认要求 IdP 返回 `email_verified=true` |

## 3. 日常健康检查

每 30 分钟或巡检时执行：

1. 检查 RAGFlow-X 进程、容器和数据库连接。
2. 调用 `GET /api/v1/system/health`，确认数据库、RAGFlow 连接和关键服务状态。
3. 调用 `GET /api/v1/system/capabilities`，确认 RAGFlow 版本、API Profile 和降级能力。
4. 打开 **系统与用量 → 告警** 或 `GET /api/v1/alerts`，处理 OPEN 告警。
5. 打开 `GET /api/v1/tasks/jobs`，确认同步、文档与保留任务无长期 RUNNING。
6. 抽查最近 Gateway 审计：请求 2xx/4xx/5xx 分布、配额结果、trace_id 是否完整。
7. 检查 `GET /api/v1/alerts/deliveries`，确认通知终态不是长期 PENDING/RETRY。

通过标准：无未处理 CRITICAL 告警；能力探测无 UNKNOWN；任务无心跳过期；审计链 `GET /api/v1/audit/verify` 为 true。

## 4. 首次初始化 SOP

### 步骤

1. 备份并固定 `RGX_MASTER_KEY`；单机自动生成时立即把 `.master.key` 导入秘密管理器。
2. 启动初始化向导，创建平台管理员，完成租户、用户、角色和权限基线。
3. 在 **系统配置 Preflight** 或 `POST /api/v1/system/config/preflight` 检查数据库、RAGFlow、模型和通知配置。
4. 在 **系统配置 Apply** 或 `POST /api/v1/system/config/apply` 应用配置。
5. 创建企业连接并做健康探测；生产实例与灰度实例分别命名。
6. 配置 `alerting.email` 或 Webhook。SMTP 必须显式启用且通过 STARTTLS。
7. 触发一次审批、配额超限和资源同步事件，确认通知与审计产生。
8. 导出审计锚点 `GET /api/v1/audit/anchors`，保存初始化基线。

### 验收

- Preflight/Apply 均成功，失败项不能进入生产。
- 三个测试事件各产生一条审计记录。
- 平台管理员、工作区管理员、内容运营员、业务使用者、只读用户和审计权限推导的六类前端视图与其 RBAC/ABAC 一致。

### 企业 OIDC SSO 初始化

1. 在 IdP 创建机密客户端，回调固定为 `https://<ragflow-x-host>/api/v1/auth/oidc/callback`。
2. 在 IdP 目录中创建应用用户，并在 RAGFlow-X 目标工作区预置同邮箱用户；不启用自动创建。
3. 配置 `oidc.issuer`、`client_id`、`redirect_url` 和 `tenant_id`；`client_secret` 通过 `RGX_OIDC_CLIENT_SECRET` 注入。
4. 除非企业 IdP 能提供可信邮箱验证声明，否则不要将 `require_email_verified` 改为 `false`；放宽配置必须记录安全评审结论。
4. 用测试账号点击“企业账号登录”，确认 IdP 重定向、state/nonce、回调、`auth.oidc.login` 审计和 `GET /auth/me` 均正常。
5. 演练失败路径：错误 state、未预置邮箱、停用用户、跨租户邮箱、错误 client secret、JWKS 不可用；每类都应返回可解释错误且不建立会话。

### OIDC 故障处置

| 现象 | 处置 |
| --- | --- |
| 登录页没有 SSO 入口 | 检查 `GET /api/v1/auth/oidc/status` 与 `oidc.enabled` |
| Start 返回 502 | 检查 discovery 地址、私有网络 DNS/代理和 HTTPS 证书 |
| Callback 返回 401/400 | 核对 clock skew、client secret、redirect URL、JWKS、state/nonce 与 issuer/audience |
| 已验证身份仍拒绝 | 确认邮箱已预置且目标租户匹配；不要直接改库，先在身份源与用户目录对齐 |

## 5. 升级 SOP

### 升级前

1. 记录当前 RAGFlow-X 版本、迁移版本、RAGFlow 版本。
2. 备份 PostgreSQL/SQLite，并验证能恢复到独立库。
3. 导出审计锚点、关键 Release Candidate/Snapshot/Evidence/Gate/Release 摘要。
4. 调用 `POST /api/v1/system/capabilities/verify`，确认 0.27 返回 READY、0.28 返回 BLOCKED_CONTRACT_DRILL_REQUIRED；0.28 能力以 COMPATIBLE 展示并保持降级，不能默认启用。
5. 冻结运行中的资源同步和生产发布。

### 升级中

1. 先在 staging 完成镜像、迁移、Gateway smoke、Release Gate smoke。
2. 生产滚动升级时先切一个副本，观察错误率、P95、SSE 首包和配额释放。
3. 使用固定环境变量部署，禁止依赖实例本地随机密钥。

### 升级后

1. 确认迁移版本与预期一致。
2. 执行第 3 节健康检查。
3. 用真实 API Key 分别 smoke Chat、Search App、Agent 的授权、拒绝和配额路径。
4. 恢复资源同步，先 scan/import，不直接 reconcile。

## 6. 回滚 SOP

### 6.1 应用回滚

1. 停止流量或保留只读入口。
2. 回退镜像到上一个验证版本；如果新迁移已执行，必须使用已审批的数据库恢复方案，禁止手工改表。
3. 恢复配置，保留 `RGX_MASTER_KEY`、数据库 DSN 和通知秘密。
4. 执行健康检查和 Gateway smoke，再恢复全量流量。

### 6.2 系统设置回滚

1. `GET /api/v1/system/settings/revisions` 找到目标 revision。
2. 确认 `rollback_from_revision_id`、diff 和审批记录。
3. `POST /api/v1/system/settings/revisions/:id/rollback`，请求必须带审批/原因。
4. 刷新前端与网关配置，验证资源路由和权限。

### 6.3 助手发布回滚

1. `GET /api/v1/releases` 找到当前 `RELEASED` 记录。
2. `POST /api/v1/releases/:id/rollback`，记录原因和影响。
3. 确认当前 Release 变为 `ROLLED_BACK`，回滚基线指向同候选、同环境的上一个 `RELEASED`。
4. 重新执行 EvalSet 或关键业务 smoke；失败不得继续对外提供服务。

## 7. RAGFlow 降级与恢复 SOP

### 触发条件

出现任一情况：`system/health` 连接失败、能力探测降级、上游 5xx/timeout 连续升高、流式回答断流。

### 处置步骤

1. 记录 RAGFlow 实例、版本、开始时间、错误样本和 trace_id。
2. 确认 RAGFlow 自身健康，区分 RAGFlow、网关和网络故障。
3. 调用 `POST /api/v1/system/capabilities/verify`，只保留 Contract Test PASS 的能力。
4. 暂停资源同步和 Evaluation，避免把上游故障误写为业务质量失败。
5. 将生产流量切到健康 RAGFlow 实例或启用只读/降级模式。
6. 对未完成请求确认配额释放、幂等记录关闭、审计包含上游错误。
7. RAGFlow 恢复后先做 Contract Test，再扫描一个资源子集，最后恢复生产同步。

### 通过标准

- 降级期间无跨租户路由。
- 不把上游 5xx 计入用户答案质量。
- 恢复后 Run、Binding、Alert 状态收敛，无重复导入。

## 8. 资源同步失败 SOP

### 常见失败

| 现象 | 初判 |
| --- | --- |
| Run 长期 RUNNING | Worker/租约异常或上游慢 |
| 大量 CONFLICT | 外部对象被并改或同一资源重复绑定 |
| 越权/403 | 企业连接、租户映射或 API Key scope 错误 |
| Hash mismatch | 远端对象版本变化，快照过期 |

### 处置步骤

1. `GET /api/v1/system/ragflow/sync/runs` 找到失败 Run；`GET .../runs/:id/items` 查看 skipped/failed 原因。
2. 检查 `GET /api/v1/system/ragflow/sync/mappings`，确认租户映射和项目归属。
3. 停止新的 import/reconcile，避免覆盖人工修复。
4. 对可重试项，使用同一 Run `POST .../runs/:id/import` 或 `POST .../runs/:id/assign`。
5. 对冲突项，查看 Binding Versions，保留原 Binding ID，使用 relink 或 conflict resolve；不允许换 Binding ID 掩盖冲突。
6. 需要全量收敛时，先 `POST /api/v1/system/ragflow/sync/scan`，再 `POST /api/v1/system/ragflow/sync/reconcile`。
7. 记录每类资源的成功、失败、跳过数量和 artifact hash。

### 通过标准

- 同一外部资源不产生重复影子对象。
- 跨租户对象保持隔离。
- 修复动作均有审计和 Run 明细。

## 9. Gateway 配额与幂等异常 SOP

### 现象

429 过多、配额不释放、并发重放重复执行、幂等 409 异常、SSE 断开后配额未归还。

### 处置步骤

1. 记录 API Key、租户、用户、助手/Search App/Agent、Idempotency-Key、body hash、trace_id。
2. 在 Usage/审计中核对请求次数、Token、上游调用和配额结果。
3. 若是业务超量，调整 Quota Limit 或 API Key 预算，必须走审批；不要绕过 429。
4. 若是失败未释放，确认下游是否已经关闭幂等记录；保留 pending 台账证据。
5. 等待周期性 pending sweep；如长期残留，导出台账并在审批后修复。
6. 重放验证：同一 Idempotency-Key 只执行一次；修改 body 后应 409 且不覆盖原记录。
7. 并发压测验证 Token/Request 双配额不为负、不重复扣量。
8. 对 SSE cancel 验证：客户端断开后配额释放、幂等收敛、审计可回查。

### 通过标准

- 配额终态与请求账本一致。
- 重放和失败路径均能收敛。
- 错误契约稳定：401/403/409/429/502/timeout 可区分。

## 10. 通知失败 SOP

### 配置要求

Email 示例：

```yaml
alerting:
  enabled: true
  email:
    enabled: true
    host: smtp.example.com
    port: 587
    username: ""
    password: ""
    from: ragflow-x@example.com
    to: ops@example.com
    timeout_sec: 10
    max_attempts: 3
    backoff_initial_ms: 250
    backoff_max_ms: 2000
```

推荐通过 `RGX_ALERTING_EMAIL_*` 环境变量注入，不要把密码写入 YAML。

企业机器人示例：

```yaml
alerting:
  enabled: true
  webhooks:
    - name: ops-wecom
      type: wecom
      url: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
      enabled: true
    - name: ops-dingtalk
      type: dingtalk
      url: "https://oapi.dingtalk.com/robot/send?access_token=..."
      enabled: true
```

钉钉 `secret` 使用时间戳 + HMAC-SHA256 签名，必须通过 `alerting.webhook_secret` 或安全配置注入；不要写入 YAML。企微 key 已包含在机器人 URL 中时，必须按生产秘密保护回调 URL。

### 演练与处置

1. 触发审批、告警、配额超限、发布失败四类事件。
2. `GET /api/v1/alerts` 确认事件；`GET /api/v1/alerts/deliveries` 确认通道、attempt、状态、错误和幂等键。
3. 检查 SMTP 日志是否完成 STARTTLS、认证和 2xx 收据。
4. generic Webhook 校验 JSON 是否包含 tenant、trace_id、object_type、action、object_id、deadline；HMAC 是否通过。企微/钉钉校验 Markdown 消息、2xx 返回和 `errcode=0`；钉钉还要校验 `timestamp` 与 `sign`。
5. 同一事件短时间重复触发，确认节流生效且不重复轰炸。
6. 故意停用邮件服务或 Webhook 返回 5xx，确认重试、退避、失败终态和审计。
7. 通道恢复后可使用投递列表中的 **手动重试**，或在补偿 Worker 下次扫描时重放；不要直接改库。

### 通过标准

- 四类事件均可触达；失败可见、可重试、可审计。
- 敏感信息不进入邮件正文或日志。
- 重试不会造成重复业务执行。

## 11. 审计链校验与修复 SOP

审计修复是高风险操作，必须先取证、后审批、再修复。

### 校验

1. `GET /api/v1/audit/verify` 返回完整性结果。
2. `GET /api/v1/audit/anchors` 导出锚点和时间范围。
3. `GET /api/v1/audit/export` 保存 CSV/JSON 作为只读证据。
4. 记录第一条断裂/篡改位置、租户、trace_id 和相关操作。

### 修复

1. 冻结相关写入或把环境切到只读。
2. 由合规/安全负责人出具修复授权，说明允许修改的记录、原因和边界。
3. 平台管理员调用 `POST /api/v1/audit/repair`；必须传审批/说明，不使用匿名或空说明修复。
4. 修复后再次 `GET /api/v1/audit/verify`。
5. 导出新的审计锚点，保存修复前/后证据。

### 通过标准

- 只有授权修复被执行。
- 修复不能掩盖原始事故记录。
- 审计链最终可验证，且修复动作本身有审计。

## 12. 故障升级矩阵

| 故障 | 第一响应 | 升级条件 | 恢复入口 |
| --- | --- | --- | --- |
| 服务不可用 | 查进程/数据库/最近发布 | 影响生产 15 分钟 | 第 5/6 节 |
| RAGFlow 不可用 | 能力探测 + 切换实例 | 生产问答失败率上升 | 第 7 节 |
| 同步失败 | 停止 reconcile，查 Run items | 出现跨租户或覆盖风险 | 第 8 节 |
| 配额异常 | 冻结可疑 Key，查台账 | 重复扣量或资损风险 | 第 9 节 |
| 通知失败 | 查 Delivery | 审批/生产发布通知不可达 | 第 10 节 |
| 审计异常 | 保留证据并冻结写入 | 哈希链断裂或越权 | 第 11 节 |
| 安全事件 | 撤销 Key、切只读 | 数据泄漏或横向移动 | 第 11/6 节 |

## 13. 演练证据模板

```text
演练编号：OPS-YYYYMMDD-01
场景：RAGFlow 降级 / 同步失败 / SMTP 失败 / 回滚
环境：
开始/结束时间：
操作者 / 审批人：
变更对象：
trace_id：
输入证据：
执行命令：
输出证据：
失败路径：
恢复结果：
剩余风险：
签署人：
```

每次生产出口演练至少保留一份该模板与原始输出。截图不能替代命令输出；需要展示用户结果时可补截图。

## 14. 当前验收状态

| 检查项 | 状态 | 证据 |
| --- | --- | --- |
| SOP 编写 | Done | 本文 |
| 代码与测试回归 | Done | `go vet ./...`、`go test -count=1 -shuffle=on ./...`、`npx tsc --noEmit`、`npm test`（56 files / 281 tests）、`npm run build`、Playwright E2E（10 tests） |
| 六类导航视图 | Implemented | `web/src/lib/navigationRoles.ts`、内置角色目录测试与前端权限回归 |
| 通知手动重试 | Done | Service/Router 单测与前端类型测试 |
| 成本归因 | Implemented | Cost Metric cost 字段、v85 迁移、过滤测试与前端明细测试 |
| 反馈坏例闭环 | Implemented | request_id 契约、v86 迁移、KnowledgeOps 自动挂接、EvalCase 来源链路与服务/前端回归 |
| Gateway E2E | Pending | 待真实环境证据 |
| 上游引擎 Contract Drill | Done（v1.0.0，2026-11-10） | P1 provider 缝契约 4/4 + `--full` 串行套件 5 passed / 0 failed / 0 skipped；`> 1.0.0` 仍要求复验 |
| Release 生产候选演练 | Pending | 待真实环境证据 |
| 双实例同步演练 | Pending | 待真实环境证据 |
| 性能基线报告 | Controlled Done / Production Pending | 受控基线证据（本地 SQLite + controlled upstream）由工程团队归档；生产环境复测仍待归档 |
| SMTP/Webhook/企微/钉钉送达演练 | Pending | 待真实环境证据 |

## 15. OTel Trace 深链约定（2026-09-27 新增）

TraceRun 的证据指针支持跳转到外部 tracing UI（Grafana Tempo / Jaeger 等）。约定如下：

| 项 | 约定 |
| --- | --- |
| 配置 | `RGX_OTEL_TRACE_UI_URL_TEMPLATE`（yaml `observability.otel_trace_ui_url_template`）；`GET /api/v1/system/settings` 的 observability 组以只读项展示（`source: yaml_env`、`editable: false`，不可经 PATCH 修改，变更需改部署配置后重启） |
| 模板格式 | 完整 URL，含 `{trace_id}` 与可选 `{span_id}` 占位符，运行时替换后写入证据指针 |
| 示例（Grafana Tempo） | `https://grafana.example.com/explore?left={"queries":[{"queryType":"traceql","query":"{trace_id}"}]}&span={span_id}`（URL 编码后配置） |
| 示例（Jaeger） | `https://jaeger.example.com/trace/{trace_id}?uiFind={span_id}` |
| 安全约束 | 仅接受 `http://`/`https://` 前缀且必须包含 `{trace_id}`，其余模板一律忽略（防 `javascript:`/`data:` 注入）；空值时证据只带原始 id，不出链接 |
| 证据指针字段 | `otel_trace_id`（32 hex）、`otel_span_id`（16 hex）、`otel_endpoint`（collector host:port，脱敏）、`otel_ui_link`（渲染后的完整 URL，模板未配置时缺省） |
| 优先级 | 前端优先使用 `otel_ui_link`；缺省时若 `otel_endpoint` 存在则回退拼接 `https://<endpoint>/trace/<trace_id>?span=<span_id>`；均无则仅显示 id 文本 |
| 权限 | 深链入口与 TraceRun 查询同权（`knowledge-ops:read`）；tracing UI 本身的访问控制在部署侧（SSO/反代） |

Runbook 使用：演练证据模板（§13）中的 `trace_id` 可直接粘贴到 governance trace 查询卡片；开启 tracing 且配置模板后，卡片中的「查看 OTel Trace」链接直达该请求的 span 视图，无需手工拼接 Tempo/Jaeger URL。

## 16. v0.5.0 增量处置（2026-11-10，与代码同版）

本节覆盖 v0.3.0/v0.4.0/v0.5.0 新增能力与已实测故障类的处置口径（三条版本线合并为一次 v0.5.0 正式发布）。**原则不变**：先取证、再修复；改设置/审计链/发布基线要审批；修复后复核健康、权限、审计与关键业务路径。

### 16.1 新增能力与处置入口

| 能力 | 入口 | 失败时先看什么 |
| --- | --- | --- |
| 场景模板目录（T7.0） | `GET /api/v1/scenario-template-catalog`（只读，强制本工作区） | 返回空 → 查 `rgx_scenario_template` 该租户行数与 seed 日志；跨租户 404 属预期（`doc/234` §2.2） |
| 模板实例化建助手 | `POST /api/v1/scenario-template-assets/:id/instantiate-chat` | 403 看 `chat:manage`；409 看参数画像是否被改（`trialProfileMismatch`） |
| 上手就绪度（T7.1′） | `GET /api/v1/onboarding/readiness` | 4 里程碑全派生，无新表；某步恒 false → 按 `doc/232` §2.4 的派生条件逐条核 |
| 检索级试跑（T7.3′） | `POST /api/v1/trial-runs`、`GET /api/v1/trial-runs/:id`、`.../cancel` | `failure_reason=retrieval_failed` → 查上游 `/retrieval` 时延与超时；`gate_eligible` 恒 false 属设计（`231` D11） |
| 试跑幂等 | 同上，`idempotency_key` | 409/40941 = 同 key 换了助手；409/40942 = 助手未绑定该模板 |
| 流式失败审计 | `gateway.stream_error` 审计动作；错误码 `502/50231`（chat）、`502/50265`（search）、`502/50289`（agent） | 失败投放为 `SYSTEM_FAILED` 且**不计费**；同一流只有一条终态错误（`doc/235` §4） |

### 16.2 升级与回滚（迁移 150–162）

v0.5.0 的迁移上新至 **162**，全部为前向迁移（不写 down 脚本），重复执行安全：

| 迁移 | 内容 | 回滚影响 |
| --- | --- | --- |
| 150 | 治理字典 seed | 纯数据，回滚无影响 |
| 151 | 多维配额桶（bucket 维度 tenant/model/assistant） | 旧版本忽略新桶表，按旧口径限流 |
| 152 | 发版版本带 preflight | 旧版本不读，回滚后升级门禁退回旧判定 |
| 153–156 | 数据源连接器基础表（binding/run/actor_enabled/source_identity） | 旧版本忽略；连接器能力仍在补齐 |
| 157 | `rgx_trial_run`、`rgx_trial_case_result`、`rgx_chat` 两列 + 两个唯一索引 | 旧版本不读试跑表；**禁止**手工 `DROP TABLE`（已产生的试跑证据会丢） |
| 158–161 | RBAC 数据同步：`business_user` 读意图、`team_admin` 成员面、`trial-run` 独立资源、`viewer` 读权收敛 | 纯数据、幂等；回滚后重启会按旧版内置矩阵重新 reconcile |
| 162 | 引用定位列（`ref_index`/`positions`）+ 每轮 top 检索相似度 | 加列型，旧版本忽略新列 |


1. **升级前**：按 §6 备份；确认 `RGX_TEST_POSTGRES_DSN` 指向**非生产库**先跑一次重入契约
   `go test ./internal/db -run 'TestMigrationV157' -count=1 -v`（SQLite 用例恒跑；PG 用例无 DSN 时 SKIP）。
2. **升级**：`Migrate` 对 157 走 `HasTable`/`HasColumn` 守卫，重复执行安全（PG 实测：`doc/238` §1）。
3. **回滚**：应用可回滚到上一版本，**表与列保留**（前向迁移，不写 down 脚本）。回滚后旧版本不读 `rgx_trial_run`，无兼容风险；**禁止**为了"干净"手工 `DROP TABLE rgx_trial_run`——已产生的试跑证据会丢，且 `rgx_chat` 两列被旧版本忽略，不影响助手读写。
4. **演练脚本**：`scripts/pg_release_rollback_drill.ps1`（`RGX_PG_RELEASE_DSN` / `RGX_PG_RELEASE_BACKUP_DIR` / `RGX_PG_RELEASE_TABLES`），演练证据按 §13 模板归档。

### 16.3 已实测的三类现场故障（2026-10-27，`doc/236` §5）

| 症状 | 现场判定 | 处置 | 不要做什么 |
| --- | --- | --- | --- |
| 新上传文档解析停在 `progress=0.8`、`chunk_count=0`、`progress_msg` 空、20 分钟不推进 | 上游解析任务静默卡住（根因未定） | 查 `ingestion_task_log` 与 DeepDoc 模型加载日志；**换小文件复测**确认是否与文档相关；记录 trace 后重启解析任务 | 不要在前端反复重建数据集；不要把"卡住"当成本产品 bug 上报（根因未定，未提 issue，见 `doc/236` §5） |
| `POST /chats` 返回 `code=102 ... doesn't own parsed file` | 上游设计语义：库内没有已解析文件时不允许建助手 | 按"先入库并解析 → 再建助手"的顺序操作 | 不要提 issue（属上游设计语义，不是缺陷） |
| 单次检索 60 s 级（实测 60031/60032 ms） | 该环境上游检索慢，非本产品链路问题 | 用 `ragflow_provider_roundtrip` 与 `qa_ragflow_chat` 两条受控基线区分"网关慢"还是"上游慢"（受控性能基线证据由工程团队归档） | 不要在慢环境上判定试跑/发布门失败；试跑会如实记 `retrieval_failed` |

### 16.4 上游缺陷的本地 hotfix 清单（**禁止删除**）

以下三处是等上游修复期间的本地兜底，移除前必须先按 `doc/236` §6 复核上游源码：

| 文件 | 对应上游 issue | 移除判据 |
| --- | --- | --- |
| `internal/common/http.go` | #21097（60s/20min 双超时硬编码） | 上游 `git grep` 命中 `LLM_HTTP_RESPONSE_HEADER_TIMEOUT` 且 fix commit 已进 release tag |
| `internal/ingestion/component/knowledge_compiler/common/jsonchat.go` | #21097 同源 | 同上 |
| `internal/service/nlp/retrieval.go` | #21095（检索投影丢 `source_doc_ids`） | 上游 `retrieval.go` 的 `source_doc_ids` 白名单命中且已发版 |

> 版本带提醒：上游 `1.0` 线已发布正式版 `v1.0.0`（2026-11-10 复验通过），`MaxVerified` 已抬钉到 `v1.0.0`。升级 preflight 对"新于已验证带"（`> 1.0.0`）的引擎仍一律要求契约复验（`verification_required`；不可解析或不可达记 `blocked`），不会因为"版本号更大"直接放行。

### 16.5 上游引擎升级检查表（v1.0.0）

把 RAGFlow 引擎从 `0.27.x` 升到 `1.0.0` 前，逐条确认以下实测约束（均为 v1.0.0 的真实行为，不是本产品的兼容开关）：

| 编号 | 约束 | 现场口径 |
| --- | --- | --- |
| D1 | 新建知识库必须显式给 `embedding_model` | 不给时上游回落到默认值 `"0"`，解析报 `500 model configuration unavailable`；先确认 `/models` 中存在该 embedding 模型名 |
| D2 | 对话端点从 `POST /chats/{id}/completions` 改为全局 `POST /chat/completions` | 旧端点返回 404；请求体需带 `chat_id`/`session_id`，`stream:false` 时一次性返回 |
| D3 | 知识编译产物面只剩 `wiki` | 只暴露 `wiki` 产物；`page_index` 变体在入口被拒（`422/42261` + 提示改用 `wiki`），不会静默产出空结果 |
| D4 | `/agents` 列表混入 `type=compilation_template_group` 行 | 把模板组当画布用会拿到 `103 permission to access the agent`；需按类型过滤后再取画布 |
| D5 | Pipeline 库与文档绑定必须带 `pipeline_version_id` | 版本行只能由 `POST /agents/{id}/publish` 产生；文档 re-bind 到另一画布同样要带该画布的版本 id |

**升级顺序建议**：先升级引擎并保留旧实例回滚路径 → 调 `POST /api/v1/system/capabilities/verify` 确认版本带与能力状态 → 再处理知识编译与 Pipeline 库（两者对 `pipeline_version_id` 与产物 kind 最敏感） → 最后放开业务流量。引擎版本超出已验证带（`> 1.0.0`）时一律先复验再放行。

#### 已知上游缺陷台账（2026-11-10 实测，上游未修）

U1–U5 已被本产品行为吸收，U6–U8 是公开 issue 跟踪项，U9–U11 为未验证面，U12 上游已修。
现场排查前先用 `GET /api/v1/system/capabilities` 的 `version_issues` 对一遍台账（与本节同源，机器可读）。

| 编号 | 上游缺陷 | 现场表现 | 处置 / 注意 |
| --- | --- | --- | --- |
| U1 | 结构化编译产物无读取面（artifacts 固定 `type_kwd=wiki_page`，检索只回 `compile_kwd=wiki`，但 Compiler 仍在写 entity/relation 行） | 编译 `state=completed` 却绑不上产物、检索零命中 | 触发时即拒 `422/42261` 并提示改 `wiki`；**不要**在 `1.0+` 上断言 `page_index` 产物存在 |
| U2 | 上游 web bundle 登录发 `RSA(base64(密码))`、后端解密 `RSA(明文)` | 浏览器登录恒 `109`（引擎自带 UI） | 业务登录走 RAGFlow-X；引擎侧用 API Key；用上游 UI 必须打 dist 补丁（部署侧，不入库） |
| U3 | 建库不显式给 `embedding_model` 时回落 `"0"` | 解析 `500 model configuration unavailable` | 平台已自动解析租户默认 embedding（5 分钟 TTL）；换模型实例后改默认值即可，无需重启 |
| U4 | `/agents` 混入 `type=compilation_template_group` 且计入 `total` | 同步出“假 agent”，调版本面 `103` | 已按 `type` 过滤并修正 `total`；核对资源同步条数时记得减去该类行 |
| U5 | 文档改绑另一画布未带该画布已发布版本 | `102 pipeline version does not exist / does not belong to canvas X` | 先 `POST /agents/{id}/publish` 再带 `pipeline_version_id`；文档 re-bind 同样要带 |
| U6 | #21095 检索 `_source` 投影丢 `source_doc_ids`（PR #21106 无人评审） | 回读引用无法还原切片归属 | 已改为完成态快照 + `ref_index`/`positions` 落库；**不得**依赖回读字段修引用 |
| U7 | #21097 `ResponseHeaderTimeout` 20 分钟 / 流式 60 秒硬编码（上游 `main` 已合 PR #21434，未进 release tag） | 长编译、慢模型被上游超时打断 | 长任务一律走 X 调度器（显式 deadline + 单轮恢复）；不靠引擎超时判定失败 |
| U8 | #21096 Windows 构建破损（`statusFromExitCode`，PR #21099 无人评审） | Windows 上构建上游 sandbox 失败 | 上游 sandbox 只在 Linux 运行；本产品不重写执行器，只在平台侧治理审计 |
| U9–U11 | 图检索评分形式 / Memory `team-me` 权限模型 / agent `sandbox`·`skills` 面跨版本漂移 | 版本间行为不一致（`untested`） | 只在 dry run 触达，**不得**在未验证带上当发版门禁；升级后先小流量验证再放量 |
| U12 | search app 单数 `/completion` 回归 `50265` | 旧路径报错 | 上游已修复；本侧固定复数 `/searches/{id}/completions`，改路径前先看契约测试 |

### 16.6 发版前五条复核命令

```
go build ./... && go vet ./...
go test ./internal/service -count=1 -timeout 30m     # 默认 10m 会 panic，必须显式放宽
go test ./internal/db ./internal/router ./internal/gateway/contract -count=1
RGX_TEST_POSTGRES_DSN=... go test ./internal/db -run 'TestMigrationV157' -count=1 -v
cd web && npx tsc --noEmit && npx vitest run
```

已知执行约束（非代码缺陷）：`go build` 范围用 `./cmd/... ./internal/...`（仓库内若留有上游解包目录，`./...` 会编译失败）；`go test ./internal/service` 全包耗时约 15 分钟，需显式放宽 `-timeout`；live 套件一律单轮串行、无并发，按 `RGX_ALLOW_*` 显式开启；前端静态检查以 `tsc --noEmit` 为准。
