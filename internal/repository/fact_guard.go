package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type FactGuardRepo interface {
	CreateFactRegistry(ctx context.Context, fact *model.FactRegistry) error
	UpdateFactRegistry(ctx context.Context, fact *model.FactRegistry) error
	ListFactRegistryByIDs(ctx context.Context, tenantID, answerRunID string, ids []string) ([]model.FactRegistry, error)
	ListFactRegistry(ctx context.Context, tenantID, answerRunID string) ([]model.FactRegistry, error)
	CreateClaimValidation(ctx context.Context, validation *model.ClaimValidation) error
	ListClaimValidations(ctx context.Context, tenantID, answerRunID string) ([]model.ClaimValidation, error)
	CreateEvidenceConflict(ctx context.Context, conflict *model.EvidenceConflict) error
	ListEvidenceConflicts(ctx context.Context, tenantID, answerRunID string) ([]model.EvidenceConflict, error)
}

func (s *store) CreateFactRegistry(ctx context.Context, fact *model.FactRegistry) error {
	return s.WithContext(ctx).Create(fact).Error
}

func (s *store) UpdateFactRegistry(ctx context.Context, fact *model.FactRegistry) error {
	return s.WithContext(ctx).Save(fact).Error
}

func (s *store) ListFactRegistryByIDs(ctx context.Context, tenantID, answerRunID string, ids []string) ([]model.FactRegistry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var facts []model.FactRegistry
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND answer_run_id = ? AND id IN (?)", tenantID, answerRunID, ids).
		Find(&facts).Error
	return facts, err
}

func (s *store) ListFactRegistry(ctx context.Context, tenantID, answerRunID string) ([]model.FactRegistry, error) {
	var facts []model.FactRegistry
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND answer_run_id = ?", tenantID, answerRunID).
		Order("created_at ASC, id ASC").
		Find(&facts).Error
	return facts, err
}

func (s *store) CreateClaimValidation(ctx context.Context, validation *model.ClaimValidation) error {
	return s.WithContext(ctx).Create(validation).Error
}

func (s *store) ListClaimValidations(ctx context.Context, tenantID, answerRunID string) ([]model.ClaimValidation, error) {
	var validations []model.ClaimValidation
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND answer_run_id = ?", tenantID, answerRunID).
		Order("created_at ASC, id ASC").
		Find(&validations).Error
	return validations, err
}

func (s *store) CreateEvidenceConflict(ctx context.Context, conflict *model.EvidenceConflict) error {
	return s.WithContext(ctx).Create(conflict).Error
}

func (s *store) ListEvidenceConflicts(ctx context.Context, tenantID, answerRunID string) ([]model.EvidenceConflict, error) {
	var conflicts []model.EvidenceConflict
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND answer_run_id = ?", tenantID, answerRunID).
		Order("created_at ASC, id ASC").
		Find(&conflicts).Error
	return conflicts, err
}
