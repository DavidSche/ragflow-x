package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func (s *store) CreateGovernanceSchema(ctx context.Context, g *model.GovernanceSchema) error {
	return s.WithContext(ctx).Create(g).Error
}

func (s *store) GetGovernanceSchema(ctx context.Context, tenantID, id string) (*model.GovernanceSchema, error) {
	var g model.GovernanceSchema
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *store) ListGovernanceSchemas(ctx context.Context, tenantID string, filter GovernanceSchemaFilter) ([]model.GovernanceSchema, error) {
	var list []model.GovernanceSchema
	q := s.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if filter.DatasetID != "" {
		q = q.Where("dataset_id = ?", filter.DatasetID)
	}
	if filter.DocumentID != "" {
		q = q.Where("document_id = ?", filter.DocumentID)
	}
	if filter.Active != nil {
		q = q.Where("active = ?", *filter.Active)
	}
	err := q.Order("category ASC").Order("name ASC").Find(&list).Error
	return list, err
}

func (s *store) UpdateGovernanceSchema(ctx context.Context, g *model.GovernanceSchema) error {
	return s.WithContext(ctx).Save(g).Error
}

func (s *store) DeleteGovernanceSchema(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.GovernanceSchema{}).Error
}
