package repository

import "strings"

// TenantFilter holds optional filter criteria for listing tenants.
type TenantFilter struct {
	Type        string // exact match; platform rows are excluded unless requested
	Name        string // LIKE match on name
	Status      string // exact match on status
	CreatedFrom string // >= created_at (RFC3339 or date-only)
	CreatedTo   string // < created_at (RFC3339 or date-only)
}

// UserFilter holds optional filter criteria for listing users.
type UserFilter struct {
	TenantID    string // exact match; only trusted governance APIs use it
	Username    string // LIKE match on username
	Status      string // exact match on status
	Role        string // exact match on role
	CreatedFrom string // >= created_at (RFC3339 or date-only)
	CreatedTo   string // < created_at (RFC3339 or date-only)
}

// TeamFilter holds optional filter criteria for listing teams.
type TeamFilter struct {
	Name        string // LIKE match on name
	CreatedFrom string // >= created_at (RFC3339 or date-only)
	CreatedTo   string // < created_at (RFC3339 or date-only)
}

// RoleFilter holds optional filter criteria for listing roles.
type RoleFilter struct {
	Name  string // LIKE match on name
	Scope string // exact match on scope
}

// DatasetFilter holds optional filter criteria for listing datasets.
type DatasetFilter struct {
	Name        string // LIKE match on name
	TenantID    string // exact match on tenant_id
	CreatedFrom string // >= created_at (RFC3339 or date-only)
	CreatedTo   string // < created_at (RFC3339 or date-only)
}

// AuditFilter holds optional filter criteria for listing audit logs.
type AuditFilter struct {
	UserID          string // exact match on user_id
	Action          string // LIKE match on action
	Resource        string // LIKE match on resource
	TargetTenant    string // exact match on target_tenant_id
	Result          string // exact match on result
	ApprovalID      string // exact match on approval_id
	ActingContextID string // exact match on acting_context_id
	CreatedFrom     string // >= at (RFC3339 or date-only)
	CreatedTo       string // < at (RFC3339 or date-only)
}

// likePattern wraps a user-supplied fragment in a LIKE pattern, escaping
// wildcard characters so user input is matched literally.
func likePattern(fragment string) string {
	if fragment == "" {
		return ""
	}
	return "%" + escapeLike(fragment) + "%"
}

// escapeLike escapes SQL LIKE wildcards so user input is not interpreted as
// a pattern. The backslash escape works on both MySQL and PostgreSQL with
// the default no-backslash-escapes disabled; to be portable we escape the
// backslash itself first.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// IsActive returns true when a DatasetFilter carries any meaningful criteria.
func (f DatasetFilter) IsActive() bool {
	return f.Name != "" || f.TenantID != "" || f.CreatedFrom != "" || f.CreatedTo != ""
}

// CostMetricFilter carries optional criteria for listing usage detail rows.
type CostMetricFilter struct {
	Model     string // exact match on model
	Scenario  string // exact match on scenario
	SessionID string // exact match on session_id
	UserID    string // exact match on user_id
	KeyID     string // exact match on key_id
	ChatID    string // exact match on chat_id / assistant target
	TenantID  string // optional tenant filter when the caller has governance scope
	DateFrom  string // >= created_at (RFC3339 or date-only)
	DateTo    string // < created_at (RFC3339 or date-only)
}
