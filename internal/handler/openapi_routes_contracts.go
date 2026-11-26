package handler

// Hand-written OpenAPI contracts for the auth / users / roles / tenants surface
// (doc/310 §22.4 T310-01, batch 1).
//
// Why these annotations do not live next to the handlers, and why they are not in
// `openapi_routes_gen.go`:
//
//   * swag resolves a qualified type (`model.Role`, `response.Page`) through the
//     **imports of the file the annotation is in**. The generated stub file imports
//     only `docs`, so a contract written there fails generation with "cannot find type
//     definition: model.Role" — the annotation is not wrong, it is just unreadable to
//     the parser.
//   * every `@Param … body` / `@Success … data=` below names the very type the handler
//     binds or emits (`internal/handler/openapi_contracts.go`). A rename breaks the
//     build, not just the document.
//
// The `var` block at the bottom is what makes that true: each type an annotation names
// is referenced here, so deleting or renaming one is a compile error in this file.
//
// Nothing regenerates this file. Adding a route means writing its contract here (or in
// the stub file if it needs no cross-package type) and running `make api`.

import (
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/docs"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// contractOidcStatus documents GET /api/v1/auth/oidc/status.
//
// @Summary 查询企业单点登录是否已配置
// @Description 登录页在渲染前调用本接口决定是否显示「企业 SSO」入口；无需认证。返回是否启用、IdP 的 issuer，以及发起登录的相对路径（前端不应硬编码该路径）。
// @Tags Auth
// @ID oidcStatus
// @Produce json
// @Success 200 {object} handler.OIDCStatusEnvelope
// @Router /api/v1/auth/oidc/status [get]
func contractOidcStatus() {}

// contractOidcStart documents GET /api/v1/auth/oidc/start.
//
// @Summary 跳转到企业 IdP 发起 OIDC 登录
// @Description 生成 state/nonce 并写入两个短时效 Cookie（rgx_oidc_state / rgx_oidc_nonce，绑定到 /api/v1/auth/oidc），随后以 302 跳转到 IdP 授权地址。跳转目标是本次唯一的响应体，所以契约是 Location 头而不是 JSON；失败（本部署未配置 OIDC、或签名材料缺失）返回 500 的标准错误信封，不会跳转。
// @Tags Auth
// @ID oidcStart
// @Produce html
// @Success 302 {string} string "浏览器跟随 Location 到 IdP"
// @Header 302 {string} Location "IdP 授权地址（含 client_id/state/nonce）"
// @Router /api/v1/auth/oidc/start [get]
func contractOidcStart() {}

// contractOidcCallback documents GET /api/v1/auth/oidc/callback.
//
// @Summary OIDC 回调：校验 state 并建立会话
// @Description IdP 在用户认证后回跳本接口。校验 state/nonce 与 ID Token 的签名、issuer、audience、有效期，把通过校验的企业身份映射到本地账号，写入 rgx_access/rgx_refresh Cookie 后 302 回前端；校验失败时清空流程 Cookie 并 302 回登录页（不返回错误码），调用方应把「回跳后仍未登录」当作失败信号。
// @Tags Auth
// @ID oidcCallback
// @Produce html
// @Param state query string true "IdP 回传的 state，必须与 rgx_oidc_state 一致"
// @Param code query string true "IdP 授权码"
// @Success 302 {string} string "浏览器跟随 Location 进入工作台或登录页"
// @Header 302 {string} Location "前端落地页"
// @Router /api/v1/auth/oidc/callback [get]
func contractOidcCallback() {}

// contractGetCurrentUser documents GET /api/v1/auth/me.
//
// @Summary 读取当前账号的资料与权限
// @Description 返回登录账号的 id/用户名/邮箱、所属工作区（含用于展示的名称）、主角色、已解析的权限规则、是否平台管理员，以及该部署是否开启审批。前端据此渲染侧边栏与按钮，无需再逐条试探接口。
// @Tags Auth
// @ID getCurrentUser
// @Produce json
// @Success 200 {object} handler.MeEnvelope
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/auth/me [get]
func contractGetCurrentUser() {}

// contractListRoles documents GET /api/v1/roles.
//
// @Summary 列出租户可分配的角色
// @Description 返回当前调用者可见的角色（含内置角色，builtin=true）。平台级角色只有平台管理员能看到；按 name/scope 过滤。角色列表是「新建用户」与「变更角色」两个页面的数据源。
// @Tags Roles
// @ID listRoles
// @Produce json
// @Param name query string false "按角色名模糊匹配"
// @Param scope query string false "按作用域过滤：platform / tenant / project"
// @Success 200 {object} handler.RoleListEnvelope
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles [get]
func contractListRoles() {}

// contractGetPermissionCatalog documents GET /api/v1/roles/permission-catalog.
//
// @Summary 读取可授予的权限目录
// @Description 返回全部可用的 (resource, actions) 组合，驱动角色权限编辑页的勾选项。目录由服务端内置矩阵生成：前端不应硬编码资源名，否则新增资源时界面会静默漏掉一整个权限面。
// @Tags Roles
// @ID getPermissionCatalog
// @Produce json
// @Success 200 {object} handler.PermissionCatalogEnvelope
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles/permission-catalog [get]
func contractGetPermissionCatalog() {}

// contractGetRolePermissions documents GET /api/v1/roles/{id}/permissions.
//
// @Summary 读取角色的权限规则
// @Description 返回该角色的全部 allow/deny 规则。这是「这个角色到底能做什么」的唯一机器可读答案，权限审计与客户侧核对都以此为准。
// @Tags Roles
// @ID getRolePermissions
// @Produce json
// @Param id path string true "角色 ID"
// @Success 200 {object} handler.PermissionListEnvelope
// @Failure 404 {object} docs.Envelope "角色不存在或不可见"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles/{id}/permissions [get]
func contractGetRolePermissions() {}

// contractListTenants documents GET /api/v1/tenants.
//
// @Summary 分页查询工作区（租户）
// @Description 平台管理员可看到全部工作区并按 name/status/创建时间过滤；租户管理员只看得到自己那一个——服务端按调用者的治理范围裁剪，不依赖前端隐藏。
// @Tags Tenants
// @ID listTenants
// @Produce json
// @Param page query int false "页码，从 1 开始（默认 1）"
// @Param page_size query int false "每页条数（默认 20）"
// @Param name query string false "按工作区名模糊匹配"
// @Param status query string false "按状态过滤：active / disabled"
// @Param created_from query string false "创建时间下界（RFC3339）"
// @Param created_to query string false "创建时间上界（RFC3339）"
// @Param scope query string false "治理范围：all / current / specific（由权限决定是否允许）"
// @Success 200 {object} handler.TenantPageEnvelope
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/tenants [get]
func contractListTenants() {}

// contractExportTenants documents GET /api/v1/tenants/export.
//
// @Summary 导出工作区清单为 CSV
// @Description 按调用者可见范围导出工作区清单（UTF-8 CSV，带表头），用于离线盘点与合规归档。响应是文件流而不是 JSON 信封，客户端应按 Content-Disposition 保存；导出动作本身写审计。缺权限时按标准错误信封返回 401/403（内容类型为 application/json，不是 CSV）。
// @Tags Tenants
// @ID exportTenants
// @Produce text/csv
// @Success 200 {string} string "CSV 文件内容（Content-Disposition: attachment; filename=\"tenants.csv\"）"
// @Security BearerAuth
// @Router /api/v1/tenants/export [get]
func contractExportTenants() {}

// contractListUsers documents GET /api/v1/users.
//
// @Summary 分页查询本工作区的用户
// @Description 按 username/status/role/创建时间过滤，默认只返回当前工作区的用户。scope=all 只对持有 tenant 的 governance.read 的调用者生效，用于平台管理员跨工作区审计；其余调用者即使传 scope=all 也只会看到本工作区数据。
// @Tags Users
// @ID listUsers
// @Produce json
// @Param page query int false "页码，从 1 开始（默认 1）"
// @Param page_size query int false "每页条数（默认 20）"
// @Param tenant_id query string false "按工作区过滤（需 governance.read）"
// @Param username query string false "按用户名模糊匹配"
// @Param status query string false "按状态过滤：active / disabled"
// @Param role query string false "按角色过滤"
// @Param created_from query string false "创建时间下界（RFC3339）"
// @Param created_to query string false "创建时间上界（RFC3339）"
// @Param scope query string false "all 表示跨工作区（需要平台级 governance.read）"
// @Success 200 {object} handler.UserPageEnvelope
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users [get]
func contractListUsers() {}

// contractGetUserClearance documents GET /api/v1/users/{id}/clearance.
//
// @Summary 读取用户的文档密级
// @Description 返回该用户当前的文档访问密级；id 传 me 时读取调用者自己。recorded=false 表示从未显式设置过，返回的是默认密级（internal）——审计时这两种情况必须能区分。
// @Tags Users
// @ID getUserClearance
// @Produce json
// @Param id path string true "用户 ID，或 me 表示当前账号"
// @Success 200 {object} handler.UserClearanceEnvelope
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users/{id}/clearance [get]
func contractGetUserClearance() {}

// contractAuthLogin documents POST /api/v1/auth/login.
//
// @Summary 用用户名密码登录并开启会话
// @Description 校验凭据、按来源 IP 限制失败次数，成功后签发访问令牌与刷新令牌并写入 HttpOnly Cookie，同时记录一条登录审计。连续失败返回 429 而不是 401，客户端应提示稍后重试。
// @Tags Auth
// @ID authLogin
// @Accept json
// @Produce json
// @Param request body handler.LoginRequest true "用户名与密码"
// @Success 200 {object} handler.LoginEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法（缺少 username/password）"
// @Failure 401 {object} docs.Envelope "凭据错误或账号已停用"
// @Failure 429 {object} docs.Envelope "该来源的失败次数超限，请稍后重试"
// @Router /api/v1/auth/login [post]
func contractAuthLogin() {}

// contractAuthLogout documents POST /api/v1/auth/logout.
//
// @Summary 注销当前会话
// @Description 吊销请求体（或 Cookie）里的刷新令牌并清空 rgx_access/rgx_refresh，因此拿旧刷新令牌无法再续期。幂等：令牌无效或缺失同样返回成功，客户端不需要区分「本来就没登录」。
// @Tags Auth
// @ID authLogout
// @Accept json
// @Produce json
// @Param request body handler.RefreshRequest false "刷新令牌（可省略，省略时用 Cookie）"
// @Success 200 {object} handler.LogoutEnvelope
// @Failure 400 {object} docs.Envelope "请求体不是合法 JSON"
// @Router /api/v1/auth/logout [post]
func contractAuthLogout() {}

// contractAuthRefresh documents POST /api/v1/auth/refresh.
//
// @Summary 用刷新令牌换取新的访问令牌
// @Description 旋转刷新令牌：旧令牌立即失效，返回新的访问令牌与刷新令牌并重写 Cookie。请求体可以省略 refresh_token，此时从 rgx_refresh Cookie 读取——浏览器客户端正是这样静默续期的。
// @Tags Auth
// @ID authRefresh
// @Accept json
// @Produce json
// @Param request body handler.RefreshRequest true "刷新令牌（留空则用 Cookie）"
// @Success 200 {object} handler.RefreshEnvelope
// @Failure 400 {object} docs.Envelope "既没有 refresh_token 也没有 rgx_refresh Cookie"
// @Failure 401 {object} docs.Envelope "刷新令牌无效、已旋转或已过期"
// @Router /api/v1/auth/refresh [post]
func contractAuthRefresh() {}

// contractCreateRole documents POST /api/v1/roles.
//
// @Summary 新建角色
// @Description 作用域可为 platform/tenant/project；parent_id 用于角色继承（子角色在授权解析时叠加父角色规则）。新角色默认没有任何权限——必须再调用 PUT /roles/{id}/permissions 授权，否则用它登录后会处处 403。
// @Tags Roles
// @ID createRole
// @Accept json
// @Produce json
// @Param request body handler.CreateRoleRequest true "角色名、作用域与可选父角色"
// @Success 200 {object} handler.RoleEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法或作用域非法"
// @Failure 409 {object} docs.Envelope "同作用域下角色名重复"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles [post]
func contractCreateRole() {}

// contractCreateTenant documents POST /api/v1/tenants.
//
// @Summary 新建工作区（租户）
// @Description 需要 tenant 的写权限；创建后可顺带设置状态与品牌（名称/LoGo）。品牌用于该工作区所有页面与导出文件的落款，因此 logo 必须是可长期访问的 URL。
// @Tags Tenants
// @ID createTenant
// @Accept json
// @Produce json
// @Param request body handler.CreateTenantRequest true "工作区名称与可选品牌配置"
// @Success 200 {object} handler.TenantEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/tenants [post]
func contractCreateTenant() {}

// contractCreateUser documents POST /api/v1/users.
//
// @Summary 在工作区内新建用户
// @Description 需要 user 的 manage 权限。tenant_id 只有平台管理员（tenant 的 governance.manage）可以指定，普通管理员只能在本工作区建号。密码由服务端哈希存储，返回体不含任何密码字段。
// @Tags Users
// @ID createUser
// @Accept json
// @Produce json
// @Param request body handler.CreateUserRequest true "新用户的用户名、密码、邮箱与角色"
// @Success 200 {object} handler.UserEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法，或非平台管理员指定了 tenant_id"
// @Failure 409 {object} docs.Envelope "用户名已存在"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users [post]
func contractCreateUser() {}

// contractUpdateRole documents PUT /api/v1/roles/{id}.
//
// @Summary 修改角色的名称与描述
// @Description 只改元数据，不改权限规则（权限走 PUT /roles/{id}/permissions）。内置角色不允许改名或改作用域，会被拒绝。字段留空表示不修改。
// @Tags Roles
// @ID updateRole
// @Accept json
// @Produce json
// @Param id path string true "角色 ID"
// @Param request body handler.UpdateRoleRequest true "要修改的字段"
// @Success 200 {object} handler.RoleEnvelope
// @Failure 409 {object} docs.Envelope "内置角色不可修改，或名称冲突"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles/{id} [put]
func contractUpdateRole() {}

// contractSetRolePermissions documents PUT /api/v1/roles/{id}/permissions.
//
// @Summary 整体替换角色的权限规则
// @Description 语义是「替换」而不是「追加」：请求体里没有的规则会被删除，因此前端必须先读再改再写。deny 规则优先于 allow，effect 只接受 allow/deny。对内置角色写入会被拒绝——内置矩阵只能由版本升级变更。
// @Tags Roles
// @ID setRolePermissions
// @Accept json
// @Produce json
// @Param id path string true "角色 ID"
// @Param request body handler.SetRolePermissionsRequest true "完整的规则集合"
// @Success 200 {object} handler.RolePermissionsEnvelope
// @Failure 400 {object} docs.Envelope "规则不合法（未知资源或 effect 取值非法）"
// @Failure 409 {object} docs.Envelope "内置角色的规则不可修改"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles/{id}/permissions [put]
func contractSetRolePermissions() {}

// contractBatchUpdateTenantStatus documents PUT /api/v1/tenants/status.
//
// @Summary 批量启用或停用工作区
// @Description 一次调用切换多个工作区的状态，ids 为空会被拒绝（避免「什么都没做但返回成功」）。任一工作区不存在或无权操作时整体回滚，不做部分成功。
// @Tags Tenants
// @ID batchUpdateTenantStatus
// @Accept json
// @Produce json
// @Param request body handler.BatchTenantStatusRequest true "工作区 ID 列表与目标状态"
// @Success 200 {object} handler.BatchTenantStatusEnvelope
// @Failure 400 {object} docs.Envelope "ids 为空或状态取值非法"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/tenants/status [put]
func contractBatchUpdateTenantStatus() {}

// contractUpdateTenant documents PUT /api/v1/tenants/{id}.
//
// @Summary 修改工作区设置
// @Description 可改名称、状态、品牌与编译令牌预算。字段未提供表示保持原值，显式传空字符串表示清空该字段——这是「不动」和「清空」能被区分开的唯一方式。停用工作区会立刻阻止其成员登录。
// @Tags Tenants
// @ID updateTenant
// @Accept json
// @Produce json
// @Param id path string true "工作区 ID"
// @Param request body handler.UpdateTenantRequest true "要修改的设置"
// @Success 200 {object} handler.TenantEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法或状态取值非法"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/tenants/{id} [put]
func contractUpdateTenant() {}

// contractUpdateUser documents PUT /api/v1/users/{id}.
//
// @Summary 修改用户资料（可选重置密码）
// @Description 需要 user 的 manage 权限；字段留空表示不修改。修改自己的角色会被拒绝（40906）——避免管理员给自己提权，改角色请走 PUT /users/{id}/role 并留下独立审计。
// @Tags Users
// @ID updateUser
// @Accept json
// @Produce json
// @Param id path string true "用户 ID"
// @Param request body handler.UpdateUserRequest true "要修改的字段"
// @Success 200 {object} handler.IDEnvelope
// @Failure 409 {object} docs.Envelope "40906：不能修改自己的角色"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users/{id} [put]
func contractUpdateUser() {}

// contractSetUserClearance documents PUT /api/v1/users/{id}/clearance.
//
// @Summary 设置用户的文档密级
// @Description 仅平台管理员或租户管理员可调用（服务层会二次校验，不依赖中间件）。密级决定该用户能检索到哪些文档，属于治理动作，每次变更都会写入审计。
// @Tags Users
// @ID setUserClearance
// @Accept json
// @Produce json
// @Param id path string true "用户 ID"
// @Param request body handler.SetClearanceRequest true "目标密级"
// @Success 200 {object} handler.ClearanceEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法或密级取值非法"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users/{id}/clearance [put]
func contractSetUserClearance() {}

// contractSetUserStatus documents PUT /api/v1/users/{id}/status.
//
// @Summary 启用或停用用户
// @Description 停用后该用户的现有令牌在下次校验时失效，且无法再登录。不能停用自己（40907）。状态变更写审计，运维排查「为什么某人登不上」时以本接口的记录为准。
// @Tags Users
// @ID setUserStatus
// @Accept json
// @Produce json
// @Param id path string true "用户 ID"
// @Param request body handler.UserStatusRequest true "目标状态"
// @Success 200 {object} handler.UserStatusEnvelope
// @Failure 400 {object} docs.Envelope "请求体不合法或状态取值非法"
// @Failure 409 {object} docs.Envelope "40907：不能修改自己的状态"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users/{id}/status [put]
func contractSetUserStatus() {}

// contractSetUserRole documents PUT /api/v1/users/{id}/role.
//
// @Summary 变更用户的主角色
// @Description 需要 user 的 manage 权限，且不能修改自己的角色（40906）。角色必须存在且可被当前调用者授予，否则返回 409/400。变更会重建该用户的 rgx_user_role 关系并写独立审计。
// @Tags Users
// @ID setUserRole
// @Accept json
// @Produce json
// @Param id path string true "用户 ID"
// @Param request body handler.AssignRoleRequest true "目标角色名"
// @Success 200 {object} handler.AssignRoleEnvelope
// @Failure 409 {object} docs.Envelope "40906 修改自己的角色；或角色不可授予"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users/{id}/role [put]
func contractSetUserRole() {}

// contractDeleteTenant documents DELETE /api/v1/tenants/{id}.
//
// @Summary 删除工作区
// @Description 默认拒绝删除仍有子资源的工作区；force=true 会连同子资源一起删除（不可恢复，需平台管理员）。删除写审计，工作区 ID 会进入墓碑记录，避免同名重建后历史审计指向错误的对象。
// @Tags Tenants
// @ID deleteTenant
// @Produce json
// @Param id path string true "工作区 ID"
// @Param force query bool false "true 表示连同子资源强制删除"
// @Success 200 {object} handler.IDEnvelope
// @Failure 409 {object} docs.Envelope "工作区仍有子资源且未指定 force"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/tenants/{id} [delete]
func contractDeleteTenant() {}

// contractDeleteRole documents DELETE /api/v1/roles/{id}.
//
// @Summary 删除角色
// @Description 拒绝删除的三种情形都是 400（不是 409），各带一个码：40082 内置角色、40083 仍被用户占用、40084 仍被其他角色当作父角色。占用必须先释放——把用户改到别的角色，或删除该用户（删除用户会在同一事务里清掉角色关系）；否则这个角色会被 40083 永久卡住。
// @Tags Roles
// @ID deleteRole
// @Produce json
// @Param id path string true "角色 ID"
// @Success 200 {object} handler.IDEnvelope
// @Failure 400 {object} docs.Envelope "40082 内置角色不可删除；40083 仍被用户占用；40084 仍被其他角色引用为父角色"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/roles/{id} [delete]
func contractDeleteRole() {}

// contractDeleteUser documents DELETE /api/v1/users/{id}.
//
// @Summary 删除用户
// @Description 需要 user 的 manage 权限。三种 409：40908 删除自己、40903 目标是平台管理员（平台永不失去它的管理员）、40905 目标是本工作区最后一个仍然启用的管理员。删除会在同一事务里清掉该用户的角色关系，因此之后可以正常删除其角色——否则角色会被 40083「仍被用户占用」永久卡住。
// @Tags Users
// @ID deleteUser
// @Produce json
// @Param id path string true "用户 ID"
// @Success 200 {object} handler.IDEnvelope
// @Failure 409 {object} docs.Envelope "40908 删除自己；40903 目标是平台管理员；40905 目标是本工作区最后一个启用中的管理员"
// @Failure 401 {object} docs.Envelope
// @Failure 403 {object} docs.Envelope
// @Security BearerAuth
// @Router /api/v1/users/{id} [delete]
func contractDeleteUser() {}

// Compile-time existence check for every type the annotations above name. swag reads
// these names as strings; this block makes the compiler read them too.
var (
	_ = docs.Envelope{}
	_ = model.User{}
	_ = model.Role{}
	_ = model.Permission{}
	_ = model.Tenant{}
	_ = model.UserClearance{}
	_ = db.ResourceActions{}
	_ = response.Page{}
	_ = service.TokenResult{}
	_ = LoginRequest{}
	_ = RefreshRequest{}
	_ = LogoutResponse{}
	_ = MeResponse{}
	_ = OIDCStatusResponse{}
	_ = CreateUserRequest{}
	_ = UpdateUserRequest{}
	_ = AssignRoleRequest{}
	_ = AssignRoleResponse{}
	_ = UserStatusRequest{}
	_ = UserStatusResponse{}
	_ = IDResponse{}
	_ = SetClearanceRequest{}
	_ = UserClearanceResponse{}
	_ = CreateRoleRequest{}
	_ = UpdateRoleRequest{}
	_ = SetRolePermissionsRequest{}
	_ = RolePermissionsResponse{}
	_ = CreateTenantRequest{}
	_ = UpdateTenantRequest{}
	_ = BatchTenantStatusRequest{}
	_ = BatchTenantStatusResponse{}
)
