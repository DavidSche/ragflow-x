package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type ContextDecisionFilter struct {
	AssistantID string
	SessionID   string
	UserID      string
	RequestKind string
	Decision    string
}

type ConversationContextRepo interface {
	CreateContextDecision(ctx context.Context, decision *model.ContextDecision) error
	UpdateContextDecisionDetail(ctx context.Context, tenantID, decisionID, detail string) error
	GetContextDecision(ctx context.Context, tenantID, decisionID string) (*model.ContextDecision, error)
	GetContextDecisionByRequest(ctx context.Context, tenantID, requestID string) (*model.ContextDecision, error)
	ListContextDecisions(ctx context.Context, tenantID string, filter ContextDecisionFilter, page, pageSize int) ([]model.ContextDecision, int64, error)
}

func (s *store) CreateContextDecision(ctx context.Context, decision *model.ContextDecision) error {
	return s.WithContext(ctx).Create(decision).Error
}

func (s *store) UpdateContextDecisionDetail(
	ctx context.Context, tenantID, decisionID, detail string,
) error {
	return s.WithContext(ctx).Model(&model.ContextDecision{}).
		Where("tenant_id = ? AND id = ?", tenantID, decisionID).
		Update("detail", detail).Error
}

func (s *store) GetContextDecision(ctx context.Context, tenantID, decisionID string) (*model.ContextDecision, error) {
	var decision model.ContextDecision
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, decisionID).First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &decision, nil
}

func (s *store) GetContextDecisionByRequest(ctx context.Context, tenantID, requestID string) (*model.ContextDecision, error) {
	var decision model.ContextDecision
	err := s.WithContext(ctx).Where("tenant_id = ? AND request_id = ?", tenantID, requestID).First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &decision, nil
}

func (s *store) ListContextDecisions(
	ctx context.Context, tenantID string, filter ContextDecisionFilter, page, pageSize int,
) ([]model.ContextDecision, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.AssistantID != "" {
		query += " AND assistant_id = ?"
		args = append(args, filter.AssistantID)
	}
	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}
	if filter.UserID != "" {
		query += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.RequestKind != "" {
		query += " AND request_kind = ?"
		args = append(args, filter.RequestKind)
	}
	if filter.Decision != "" {
		query += " AND decision = ?"
		args = append(args, filter.Decision)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.ContextDecision{}, query,
		"created_at DESC, id DESC", page, pageSize, args...,
	)
}
