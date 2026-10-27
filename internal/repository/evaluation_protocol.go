package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Evaluation protocol registration persistence lives alongside the release
// governance repo because preregistration gates scoped evaluation execution.

type EvaluationProtocolRegistrationFilter struct {
	Capability      string
	ContractVersion string
	Status          string
}

func (s *store) CreateEvaluationProtocolRegistration(ctx context.Context, registration *model.EvaluationProtocolRegistration) error {
	return s.WithContext(ctx).Create(registration).Error
}

func (s *store) GetEvaluationProtocolRegistration(ctx context.Context, tenantID, id string) (*model.EvaluationProtocolRegistration, error) {
	var registration model.EvaluationProtocolRegistration
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&registration).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &registration, err
}

func (s *store) FindEvaluationProtocolRegistration(ctx context.Context, tenantID, capability, contractVersion, protocolHash string) (*model.EvaluationProtocolRegistration, error) {
	var registration model.EvaluationProtocolRegistration
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND capability = ? AND contract_version = ? AND protocol_hash = ?",
			tenantID, capability, contractVersion, protocolHash).
		Order("created_at DESC").
		First(&registration).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &registration, err
}

func (s *store) ListEvaluationProtocolRegistrations(ctx context.Context, tenantID string, filter EvaluationProtocolRegistrationFilter, page, pageSize int) ([]model.EvaluationProtocolRegistration, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.Capability != "" {
		query += " AND capability = ?"
		args = append(args, filter.Capability)
	}
	if filter.ContractVersion != "" {
		query += " AND contract_version = ?"
		args = append(args, filter.ContractVersion)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	return listTenantPage(ctx, s.DB, &[]model.EvaluationProtocolRegistration{}, query, "created_at DESC, id ASC", page, pageSize, args...)
}

func (s *store) RevokeEvaluationProtocolRegistration(ctx context.Context, registration *model.EvaluationProtocolRegistration) error {
	return s.WithContext(ctx).Model(&model.EvaluationProtocolRegistration{}).
		Where("tenant_id = ? AND id = ? AND status = ?", registration.TenantID, registration.ID, model.EvaluationProtocolRegistrationActive).
		Updates(map[string]interface{}{
			"status":        model.EvaluationProtocolRegistrationRevoked,
			"revoked_by":    registration.RevokedBy,
			"revoke_reason": registration.RevokeReason,
			"revoked_at":    registration.RevokedAt,
			"updated_at":    registration.UpdatedAt,
		}).Error
}
