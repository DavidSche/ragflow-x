package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SearchAppFilter carries optional criteria for listing Search Apps.
type SearchAppFilter struct {
	Name     string // LIKE match on name
	Status   string // exact match on status
	OwnerID  string // exact match on owner_id
	TenantID string // exact match on tenant_id
}

// SearchAppShadowRepo persists the platform's Search App ownership records used
// to keep multi-tenant isolation for RAGFlow Search Apps.
type SearchAppShadowRepo interface {
	UpsertSearchAppShadow(ctx context.Context, s *model.SearchAppShadow) error
	ListSearchAppShadows(ctx context.Context, tenantID string, scopeAll bool, filter SearchAppFilter, page, pageSize int) ([]model.SearchAppShadow, int64, error)
	ListSearchAppShadowsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter SearchAppFilter, page, pageSize int) ([]model.SearchAppShadow, int64, error)
	GetSearchAppShadow(ctx context.Context, tenantID, searchAppID string, scopeAll bool) (*model.SearchAppShadow, error)
	GetSearchAppShadowForScope(ctx context.Context, scopeAll bool, tenantIDs []string, searchAppID string) (*model.SearchAppShadow, error)
	DeleteSearchAppShadow(ctx context.Context, searchAppID string) error
}

func (s *store) UpsertSearchAppShadow(ctx context.Context, sa *model.SearchAppShadow) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"name": sa.Name, "status": sa.Status, "dataset_ids": sa.DatasetIDs, "owner_id": sa.OwnerID, "tenant_id": sa.TenantID, "updated_at": sa.UpdatedAt}),
	}).Create(sa).Error
}

func (s *store) ListSearchAppShadows(ctx context.Context, tenantID string, scopeAll bool, filter SearchAppFilter, page, pageSize int) ([]model.SearchAppShadow, int64, error) {
	var list []model.SearchAppShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.SearchAppShadow{})
	if !scopeAll {
		if tenantID == "" {
			return list, 0, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
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

func (s *store) ListSearchAppShadowsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter SearchAppFilter, page, pageSize int) ([]model.SearchAppShadow, int64, error) {
	var list []model.SearchAppShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.SearchAppShadow{})
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return list, 0, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
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

func (s *store) GetSearchAppShadow(ctx context.Context, tenantID, searchAppID string, scopeAll bool) (*model.SearchAppShadow, error) {
	var sa model.SearchAppShadow
	q := s.WithContext(ctx).Where("id = ?", searchAppID)
	if !scopeAll {
		if tenantID == "" {
			return nil, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.First(&sa).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &sa, nil
}

func (s *store) GetSearchAppShadowForScope(ctx context.Context, scopeAll bool, tenantIDs []string, searchAppID string) (*model.SearchAppShadow, error) {
	var app model.SearchAppShadow
	q := s.WithContext(ctx).Where("id = ?", searchAppID)
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return nil, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if err := q.First(&app).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &app, nil
}

func (s *store) DeleteSearchAppShadow(ctx context.Context, searchAppID string) error {
	return s.WithContext(ctx).Where("id = ?", searchAppID).Delete(&model.SearchAppShadow{}).Error
}
