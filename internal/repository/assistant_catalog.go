package repository

import (
	"context"
	"errors"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AssistantCatalogFilter carries optional criteria for the unified entry.
type AssistantCatalogFilter struct {
	Kinds []string
	Query string
}

// AssistantCatalogRepo persists the discovery read model. Governance fields are
// never overwritten by upstream shadow synchronization.
type AssistantCatalogRepo interface {
	UpsertAssistantCatalog(ctx context.Context, item *model.AssistantCatalog) error
	GetAssistantCatalog(ctx context.Context, tenantID, kind, targetID string) (*model.AssistantCatalog, error)
	SyncAssistantCatalogBasicFields(ctx context.Context, item *model.AssistantCatalog) error
	UpdateAssistantGovernanceFields(ctx context.Context, item *model.AssistantCatalog) error
	ListAssistantCatalogs(ctx context.Context, tenantID string, filter AssistantCatalogFilter, page, pageSize int) ([]model.AssistantCatalog, int64, error)
	DeleteAssistantCatalog(ctx context.Context, tenantID, kind, targetID string) error
}

func (s *store) UpsertAssistantCatalog(ctx context.Context, item *model.AssistantCatalog) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"tenant_id": item.TenantID, "kind": item.Kind, "target_id": item.TargetID,
			"name":            item.Name,
			"upstream_status": item.UpstreamStatus, "effective_status": item.EffectiveStatus,
			"effective_status_reason": item.EffectiveStatusReason, "owner_id": item.OwnerID,
			"updated_at": item.UpdatedAt,
		}),
	}).Create(item).Error
}

func (s *store) GetAssistantCatalog(ctx context.Context, tenantID, kind, targetID string) (*model.AssistantCatalog, error) {
	var item model.AssistantCatalog
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND kind = ? AND target_id = ?", tenantID, kind, targetID).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &item, err
}

func (s *store) SyncAssistantCatalogBasicFields(ctx context.Context, item *model.AssistantCatalog) error {
	return s.WithContext(ctx).Exec(`
		UPDATE rgx_assistant_catalog SET
			name = ?, upstream_status = ?, effective_status = ?,
			effective_status_reason = ?, owner_id = ?, updated_at = ?
		WHERE tenant_id = ? AND kind = ? AND target_id = ?
	`, item.Name, item.UpstreamStatus, item.EffectiveStatus,
		item.EffectiveStatusReason, item.OwnerID, item.UpdatedAt,
		item.TenantID, item.Kind, item.TargetID).Error
}

func (s *store) UpdateAssistantGovernanceFields(ctx context.Context, item *model.AssistantCatalog) error {
	return s.WithContext(ctx).Exec(`
		UPDATE rgx_assistant_catalog SET
			governance_status = ?, discoverable = ?, routing_weight = ?, owner_id = ?,
			description = ?, categories_json = ?, capabilities_json = ?, intents_json = ?,
			keywords_json = ?, examples_json = ?, assistant_risk_level = ?,
			capability_risk_json = ?, workflow_risk_upper_bound = ?,
			auto_select_enabled = ?, routing_readiness = ?, agent_flow_readiness = ?,
			catalog_version = ?, updated_at = ?
		WHERE tenant_id = ? AND kind = ? AND target_id = ?
	`, item.GovernanceStatus, item.Discoverable, item.RoutingWeight, item.OwnerID,
		item.Description, item.CategoriesJSON, item.CapabilitiesJSON, item.IntentsJSON,
		item.KeywordsJSON, item.ExamplesJSON, item.AssistantRiskLevel,
		item.CapabilityRiskJSON, item.WorkflowRiskUpperBound, item.AutoSelectEnabled,
		item.RoutingReadiness, item.AgentFlowReadiness, item.CatalogVersion, item.UpdatedAt,
		item.TenantID, item.Kind, item.TargetID).Error
}

func (s *store) ListAssistantCatalogs(ctx context.Context, tenantID string, filter AssistantCatalogFilter, page, pageSize int) ([]model.AssistantCatalog, int64, error) {
	var items []model.AssistantCatalog
	var total int64
	q := s.WithContext(ctx).Model(&model.AssistantCatalog{}).
		Where("tenant_id = ? AND effective_status = ? AND discoverable = ?", tenantID, model.AssistantEffectiveActive, true)
	if len(filter.Kinds) > 0 {
		q = q.Where("kind IN ?", filter.Kinds)
	}
	if filter.Query != "" {
		like := likePattern(filter.Query)
		q = q.Where(`(
			name LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\' OR
			categories_json LIKE ? ESCAPE '\' OR capabilities_json LIKE ? ESCAPE '\' OR
			intents_json LIKE ? ESCAPE '\' OR keywords_json LIKE ? ESCAPE '\'
		)`, like, like, like, like, like, like)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("updated_at DESC, name ASC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) DeleteAssistantCatalog(ctx context.Context, tenantID, kind, targetID string) error {
	return s.WithContext(ctx).
		Where("tenant_id = ? AND kind = ? AND target_id = ?", tenantID, kind, targetID).
		Delete(&model.AssistantCatalog{}).Error
}
