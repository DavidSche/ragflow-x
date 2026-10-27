package model

import "time"

const (
	EnterpriseConnectionManagedByPlatform  = "PLATFORM"
	EnterpriseConnectionManagedByWorkspace = "WORKSPACE"

	EnterpriseConnectionVisibilityPrivate = "PRIVATE"
	EnterpriseConnectionVisibilityShared  = "SHARED"

	EnterpriseConnectionCredentialScopeConnection = "CONNECTION"
	EnterpriseConnectionCredentialScopeBinding    = "BINDING"

	EnterpriseLifecycleDraft      = "DRAFT"
	EnterpriseLifecycleActive     = "ACTIVE"
	EnterpriseLifecycleDeprecated = "DEPRECATED"
	EnterpriseLifecycleDisabled   = "DISABLED"
	EnterpriseLifecycleRetired    = "RETIRED"

	RuntimeHealthUnknown     = "UNKNOWN"
	RuntimeHealthHealthy     = "HEALTHY"
	RuntimeHealthDegraded    = "DEGRADED"
	RuntimeHealthUnavailable = "UNAVAILABLE"
)

// EnterpriseConnection is the logical connection identity. Runtime semantics
// are pinned by CurrentConnectionVersion, not by mutable connection fields.
type EnterpriseConnection struct {
	ID                       string     `gorm:"column:id;primaryKey;size:64" json:"id"`
	OwnerTenantID            string     `gorm:"column:owner_tenant_id;size:32;not null;index" json:"owner_tenant_id"`
	ManagedBy                string     `gorm:"column:managed_by;size:24;not null" json:"managed_by"`
	Visibility               string     `gorm:"column:visibility;size:24;not null" json:"visibility"`
	UsableBy                 string     `gorm:"column:usable_by;size:24;not null" json:"usable_by"`
	CredentialScope          string     `gorm:"column:credential_scope;size:24;not null" json:"credential_scope"`
	CurrentConnectionVersion int64      `gorm:"column:current_connection_version;not null" json:"current_connection_version"`
	LifecycleStatus          string     `gorm:"column:lifecycle_status;size:24;not null;default:ACTIVE;index" json:"lifecycle_status"`
	RuntimeHealth            string     `gorm:"column:runtime_health;size:24;not null;default:UNKNOWN" json:"runtime_health"`
	LastHealthCheckAt        *time.Time `gorm:"column:last_health_check_at" json:"last_health_check_at,omitempty"`
	CreatedAt                time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// EnterpriseConnectionVersion is immutable after creation. Config changes must
// create a new version and atomically move the parent current pointer.
type EnterpriseConnectionVersion struct {
	ConnectionID string `gorm:"column:connection_id;primaryKey;size:64;uniqueIndex:uk_enterprise_connection_version,priority:1"`
	Version      int64  `gorm:"column:version;primaryKey;uniqueIndex:uk_enterprise_connection_version,priority:2"`
	ProviderName string `gorm:"column:provider_name;size:96;not null" json:"provider_name"`
	DisplayName  string `gorm:"column:display_name;size:128;not null" json:"display_name"`
	BaseURL      string `gorm:"column:base_url;size:512;not null" json:"base_url"`
	// CredentialRef is an internal Vault/Secrets Manager reference. It is
	// deliberately not serialized by API views.
	CredentialRef           string    `gorm:"column:credential_ref;size:256;not null" json:"-"`
	CredentialVersion       string    `gorm:"column:credential_version;size:64;not null" json:"credential_version"`
	CredentialPolicyVersion string    `gorm:"column:credential_policy_version;size:64;not null" json:"credential_policy_version"`
	Visibility              string    `gorm:"column:visibility;size:24;not null" json:"visibility"`
	UsableBy                string    `gorm:"column:usable_by;size:24;not null" json:"usable_by"`
	ManagedBy               string    `gorm:"column:managed_by;size:24;not null" json:"managed_by"`
	CredentialScope         string    `gorm:"column:credential_scope;size:24;not null" json:"credential_scope"`
	ConnectionConfigHash    string    `gorm:"column:connection_config_hash;size:80;not null;index" json:"connection_config_hash"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
}

func (EnterpriseConnection) TableName() string        { return "rgx_enterprise_connection" }
func (EnterpriseConnectionVersion) TableName() string { return "rgx_enterprise_connection_version" }

// EnterpriseConnectionBinding separates logical authorization from immutable
// versions. Releases must reference binding_id + binding_version.
type EnterpriseConnectionBinding struct {
	BindingID             string    `gorm:"column:binding_id;primaryKey;size:64" json:"binding_id"`
	ConnectionID          string    `gorm:"column:connection_id;size:64;not null;index" json:"connection_id"`
	TenantID              string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	CurrentBindingVersion int64     `gorm:"column:current_binding_version;not null" json:"current_binding_version"`
	LifecycleStatus       string    `gorm:"column:lifecycle_status;size:24;not null;default:ACTIVE;index" json:"lifecycle_status"`
	CreatedAt             time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt             time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// EnterpriseConnectionBindingVersion is immutable once created. Updating the
// authorization scope creates a new version in the same transaction that moves
// the parent current pointer.
type EnterpriseConnectionBindingVersion struct {
	BindingID           string    `gorm:"column:binding_id;primaryKey;size:64;uniqueIndex:uk_enterprise_binding_version,priority:1"`
	Version             int64     `gorm:"column:version;primaryKey;uniqueIndex:uk_enterprise_binding_version,priority:2;uniqueIndex:uk_enterprise_binding_tenant_version,priority:3"`
	ConnectionID        string    `gorm:"column:connection_id;size:64;not null;uniqueIndex:uk_enterprise_binding_tenant_version,priority:1"`
	TenantID            string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uk_enterprise_binding_tenant_version,priority:2"`
	AllowedCapabilities string    `gorm:"column:allowed_capabilities;type:text;not null" json:"allowed_capabilities"`
	AllowedModelRefs    string    `gorm:"column:allowed_model_refs;type:text;not null" json:"allowed_model_refs"`
	ApprovalID          string    `gorm:"column:approval_id;size:64" json:"approval_id"`
	CreatedBy           string    `gorm:"column:created_by;size:64;not null" json:"created_by"`
	CreatedAt           time.Time `gorm:"column:created_at" json:"created_at"`
}

func (EnterpriseConnectionBinding) TableName() string { return "rgx_enterprise_connection_binding" }
func (EnterpriseConnectionBindingVersion) TableName() string {
	return "rgx_enterprise_connection_binding_version"
}

// EnterpriseConnectionHealthCheck is an immutable probe result. Messages are
// operator-safe summaries and never contain provider request/response payloads.
type EnterpriseConnectionHealthCheck struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	ConnectionID      string    `gorm:"column:connection_id;size:64;not null;index" json:"connection_id"`
	ConnectionVersion int64     `gorm:"column:connection_version;not null" json:"connection_version"`
	Health            string    `gorm:"column:health;size:24;not null;index" json:"health"`
	HTTPStatus        int       `gorm:"column:http_status" json:"http_status"`
	LatencyMS         int64     `gorm:"column:latency_ms;not null;default:0" json:"latency_ms"`
	Message           string    `gorm:"column:message;size:256" json:"message"`
	CreatedBy         string    `gorm:"column:created_by;size:64;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at;index" json:"created_at"`
}

func (EnterpriseConnectionHealthCheck) TableName() string {
	return "rgx_enterprise_connection_health_check"
}
