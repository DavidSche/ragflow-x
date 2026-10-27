package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type KnowledgeStrategyFilter struct {
	DatasetID    string
	ProjectID    string
	ProjectIDs   []string
	StrategyType string
	Active       *bool
}

type KnowledgeStrategyRepo interface {
	CreateKnowledgeStrategy(ctx context.Context, strategy *model.KnowledgeStrategy) error
	GetKnowledgeStrategy(ctx context.Context, tenantID, id string) (*model.KnowledgeStrategy, error)
	UpdateKnowledgeStrategy(ctx context.Context, strategy *model.KnowledgeStrategy) error
	DeleteKnowledgeStrategy(ctx context.Context, tenantID, id string) error
	ListKnowledgeStrategies(
		ctx context.Context, tenantID string, filter KnowledgeStrategyFilter, page, pageSize int,
	) ([]model.KnowledgeStrategy, int64, error)
	CreateKnowledgeCapabilityProbe(ctx context.Context, probe *model.KnowledgeCapabilityProbe) error
	ListKnowledgeCapabilityProbes(
		ctx context.Context, tenantID, strategyID string, limit int,
	) ([]model.KnowledgeCapabilityProbe, error)
}

func (s *store) CreateKnowledgeStrategy(ctx context.Context, strategy *model.KnowledgeStrategy) error {
	return s.WithContext(ctx).Create(strategy).Error
}

func (s *store) GetKnowledgeStrategy(ctx context.Context, tenantID, id string) (*model.KnowledgeStrategy, error) {
	var strategy model.KnowledgeStrategy
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).First(&strategy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &strategy, nil
}

func (s *store) UpdateKnowledgeStrategy(ctx context.Context, strategy *model.KnowledgeStrategy) error {
	return s.WithContext(ctx).Save(strategy).Error
}

func (s *store) DeleteKnowledgeStrategy(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&model.KnowledgeStrategy{}).Error
}

func (s *store) ListKnowledgeStrategies(
	ctx context.Context, tenantID string, filter KnowledgeStrategyFilter, page, pageSize int,
) ([]model.KnowledgeStrategy, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.DatasetID != "" {
		query += " AND dataset_id = ?"
		args = append(args, filter.DatasetID)
	}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if len(filter.ProjectIDs) > 0 {
		query += " AND project_id IN ?"
		args = append(args, filter.ProjectIDs)
	}
	if filter.StrategyType != "" {
		query += " AND strategy_type = ?"
		args = append(args, filter.StrategyType)
	}
	if filter.Active != nil {
		query += " AND active = ?"
		args = append(args, *filter.Active)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.KnowledgeStrategy{}, query, "strategy_type ASC, created_at ASC, id ASC", page, pageSize, args...,
	)
}

func (s *store) CreateKnowledgeCapabilityProbe(ctx context.Context, probe *model.KnowledgeCapabilityProbe) error {
	return s.WithContext(ctx).Create(probe).Error
}

func (s *store) ListKnowledgeCapabilityProbes(
	ctx context.Context, tenantID, strategyID string, limit int,
) ([]model.KnowledgeCapabilityProbe, error) {
	probes := []model.KnowledgeCapabilityProbe{}
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND strategy_id = ?", tenantID, strategyID).
		Order("checked_at DESC, id DESC").
		Limit(limit).
		Find(&probes).Error
	return probes, err
}
