package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// GovernanceDictionaryRepo persists the controlled governance vocabulary
// (T1, doc/224 §2.2). Platform builtins live under tenant "" and are seeded by
// migration; tenant extensions under the tenant id.
type GovernanceDictionaryRepo interface {
	// ListGovernanceDictionary returns the effective vocabulary for a tenant:
	// platform builtins merged with this tenant's approved extensions.
	ListGovernanceDictionary(ctx context.Context, tenantID, category string) ([]model.GovernanceDictionaryEntry, error)
	// ListAllGovernanceDictionary returns every row (platform + all tenants),
	// used by governance dashboards and audit.
	ListAllGovernanceDictionary(ctx context.Context, category string) ([]model.GovernanceDictionaryEntry, error)
	GetGovernanceDictionaryEntry(ctx context.Context, tenantID, category, key string) (*model.GovernanceDictionaryEntry, error)
	GetGovernanceDictionaryEntryByID(ctx context.Context, tenantID, id string) (*model.GovernanceDictionaryEntry, error)
	UpsertGovernanceDictionaryEntry(ctx context.Context, e *model.GovernanceDictionaryEntry) error
	DeleteGovernanceDictionaryEntry(ctx context.Context, tenantID, id string) error
	// SetGovernanceDictionaryApproval flips a tenant extension's approval
	// state and activation atomically.
	SetGovernanceDictionaryApproval(ctx context.Context, id, state string, approvedBy string, approvedAt *time.Time) error
}

// GovernanceSchemaRepo persists governance extraction schemas (T1, doc/224
// §2.2). A schema declares how a dictionary target is projected from document
// content for one dataset/document bind.
type GovernanceSchemaRepo interface {
	CreateGovernanceSchema(ctx context.Context, s *model.GovernanceSchema) error
	GetGovernanceSchema(ctx context.Context, tenantID, id string) (*model.GovernanceSchema, error)
	ListGovernanceSchemas(ctx context.Context, tenantID string, filter GovernanceSchemaFilter) ([]model.GovernanceSchema, error)
	UpdateGovernanceSchema(ctx context.Context, s *model.GovernanceSchema) error
	DeleteGovernanceSchema(ctx context.Context, tenantID, id string) error
}

// GovernanceSchemaFilter narrows schema listing.
type GovernanceSchemaFilter struct {
	DatasetID  string
	DocumentID string
	Active     *bool
}
