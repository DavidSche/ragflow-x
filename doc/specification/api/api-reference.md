# RAGFlow-X 接口参考（OpenAPI 3.1.0）

> **本文件是生成物，不要手改。** 源 = `swagger.json`（`info.version 1.0.0`，452 条路径 / 616 个操作），
> 生成命令：`bash scripts/release-gate/api-doc.sh gen`；
> 门禁 `s28.api-doc` 会重新生成并**逐字节**比较，手改必红。

## 这份文档能说什么、不能说什么

它由 OpenAPI 描述符渲染而来，所以它只能写下描述符里真的有的东西。下面的数字是每次生成时从描述符里**数出来**的——它们变，文档就变，门禁就红。

| 项 | 数量 | 含义 |
| --- | --- | --- |
| 路径 / 操作 | 452 / 616 | 路由清单本身是完整的 |
| 操作标题仍是占位符 | 588 / 616 | `Auto-generated API route` = handler 上没有 `@Summary` 注解 |
| 有 `@Description` | 28 / 616 | 业务语义、前置条件、副作用 |
| 有请求体 | 14 / 616 | 今天是 0：`@Param … body` 还没写 |
| 有参数 | 373 / 616 | 路径/查询参数的名字、位置、类型、是否必填 |
| 声明的 schema | 53（docs.Envelope、github_com_ragflow-x_ragflow-x_internal_db.ResourceActions、github_com_ragflow-x_ragflow-x_internal_model.Permission、github_com_ragflow-x_ragflow-x_internal_model.Role、github_com_ragflow-x_ragflow-x_internal_model.Tenant、github_com_ragflow-x_ragflow-x_internal_model.User、github_com_ragflow-x_ragflow-x_internal_model.UserClearance、github_com_ragflow-x_ragflow-x_internal_service.TokenResult、internal_handler.AssignRoleEnvelope、internal_handler.AssignRoleRequest、internal_handler.AssignRoleResponse、internal_handler.BatchTenantStatusEnvelope、internal_handler.BatchTenantStatusRequest、internal_handler.BatchTenantStatusResponse、internal_handler.ClearanceEnvelope、internal_handler.CreateRoleRequest、internal_handler.CreateTenantRequest、internal_handler.CreateUserRequest、internal_handler.IDEnvelope、internal_handler.IDResponse、internal_handler.LoginEnvelope、internal_handler.LoginRequest、internal_handler.LogoutEnvelope、internal_handler.LogoutResponse、internal_handler.MeEnvelope、internal_handler.MeResponse、internal_handler.OIDCStatusEnvelope、internal_handler.OIDCStatusResponse、internal_handler.PermissionCatalogEnvelope、internal_handler.PermissionGrant、internal_handler.PermissionListEnvelope、internal_handler.RefreshEnvelope、internal_handler.RefreshRequest、internal_handler.RoleEnvelope、internal_handler.RoleListEnvelope、internal_handler.RolePermissionsEnvelope、internal_handler.RolePermissionsResponse、internal_handler.SetClearanceRequest、internal_handler.SetRolePermissionsRequest、internal_handler.TenantEnvelope、internal_handler.TenantPage、internal_handler.TenantPageEnvelope、internal_handler.UpdateRoleRequest、internal_handler.UpdateTenantRequest、internal_handler.UpdateUserRequest、internal_handler.UserClearanceEnvelope、internal_handler.UserClearanceResponse、internal_handler.UserEnvelope、internal_handler.UserPage、internal_handler.UserPageEnvelope、internal_handler.UserStatusEnvelope、internal_handler.UserStatusRequest、internal_handler.UserStatusResponse） | 目前只有统一信封，业务对象没有 schema |
| 鉴权 scheme | BearerAuth | 所有受保护端点都是 Bearer JWT |

## 约定

- Base URL：任一部署实例，例如测试环境 `http://192.168.4.151:19193`；所有业务路径都以 `/api/v1` 开头。
- 鉴权：`Authorization: Bearer <JWT>`。JWT 由 `POST /api/v1/auth/login` 换取（body：`{"username": "…", "password": "…"}`，响应 `data.token`）。
- 响应信封：`{"code": 0, "message": "ok", "data": {…}, "trace_id": "…"}`；`code != 0` 时 `message` 可读，`trace_id` 用于对日志。
- 权限：每个端点在服务端还有一层 RBAC `(action, resource)` 判断，授予关系见 `doc/user/zh-CN/permission-matrix.md`；描述符里目前只写 BearerAuth，没写所需权限。

## 目录

- **root**（1）
- **agents**（21）
- **alerts**（5）
- **answer-snapshots**（7）
- **approval-delegations**（3）
- **approval-policies**（3）
- **approvals**（10）
- **assistant-releases**（18）
- **assistants**（4）
- **audit**（5）
- **auth**（7）
- **branding**（4）
- **chat**（8）
- **chats**（17）
- **connectors**（15）
- **conversation-context**（4）
- **conversation**（11）
- **dashboard**（1）
- **datasets**（45）
- **db-connections**（7）
- **documents**（1）
- **enterprise-connections**（17）
- **eval-cases**（3）
- **eval-sets**（6）
- **evaluation-case-versions**（1）
- **evaluation-protocol-registrations**（3）
- **evaluation-runs**（5）
- **evaluation-set-versions**（1）
- **evidence-bundles**（2）
- **evidence-snapshots**（2）
- **execution-snapshots**（2）
- **governance**（10）
- **health**（1）
- **healthz**（1）
- **im**（4）
- **keys**（4）
- **knowledge-duplicate-candidates**（3）
- **knowledge-impact-reports**（2）
- **knowledge-lifecycle**（3）
- **knowledge-ops**（16）
- **knowledge-profiles**（5）
- **knowledge-strategies**（9）
- **knowledge-tasks**（5）
- **logical-documents**（10）
- **memories**（15）
- **memory-operations**（3）
- **memory-policies**（9）
- **memory-privacy**（6）
- **model-providers**（18）
- **model-routes**（6）
- **onboarding**（1）
- **outbox-events**（3）
- **parser-policies**（6）
- **projects**（12）
- **prompt-policies**（5）
- **quality-issues**（4）
- **quality-profiles**（6）
- **query-templates**（6）
- **readyz**（1）
- **release-candidates**（3）
- **release-gates**（3）
- **releases**（4）
- **report-jobs**（3）
- **report-metrics**（1）
- **report-retention**（2）
- **report-templates**（6）
- **reports**（26）
- **roles**（7）
- **scenario-template-assets**（15）
- **scenario-template-baseline**（2）
- **scenario-template-catalog**（1）
- **scenario-template-skeletons**（2）
- **search-apps**（10）
- **self**（2）
- **source-routing-rules**（6）
- **static**（1）
- **system**（39）
- **tasks**（4）
- **teams**（10）
- **template-instances**（11）
- **tenants**（6）
- **tool-registry**（7）
- **tool-routing**（8）
- **trial-runs**（4）
- **usage**（8）
- **users**（8）
- **workbench**（1）
- **v1/agents**（1）
- **v1/chat**（1）
- **v1/search-apps**（1）

## root（1 个操作）

### GET `/`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute1`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## agents（21 个操作）

### GET `/api/v1/agents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute2`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute291`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/authoring-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute3`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/validate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute292`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute4`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/agents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute537`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/agents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute605`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/{id}/chat/completions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute293`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/{id}/chat/completions/stream`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute294`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/detail`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute11`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/{id}/publish`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute295`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/sessions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute8`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/{id}/sessions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute297`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/sessions/{sessionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute9`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/agents/{id}/sessions/{sessionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute606`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/sessions/{sessionId}/messages`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute10`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/{id}/upload`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute298`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/validation`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute7`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute5`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/agents/{id}/versions/{versionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute6`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `versionId` | path | string | 是 | versionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/agents/{id}/versions/{versionId}/rollback`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute296`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `versionId` | path | string | 是 | versionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## alerts（5 个操作）

### GET `/api/v1/alerts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute39`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/alerts/deliveries`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute40`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/alerts/{id}/claim`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute305`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/alerts/{id}/deliveries/{channel}/retry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute303`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `channel` | path | string | 是 | channel |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/alerts/{id}/read`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute304`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## answer-snapshots（7 个操作）

### GET `/api/v1/answer-snapshots/artifacts/{artifactId}/download`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute31`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `artifactId` | path | string | 是 | artifactId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/answer-snapshots/by-request/{requestId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute29`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `requestId` | path | string | 是 | requestId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/answer-snapshots/export-jobs/{jobId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute30`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `jobId` | path | string | 是 | jobId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/answer-snapshots/export-jobs/{jobId}/retry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute306`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `jobId` | path | string | 是 | jobId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/answer-snapshots/{snapshotId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute32`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `snapshotId` | path | string | 是 | snapshotId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/answer-snapshots/{snapshotId}/audit-export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute33`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `snapshotId` | path | string | 是 | snapshotId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/answer-snapshots/{snapshotId}/export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute307`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `snapshotId` | path | string | 是 | snapshotId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## approval-delegations（3 个操作）

### GET `/api/v1/approval-delegations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute37`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approval-delegations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute289`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/approval-delegations/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute607`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## approval-policies（3 个操作）

### GET `/api/v1/approval-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute38`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approval-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute290`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/approval-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute538`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## approvals（10 个操作）

### GET `/api/v1/approvals`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute34`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute282`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals/batch-approve`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute283`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals/batch-reject`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute284`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/approvals/export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute35`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/approvals/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute36`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals/{id}/approve`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute287`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals/{id}/cancel`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute288`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals/{id}/reject`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute285`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/approvals/{id}/retry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute286`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## assistant-releases（18 个操作）

### GET `/api/v1/assistant-releases`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute12`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute271`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistant-releases/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute13`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/activate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute273`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/apply`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute272`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistant-releases/{id}/authorization-snapshots`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute18`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/authorization-snapshots`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute274`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/compensate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute280`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistant-releases/{id}/gate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute15`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistant-releases/{id}/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute17`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/assistant-releases/{id}/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute535`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistant-releases/{id}/manifest`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute14`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistant-releases/{id}/operations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute16`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/operations/{operationId}/recover`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute279`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `operationId` | path | string | 是 | operationId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/promote-canary`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute278`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/reconcile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute276`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/rollback/{targetId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute275`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `targetId` | path | string | 是 | targetId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistant-releases/{id}/transition`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute277`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## assistants（4 个操作）

### GET `/api/v1/assistants`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute19`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/assistants/{id}/rollout-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute20`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/assistants/{id}/rollout-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute281`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/assistants/{id}/rollout-policies/{policyId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute536`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## audit（5 个操作）

### GET `/api/v1/audit`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute25`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/audit/anchors`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute27`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/audit/export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute26`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/audit/repair`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute302`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/audit/verify`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute28`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## auth（7 个操作）

### POST `/api/v1/auth/login`

- 说明：用用户名密码登录并开启会话
- 鉴权：公开（无 security 声明）
- operationId：`authLogin`
- 描述：校验凭据、按来源 IP 限制失败次数，成功后签发访问令牌与刷新令牌并写入 HttpOnly Cookie，同时记录一条登录审计。连续失败返回 429 而不是 401，客户端应提示稍后重试。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.LoginRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.LoginEnvelope` |
| 400 | 请求体不合法（缺少 username/password） | application/json → `docs.Envelope` |
| 401 | 凭据错误或账号已停用 | application/json → `docs.Envelope` |
| 429 | 该来源的失败次数超限，请稍后重试 | application/json → `docs.Envelope` |

### POST `/api/v1/auth/logout`

- 说明：注销当前会话
- 鉴权：公开（无 security 声明）
- operationId：`authLogout`
- 描述：吊销请求体（或 Cookie）里的刷新令牌并清空 rgx_access/rgx_refresh，因此拿旧刷新令牌无法再续期。幂等：令牌无效或缺失同样返回成功，客户端不需要区分「本来就没登录」。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.RefreshRequest` | 否 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.LogoutEnvelope` |
| 400 | 请求体不是合法 JSON | application/json → `docs.Envelope` |

### GET `/api/v1/auth/me`

- 说明：读取当前账号的资料与权限
- 鉴权：BearerAuth
- operationId：`getCurrentUser`
- 描述：返回登录账号的 id/用户名/邮箱、所属工作区（含用于展示的名称）、主角色、已解析的权限规则、是否平台管理员，以及该部署是否开启审批。前端据此渲染侧边栏与按钮，无需再逐条试探接口。

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.MeEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### GET `/api/v1/auth/oidc/callback`

- 说明：OIDC 回调：校验 state 并建立会话
- 鉴权：公开（无 security 声明）
- operationId：`oidcCallback`
- 描述：IdP 在用户认证后回跳本接口。校验 state/nonce 与 ID Token 的签名、issuer、audience、有效期，把通过校验的企业身份映射到本地账号，写入 rgx_access/rgx_refresh Cookie 后 302 回前端；校验失败时清空流程 Cookie 并 302 回登录页（不返回错误码），调用方应把「回跳后仍未登录」当作失败信号。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `state` | query | string | 是 | IdP 回传的 state，必须与 rgx_oidc_state 一致 |
| `code` | query | string | 是 | IdP 授权码 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 302 | 浏览器跟随 Location 进入工作台或登录页 | text/html → string |

### GET `/api/v1/auth/oidc/start`

- 说明：跳转到企业 IdP 发起 OIDC 登录
- 鉴权：公开（无 security 声明）
- operationId：`oidcStart`
- 描述：生成 state/nonce 并写入两个短时效 Cookie（rgx_oidc_state / rgx_oidc_nonce，绑定到 /api/v1/auth/oidc），随后以 302 跳转到 IdP 授权地址。跳转目标是本次唯一的响应体，所以契约是 Location 头而不是 JSON；失败（本部署未配置 OIDC、或签名材料缺失）返回 500 的标准错误信封，不会跳转。

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 302 | 浏览器跟随 Location 到 IdP | text/html → string |

### GET `/api/v1/auth/oidc/status`

- 说明：查询企业单点登录是否已配置
- 鉴权：公开（无 security 声明）
- operationId：`oidcStatus`
- 描述：登录页在渲染前调用本接口决定是否显示「企业 SSO」入口；无需认证。返回是否启用、IdP 的 issuer，以及发起登录的相对路径（前端不应硬编码该路径）。

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.OIDCStatusEnvelope` |

### POST `/api/v1/auth/refresh`

- 说明：用刷新令牌换取新的访问令牌
- 鉴权：公开（无 security 声明）
- operationId：`authRefresh`
- 描述：旋转刷新令牌：旧令牌立即失效，返回新的访问令牌与刷新令牌并重写 Cookie。请求体可以省略 refresh_token，此时从 rgx_refresh Cookie 读取——浏览器客户端正是这样静默续期的。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.RefreshRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.RefreshEnvelope` |
| 400 | 既没有 refresh_token 也没有 rgx_refresh Cookie | application/json → `docs.Envelope` |
| 401 | 刷新令牌无效、已旋转或已过期 | application/json → `docs.Envelope` |

## branding（4 个操作）

### GET `/api/v1/branding`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute266`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/branding`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute551`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/branding/logo`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute490`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/branding/public`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute267`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## chat（8 个操作）

### POST `/api/v1/chat/completions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute416`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chat/document/chunks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute84`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chat/document/preview`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute85`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/chat/feedback`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute418`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chat/image`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute87`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chat/reference`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute86`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/chat/upload`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute417`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chat/{id}/references`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute88`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## chats（17 个操作）

### GET `/api/v1/chats`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute76`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/chats`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute419`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/chats`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute589`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/chats/batch-status`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute420`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/model-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute77`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute78`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/chats/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute521`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/chats/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute590`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/{id}/config`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute83`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/{id}/sessions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute79`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/chats/{id}/sessions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute421`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/chats/{id}/sessions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute591`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/{id}/sessions/{sessionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute80`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/chats/{id}/sessions/{sessionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute569`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/chats/{id}/sessions/{sessionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute592`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/{id}/sessions/{sessionId}/messages`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute81`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `sessionId` | path | string | 是 | sessionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/chats/{id}/usage`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute82`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## connectors（15 个操作）

### GET `/api/v1/connectors`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute96`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute404`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/connectors/providers/meta`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute97`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors/test`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute405`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/connectors/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute98`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/connectors/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute519`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/connectors/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute593`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors/{id}/disable`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute407`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors/{id}/enable`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute406`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors/{id}/preview`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute410`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/connectors/{id}/runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute99`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/connectors/{id}/runs/{runID}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute100`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `runID` | path | string | 是 | runID |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/connectors/{id}/schedule`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute520`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors/{id}/sync`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute409`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/connectors/{id}/test`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute408`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## conversation-context（4 个操作）

### GET `/api/v1/conversation-context/decisions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute93`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/conversation-context/decisions/by-request/{requestId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute94`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `requestId` | path | string | 是 | requestId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/conversation-context/decisions/{decisionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute95`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `decisionId` | path | string | 是 | decisionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/conversation-context/preview`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute415`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## conversation（11 个操作）

### GET `/api/v1/conversation/assistants`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute92`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/conversation/assistants/{kind}/{targetId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute516`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `kind` | path | string | 是 | kind |
| `targetId` | path | string | 是 | targetId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/conversation/assistants/{kind}/{targetId}/governance`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute517`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `kind` | path | string | 是 | kind |
| `targetId` | path | string | 是 | targetId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/conversation/route`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute411`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/conversation/route-selections/{routeSelectionId}/bootstrap`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute413`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `routeSelectionId` | path | string | 是 | routeSelectionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/conversation/route/{routeId}/select`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute412`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `routeId` | path | string | 是 | routeId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/conversation/routing/evaluations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute89`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/conversation/routing/evaluations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute414`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/conversation/routing/evaluations/{runId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute90`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/conversation/routing/policy`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute91`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/conversation/routing/policy`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute518`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## dashboard（1 个操作）

### GET `/api/v1/dashboard`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute191`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## datasets（45 个操作）

### GET `/api/v1/datasets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute173`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute422`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/datasets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute578`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute175`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/model-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute174`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute176`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute495`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/datasets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute579`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/artifacts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute188`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/compilation/reconcile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute430`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/compilation/runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute184`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/compilation/runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute428`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/compilation/runs/{runId}/cancel`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute429`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/config`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute185`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}/config`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute500`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute177`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/documents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute423`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/datasets/{id}/documents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute580`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/documents/status`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute425`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/documents/stop`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute424`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents/{docId}/chunks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute183`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/documents/{docId}/chunks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute427`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/datasets/{id}/documents/{docId}/chunks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute562`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/datasets/{id}/documents/{docId}/chunks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute582`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}/documents/{docId}/chunks/{chunkId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute498`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |
| `chunkId` | path | string | 是 | chunkId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents/{docId}/governance`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute182`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}/documents/{docId}/governance`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute497`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/datasets/{id}/documents/{docId}/governance`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute581`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/documents/{docId}/governance/{decision}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute426`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |
| `decision` | path | string | 是 | decision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}/documents/{docId}/metadata`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute496`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents/{docId}/parse-attempts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute180`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents/{docId}/parse-quality-report`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute178`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents/{docId}/parse-quality-reports`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute179`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/documents/{docId}/preview`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute181`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `docId` | path | string | 是 | docId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/governance/auto-project`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute435`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/graph`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute189`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/incremental`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute190`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/incremental/scan`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute434`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}/lifecycle`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute501`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/parse`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute431`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/parse-profile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute187`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/parse-profile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute432`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/datasets/{id}/parse-quality-reports`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute186`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/datasets/{id}/project`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute499`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/datasets/{id}/retrieval-test`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute433`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## db-connections（7 个操作）

### GET `/api/v1/db-connections`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute192`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/db-connections`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute436`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/db-connections/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute193`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/db-connections/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute502`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/db-connections/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute561`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/db-connections/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute583`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/db-connections/{id}/test`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute437`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## documents（1 个操作）

### GET `/api/v1/documents/governance`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute194`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## enterprise-connections（17 个操作）

### GET `/api/v1/enterprise-connections`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute221`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute396`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute222`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/enterprise-connections/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute541`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}/availability`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute228`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}/bindings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute223`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections/{id}/bindings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute399`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}/bindings/{bindingId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute224`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `bindingId` | path | string | 是 | bindingId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/enterprise-connections/{id}/bindings/{bindingId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute542`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `bindingId` | path | string | 是 | bindingId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections/{id}/bindings/{bindingId}/revoke`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute400`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `bindingId` | path | string | 是 | bindingId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}/bindings/{bindingId}/versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute225`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `bindingId` | path | string | 是 | bindingId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections/{id}/deprecate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute401`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute227`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections/{id}/retire`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute398`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections/{id}/rotate-credential`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute397`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/enterprise-connections/{id}/test`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute402`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/enterprise-connections/{id}/versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute226`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## eval-cases（3 个操作）

### GET `/api/v1/eval-cases/{id}/evidence`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute215`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/eval-cases/{id}/evidence`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute539`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/eval-cases/{id}/revalidate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute394`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## eval-sets（6 个操作）

### GET `/api/v1/eval-sets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute213`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/eval-sets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute392`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/eval-sets/generate-from-templates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute393`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/eval-sets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute214`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/eval-sets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute540`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/eval-sets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute616`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## evaluation-case-versions（1 个操作）

### POST `/api/v1/evaluation-case-versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute391`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## evaluation-protocol-registrations（3 个操作）

### GET `/api/v1/evaluation-protocol-registrations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute217`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evaluation-protocol-registrations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute388`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evaluation-protocol-registrations/{id}/revoke`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute389`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## evaluation-runs（5 个操作）

### GET `/api/v1/evaluation-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute216`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evaluation-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute384`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evaluation-runs/{id}/case-results`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute385`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evaluation-runs/{id}/complete`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute386`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evaluation-runs/{id}/execute`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute387`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## evaluation-set-versions（1 个操作）

### POST `/api/v1/evaluation-set-versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute390`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## evidence-bundles（2 个操作）

### GET `/api/v1/evidence-bundles`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute220`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/evidence-bundles`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute395`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## evidence-snapshots（2 个操作）

### GET `/api/v1/evidence-snapshots`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute218`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/evidence-snapshots/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute219`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## execution-snapshots（2 个操作）

### GET `/api/v1/execution-snapshots`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute229`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/execution-snapshots`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute403`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## governance（10 个操作）

### GET `/api/v1/governance/dictionary`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute259`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/governance/dictionary`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute486`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/governance/dictionary/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute612`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/governance/dictionary/{id}/{decision}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute487`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `decision` | path | string | 是 | decision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/governance/overview`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute260`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/governance/schemas`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute257`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/governance/schemas`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute488`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/governance/schemas/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute258`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/governance/schemas/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute552`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/governance/schemas/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute613`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## health（1 个操作）

### GET `/api/v1/health`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute264`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## healthz（1 个操作）

### GET `/api/v1/healthz`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute265`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## im（4 个操作）

### POST `/api/v1/im/dingtalk/callback`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute482`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/im/feishu/callback`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute481`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/im/wecom/callback`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute268`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/im/wecom/callback`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute480`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## keys（4 个操作）

### GET `/api/v1/keys`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute172`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/keys`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute467`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/keys/{id}/revoke`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute468`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/keys/{id}/rotate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute469`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-duplicate-candidates（3 个操作）

### GET `/api/v1/knowledge-duplicate-candidates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute171`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-duplicate-candidates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute462`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-duplicate-candidates/{id}/decision`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute463`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-impact-reports（2 个操作）

### GET `/api/v1/knowledge-impact-reports`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute170`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-impact-reports`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute466`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-lifecycle（3 个操作）

### GET `/api/v1/knowledge-lifecycle`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute165`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-lifecycle/asset-map`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute166`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-lifecycle/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute167`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-ops（16 个操作）

### GET `/api/v1/knowledge-ops`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute149`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/answer-quality`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute156`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-ops/citation-audit`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute456`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/citation-audit/runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute153`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/citation-audit/runs/{runId}/findings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute154`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/compilation/recovery`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute155`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-ops/compilation/runs/{runId}/recovery-approval`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute455`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-ops/compilation/runs/{runId}/recovery-probe`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute454`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/events`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute157`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/knowledge-ops/events/{id}/review`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute554`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-ops/events/{id}/to-eval-case`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute458`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/metrics-contract`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute158`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/top-queries`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute152`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/trace-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute150`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-ops/trace-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute457`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-ops/trace-runs/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute151`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-profiles（5 个操作）

### GET `/api/v1/knowledge-profiles`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute168`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-profiles`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute464`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-profiles/{profileId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute169`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `profileId` | path | string | 是 | profileId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/knowledge-profiles/{profileId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute549`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `profileId` | path | string | 是 | profileId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/knowledge-profiles/{profileId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute611`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `profileId` | path | string | 是 | profileId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-strategies（9 个操作）

### GET `/api/v1/knowledge-strategies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute159`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-strategies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute459`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-strategies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute160`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/knowledge-strategies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute548`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/knowledge-strategies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute553`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/knowledge-strategies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute610`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-strategies/{id}/probe`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute460`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-strategies/{id}/quality-gate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute161`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-strategies/{id}/retrieve`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute461`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## knowledge-tasks（5 个操作）

### GET `/api/v1/knowledge-tasks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute162`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/knowledge-tasks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute465`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-tasks/summary`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute163`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/knowledge-tasks/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute164`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/knowledge-tasks/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute555`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## logical-documents（10 个操作）

### GET `/api/v1/logical-documents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute253`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/logical-documents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute483`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/logical-documents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute254`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/logical-documents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute550`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/logical-documents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute567`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/logical-documents/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute614`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/logical-documents/{id}/publish-attempts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute256`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/logical-documents/{id}/restore`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute485`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/logical-documents/{id}/supersede`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute484`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/logical-documents/{id}/versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute255`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## memories（15 个操作）

### GET `/api/v1/memories`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute133`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memories`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute443`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memories/model-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute134`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memories/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute135`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/memories/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute506`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/memories/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute572`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memories/{id}/config`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute139`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memories/{id}/governed-search`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute446`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memories/{id}/messages`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute136`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memories/{id}/messages`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute444`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memories/{id}/messages/search`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute137`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/memories/{id}/messages/{messageId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute507`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `messageId` | path | string | 是 | messageId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/memories/{id}/messages/{messageId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute573`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `messageId` | path | string | 是 | messageId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memories/{id}/messages/{messageId}/compliance-delete`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute445`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `messageId` | path | string | 是 | messageId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memories/{id}/messages/{messageId}/content`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute138`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `messageId` | path | string | 是 | messageId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## memory-operations（3 个操作）

### GET `/api/v1/memory-operations`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute131`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memory-operations/{operationId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute132`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `operationId` | path | string | 是 | operationId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memory-operations/{operationId}/tombstone`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute442`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `operationId` | path | string | 是 | operationId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## memory-policies（9 个操作）

### GET `/api/v1/memory-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute125`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memory-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute438`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memory-policies/{policyId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute126`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/memory-policies/{policyId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute505`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/memory-policies/{policyId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute570`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memory-policies/{policyId}/consents`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute128`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memory-policies/{policyId}/opt-in`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute439`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memory-policies/{policyId}/opt-out`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute440`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memory-policies/{policyId}/quality-gate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute127`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `policyId` | path | string | 是 | policyId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## memory-privacy（6 个操作）

### GET `/api/v1/memory-privacy/pii-exemptions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute130`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/memory-privacy/pii-exemptions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute504`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/memory-privacy/pii-exemptions/{exemptionId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute571`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `exemptionId` | path | string | 是 | exemptionId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/memory-privacy/pii-preview`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute441`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/memory-privacy/self-policy-template`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute129`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/memory-privacy/self-policy-template`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute503`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## model-providers（18 个操作）

### GET `/api/v1/model-providers`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute140`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-providers`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute447`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-providers/catalog`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute143`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-providers/defaults`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute141`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/model-providers/defaults`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute563`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-providers/defaults/options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute142`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-providers/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute144`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/model-providers/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute574`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-providers/{id}/instances`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute145`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-providers/{id}/instances`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute448`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/model-providers/{id}/instances`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute575`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/model-providers/{id}/instances/{instanceId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute508`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `instanceId` | path | string | 是 | instanceId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-providers/{id}/instances/{instanceId}/models`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute146`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `instanceId` | path | string | 是 | instanceId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-providers/{id}/instances/{instanceId}/models`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute449`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `instanceId` | path | string | 是 | instanceId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/model-providers/{id}/instances/{instanceId}/models`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute576`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `instanceId` | path | string | 是 | instanceId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/model-providers/{id}/instances/{instanceId}/models/{modelId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute564`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `instanceId` | path | string | 是 | instanceId |
| `modelId` | path | string | 是 | modelId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-providers/{id}/instances/{instanceId}/models/{modelId}/test`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute450`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `instanceId` | path | string | 是 | instanceId |
| `modelId` | path | string | 是 | modelId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-providers/{id}/verify`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute451`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## model-routes（6 个操作）

### GET `/api/v1/model-routes`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute147`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-routes`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute452`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/model-routes/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute509`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/model-routes/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute577`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/model-routes/{id}/enterprise-pin`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute453`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/model-routes/{id}/enterprise-pins`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute148`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## onboarding（1 个操作）

### GET `/api/v1/onboarding/readiness`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute263`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## outbox-events（3 个操作）

### GET `/api/v1/outbox-events`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute261`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/outbox-events/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute262`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/outbox-events/{id}/retry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute489`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## parser-policies（6 个操作）

### GET `/api/v1/parser-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute246`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/parser-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute479`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/parser-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute247`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/parser-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute530`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/parser-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute565`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/parser-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute601`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## projects（12 个操作）

### GET `/api/v1/projects`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute239`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/projects`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute475`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/projects/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute240`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/projects/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute527`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/projects/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute598`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/projects/{id}/datasets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute242`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/projects/{id}/datasets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute528`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/projects/{id}/teams`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute243`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/projects/{id}/teams`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute529`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/projects/{id}/users`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute241`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/projects/{id}/users`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute476`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/projects/{id}/users/{userId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute599`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `userId` | path | string | 是 | userId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## prompt-policies（5 个操作）

### GET `/api/v1/prompt-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute244`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/prompt-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute477`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/prompt-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute245`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/prompt-policies/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute600`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/prompt-policies/{id}/rollback`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute478`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## quality-issues（4 个操作）

### GET `/api/v1/quality-issues`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute250`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/quality-issues`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute470`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/quality-issues/{id}/state`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute471`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/quality-issues/{id}/verify`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute472`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## quality-profiles（6 个操作）

### GET `/api/v1/quality-profiles`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute248`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/quality-profiles`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute473`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/quality-profiles/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute249`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/quality-profiles/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute546`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/quality-profiles/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute559`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/quality-profiles/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute608`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## query-templates（6 个操作）

### GET `/api/v1/query-templates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute251`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/query-templates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute474`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/query-templates/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute252`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/query-templates/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute547`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/query-templates/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute560`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/query-templates/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute609`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## readyz（1 个操作）

### GET `/api/v1/readyz`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute121`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## release-candidates（3 个操作）

### GET `/api/v1/release-candidates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute119`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/release-candidates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute356`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/release-candidates/{id}/ready`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute357`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## release-gates（3 个操作）

### GET `/api/v1/release-gates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute117`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/release-gates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute358`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/release-gates/evidence`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute118`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## releases（4 个操作）

### GET `/api/v1/releases`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute120`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/releases`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute359`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/releases/{id}/complete`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute360`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/releases/{id}/rollback`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute361`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## report-jobs（3 个操作）

### GET `/api/v1/report-jobs/{jobId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute112`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `jobId` | path | string | 是 | jobId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/report-jobs/{jobId}/events`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute113`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `jobId` | path | string | 是 | jobId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/report-jobs/{jobId}/sections/{ord}/retry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute355`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `jobId` | path | string | 是 | jobId |
| `ord` | path | string | 是 | ord |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## report-metrics（1 个操作）

### GET `/api/v1/report-metrics/trend`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute116`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## report-retention（2 个操作）

### GET `/api/v1/report-retention`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute115`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/report-retention`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute513`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## report-templates（6 个操作）

### GET `/api/v1/report-templates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute114`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/report-templates`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute351`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/report-templates/{key}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute512`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | path | string | 是 | key |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/report-templates/{key}/capability-check`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute352`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | path | string | 是 | key |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/report-templates/{key}/clone`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute353`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | path | string | 是 | key |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/report-templates/{key}/retire`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute354`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | path | string | 是 | key |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## reports（26 个操作）

### GET `/api/v1/reports`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute101`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute341`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute102`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/reports/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute602`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/reports/{id}/access`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute511`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/archive`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute347`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/budget-extension`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute350`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/cancel`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute349`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/follow-ups`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute110`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/restore`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute346`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute103`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/revisions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute342`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions/{revision}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute104`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/revisions/{revision}/artifacts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute343`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions/{revision}/artifacts/{artifactId}/download`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute107`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |
| `artifactId` | path | string | 是 | artifactId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions/{revision}/assurance-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute105`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/revisions/{revision}/assurance-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute344`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions/{revision}/assurance-runs/{runId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute106`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/reports/{id}/revisions/{revision}/findings/{findingId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute568`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |
| `findingId` | path | string | 是 | findingId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/revisions/{revision}/outline/confirm`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute345`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions/{revision}/review`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute109`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/revisions/{revision}/sections`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute108`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `revision` | path | string | 是 | revision |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/reports/{id}/schedule`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute111`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/reports/{id}/schedule`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute510`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/reports/{id}/schedule`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute603`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/reports/{id}/unarchive`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute348`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## roles（7 个操作）

### GET `/api/v1/roles`

- 说明：列出租户可分配的角色
- 鉴权：BearerAuth
- operationId：`listRoles`
- 描述：返回当前调用者可见的角色（含内置角色，builtin=true）。平台级角色只有平台管理员能看到；按 name/scope 过滤。角色列表是「新建用户」与「变更角色」两个页面的数据源。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `name` | query | string | 否 | 按角色名模糊匹配 |
| `scope` | query | string | 否 | 按作用域过滤：platform / tenant / project |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.RoleListEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### POST `/api/v1/roles`

- 说明：新建角色
- 鉴权：BearerAuth
- operationId：`createRole`
- 描述：作用域可为 platform/tenant/project；parent_id 用于角色继承（子角色在授权解析时叠加父角色规则）。新角色默认没有任何权限——必须再调用 PUT /roles/{id}/permissions 授权，否则用它登录后会处处 403。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.CreateRoleRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.RoleEnvelope` |
| 400 | 请求体不合法或作用域非法 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 同作用域下角色名重复 | application/json → `docs.Envelope` |

### GET `/api/v1/roles/permission-catalog`

- 说明：读取可授予的权限目录
- 鉴权：BearerAuth
- operationId：`getPermissionCatalog`
- 描述：返回全部可用的 (resource, actions) 组合，驱动角色权限编辑页的勾选项。目录由服务端内置矩阵生成：前端不应硬编码资源名，否则新增资源时界面会静默漏掉一整个权限面。

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.PermissionCatalogEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### PUT `/api/v1/roles/{id}`

- 说明：修改角色的名称与描述
- 鉴权：BearerAuth
- operationId：`updateRole`
- 描述：只改元数据，不改权限规则（权限走 PUT /roles/{id}/permissions）。内置角色不允许改名或改作用域，会被拒绝。字段留空表示不修改。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 角色 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.UpdateRoleRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.RoleEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 内置角色不可修改，或名称冲突 | application/json → `docs.Envelope` |

### DELETE `/api/v1/roles/{id}`

- 说明：删除角色
- 鉴权：BearerAuth
- operationId：`deleteRole`
- 描述：拒绝删除的三种情形都是 400（不是 409），各带一个码：40082 内置角色、40083 仍被用户占用、40084 仍被其他角色当作父角色。占用必须先释放——把用户改到别的角色，或删除该用户（删除用户会在同一事务里清掉角色关系）；否则这个角色会被 40083 永久卡住。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 角色 ID |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.IDEnvelope` |
| 400 | 40082 内置角色不可删除；40083 仍被用户占用；40084 仍被其他角色引用为父角色 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### GET `/api/v1/roles/{id}/permissions`

- 说明：读取角色的权限规则
- 鉴权：BearerAuth
- operationId：`getRolePermissions`
- 描述：返回该角色的全部 allow/deny 规则。这是「这个角色到底能做什么」的唯一机器可读答案，权限审计与客户侧核对都以此为准。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 角色 ID |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.PermissionListEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 404 | 角色不存在或不可见 | application/json → `docs.Envelope` |

### PUT `/api/v1/roles/{id}/permissions`

- 说明：整体替换角色的权限规则
- 鉴权：BearerAuth
- operationId：`setRolePermissions`
- 描述：语义是「替换」而不是「追加」：请求体里没有的规则会被删除，因此前端必须先读再改再写。deny 规则优先于 allow，effect 只接受 allow/deny。对内置角色写入会被拒绝——内置矩阵只能由版本升级变更。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 角色 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.SetRolePermissionsRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.RolePermissionsEnvelope` |
| 400 | 规则不合法（未知资源或 effect 取值非法） | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 内置角色的规则不可修改 | application/json → `docs.Envelope` |

## scenario-template-assets（15 个操作）

### GET `/api/v1/scenario-template-assets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute60`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute325`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/import`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute326`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/scenario-template-assets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute61`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/scenario-template-assets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute545`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/scenario-template-assets/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute597`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/capability-check`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute330`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/copy`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute329`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/eval-set`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute332`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/scenario-template-assets/{id}/export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute63`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/fork`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute331`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/scenario-template-assets/{id}/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute62`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/instantiate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute327`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/instantiate-chat`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute328`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-assets/{id}/release-candidate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute333`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## scenario-template-baseline（2 个操作）

### GET `/api/v1/scenario-template-baseline`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute66`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-baseline/{key}/publish`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute335`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | path | string | 是 | key |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## scenario-template-catalog（1 个操作）

### GET `/api/v1/scenario-template-catalog`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute64`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## scenario-template-skeletons（2 个操作）

### GET `/api/v1/scenario-template-skeletons`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute65`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/scenario-template-skeletons/{id}/import`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute334`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## search-apps（10 个操作）

### GET `/api/v1/search-apps`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute67`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/search-apps`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute336`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/search-apps/dataset-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute68`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/search-apps/model-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute69`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/search-apps/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute70`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/search-apps/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute544`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/search-apps/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute596`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/search-apps/{id}/completion`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute337`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/search-apps/{id}/completion/stream`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute338`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/search-apps/{id}/config`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute71`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## self（2 个操作）

### GET `/api/v1/self/memory-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute72`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/self/memory-policies`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute339`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## source-routing-rules（6 个操作）

### GET `/api/v1/source-routing-rules`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute73`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/source-routing-rules`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute340`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/source-routing-rules/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute74`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/source-routing-rules/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute543`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/source-routing-rules/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute558`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/source-routing-rules/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute594`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## static（1 个操作）

### GET `/api/v1/static/logos/{filepath}`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute75`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `filepath` | path | string | 是 | filepath |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## system（39 个操作）

### GET `/api/v1/system/capabilities`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute58`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/capabilities/verify`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute323`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/config`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute56`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/config/apply`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute322`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/config/preflight`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute321`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/config/public-key`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute57`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute59`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/artifacts/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute50`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/mappings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute49`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/mappings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute313`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/system/ragflow/sync/mappings/{externalTenantId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute595`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `externalTenantId` | path | string | 是 | externalTenantId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/reconcile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute310`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/relink/resource-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute42`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/relink/tenant-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute41`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/resources`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute43`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/resources/{type}/{externalTenantId}/{id}/conflicts/resolve`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute309`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `type` | path | string | 是 | type |
| `externalTenantId` | path | string | 是 | externalTenantId |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/resources/{type}/{externalTenantId}/{id}/relink`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute308`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `type` | path | string | 是 | type |
| `externalTenantId` | path | string | 是 | externalTenantId |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/resources/{type}/{externalTenantId}/{id}/versions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute44`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `type` | path | string | 是 | type |
| `externalTenantId` | path | string | 是 | externalTenantId |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute45`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/runs/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute46`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/runs/{id}/assign`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute312`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/runs/{id}/import`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute311`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/runs/{id}/items`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute47`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/ragflow/sync/scan`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute314`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/ragflow/sync/settings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute48`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/system/ragflow/sync/settings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute556`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/settings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute51`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/system/settings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute557`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/settings/revisions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute52`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/settings/revisions/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute53`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/settings/revisions/{id}/rollback`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute319`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/settings/runtime/report`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute318`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/settings/secrets/{key}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute320`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | path | string | 是 | key |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/setup/admin`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute315`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/setup/apply`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute316`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/setup/preflight`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute317`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/setup/public-key`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute55`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/system/setup/status`

- 说明：（未注解，见文首说明）
- 鉴权：公开（无 security 声明）
- operationId：`OpenAPIRoute54`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/system/usage/reconcile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute324`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## tasks（4 个操作）

### GET `/api/v1/tasks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute209`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/tasks`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute588`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/tasks/jobs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute210`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tasks/sync`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute383`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## teams（10 个操作）

### GET `/api/v1/teams`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute199`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/teams`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute370`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/teams/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute200`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/teams/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute524`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/teams/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute584`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/teams/{id}/projects`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute202`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/teams/{id}/projects`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute525`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/teams/{id}/users`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute201`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/teams/{id}/users`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute371`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/teams/{id}/users/{userId}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute585`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `userId` | path | string | 是 | userId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## template-instances（11 个操作）

### GET `/api/v1/template-instances`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute195`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute363`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/template-instances/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute196`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances/{id}/dry-run`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute368`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances/{id}/evaluate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute369`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/template-instances/{id}/health`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute197`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances/{id}/release`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute364`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/template-instances/{id}/releases`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute198`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances/{id}/releases/{releaseId}/activate`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute365`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `releaseId` | path | string | 是 | releaseId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances/{id}/releases/{releaseId}/reconcile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute366`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |
| `releaseId` | path | string | 是 | releaseId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/template-instances/{id}/rollback`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute367`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## tenants（6 个操作）

### GET `/api/v1/tenants`

- 说明：分页查询工作区（租户）
- 鉴权：BearerAuth
- operationId：`listTenants`
- 描述：平台管理员可看到全部工作区并按 name/status/创建时间过滤；租户管理员只看得到自己那一个——服务端按调用者的治理范围裁剪，不依赖前端隐藏。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `page` | query | integer | 否 | 页码，从 1 开始（默认 1） |
| `page_size` | query | integer | 否 | 每页条数（默认 20） |
| `name` | query | string | 否 | 按工作区名模糊匹配 |
| `status` | query | string | 否 | 按状态过滤：active / disabled |
| `created_from` | query | string | 否 | 创建时间下界（RFC3339） |
| `created_to` | query | string | 否 | 创建时间上界（RFC3339） |
| `scope` | query | string | 否 | 治理范围：all / current / specific（由权限决定是否允许） |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.TenantPageEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### POST `/api/v1/tenants`

- 说明：新建工作区（租户）
- 鉴权：BearerAuth
- operationId：`createTenant`
- 描述：需要 tenant 的写权限；创建后可顺带设置状态与品牌（名称/LoGo）。品牌用于该工作区所有页面与导出文件的落款，因此 logo 必须是可长期访问的 URL。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.CreateTenantRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.TenantEnvelope` |
| 400 | 请求体不合法 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### GET `/api/v1/tenants/export`

- 说明：导出工作区清单为 CSV
- 鉴权：BearerAuth
- operationId：`exportTenants`
- 描述：按调用者可见范围导出工作区清单（UTF-8 CSV，带表头），用于离线盘点与合规归档。响应是文件流而不是 JSON 信封，客户端应按 Content-Disposition 保存；导出动作本身写审计。缺权限时按标准错误信封返回 401/403（内容类型为 application/json，不是 CSV）。

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | CSV 文件内容（Content-Disposition: attachment; filename=\&quot;tenants.csv\&quot;） | text/csv → string |

### PUT `/api/v1/tenants/status`

- 说明：批量启用或停用工作区
- 鉴权：BearerAuth
- operationId：`batchUpdateTenantStatus`
- 描述：一次调用切换多个工作区的状态，ids 为空会被拒绝（避免「什么都没做但返回成功」）。任一工作区不存在或无权操作时整体回滚，不做部分成功。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.BatchTenantStatusRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.BatchTenantStatusEnvelope` |
| 400 | ids 为空或状态取值非法 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### PUT `/api/v1/tenants/{id}`

- 说明：修改工作区设置
- 鉴权：BearerAuth
- operationId：`updateTenant`
- 描述：可改名称、状态、品牌与编译令牌预算。字段未提供表示保持原值，显式传空字符串表示清空该字段——这是「不动」和「清空」能被区分开的唯一方式。停用工作区会立刻阻止其成员登录。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 工作区 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.UpdateTenantRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.TenantEnvelope` |
| 400 | 请求体不合法或状态取值非法 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### DELETE `/api/v1/tenants/{id}`

- 说明：删除工作区
- 鉴权：BearerAuth
- operationId：`deleteTenant`
- 描述：默认拒绝删除仍有子资源的工作区；force=true 会连同子资源一起删除（不可恢复，需平台管理员）。删除写审计，工作区 ID 会进入墓碑记录，避免同名重建后历史审计指向错误的对象。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 工作区 ID |
| `force` | query | boolean | 否 | true 表示连同子资源强制删除 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.IDEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 工作区仍有子资源且未指定 force | application/json → `docs.Envelope` |

## tool-registry（7 个操作）

### GET `/api/v1/tool-registry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute205`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-registry`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute379`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/tool-registry/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute206`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PUT `/api/v1/tool-registry/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute526`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### PATCH `/api/v1/tool-registry/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute566`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### DELETE `/api/v1/tool-registry/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute587`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-registry/{id}/execute`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute380`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## tool-routing（8 个操作）

### POST `/api/v1/tool-routing/answer-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute376`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/tool-routing/answer-runs/{answerRunId}/evidence-facts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute207`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `answerRunId` | path | string | 是 | answerRunId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-routing/answer-runs/{answerRunId}/evidence-facts`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute377`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `answerRunId` | path | string | 是 | answerRunId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/tool-routing/answer-runs/{answerRunId}/fact-guard`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute208`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `answerRunId` | path | string | 是 | answerRunId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-routing/planned-mixed-tool-answers`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute378`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-routing/routed-mixed-tool-answers`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute375`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-routing/routed-sql-answer`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute373`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/tool-routing/routed-sql-answers`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute374`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## trial-runs（4 个操作）

### GET `/api/v1/trial-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute211`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/trial-runs`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute381`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/trial-runs/{id}`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute212`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### POST `/api/v1/trial-runs/{id}/cancel`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute382`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## usage（8 个操作）

### GET `/api/v1/usage`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute230`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/attribution`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute235`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/details`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute234`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/export`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute236`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/filter-options`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute233`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/metering`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute617`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/reconcile`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute231`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

### GET `/api/v1/usage/reconcile/{runId}/findings`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute232`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `runId` | path | string | 是 | runId |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## users（8 个操作）

### GET `/api/v1/users`

- 说明：分页查询本工作区的用户
- 鉴权：BearerAuth
- operationId：`listUsers`
- 描述：按 username/status/role/创建时间过滤，默认只返回当前工作区的用户。scope=all 只对持有 tenant 的 governance.read 的调用者生效，用于平台管理员跨工作区审计；其余调用者即使传 scope=all 也只会看到本工作区数据。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `page` | query | integer | 否 | 页码，从 1 开始（默认 1） |
| `page_size` | query | integer | 否 | 每页条数（默认 20） |
| `tenant_id` | query | string | 否 | 按工作区过滤（需 governance.read） |
| `username` | query | string | 否 | 按用户名模糊匹配 |
| `status` | query | string | 否 | 按状态过滤：active / disabled |
| `role` | query | string | 否 | 按角色过滤 |
| `created_from` | query | string | 否 | 创建时间下界（RFC3339） |
| `created_to` | query | string | 否 | 创建时间上界（RFC3339） |
| `scope` | query | string | 否 | all 表示跨工作区（需要平台级 governance.read） |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.UserPageEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### POST `/api/v1/users`

- 说明：在工作区内新建用户
- 鉴权：BearerAuth
- operationId：`createUser`
- 描述：需要 user 的 manage 权限。tenant_id 只有平台管理员（tenant 的 governance.manage）可以指定，普通管理员只能在本工作区建号。密码由服务端哈希存储，返回体不含任何密码字段。

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.CreateUserRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.UserEnvelope` |
| 400 | 请求体不合法，或非平台管理员指定了 tenant_id | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 用户名已存在 | application/json → `docs.Envelope` |

### PUT `/api/v1/users/{id}`

- 说明：修改用户资料（可选重置密码）
- 鉴权：BearerAuth
- operationId：`updateUser`
- 描述：需要 user 的 manage 权限；字段留空表示不修改。修改自己的角色会被拒绝（40906）——避免管理员给自己提权，改角色请走 PUT /users/{id}/role 并留下独立审计。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 用户 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.UpdateUserRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.IDEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 40906：不能修改自己的角色 | application/json → `docs.Envelope` |

### DELETE `/api/v1/users/{id}`

- 说明：删除用户
- 鉴权：BearerAuth
- operationId：`deleteUser`
- 描述：需要 user 的 manage 权限。三种 409：40908 删除自己、40903 目标是平台管理员（平台永不失去它的管理员）、40905 目标是本工作区最后一个仍然启用的管理员。删除会在同一事务里清掉该用户的角色关系，因此之后可以正常删除其角色——否则角色会被 40083「仍被用户占用」永久卡住。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 用户 ID |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.IDEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 40908 删除自己；40903 目标是平台管理员；40905 目标是本工作区最后一个启用中的管理员 | application/json → `docs.Envelope` |

### GET `/api/v1/users/{id}/clearance`

- 说明：读取用户的文档密级
- 鉴权：BearerAuth
- operationId：`getUserClearance`
- 描述：返回该用户当前的文档访问密级；id 传 me 时读取调用者自己。recorded=false 表示从未显式设置过，返回的是默认密级（internal）——审计时这两种情况必须能区分。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 用户 ID，或 me 表示当前账号 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.UserClearanceEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### PUT `/api/v1/users/{id}/clearance`

- 说明：设置用户的文档密级
- 鉴权：BearerAuth
- operationId：`setUserClearance`
- 描述：仅平台管理员或租户管理员可调用（服务层会二次校验，不依赖中间件）。密级决定该用户能检索到哪些文档，属于治理动作，每次变更都会写入审计。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 用户 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.SetClearanceRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.ClearanceEnvelope` |
| 400 | 请求体不合法或密级取值非法 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |

### PUT `/api/v1/users/{id}/role`

- 说明：变更用户的主角色
- 鉴权：BearerAuth
- operationId：`setUserRole`
- 描述：需要 user 的 manage 权限，且不能修改自己的角色（40906）。角色必须存在且可被当前调用者授予，否则返回 409/400。变更会重建该用户的 rgx_user_role 关系并写独立审计。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 用户 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.AssignRoleRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.AssignRoleEnvelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 40906 修改自己的角色；或角色不可授予 | application/json → `docs.Envelope` |

### PUT `/api/v1/users/{id}/status`

- 说明：启用或停用用户
- 鉴权：BearerAuth
- operationId：`setUserStatus`
- 描述：停用后该用户的现有令牌在下次校验时失效，且无法再登录。不能停用自己（40907）。状态变更写审计，运维排查「为什么某人登不上」时以本接口的记录为准。

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | 用户 ID |

| 请求体类型 | Schema | 必填 |
| --- | --- | --- |
| application/json | `internal_handler.UserStatusRequest` | 是 |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `internal_handler.UserStatusEnvelope` |
| 400 | 请求体不合法或状态取值非法 | application/json → `docs.Envelope` |
| 401 | Unauthorized | application/json → `docs.Envelope` |
| 403 | Forbidden | application/json → `docs.Envelope` |
| 409 | 40907：不能修改自己的状态 | application/json → `docs.Envelope` |

## workbench（1 个操作）

### GET `/api/v1/workbench/recent-sessions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute269`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## v1/agents（1 个操作）

### POST `/v1/agents/{id}/completions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute494`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## v1/chat（1 个操作）

### POST `/v1/chat/completions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute492`

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

## v1/search-apps（1 个操作）

### POST `/v1/search-apps/{id}/completions`

- 说明：（未注解，见文首说明）
- 鉴权：BearerAuth
- operationId：`OpenAPIRoute493`

| 参数 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | path | string | 是 | id |

| 状态码 | 说明 | 响应 schema |
| --- | --- | --- |
| 200 | OK | application/json → `docs.Envelope` |

---

生成器：`scripts/release-gate/api_doc_gen.py`；门禁：`s28.api-doc`（`--selftest` 证明它会红）。

