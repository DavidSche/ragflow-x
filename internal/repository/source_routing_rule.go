package repository

import (
	"context"
	"errors"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

type SourceRoutingRuleFilter struct {
	AssistantID string
	SourceType  string
	Active      *bool
}

type SourceRoutingRuleRepo interface {
	CreateSourceRoutingRule(ctx context.Context, rule *model.SourceRoutingRule) error
	GetSourceRoutingRule(ctx context.Context, tenantID, id string) (*model.SourceRoutingRule, error)
	UpdateSourceRoutingRule(ctx context.Context, rule *model.SourceRoutingRule) error
	DeleteSourceRoutingRule(ctx context.Context, tenantID, id string) error
	ListSourceRoutingRules(ctx context.Context, tenantID string, filter SourceRoutingRuleFilter, page, pageSize int) ([]model.SourceRoutingRule, int64, error)
	ListActiveSourceRoutingRules(ctx context.Context, tenantID, assistantID string) ([]model.SourceRoutingRule, error)
}

func (s *store) CreateSourceRoutingRule(ctx context.Context, rule *model.SourceRoutingRule) error {
	return s.WithContext(ctx).Create(rule).Error
}

func (s *store) GetSourceRoutingRule(ctx context.Context, tenantID, id string) (*model.SourceRoutingRule, error) {
	var rule model.SourceRoutingRule
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&rule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rule, err
}

func (s *store) UpdateSourceRoutingRule(ctx context.Context, rule *model.SourceRoutingRule) error {
	return s.WithContext(ctx).Save(rule).Error
}

func (s *store) DeleteSourceRoutingRule(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.SourceRoutingRule{}).Error
}

func (s *store) ListSourceRoutingRules(
	ctx context.Context, tenantID string, filter SourceRoutingRuleFilter, page, pageSize int,
) ([]model.SourceRoutingRule, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.AssistantID != "" {
		query += " AND assistant_id = ?"
		args = append(args, filter.AssistantID)
	}
	if filter.SourceType != "" {
		query += " AND source_type = ?"
		args = append(args, filter.SourceType)
	}
	if filter.Active != nil {
		query += " AND active = ?"
		args = append(args, *filter.Active)
	}
	return listTenantPage(ctx, s.DB, &[]model.SourceRoutingRule{}, query, "priority ASC, created_at ASC, id ASC", page, pageSize, args...)
}

func (s *store) ListActiveSourceRoutingRules(
	ctx context.Context, tenantID, assistantID string,
) ([]model.SourceRoutingRule, error) {
	var rules []model.SourceRoutingRule
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND active = ? AND (assistant_id = '' OR assistant_id = ?)", tenantID, true, assistantID).
		Order("priority ASC, created_at ASC, id ASC").
		Find(&rules).Error
	return rules, err
}
