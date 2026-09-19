package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChatFilter carries optional criteria for listing chat assistants.
type ChatFilter struct {
	Name        string // LIKE match on name
	Status      string // exact match on status
	OwnerID     string // exact match on owner_id
	TenantID    string // exact match on tenant_id
	MinMessages int    // message_count >= value
}

// ChatShadowRepo persists the platform's chat ownership records used to keep
// multi-tenant isolation for RAGFlow chat assistants.
type ChatShadowRepo interface {
	UpsertChatShadow(ctx context.Context, s *model.ChatShadow) error
	ListChatShadows(ctx context.Context, tenantID string, scopeAll bool, filter ChatFilter, page, pageSize int) ([]model.ChatShadow, int64, error)
	ListChatShadowsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter ChatFilter, page, pageSize int) ([]model.ChatShadow, int64, error)
	GetChatShadow(ctx context.Context, tenantID, chatID string, scopeAll bool) (*model.ChatShadow, error)
	GetChatShadowForScope(ctx context.Context, scopeAll bool, tenantIDs []string, chatID string) (*model.ChatShadow, error)
	DeleteChatShadow(ctx context.Context, chatID string) error
	BatchUpdateChatStatus(ctx context.Context, tenantID string, scopeAll bool, ids []string, status string) error
	IncChatMessageCount(ctx context.Context, chatID string) error
}

func (s *store) UpsertChatShadow(ctx context.Context, cs *model.ChatShadow) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"name": cs.Name, "status": cs.Status, "dataset_ids": cs.DatasetIDs, "config_json": cs.ConfigJSON, "owner_id": cs.OwnerID, "tenant_id": cs.TenantID, "updated_at": cs.UpdatedAt}),
	}).Create(cs).Error
}

func (s *store) ListChatShadows(ctx context.Context, tenantID string, scopeAll bool, filter ChatFilter, page, pageSize int) ([]model.ChatShadow, int64, error) {
	var list []model.ChatShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.ChatShadow{})
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
	if filter.MinMessages > 0 {
		q = q.Where("message_count >= ?", filter.MinMessages)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) ListChatShadowsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter ChatFilter, page, pageSize int) ([]model.ChatShadow, int64, error) {
	var list []model.ChatShadow
	var total int64
	q := s.WithContext(ctx).Model(&model.ChatShadow{})
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
	if filter.MinMessages > 0 {
		q = q.Where("message_count >= ?", filter.MinMessages)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) GetChatShadow(ctx context.Context, tenantID, chatID string, scopeAll bool) (*model.ChatShadow, error) {
	var cs model.ChatShadow
	q := s.WithContext(ctx).Where("id = ?", chatID)
	if !scopeAll {
		if tenantID == "" {
			return nil, nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.First(&cs).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cs, nil
}

func (s *store) GetChatShadowForScope(ctx context.Context, scopeAll bool, tenantIDs []string, chatID string) (*model.ChatShadow, error) {
	var chat model.ChatShadow
	q := s.WithContext(ctx).Where("id = ?", chatID)
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return nil, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if err := q.First(&chat).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &chat, nil
}

func (s *store) DeleteChatShadow(ctx context.Context, chatID string) error {
	return s.WithContext(ctx).Where("id = ?", chatID).Delete(&model.ChatShadow{}).Error
}

func (s *store) BatchUpdateChatStatus(ctx context.Context, tenantID string, scopeAll bool, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	q := s.WithContext(ctx).Model(&model.ChatShadow{}).Where("id IN ?", ids)
	if !scopeAll {
		if tenantID == "" {
			return nil
		}
		q = q.Where("tenant_id = ?", tenantID)
	}
	return q.Update("status", status).Error
}

func (s *store) IncChatMessageCount(ctx context.Context, chatID string) error {
	return s.WithContext(ctx).Model(&model.ChatShadow{}).
		Where("id = ?", chatID).
		UpdateColumn("message_count", gorm.Expr("message_count + 1")).Error
}
