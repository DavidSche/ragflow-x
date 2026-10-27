package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentFilter carries optional criteria for listing agents.
type AgentFilter struct {
	Title    string // LIKE match on title
	Status   string // exact match on status
	OwnerID  string // exact match on owner_id
	TenantID string // exact match on tenant_id
}

// AgentShadowRepo persists the platform's agent ownership records used to keep
// multi-tenant isolation for RAGFlow agents.
type AgentShadowRepo interface {
	UpsertAgentShadow(ctx context.Context, s *model.AgentShadow) error
	GetAgentShadowByTitle(ctx context.Context, tenantID, title, excludeID string) (*model.AgentShadow, error)
	ListAgentShadows(ctx context.Context, tenantID string, scopeAll bool, filter AgentFilter, page, pageSize int) ([]model.AgentShadow, int64, error)
	ListAgentShadowsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter AgentFilter, page, pageSize int) ([]model.AgentShadow, int64, error)
	GetAgentShadow(ctx context.Context, tenantID, agentID string, scopeAll bool) (*model.AgentShadow, error)
	GetAgentShadowForScope(ctx context.Context, scopeAll bool, tenantIDs []string, agentID string) (*model.AgentShadow, error)
	DeleteAgentShadow(ctx context.Context, agentID string) error
}

func (s *store) GetAgentShadowByTitle(ctx context.Context, tenantID, title, excludeID string) (*model.AgentShadow, error) {
	var a model.AgentShadow
	q := s.WithContext(ctx).Where("tenant_id = ? AND LOWER(title) = LOWER(?)", tenantID, title)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.First(&a).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &a, err
}

func (s *store) UpsertAgentShadow(ctx context.Context, a *model.AgentShadow) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"title": a.Title, "status": a.Status, "release": a.Release, "owner_id": a.OwnerID, "tenant_id": a.TenantID, "updated_at": a.UpdatedAt}),
	}).Create(a).Error
}

func (s *store) ListAgentShadows(ctx context.Context, tenantID string, scopeAll bool, filter AgentFilter, page, pageSize int) ([]model.AgentShadow, int64, error) {
	var list []model.AgentShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.AgentShadow{})
	if !scopeAll {
		if tenantID == "" {
			return list, 0, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Title != "" {
		q = q.Where("title LIKE ? ESCAPE '\\'", likePattern(filter.Title))
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.OwnerID != "" {
		q = q.Where("owner_id = ?", filter.OwnerID)
	}
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) ListAgentShadowsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter AgentFilter, page, pageSize int) ([]model.AgentShadow, int64, error) {
	var list []model.AgentShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.AgentShadow{})
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return list, 0, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if filter.Title != "" {
		q = q.Where("title LIKE ? ESCAPE '\\'", likePattern(filter.Title))
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.OwnerID != "" {
		q = q.Where("owner_id = ?", filter.OwnerID)
	}
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) GetAgentShadow(ctx context.Context, tenantID, agentID string, scopeAll bool) (*model.AgentShadow, error) {
	var a model.AgentShadow
	q := s.WithContext(ctx).Where("id = ?", agentID)
	if !scopeAll {
		if tenantID == "" {
			return nil, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.First(&a).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (s *store) GetAgentShadowForScope(ctx context.Context, scopeAll bool, tenantIDs []string, agentID string) (*model.AgentShadow, error) {
	var agent model.AgentShadow
	q := s.WithContext(ctx).Where("id = ?", agentID)
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return nil, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if err := q.First(&agent).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &agent, nil
}

func (s *store) DeleteAgentShadow(ctx context.Context, agentID string) error {
	return s.WithContext(ctx).Where("id = ?", agentID).Delete(&model.AgentShadow{}).Error
}
