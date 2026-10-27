package repository

import (
	"context"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

type ApprovalOperationRepo interface {
	GetApprovalOperationByContext(ctx context.Context, actingContextID string) (*model.ApprovalOperation, error)
	GetLatestApprovalOperation(ctx context.Context, tenantID, approvalID string) (*model.ApprovalOperation, error)
	ClaimActingContextForOperation(
		ctx context.Context, contextID, claimedBy string, now, claimExpiresAt time.Time,
		operation *model.ApprovalOperation,
	) (*model.ApprovalOperation, bool, error)
	CompleteApprovalOperation(
		ctx context.Context, id, from, to, resultJSON, lastError string, now time.Time,
	) (bool, error)
	MarkStaleApprovalOperationUnknown(ctx context.Context, id string, staleBefore, now time.Time) (bool, error)
}

func (s *store) GetApprovalOperationByContext(ctx context.Context, actingContextID string) (*model.ApprovalOperation, error) {
	var operation model.ApprovalOperation
	err := s.WithContext(ctx).Where("acting_context_id = ?", actingContextID).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

func (s *store) GetLatestApprovalOperation(ctx context.Context, tenantID, approvalID string) (*model.ApprovalOperation, error) {
	var operation model.ApprovalOperation
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND approval_id = ?", tenantID, approvalID).
		Order("attempt_no DESC").First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

// ClaimActingContextForOperation atomically claims an ACTIVE context and
// creates the business outcome ledger in one transaction. A crash therefore
// always leaves either no claim/no operation or a recoverable RUNNING pair.
func (s *store) ClaimActingContextForOperation(
	ctx context.Context, contextID, claimedBy string, now, claimExpiresAt time.Time,
	operation *model.ApprovalOperation,
) (*model.ApprovalOperation, bool, error) {
	var result *model.ApprovalOperation
	claimed := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Model(&model.ActingContext{}).
			Where("id = ? AND status = ? AND expires_at > ?", contextID, model.ActingContextActive, now).
			Updates(map[string]interface{}{
				"status":           model.ActingContextClaimed,
				"claimed_at":       now,
				"claimed_by":       claimedBy,
				"claim_expires_at": claimExpiresAt,
				"updated_at":       now,
			})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return nil
		}

		var existing model.ApprovalOperation
		err := tx.Where("acting_context_id = ?", contextID).First(&existing).Error
		if err == nil {
			result = &existing
			claimed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(operation).Error; err != nil {
			return err
		}
		result = operation
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return nil, claimed, err
	}
	return result, true, nil
}

func (s *store) CompleteApprovalOperation(
	ctx context.Context, id, from, to, resultJSON, lastError string, now time.Time,
) (bool, error) {
	updates := map[string]interface{}{
		"status":      to,
		"result_json": resultJSON,
		"last_error":  lastError,
		"updated_at":  now,
	}
	if to == model.ApprovalOperationCompleted || to == model.ApprovalOperationFailed || to == model.ApprovalOperationUnknown {
		updates["completed_at"] = now
	}
	result := s.WithContext(ctx).Model(&model.ApprovalOperation{}).
		Where("id = ? AND status = ? AND status NOT IN ?", id, from, []string{
			model.ApprovalOperationCompleted,
			model.ApprovalOperationFailed,
			model.ApprovalOperationUnknown,
		}).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (s *store) MarkStaleApprovalOperationUnknown(ctx context.Context, id string, staleBefore, now time.Time) (bool, error) {
	result := s.WithContext(ctx).Model(&model.ApprovalOperation{}).
		Where("id = ? AND status = ? AND updated_at < ?", id, model.ApprovalOperationRunning, staleBefore).
		Updates(map[string]interface{}{
			"status":       model.ApprovalOperationUnknown,
			"last_error":   "approval operation outcome is unknown",
			"completed_at": now,
			"updated_at":   now,
		})
	return result.RowsAffected == 1, result.Error
}
