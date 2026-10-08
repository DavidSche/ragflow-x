package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type ToolRegistryFilter struct {
	ToolType string
	Active   *bool
}

type ToolRegistryRepo interface {
	CreateToolRegistry(ctx context.Context, tool *model.ToolRegistry) error
	GetToolRegistry(ctx context.Context, tenantID, id string) (*model.ToolRegistry, error)
	UpdateToolRegistry(ctx context.Context, tool *model.ToolRegistry) error
	DeleteToolRegistry(ctx context.Context, tenantID, id string) error
	ListToolRegistries(ctx context.Context, tenantID string, filter ToolRegistryFilter, page, pageSize int) ([]model.ToolRegistry, int64, error)
	CountActiveToolRegistries(ctx context.Context, tenantID, toolID, excludeID string) (int64, error)
	FindActiveToolRegistry(ctx context.Context, tenantID, toolID, version string) (*model.ToolRegistry, error)
}

func (s *store) CreateToolRegistry(ctx context.Context, tool *model.ToolRegistry) error {
	return s.WithContext(ctx).Create(tool).Error
}

func (s *store) GetToolRegistry(ctx context.Context, tenantID, id string) (*model.ToolRegistry, error) {
	var tool model.ToolRegistry
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&tool).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &tool, nil
}

func (s *store) UpdateToolRegistry(ctx context.Context, tool *model.ToolRegistry) error {
	return s.WithContext(ctx).Save(tool).Error
}

func (s *store) DeleteToolRegistry(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.ToolRegistry{}).Error
}

func (s *store) ListToolRegistries(
	ctx context.Context, tenantID string, filter ToolRegistryFilter, page, pageSize int,
) ([]model.ToolRegistry, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.ToolType != "" {
		query += " AND tool_type = ?"
		args = append(args, filter.ToolType)
	}
	if filter.Active != nil {
		query += " AND active = ?"
		args = append(args, *filter.Active)
	}
	return listTenantPage(ctx, s.DB, &[]model.ToolRegistry{}, query, "tool_id ASC, version DESC, id ASC", page, pageSize, args...)
}

func (s *store) CountActiveToolRegistries(ctx context.Context, tenantID, toolID, excludeID string) (int64, error) {
	var count int64
	query := s.WithContext(ctx).Model(&model.ToolRegistry{}).
		Where("tenant_id = ? AND tool_id = ? AND active = ?", tenantID, toolID, true)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	err := query.Count(&count).Error
	return count, err
}

func (s *store) FindActiveToolRegistry(ctx context.Context, tenantID, toolID, version string) (*model.ToolRegistry, error) {
	var tool model.ToolRegistry
	query := s.WithContext(ctx).Where("tenant_id = ? AND tool_id = ? AND active = ?", tenantID, toolID, true)
	if version != "" {
		query = query.Where("version = ?", version)
	}
	err := query.Order("version DESC, id ASC").First(&tool).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &tool, nil
}
