package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func (s *store) ListGovernanceDictionary(ctx context.Context, tenantID, category string) ([]model.GovernanceDictionaryEntry, error) {
	var entries []model.GovernanceDictionaryEntry
	err := s.WithContext(ctx).
		Where("(tenant_id = ? OR tenant_id = '') AND (category = ? OR ? = '')", tenantID, category, category).
		Order("CASE WHEN tenant_id = '' THEN 0 ELSE 1 END").
		Order("rank ASC").
		Find(&entries).Error
	return entries, err
}

func (s *store) ListAllGovernanceDictionary(ctx context.Context, category string) ([]model.GovernanceDictionaryEntry, error) {
	var entries []model.GovernanceDictionaryEntry
	q := s.WithContext(ctx)
	if category != "" {
		q = q.Where("category = ?", category)
	}
	err := q.Order("CASE WHEN tenant_id = '' THEN 0 ELSE 1 END").Order("rank ASC").Find(&entries).Error
	return entries, err
}

func (s *store) GetGovernanceDictionaryEntry(ctx context.Context, tenantID, category, key string) (*model.GovernanceDictionaryEntry, error) {
	if key == "" {
		return nil, errors.New("dictionary key is required")
	}
	var e model.GovernanceDictionaryEntry
	err := s.WithContext(ctx).
		Where("(tenant_id = ? OR tenant_id = '') AND category = ? AND key = ?", tenantID, category, key).
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *store) GetGovernanceDictionaryEntryByID(ctx context.Context, tenantID, id string) (*model.GovernanceDictionaryEntry, error) {
	var e model.GovernanceDictionaryEntry
	q := s.WithContext(ctx).Where("id = ?", id)
	if tenantID != "" {
		q = q.Where("tenant_id = ? OR tenant_id = ''", tenantID)
	}
	err := q.First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *store) UpsertGovernanceDictionaryEntry(ctx context.Context, e *model.GovernanceDictionaryEntry) error {
	return s.WithContext(ctx).Save(e).Error
}

func (s *store) DeleteGovernanceDictionaryEntry(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND origin = ?", id, tenantID, model.DictOriginTenant).
		Delete(&model.GovernanceDictionaryEntry{}).Error
}

func (s *store) SetGovernanceDictionaryApproval(ctx context.Context, id, state string, approvedBy string, approvedAt *time.Time) error {
	updates := map[string]interface{}{
		"approval_state": state,
		"active":         state == model.DictApprovalApproved,
		"updated_at":     time.Now().UTC(),
	}
	if state == model.DictApprovalApproved {
		updates["approved_by"] = approvedBy
		updates["approved_at"] = approvedAt
	} else {
		updates["approved_by"] = nil
		updates["approved_at"] = nil
	}
	return s.WithContext(ctx).Model(&model.GovernanceDictionaryEntry{}).
		Where("id = ? AND tenant_id <> ''", id).
		Updates(updates).Error
}
