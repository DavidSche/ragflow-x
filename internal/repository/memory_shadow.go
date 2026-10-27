package repository

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MemoryFilter carries optional criteria for listing memories.
type MemoryFilter struct {
	Name       string // LIKE match on name
	MemoryType string // exact substring match on memory_type
	OwnerID    string // exact match on owner_id
}

// MemoryShadowRepo persists the platform's Memory ownership records used to
// keep multi-tenant isolation for RAGFlow memories.
type MemoryShadowRepo interface {
	UpsertMemoryShadow(ctx context.Context, s *model.MemoryShadow) error
	ListMemoryShadows(ctx context.Context, tenantID string, scopeAll bool, filter MemoryFilter, page, pageSize int) ([]model.MemoryShadow, int64, error)
	GetMemoryShadow(ctx context.Context, tenantID, memoryID string, scopeAll bool) (*model.MemoryShadow, error)
	DeleteMemoryShadow(ctx context.Context, memoryID string) error
}

func (s *store) UpsertMemoryShadow(ctx context.Context, mem *model.MemoryShadow) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"name": mem.Name, "memory_type": mem.MemoryType, "owner_id": mem.OwnerID, "tenant_id": mem.TenantID, "updated_at": mem.UpdatedAt}),
	}).Create(mem).Error
}

func (s *store) ListMemoryShadows(ctx context.Context, tenantID string, scopeAll bool, filter MemoryFilter, page, pageSize int) ([]model.MemoryShadow, int64, error) {
	var list []model.MemoryShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.MemoryShadow{})
	if !scopeAll {
		if tenantID == "" {
			return list, 0, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.MemoryType != "" {
		q = q.Where("memory_type LIKE ? ESCAPE '\\'", "%"+strings.ReplaceAll(filter.MemoryType, "%", "\\%")+"%")
	}
	if filter.OwnerID != "" {
		q = q.Where("owner_id = ?", filter.OwnerID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) GetMemoryShadow(ctx context.Context, tenantID, memoryID string, scopeAll bool) (*model.MemoryShadow, error) {
	var mem model.MemoryShadow
	q := s.WithContext(ctx).Where("id = ?", memoryID)
	if !scopeAll {
		if tenantID == "" {
			return nil, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.First(&mem).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &mem, nil
}

func (s *store) DeleteMemoryShadow(ctx context.Context, memoryID string) error {
	return s.WithContext(ctx).Where("id = ?", memoryID).Delete(&model.MemoryShadow{}).Error
}
