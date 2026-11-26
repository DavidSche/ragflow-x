package handler

import (
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// The request/response types in this file exist so that an OpenAPI annotation can
// name the type the handler actually binds or emits (doc/310 §8.2, T310-01/T310-02).
//
// Why the handlers use them and not an inline anonymous struct: an annotation that
// points at a mirror type documents what someone believed the controller accepts.
// The only annotation that cannot drift is one whose `{object}` is the very type
// passed to `ShouldBindJSON` / `response.OK`. So the types live here, exported, and
// the handlers below reference them (`var req = LoginRequest{}`).
//
// Naming: exported, no `Request`/`Response` ambiguity with `internal/service` — swag
// emits `definitions` keyed by the bare type name, so two packages with the same
// type name would collide in the document.

// LoginRequest is the credential body of POST /api/v1/auth/login.
type LoginRequest struct {
	Username string `json:"username" binding:"required" example:"admin"`
	Password string `json:"password" binding:"required" example:"changeme"`
}

// RefreshRequest is the body of POST /api/v1/auth/refresh and the optional body of
// POST /api/v1/auth/logout. An empty `refresh_token` means "use the rgx_refresh
// cookie", which is how the browser client refreshes a session.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// LogoutResponse is the body of POST /api/v1/auth/logout.
type LogoutResponse struct {
	OK bool `json:"ok"`
}

// MeResponse is the profile body of GET /api/v1/auth/me. It is not `model.User`:
// the login screen needs the resolved permission set and the workspace display
// name, and it must not be able to read a password hash even by accident.
type MeResponse struct {
	ID              string             `json:"id"`
	Username        string             `json:"username"`
	Email           string             `json:"email"`
	TenantID        string             `json:"tenant_id"`
	TenantName      string             `json:"tenant_name"`
	Role            string             `json:"role"`
	Permissions     []model.Permission `json:"permissions"`
	Platform        bool               `json:"platform"`
	ApprovalEnabled bool               `json:"approval_enabled"`
}

// OIDCStatusResponse is the body of GET /api/v1/auth/oidc/status.
type OIDCStatusResponse struct {
	Enabled   bool   `json:"enabled"`
	Issuer    string `json:"issuer"`
	LoginPath string `json:"login_path"`
}

// CreateUserRequest is the body of POST /api/v1/users. `tenant_id` is only honoured
// for a caller holding governance.manage on tenant.
type CreateUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenant_id"`
}

// UpdateUserRequest is the body of PUT /api/v1/users/{id}. Every field is optional;
// an empty `role` means "leave the role alone" (use PUT /users/{id}/role to change it,
// which is audited separately).
type UpdateUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// AssignRoleRequest is the body of PUT /api/v1/users/{id}/role.
type AssignRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// AssignRoleResponse is what PUT /api/v1/users/{id}/role echoes back.
type AssignRoleResponse struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// UserStatusRequest is the body of PUT /api/v1/users/{id}/status.
type UserStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

// UserStatusResponse is what PUT /api/v1/users/{id}/status echoes back.
type UserStatusResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// IDResponse is the minimal "I changed this object" acknowledgement returned by the
// delete/update endpoints that have nothing else to say.
type IDResponse struct {
	ID string `json:"id"`
}

// SetClearanceRequest is the body of PUT /api/v1/users/{id}/clearance.
type SetClearanceRequest struct {
	Level string `json:"level" binding:"required"`
}

// UserClearanceResponse is the body of GET /api/v1/users/{id}/clearance. `recorded`
// distinguishes "no explicit level, the default applies" from "someone set this".
type UserClearanceResponse struct {
	UserID   string `json:"user_id"`
	Level    string `json:"level"`
	Recorded bool   `json:"recorded"`
}

// CreateRoleRequest is the body of POST /api/v1/roles.
type CreateRoleRequest struct {
	Name        string `json:"name" binding:"required"`
	Scope       string `json:"scope" binding:"required"`
	Description string `json:"description"`
	ParentID    string `json:"parent_id"`
}

// UpdateRoleRequest is the body of PUT /api/v1/roles/{id}.
type UpdateRoleRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
	ParentID    string `json:"parent_id"`
}

// PermissionGrant is one policy rule inside SetRolePermissionsRequest. `effect` is
// `allow` or `deny`; a `deny` rule wins over an `allow` rule on the same pair.
type PermissionGrant struct {
	Action   string `json:"action" example:"read"`
	Resource string `json:"resource" example:"chat"`
	Effect   string `json:"effect" example:"allow"`
}

// SetRolePermissionsRequest is the body of PUT /api/v1/roles/{id}/permissions. It
// replaces the whole rule set — it is not a patch.
type SetRolePermissionsRequest struct {
	Permissions []PermissionGrant `json:"permissions"`
}

// RolePermissionsResponse is what PUT /api/v1/roles/{id}/permissions echoes back.
type RolePermissionsResponse struct {
	ID          string `json:"id"`
	Permissions int    `json:"permissions"`
}

// CreateTenantRequest is the body of POST /api/v1/tenants.
type CreateTenantRequest struct {
	Name      string `json:"name" binding:"required"`
	Status    string `json:"status"`
	BrandName string `json:"brand_name"`
	BrandLogo string `json:"brand_logo"`
}

// UpdateTenantRequest is the body of PUT /api/v1/tenants/{id}. Pointers are absent
// when the field was not supplied: an omitted branding field keeps its value, while
// an explicit empty string clears it.
type UpdateTenantRequest struct {
	Name                   string  `json:"name"`
	Status                 string  `json:"status"`
	BrandName              *string `json:"brand_name"`
	BrandLogo              *string `json:"brand_logo"`
	CompilationTokenBudget *int64  `json:"compilation_token_budget"`
}

// BatchTenantStatusRequest is the body of PUT /api/v1/tenants/status.
type BatchTenantStatusRequest struct {
	IDs    []string `json:"ids"`
	Status string   `json:"status"`
}

// BatchTenantStatusResponse is what PUT /api/v1/tenants/status echoes back.
type BatchTenantStatusResponse struct {
	Updated int `json:"updated"`
}

// EnvelopeMeta is the non-payload half of the standard success envelope: code /
// message / trace_id, defined once. A per-endpoint envelope below embeds it and names
// only its `data` type, so the document has one definition of the envelope and one
// definition per payload — not 20 copies of the envelope.
//
// Why not `docs.Envelope{data=X}` in the annotation: swag's field override on a $ref
// parent mutates the shared `docs.Envelope` definition instead of creating a new one
// (doc/310 §22.2 P-310-12). The compiler cannot see that; a test in
// openapi_contracts_test.go pins the meta fields against `response.Body`.
type EnvelopeMeta struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

// UserPage is the paginated `data` of GET /api/v1/users: response.Page with a typed item list. It embeds the real
// pagination envelope instead of restating total/page/page_size.
type UserPage struct {
	response.Page
	Items []model.User `json:"items"`
}

// TenantPage is the paginated `data` of GET /api/v1/tenants: response.Page with a typed item list. It embeds the real
// pagination envelope instead of restating total/page/page_size.
type TenantPage struct {
	response.Page
	Items []model.Tenant `json:"items"`
}

// LoginEnvelope is the success envelope carrying the token pair returned by POST /api/v1/auth/login.
type LoginEnvelope struct {
	EnvelopeMeta
	Data service.TokenResult `json:"data"`
}

// RefreshEnvelope is the success envelope carrying the rotated token pair returned by POST /api/v1/auth/refresh.
type RefreshEnvelope struct {
	EnvelopeMeta
	Data service.TokenResult `json:"data"`
}

// LogoutEnvelope is the success envelope carrying the acknowledgement returned by POST /api/v1/auth/logout.
type LogoutEnvelope struct {
	EnvelopeMeta
	Data LogoutResponse `json:"data"`
}

// MeEnvelope is the success envelope carrying the profile returned by GET /api/v1/auth/me.
type MeEnvelope struct {
	EnvelopeMeta
	Data MeResponse `json:"data"`
}

// OIDCStatusEnvelope is the success envelope carrying the SSO availability answer from GET /api/v1/auth/oidc/status.
type OIDCStatusEnvelope struct {
	EnvelopeMeta
	Data OIDCStatusResponse `json:"data"`
}

// UserPageEnvelope is the success envelope carrying a page of users (GET /api/v1/users).
type UserPageEnvelope struct {
	EnvelopeMeta
	Data UserPage `json:"data"`
}

// UserEnvelope is the success envelope carrying one user (POST /api/v1/users).
type UserEnvelope struct {
	EnvelopeMeta
	Data model.User `json:"data"`
}

// UserClearanceEnvelope is the success envelope carrying one user's document clearance (GET /api/v1/users/{id}/clearance).
type UserClearanceEnvelope struct {
	EnvelopeMeta
	Data UserClearanceResponse `json:"data"`
}

// ClearanceEnvelope is the success envelope carrying the clearance record written by PUT /api/v1/users/{id}/clearance.
type ClearanceEnvelope struct {
	EnvelopeMeta
	Data model.UserClearance `json:"data"`
}

// IDEnvelope is the success envelope carrying the `{id}` acknowledgement of a user/role/tenant mutation.
type IDEnvelope struct {
	EnvelopeMeta
	Data IDResponse `json:"data"`
}

// UserStatusEnvelope is the success envelope carrying the status echo of PUT /api/v1/users/{id}/status.
type UserStatusEnvelope struct {
	EnvelopeMeta
	Data UserStatusResponse `json:"data"`
}

// AssignRoleEnvelope is the success envelope carrying the role echo of PUT /api/v1/users/{id}/role.
type AssignRoleEnvelope struct {
	EnvelopeMeta
	Data AssignRoleResponse `json:"data"`
}

// RoleListEnvelope is the success envelope carrying the bare role array of GET /api/v1/roles.
type RoleListEnvelope struct {
	EnvelopeMeta
	Data []model.Role `json:"data"`
}

// PermissionCatalogEnvelope is the success envelope carrying the grantable (resource, actions) matrix of GET /api/v1/roles/permission-catalog.
type PermissionCatalogEnvelope struct {
	EnvelopeMeta
	Data []db.ResourceActions `json:"data"`
}

// PermissionListEnvelope is the success envelope carrying the rule array of GET /api/v1/roles/{id}/permissions.
type PermissionListEnvelope struct {
	EnvelopeMeta
	Data []model.Permission `json:"data"`
}

// RoleEnvelope is the success envelope carrying one role (POST/PUT /api/v1/roles…).
type RoleEnvelope struct {
	EnvelopeMeta
	Data model.Role `json:"data"`
}

// RolePermissionsEnvelope is the success envelope carrying the rule-count echo of PUT /api/v1/roles/{id}/permissions.
type RolePermissionsEnvelope struct {
	EnvelopeMeta
	Data RolePermissionsResponse `json:"data"`
}

// TenantPageEnvelope is the success envelope carrying a page of tenants (GET /api/v1/tenants).
type TenantPageEnvelope struct {
	EnvelopeMeta
	Data TenantPage `json:"data"`
}

// TenantEnvelope is the success envelope carrying one tenant (POST/PUT /api/v1/tenants…).
type TenantEnvelope struct {
	EnvelopeMeta
	Data model.Tenant `json:"data"`
}

// BatchTenantStatusEnvelope is the success envelope carrying the updated-count echo of PUT /api/v1/tenants/status.
type BatchTenantStatusEnvelope struct {
	EnvelopeMeta
	Data BatchTenantStatusResponse `json:"data"`
}
