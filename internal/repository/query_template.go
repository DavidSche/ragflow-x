package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type QueryTemplateFilter struct {
	Active       *bool
	ConnectionID string
}

type QueryTemplateRepo interface {
	CreateQueryTemplate(ctx context.Context, template *model.QueryTemplate) error
	GetQueryTemplate(ctx context.Context, tenantID, id string) (*model.QueryTemplate, error)
	GetQueryTemplateByName(ctx context.Context, tenantID, name, excludeID string) (*model.QueryTemplate, error)
	UpdateQueryTemplate(ctx context.Context, template *model.QueryTemplate) error
	DeleteQueryTemplate(ctx context.Context, tenantID, id string) error
	ListQueryTemplates(ctx context.Context, tenantID string, filter QueryTemplateFilter, page, pageSize int) ([]model.QueryTemplate, int64, error)
}

func (s *store) CreateQueryTemplate(ctx context.Context, template *model.QueryTemplate) error {
	return s.WithContext(ctx).Create(template).Error
}

func (s *store) GetQueryTemplate(ctx context.Context, tenantID, id string) (*model.QueryTemplate, error) {
	var template model.QueryTemplate
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&template).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &template, nil
}

func (s *store) GetQueryTemplateByName(ctx context.Context, tenantID, name, excludeID string) (*model.QueryTemplate, error) {
	var template model.QueryTemplate
	query := s.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	err := query.First(&template).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &template, nil
}

func (s *store) UpdateQueryTemplate(ctx context.Context, template *model.QueryTemplate) error {
	return s.WithContext(ctx).Save(template).Error
}

func (s *store) DeleteQueryTemplate(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&model.QueryTemplate{}).Error
}

func (s *store) ListQueryTemplates(
	ctx context.Context, tenantID string, filter QueryTemplateFilter, page, pageSize int,
) ([]model.QueryTemplate, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.Active != nil {
		query += " AND active = ?"
		args = append(args, *filter.Active)
	}
	if filter.ConnectionID != "" {
		query += " AND connection_id = ?"
		args = append(args, filter.ConnectionID)
	}
	return listTenantPage(ctx, s.DB, &[]model.QueryTemplate{}, query, "name ASC, id ASC", page, pageSize, args...)
}
