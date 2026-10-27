package model

import "time"

// Team groups users within a tenant.
type Team struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name      string    `gorm:"column:name;size:128;not null" json:"name"`
	OwnerID   string    `gorm:"column:owner_id;size:32" json:"owner_id"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (Team) TableName() string { return "rgx_team" }

// TeamProject binds a team to a project (team-scoped authorization).
type TeamProject struct {
	TeamID    string    `gorm:"column:team_id;primaryKey;size:32" json:"team_id"`
	ProjectID string    `gorm:"column:project_id;primaryKey;size:32" json:"project_id"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (TeamProject) TableName() string { return "rgx_team_project" }

// RoleScopes describe the scoping of an RBAC role.
const (
	RoleScopePlatform = "platform"
	RoleScopeTenant   = "tenant"
)

// Default role IDs shared across the platform.
const (
	RoleOperator     = "operator"
	RoleViewer       = "viewer"
	RoleTeamAdmin    = "team_admin"
	RoleBusinessUser = "business_user"
)

// Role is an RBAC role, scoped to the platform or a tenant.
type Role struct {
	ID          string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;index" json:"tenant_id"`
	Name        string    `gorm:"column:name;size:128;not null" json:"name"`
	Scope       string    `gorm:"column:scope;size:32;not null" json:"scope"`
	Description string    `gorm:"column:description;size:255" json:"description"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	ParentID    string    `gorm:"column:parent_id;size:64;index" json:"parent_id"`
	BuiltIn     bool      `gorm:"-" json:"builtin"`
}

// TableName is the physical table name.
func (Role) TableName() string { return "rgx_role" }

// PermissionEffect states the outcome of a matching rule.
const (
	PermissionEffectAllow = "allow"
	PermissionEffectDeny  = "deny"
)

// Permission grants an action on a resource to a role. Resource may be a
// concrete name or the wildcard "*".
type Permission struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	RoleID    string    `gorm:"column:role_id;size:64;not null;index" json:"role_id"`
	Action    string    `gorm:"column:action;size:64;not null" json:"action"`
	Resource  string    `gorm:"column:resource;size:128;not null" json:"resource"`
	Effect    string    `gorm:"column:effect;size:16;not null;default:allow" json:"effect"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (Permission) TableName() string { return "rgx_permission" }

// UserRole links a user to a role.
type UserRole struct {
	UserID    string    `gorm:"column:user_id;primaryKey;size:32" json:"user_id"`
	RoleID    string    `gorm:"column:role_id;primaryKey;size:64" json:"role_id"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (UserRole) TableName() string { return "rgx_user_role" }

// UserTeam links a user to a team within a tenant.
type UserTeam struct {
	UserID    string    `gorm:"column:user_id;primaryKey;size:32" json:"user_id"`
	TeamID    string    `gorm:"column:team_id;primaryKey;size:32" json:"team_id"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (UserTeam) TableName() string { return "rgx_user_team" }

// ModelProvider is a unified LLM provider bound to a tenant.
type ModelProvider struct {
	ID           string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID     string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProviderType string    `gorm:"column:provider_type;size:64;not null" json:"provider_type"`
	Name         string    `gorm:"column:name;size:128;not null" json:"name"`
	BaseURL      string    `gorm:"column:base_url;size:255" json:"base_url"`
	APIKeyEnc    string    `gorm:"column:api_key_enc;size:1024" json:"-"`
	ModelsJSON   string    `gorm:"column:models_json;size:2048" json:"models_json"`
	Enabled      bool      `gorm:"column:enabled;not null;default:true" json:"enabled"`
	Status       string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (ModelProvider) TableName() string { return "rgx_model_provider" }

// ModelRoute maps a model alias to a target model on a provider.
type ModelRoute struct {
	ID          string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProviderID  string `gorm:"column:provider_id;size:32;not null;index" json:"provider_id"`
	Scenario    string `gorm:"column:scenario;size:64" json:"scenario"`
	ModelAlias  string `gorm:"column:model_alias;size:128;not null" json:"model_alias"`
	TargetModel string `gorm:"column:target_model;size:128;not null" json:"target_model"`
	Enabled     bool   `gorm:"column:enabled;not null;default:true" json:"enabled"`
	// CurrentPinID/Version are an explicit pointer to an immutable enterprise
	// runtime pin. Legacy routes may leave both empty.
	CurrentPinID      string    `gorm:"column:current_pin_id;size:64;index" json:"current_pin_id,omitempty"`
	CurrentPinVersion int64     `gorm:"column:current_pin_version" json:"current_pin_version,omitempty"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (ModelRoute) TableName() string { return "rgx_model_route" }

func (ModelRouteEnterprisePin) TableName() string { return "rgx_model_route_enterprise_pin" }

// ModelRouteEnterprisePin is an immutable runtime binding to one exact
// Enterprise Connection version, Binding version and Model Ref.
type ModelRouteEnterprisePin struct {
	PinID             string    `gorm:"column:pin_id;primaryKey;size:64" json:"pin_id"`
	TenantID          string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RouteID           string    `gorm:"column:route_id;size:32;not null;uniqueIndex:uk_model_route_pin,priority:1"`
	Version           int64     `gorm:"column:version;not null;uniqueIndex:uk_model_route_pin,priority:2"`
	ConnectionID      string    `gorm:"column:connection_id;size:64;not null;index" json:"connection_id"`
	ConnectionVersion int64     `gorm:"column:connection_version;not null" json:"connection_version"`
	BindingID         string    `gorm:"column:binding_id;size:64;not null;index" json:"binding_id"`
	BindingVersion    int64     `gorm:"column:binding_version;not null" json:"binding_version"`
	ModelRef          string    `gorm:"column:model_ref;size:128;not null" json:"model_ref"`
	CreatedBy         string    `gorm:"column:created_by;size:64;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
}

// APIKey is a platform/tenant gateway credential. Only a hash is persisted.
type APIKey struct {
	ID       string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	UserID   string `gorm:"column:user_id;size:32" json:"user_id"`
	Name     string `gorm:"column:name;size:128;not null" json:"name"`
	KeyHash  string `gorm:"column:key_hash;size:128;not null" json:"-"`
	Prefix   string `gorm:"column:prefix;size:24;not null" json:"prefix"`
	// TokenQuota is the monthly token budget for this key (0 = unlimited). It
	// is the spending-control input for gateway quota preauthorization.
	TokenQuota int64 `gorm:"column:token_quota;not null;default:0" json:"token_quota"`
	// RequestQuota is the monthly logical request budget for this key
	// (0 = unlimited). Provider and upstream retries reuse the reservation.
	RequestQuota int64  `gorm:"column:request_quota;not null;default:0" json:"request_quota"`
	ScopesJSON   string `gorm:"column:scopes_json;size:512" json:"scopes_json"`
	// AllowedIPsJSON is empty for unrestricted legacy keys. An explicit empty
	// array denies every source address; entries may be IPs or CIDRs.
	AllowedIPsJSON string     `gorm:"column:allowed_ips_json;size:512" json:"allowed_ips_json"`
	ExpiredAt      *time.Time `gorm:"column:expired_at" json:"expired_at"`
	Enabled        bool       `gorm:"column:enabled;not null;default:true" json:"enabled"`
	LastUsedAt     *time.Time `gorm:"column:last_used_at" json:"last_used_at"`
	CreatedAt      time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (APIKey) TableName() string { return "rgx_api_key" }

// AuditLog is an append-only record of sensitive operations.
//
// Seq is a per-tenant monotonic sequence number backing the hash chain. The
// composite unique index rgx_audit_log (tenant_id, seq) — created by migration
// v24 after backfilling existing rows — makes a forked chain impossible under
// concurrency: two writers racing for the same tail position cannot both
// commit, and the loser retries against the winner's row.
type AuditLog struct {
	ID                         string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Seq                        int64     `gorm:"column:seq;not null;default:0" json:"seq"`
	TenantID                   string    `gorm:"column:tenant_id;size:32;index" json:"tenant_id"`
	ActorTenantID              string    `gorm:"column:actor_tenant_id;size:32;index" json:"actor_tenant_id"`
	TargetTenantID             string    `gorm:"column:target_tenant_id;size:32;index" json:"target_tenant_id"`
	UserID                     string    `gorm:"column:user_id;size:32;index" json:"user_id"`
	Action                     string    `gorm:"column:action;size:64;not null" json:"action"`
	Resource                   string    `gorm:"column:resource;size:128;not null" json:"resource"`
	ResourceID                 string    `gorm:"column:resource_id;size:64" json:"resource_id"`
	DetailJSON                 string    `gorm:"column:detail_json;size:2048" json:"detail_json"`
	IP                         string    `gorm:"column:ip;size:64" json:"ip"`
	TraceID                    string    `gorm:"column:trace_id;size:64;index" json:"trace_id"`
	Scope                      string    `gorm:"column:scope;size:32" json:"scope"`
	Result                     string    `gorm:"column:result;size:32" json:"result"`
	ApprovalID                 string    `gorm:"column:approval_id;size:32;index" json:"approval_id"`
	ActingContextID            string    `gorm:"column:acting_context_id;size:64;index" json:"acting_context_id"`
	ApprovalActionHash         string    `gorm:"column:approval_action_hash;size:80" json:"approval_action_hash"`
	AuthorizationDecision      string    `gorm:"column:authorization_decision;size:32" json:"authorization_decision"`
	AuthorizationPermission    string    `gorm:"column:authorization_permission;size:128" json:"authorization_permission"`
	AuthorizationPolicyVersion string    `gorm:"column:authorization_policy_version;size:64" json:"authorization_policy_version"`
	At                         time.Time `gorm:"column:at;index" json:"at"`
	PrevHash                   string    `gorm:"column:prev_hash;size:64" json:"prev_hash"`
	Hash                       string    `gorm:"column:hash;size:64;index" json:"hash"`
}

// TableName is the physical table name.
func (AuditLog) TableName() string { return "rgx_audit_log" }

// Project is a knowledge domain (知识域/项目) within a tenant.
type Project struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name        string    `gorm:"column:name;size:128;not null" json:"name"`
	Description string    `gorm:"column:description;size:255" json:"description"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (Project) TableName() string { return "rgx_project" }

// ProjectMember links a user to a project (ABAC project ownership).
type ProjectMember struct {
	UserID    string    `gorm:"column:user_id;primaryKey;size:32" json:"user_id"`
	ProjectID string    `gorm:"column:project_id;primaryKey;size:32" json:"project_id"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (ProjectMember) TableName() string { return "rgx_project_member" }
