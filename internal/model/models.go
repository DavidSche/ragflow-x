// Package model defines the RAGFlow-X operational (shadow) data entities.
package model

import "time"

// TenantStatus describes tenant lifecycle states.
const (
	// PlatformTenantID is the fixed singleton control-plane tenant.
	PlatformTenantID = "00000000000000000000000000000000"

	TenantStatusActive   = "active"
	TenantStatusDisabled = "disabled"

	// TenantType makes Platform a formal control-plane type instead of a
	// permission shortcut for workspace resources.
	TenantTypePlatform  = "platform"
	TenantTypeWorkspace = "workspace"
)

// UserStatus describes user lifecycle states.
const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

// Role is a coarse RBAC role for M1. Refined role/permission tables land in M2.
const (
	RolePlatformAdmin = "platform_admin"
	RoleTenantAdmin   = "tenant_admin"
	RoleUser          = "user"
)

// Tenant is the platform-level control plane or an enterprise workspace.
type Tenant struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Name      string    `gorm:"column:name;size:128;not null" json:"name"`
	Type      string    `gorm:"column:type;size:24;not null;default:workspace" json:"type"`
	Status    string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
	BrandName string    `gorm:"column:brand_name;size:128" json:"brand_name"`
	BrandLogo string    `gorm:"column:brand_logo;size:512" json:"brand_logo"`
	// AutoRouteMode is the tenant-level kill switch and gradual rollout mode.
	// The system policy remains an outer boundary and can never be bypassed.
	AutoRouteMode string `gorm:"column:auto_route_mode;size:24;not null;default:recommend_only" json:"auto_route_mode"`
}

// TableName is the physical table name.
func (Tenant) TableName() string { return "rgx_tenant" }

// User is an operator account bound to a tenant.
type User struct {
	ID           string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID     string `gorm:"column:tenant_id;size:32;not null" json:"tenant_id"`
	Username     string `gorm:"column:username;size:128;not null;uniqueIndex" json:"username"`
	PasswordHash string `gorm:"column:password_hash;size:255;not null" json:"-"`
	Email        string `gorm:"column:email;size:255" json:"email"`
	// Role is a denormalized read-only primary/default-role marker derived from
	// rgx_user_role (the authoritative RBAC source). It is kept consistent by
	// SetUserPrimaryRole and is used for display/JWT/legacy fallback only.
	Role      string    `gorm:"column:role;size:32;not null;default:user" json:"role"`
	Status    string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (User) TableName() string { return "rgx_user" }

// RefreshToken tracks issued refresh tokens for rotation and revocation. Only
// a hash of the token is stored, so a leaked DB does not expose usable tokens.
type RefreshToken struct {
	ID        string     `gorm:"column:id;primaryKey;size:64" json:"id"`
	UserID    string     `gorm:"column:user_id;size:32;not null;index" json:"user_id"`
	TenantID  string     `gorm:"column:tenant_id;size:32;not null" json:"tenant_id"`
	TokenHash string     `gorm:"column:token_hash;size:64;not null;uniqueIndex" json:"-"`
	ExpiresAt time.Time  `gorm:"column:expires_at;not null;index" json:"expires_at"`
	RevokedAt *time.Time `gorm:"column:revoked_at;index" json:"revoked_at"`
	CreatedAt time.Time  `gorm:"column:created_at" json:"created_at"`
}

// OidcIdentity links a verified enterprise IdP subject to an existing local
// user. Auto-provisioning is intentionally not performed by this table.
type OidcIdentity struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Issuer      string    `gorm:"column:issuer;size:512;not null;uniqueIndex:uk_oidc_identity_issuer_subject" json:"issuer"`
	Subject     string    `gorm:"column:subject;size:255;not null;uniqueIndex:uk_oidc_identity_issuer_subject" json:"subject"`
	UserID      string    `gorm:"column:user_id;size:32;not null;index" json:"user_id"`
	Email       string    `gorm:"column:email;size:255" json:"email"`
	LastLoginAt time.Time `gorm:"column:last_login_at" json:"last_login_at"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (OidcIdentity) TableName() string { return "rgx_oidc_identity" }

// TableName is the physical table name.
func (RefreshToken) TableName() string { return "rgx_refresh_token" }

// DatasetLink maps an RAGFlow-X dataset reference to the underlying RAGFlow dataset id.
type DatasetLink struct {
	ID               string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RAGFlowDatasetID string     `gorm:"column:ragflow_dataset_id;size:64;not null;index" json:"ragflow_dataset_id"`
	Name             string     `gorm:"column:name;size:128;not null" json:"name"`
	DocumentCount    int64      `gorm:"column:document_count;not null;default:0" json:"document_count"`
	CreatedAt        time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at" json:"updated_at"`
	ProjectID        string     `gorm:"column:project_id;size:32;index" json:"project_id"`
	OwnerID          string     `gorm:"column:owner_id;size:32;index" json:"owner_id"`
	OwnerTeamID      string     `gorm:"column:owner_team_id;size:32;index" json:"owner_team_id"`
	SourceType       string     `gorm:"column:source_type;size:32" json:"source_type"`
	BusinessDomain   string     `gorm:"column:business_domain;size:64" json:"business_domain"`
	Sensitivity      string     `gorm:"column:sensitivity;size:16" json:"sensitivity"`
	EffectiveAt      *time.Time `gorm:"column:effective_at" json:"effective_at"`
	ExpiresAt        *time.Time `gorm:"column:expires_at;index" json:"expires_at"`
	LastReviewedAt   *time.Time `gorm:"column:last_reviewed_at" json:"last_reviewed_at"`
	ReviewStatus     string     `gorm:"column:review_status;size:16;not null;default:none;index" json:"review_status"`
	QualityScore     int        `gorm:"column:quality_score;not null;default:0" json:"quality_score"`
}

// TableName is the physical table name.
func (DatasetLink) TableName() string { return "rgx_dataset_link" }

// DocumentOwnershipLink tracks which workspace user uploaded an RAGFlow
// document so contributors can remove their own documents without gaining
// dataset-wide document governance permissions.
type DocumentOwnershipLink struct {
	ID         string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_document_ownership_scope" json:"tenant_id"`
	DatasetID  string    `gorm:"column:dataset_id;size:32;not null;uniqueIndex:idx_document_ownership_scope" json:"dataset_id"`
	DocumentID string    `gorm:"column:document_id;size:64;not null;uniqueIndex:idx_document_ownership_scope" json:"document_id"`
	UploaderID string    `gorm:"column:uploader_id;size:32;not null;index" json:"uploader_id"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (DocumentOwnershipLink) TableName() string { return "rgx_document_ownership" }

// SchemaVersion records applied migration versions.
type SchemaVersion struct {
	Version   int64     `gorm:"column:version;primaryKey" json:"version"`
	AppliedAt time.Time `gorm:"column:applied_at" json:"applied_at"`
}

// TableName is the physical table name.
func (SchemaVersion) TableName() string { return "rgx_schema_version" }
