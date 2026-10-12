# ragflow-x

RAGFlow-X 是面向私有数据中心的企业知识库 AI 基础设施补齐层：Go 1.25 + Gin + GORM 提供 API 与网关，React 19 + ra-core + shadcn/ui 提供管理前端。核心边界是**不 Fork RAGFlow、不侵入其 schema**；RAGFlow 继续作为核心 RAG 执行面，RAGFlow-X 通过官方 HTTP API 操作知识引擎，本地只维护租户、组织、审批、配额、审计、用量治理、知识生命周期和对话体验所需的治理与投影数据。

## Product Direction

RAGFlow-X 的目标不是复述“RAGFlow 管控平面”，而是消除企业生产环境真实需求与 RAGFlow 这类核心 RAG 引擎能力之间的 GAP。所有产品判断都要回答：这个能力是否降低了私有数据中心里搭建、运营、审计和演进知识库类 AI 基础设施的门槛，并提升了安全性与用户体验。

主要价值路径：

1. **安全与治理 GAP**：通过 RBAC/ABAC、租户/项目隔离、安全网关、配额、审计和审批，把企业级安全与合规要求落到统一入口。
2. **全生命周期知识体系建设**：覆盖接入、解析、质量、版本、发布、消费、反馈、评测、运营与治理，而不是只提供一次性上传和检索。
3. **统一对话中心**：以一致的身份、权限、目标路由、会话/Run、引用、工具摘要、富内容与导出体验承接 Chat、Search、Agent 和工作台场景。
4. **私有环境可运维性**：以契约、可观测、发布门禁、回滚和资源同步降低企业维护技能门槛。

开发红线：优先改进全生命周期知识体系建设和统一对话中心两条主轴，把安全治理、多源接入、模型路由、观测和自动化作为支撑能力；不要把 RAGFlow-X 做成第二个 RAG 引擎、Wiki、Agent 执行器、低代码应用工厂或功能堆砌目录。

## Commands

- Install：`go mod download`；前端 `cd web && npm install`。
- Dev：`make dev`（后端）或 `make dev-web`（前端）；也可用 `go run ./cmd/server/`。
- 本地演示/自测：`make start-local`（SQLite + mock RAGFlow）、`make status`、`make stop`、`make restart`。
- Build：`make build`；后端二进制 `make build-server`；前端 `make build-web`。
- Lint + Check：提交前运行 `make check`（`go vet` + `go build` + `go test` + 前端 `tsc --noEmit`）；若环境可用，另跑 `make lint`。
- Backend tests：`make test`；定向测试用 `go test ./internal/service/ -run TestApproval -count=1` 这类命令。
- Frontend tests：`cd web && npm test`；单文件用 `npx vitest run src/lib/errors.test.ts`；浏览器 E2E 用 `npm run test:e2e`。
- PostgreSQL 集成测试：先设置 `RGX_TEST_POSTGRES_DSN`，再运行 `make test-integration`。
- Database migration：启动时自动执行；定义集中在 `internal/db/migrate.go`。
- Docker：`make docker-config`、`make docker-build`、`make docker-up`、`make docker-log`。
- Go 格式化：`gofmt -s -w .`；`goimports` 的 local 前缀使用 `github.com/ragflow-x/ragflow-x`。

## 项目工程流程

1. 模糊需求先使用 `interview-me` 澄清。
2. 非小型功能先使用 `spec-driven-development` 输出规格。
3. 质量要求使用 `constraint-driven-development` 写入 `CONSTRAINTS.md`。
4. 多文件改动使用 `incremental-implementation`，保持小步提交。
5. 行为变更使用 `test-driven-development`，先写回归测试。
6. API 变更额外使用 `api-and-interface-design`。
7. UI 变更额外使用 `frontend-ui-engineering`。
8. 合并前使用 `code-review-and-quality` 审查。
9. 涉及用户输入、数据库、外部服务时使用 `security-and-hardening`。
10. 发布前使用 `shipping-and-launch` 输出检查清单。

## Architecture

### Backend

- `cmd/server/`：只做配置装载、依赖装配、迁移、HTTP 启动与优雅退出；不写业务规则。
- `internal/handler/`：HTTP 适配层。只做参数绑定/校验、调用 service、构造统一响应；禁止访问 GORM 或写业务规则。
- `internal/router/`：路由分组与端点注册。所有受保护端点必须在 `access.go` 中注册 RBAC/ABAC 规则，禁止只依赖前端隐藏入口。
- `internal/middleware/`：认证、授权、请求 ID、限流、恢复等横切逻辑。
- `internal/service/`：业务逻辑唯一归属。按领域拆分为 auth/tenant/team/project/dataset/model-provider/gateway/chat/agent/approval/worker/governance 等模块，而不是把新逻辑堆入 `service.go`。
- `internal/repository/`：GORM 查询与事务边界。必须保持租户/项目过滤和可测试的数据访问；复杂 SQL 也放在这一层。
- `internal/model/`：GORM 模型、RBAC/ABAC 枚举与领域常量的单一来源。
- `internal/db/`：PostgreSQL/SQLite 连接、版本化迁移、内置角色与权限 reconciliation。
- `internal/provider/`：外部集成层。`Provider` 是 service 唯一依赖接口；RAGFlow 的 HTTP 实现与 mock 实现成对维护。
- `internal/pkg/`、`internal/notify/`、`internal/obs/`：可复用工具、通知和可观测性；不得反向依赖 service/handler/router。
- `internal/setup/`：首次启动向导与安全初始化；不承载常规业务 CRUD。
- `config/`：`config.example.yaml` 与 `config.production.example.yaml` 是模板；真实配置和密钥不入库。

### Frontend

- `web/src/resources/<entity>/`：按资源组织 List/Show/Create/Edit 与领域组件；资源注册在该目录的 `index.tsx`。
- `web/src/components/ui/` 与 `components/admin/`：shadcn/ui 基础组件与 ra-core CRUD 胶片。
- `web/src/lib/`：纯逻辑、API 实例、错误转换、通用工具；必须有单测。
- `web/src/hooks/`、`layouts/`、`login/`、`dashboard/`、`i18n/`：分别承载自定义 hooks、布局、认证页、仪表板和多语言文案。
- 复杂交互（工作台、Agent 画布、审批）允许定制组件，但必须复用统一 API 错误处理、toast、i18n 和权限语义。

### Docs

- `doc/` 是设计权威：总体架构见 `02_总体架构.md`，后端见 `04`，前端见 `05`，数据/集成见 `06`，安全见 `07`，接口契约见 `12`，权限矩阵见 `11`/`30`，异步任务见 `21`，审批见 `34`，前端检验标准见 `26`，最新审查结论见 `49`。
- 修改架构、API 契约、权限、迁移、审批或网关语义时，先读对应文档；若文档与代码冲突，在 PR 中明确指出，不要静默选边。

## Code Conventions

### Backend

- 依赖方向固定为 `handler/router → service → repository → model/provider`；`internal/pkg`、`internal/notify`、`internal/obs` 不依赖业务层，禁止循环依赖。
- Service 方法接收 `context.Context`；所有外部 I/O 和长耗时工作可取消、可超时、可观测。
- 所有 RAGFlow 调用必须经过 `internal/provider`。禁止在 service/handler/repository 中拼 RAGFlow URL、直接创建 HTTP client 或泄漏 RAGFlow 凭据。
- 新增能力优先扩展 `Provider` 接口，并同时更新 HTTP 与 mock 实现；接口签名包含跨上下文与分页/筛选时保持两个行为一致。
- 新表结构只能通过 `internal/db/migrate.go` 追加新 `migrations` 条目；禁止修改历史迁移，禁止在业务代码中散落 `AutoMigrate`。
- 审计链是合规红线：所有写操作按现行实现记录 actor、action、resource、result、trace，并保持 `prev_hash`/hash 连续；禁止绕过审计封装。
- 异步任务和审批执行必须幂等：使用状态机、唯一键/幂等键、乐观锁或事务边界；失败路径要可重试、可补偿、可观测，不丢事件。
- 并发可变状态只归属同一把锁；锁内不调用未知回调或长 I/O；新增后台 worker 必须处理取消、退出、退避与重复启动。
- 使用 `log/slog`；错误用 `internal/pkg/httperr` 语义转换；响应遵循 `doc/12` 的 `code/message/data/trace_id` 信封。
- 不复制粘贴大段领域逻辑。同一文件建议 ≤600 行、硬上限 ≤1000 行；超过时按领域拆分，不为拆而拆。

### Frontend

- TypeScript strict；禁止非必要 `any`。优先命名导出，类型导入用 `import type`。
- 请求只经 `web/src/lib/api.ts` / DataProvider；禁止组件直接 `axios` 或 `fetch`。不得硬编码后端 URL，环境配置经 Vite env。
- 列表、筛选、批量操作优先使用现有 ra-core + shadcn 胶片；禁止手写业务表格造成交互不一致。
- 错误必须经 `ApiError` 转换后用 `notify()` 呈现；禁止 `catch {}`、`console.log` 吞错、用前端自造错误覆盖后端语义。
- 用户可见文案全部走 i18n，同时维护 zh/en；样式使用 Tailwind 和 `cn()`，不硬编码颜色或大量内联样式。
- Markdown 用 `react-markdown`；禁止 `dangerouslySetInnerHTML`。复杂富内容需保留组件级测试。
- 组件保持函数式；推荐 ≤400 行，超长优先抽纯函数、子组件或自定义 hook。
- 前端权限只控制展示与入口，实际授权必须由后端 RBAC + ABAC 强制。

## Testing

- 新功能、Bug 修复、权限、租户隔离、审批、审计和网关用量治理变更必须包含回归测试；不依赖“本地看起来正常”。
- Backend：
  - Handler/router 变更更新路由覆盖、权限规则与端到端测试。
  - Service 变更补充领域单测，优先通过 mock Provider/repository 隔离外部服务。
  - Provider 变更同时验证 HTTP 请求契约和 mock 行为。
  - 迁移变更验证 PostgreSQL + SQLite 两条路径与 reconciliation。
  - 并发、审批 SoD、审计链、配额、幂等用显式测试覆盖。
- Frontend：
  - 纯函数和通用组件必须有 Vitest 测试；测试与源码同目录。
  - UI 交互用 Testing Library，不测试实现细节。
  - Playwright E2E 覆盖登录、关键管控流程和审批闭环；外部网络 mock 或由本地服务提供。
- 提交前至少运行：`make check`；涉及前端构建时确保 `make build-web` 通过。若修改 lint、迁移、审批、PostgreSQL 路径或网关用量治理，再运行 `make lint` 与 `make test-integration`。

## Boundaries

- 禁止自动 `git push`、删除未明确授权的文件、修改生成产物或发布配置。
- 禁止提交 `config/config.yaml`、`.env`、`runtime.secrets.json`、`.master.key`、RSA key、数据库文件、日志、客户数据或真实 API Key。
- 禁止绕过 Provider 直连 RAGFlow 做写操作；只读增强数据源必须可配置、只读且能降级。
- 禁止依赖前端做安全控制；后端必须校验认证、RBAC、ABAC、租户隔离、配额与输入。
- 禁止跨租户查询或返回无租户归属的资源；新增查询必须先说明 tenant/project 隔离条件。
- 禁止把业务规则写进 handler、把 SQL 写进 service、把领域模型写进 repository。
- 禁止引入新 ORM、全局状态库、UI 库或包管理器；不新增冗余抽象，三个相似实现出现后再泛化。
- 禁止用“临时兼容”长期保留双路径；如必须兼容，写明条件、负责人与移除点。
- 禁止为了通过测试改断言或跳过测试；先确认行为契约，再改测试。

## PR & Commit

- 分支使用 `feature/*`、`fix/*`、`refactor/*`、`docs/*`、`test/*`。
- Commit 使用 Conventional Commits：`feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`；scope 可用 `service`, `repository`, `provider`, `router`, `handler`, `db`, `web`。
- PR 描述包含：动机、关键设计、影响面、数据库/权限/审计影响、测试结果和已知风险。
- 自检清单：分层正确；依赖无循环；路由已注册权限规则；审计/租户隔离完整；迁移仅追加；Provider HTTP 与 mock 同步；前后端错误与 i18n 完整；`make check` 通过。
- 需求或安全语义不清楚时先提问；不要为演示路径绕过权限、审计或租户隔离。

## Security

- 所有输入校验参数、body、上传类型与大小；所有输出按最小权限裁剪，不返回密钥、哈希或内部 ID 之外的敏感链路信息。
- 密码走 bcrypt，API Key 走 AES 加密；加密密钥、JWT secret、DSN、RAGFlow key、webhook secret 只从安全配置或环境注入。
- 日志与错误消息不得包含密码、token、API key、完整授权头、个人敏感数据或明文客户内容。
- CORS、CSP、HSTS、请求大小、私有地址限制、metrics 访问控制按配置收紧；生产必须 HTTPS、白名单来源、轮换默认凭据。
- 涉及删除、导出、越权可见性、支付/配额变更等敏感动作必须走审批、职责分离和审计。
- 保留策略删除数据时不得破坏审计取证要求；审计备份与修复遵循 `doc/44`。

## 核心工程原则

- 1.架构与领域优先:计划阶段应以理想架构为目标，明确业务目标、领域边界、模块职责、依赖方向和数据流，形成符合领域规律、面向长期维护且可持续演进的设计后再进入编码;不得以短期实现便利牺牲整体设计。设计必须完整，实现应当克制:不做推测性抽象，抽象延迟到第二个真实用例出现时才引入，单一场景直接实现。
- 2.追求优雅的代码模块:模块应高内聚、低耦合，通过精简且稳定的接口封装内部复杂度，使职责、命名、依赖和扩展方式清晰自然;代码按单一职责拆分，单个文件不得超过900行，接近上限时应优先重构模块边界。
- 3.保持边界与数据流清晰:协议模型、领域模型、持久化模型和视图模型不得相互泄漏;数据必须在边界处完成校验和独立转换，避免跨层共享可变状态。
- 4.安全与隔离默认开启:所有功能均按多租户、多用户场景设计，明确认证、授权和数据隔离边界;遵循最小权限原则，任何外部输入均视为不可信，敏感信息不得进入代码、日志或响应。
- 5.面向并发与故障设计:后端应主动考虑幕等性，竞态，事务边界、超时，取消、重试，背压和资源群放;不得通过无边界重试，看错或隐式共享状态掩盖问题和可访问性
- 6.保障宪整前端件验:前端应控制凛染成本、异步状态和并发请求，保持清晰的U结构;用户流程必须覆盖加载，空状态，错误，重试、反馈和可访问性
- 7.复用稳定的业务语义:优先复用已有感块和熊力，但不要仅因代码外形相似而过早抽象;确需重复时，必须注释说明其独立演进或暂不抽象的原因。新增依赖前先核查项目已有依赖(根 package.json与 packages/workspace)能否满足需求，不得臆断已有库缺少功態-先查阅文档和类型定义;确需引入时优先成熟且维护良好的库，不重复实现通用功能。
- 8.为未来维护者保留上下文:代码，注释、测试和架构文档是跨越时间的协作媒介。非显然的设计决策、兼容约束、已知缺陷和临时方案，必须记录原因、影响范围，潜在风险及移除条件;技术债务应关联可追踪任务，关键架构决策应同步到ADR，禁止留下缺少上下文的TODO
- 9.确保究更可验证、可观测、可回;每项改动都应行为可测试、运行状态可观测、故障可定位，并兼顾向后兼容和回浪路径;错误与日志必须保留诊断上下文，但不得泄露敏感信息。
- 10.删除优于兼容:内部路径重构时直接接删除过时实现，禁止新增兼容层、deprecated shin 或双写逻辑;对外契约(/api/*等稳定接口、数据库迁移)的兼容性按协议契约单独评估，属于合同义务而非迁就旧代码。
