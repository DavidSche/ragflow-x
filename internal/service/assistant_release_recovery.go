package service

import (
	"context"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"

	"gorm.io/gorm"
)

func (s *Service) RollbackAssistantRelease(ctx context.Context, tenantID, userID, currentReleaseID, targetReleaseID string, input RollbackInput) (*model.ReleaseOperation, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	fingerprint := canonicalJSONHash(map[string]any{
		"operation_type": model.OperationRollback, "release_id": currentReleaseID,
		"target_release_id": targetReleaseID, "expected_current_release_id": currentReleaseID,
	})
	if existing, err := s.Store.GetReleaseOperationByIdempotencyKey(ctx, tenantID, operationKey(tenantID, input.IdempotencyKey)); err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	} else if existing != nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, httperr.New(409, 40930, "idempotency key reused with different request")
		}
		if existing.OperationState == model.OperationSucceeded {
			return existing, nil
		}
		return nil, httperr.New(409, 40930, "release operation already terminal")
	}
	current, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, currentReleaseID)
	if err != nil {
		return nil, err
	}
	target, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, targetReleaseID)
	if err != nil {
		return nil, err
	}
	if current.AssistantID != target.AssistantID || current.ReleaseState != model.ReleaseActive || currentReleaseID == targetReleaseID {
		return nil, httperr.New(409, 40930, "stale release worker")
	}
	if target.ReleaseState != model.ReleaseRetired && target.ReleaseState != model.ReleaseVerified {
		return nil, httperr.New(409, 40930, "rollback target is not safe")
	}
	if err := s.validateSnapshot(ctx, tenantID, current); err != nil {
		return nil, err
	}
	if err := s.validateSnapshot(ctx, tenantID, target); err != nil {
		return nil, err
	}
	if err := s.applyAssistantProvider(ctx, tenantID, target); err != nil {
		s.recordOperationFailure(ctx, tenantID, userID, currentReleaseID, input.IdempotencyKey, fingerprint, 1, current.FencingToken, model.OperationRollback, "Apply", "ROLLBACK_FAILED", err.Error())
		return nil, err
	}
	if err := s.verifyAssistantProvider(ctx, tenantID, target); err != nil {
		s.recordOperationFailure(ctx, tenantID, userID, currentReleaseID, input.IdempotencyKey, fingerprint, 1, current.FencingToken, model.OperationRollback, "Verify", "ROLLBACK_FAILED", err.Error())
		return nil, httperr.New(422, 42230, "rollback failed")
	}
	now := time.Now().UTC()
	var operation model.ReleaseOperation
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		assistant, err := tx.GetAssistantForUpdate(ctx, tenantID, current.AssistantID)
		if err != nil {
			return err
		}
		if assistant == nil {
			return gorm.ErrRecordNotFound
		}
		if assistant.CurrentAssistantReleaseID != currentReleaseID {
			return httperr.New(409, 40930, "stale release worker")
		}
		if input.FencingToken != current.FencingToken {
			return httperr.New(409, 40930, "stale release worker")
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, currentReleaseID, model.ReleaseActive, model.ReleaseRetired, 0); err != nil {
			return err
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, targetReleaseID, target.ReleaseState, model.ReleaseActive, 0); err != nil {
			return err
		}
		if err := tx.TransitionAssistantCurrentRelease(ctx, tenantID, current.AssistantID, currentReleaseID, targetReleaseID); err != nil {
			return err
		}
		startedAt := now
		finishedAt := now
		operation = model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: currentReleaseID,
			SourceReleaseID: currentReleaseID, TargetReleaseID: targetReleaseID,
			OperationType:  model.OperationRollback,
			IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
			RequestFingerprint: fingerprint,
			FencingToken:       current.FencingToken, ExpectedCurrentReleaseID: currentReleaseID,
			OperationState: model.OperationSucceeded, CurrentStep: "Retire",
			StartedAt: &startedAt, FinishedAt: &finishedAt, CreatedBy: userID,
			CreatedAt: now, UpdatedAt: now,
		}
		return tx.CreateReleaseOperation(ctx, &operation)
	})
	if err != nil {
		if httpErr, ok := err.(*httperr.Error); ok {
			return nil, httpErr
		}
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "idempotency key conflict")
		}
		return nil, httperr.New(422, 42230, "rollback failed")
	}
	return &operation, nil
}

func (s *Service) RecoverAssistantReleaseOperation(ctx context.Context, tenantID, userID, operationID string) (*model.ReleaseOperation, error) {
	operation, err := s.Store.GetReleaseOperation(ctx, tenantID, operationID)
	if err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	}
	if operation == nil {
		return nil, httperr.NotFound("release operation not found")
	}
	if operation.OperationState == model.OperationSucceeded || operation.OperationState == model.OperationCancelled {
		return operation, nil
	}
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, operation.ReleaseID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	switch operation.OperationType {
	case model.OperationApply:
		if release.ReleaseState == model.ReleaseVerified {
			operation.OperationState = model.OperationSucceeded
			operation.CurrentStep = "Verify"
			operation.FinishedAt = &now
		} else if release.ReleaseState == model.ReleaseApplying || release.ReleaseState == model.ReleaseVerifying {
			if err := s.verifyAssistantProvider(ctx, tenantID, release); err != nil {
				release.ReleaseState = model.ReleaseApplyingFailed
				operation.OperationState = model.OperationFailed
				operation.ErrorCode = "PROVIDER_DRIFT"
				operation.ErrorMessage = "crash recovery found provider drift"
			} else {
				if err := s.Store.ReconcileAssistantRelease(ctx, &model.AssistantRelease{
					ID: release.ID, TenantID: tenantID, ActualStateJSON: release.DesiredStateJSON,
					ActualStateHash: release.DesiredStateHash, ActualObservedAt: &now,
					ProviderVersion: "verified", ReconcileStatus: model.ReconcileConsistent,
				}); err != nil {
					return nil, httperr.New(502, 50200, "release reconcile persistence failed")
				}
				if err := s.Store.UpdateAssistantReleaseState(ctx, tenantID, release.ID, release.ReleaseState, model.ReleaseVerified, 0); err != nil {
					return nil, httperr.New(409, 40930, "release state changed during recovery")
				}
				operation.OperationState = model.OperationSucceeded
				operation.CurrentStep = "Verify"
				operation.FinishedAt = &now
			}
		} else {
			operation.OperationState = model.OperationFailed
			operation.ErrorCode = "CRASH_RECOVERY_INCOMPATIBLE"
			operation.ErrorMessage = "release is not in a recoverable apply state"
		}
	case model.OperationActivate, model.OperationPromoteCanary:
		assistant, err := s.Store.GetAssistant(ctx, tenantID, release.AssistantID)
		if err != nil {
			return nil, httperr.New(502, 50200, "assistant lookup failed")
		}
		if release.ReleaseState == model.ReleaseActive && assistant != nil && assistant.CurrentAssistantReleaseID == release.ID {
			operation.OperationState = model.OperationSucceeded
			operation.CurrentStep = "Activate"
			operation.FinishedAt = &now
		} else if release.ReleaseState == model.ReleaseCanaryActive && operation.OperationType == model.OperationPromoteCanary {
			operation.OperationState = model.OperationFailed
			operation.ErrorCode = "CRASH_RECOVERY_INCOMPATIBLE"
			operation.ErrorMessage = "canary promotion requires a new operation"
		} else {
			operation.OperationState = model.OperationFailed
			operation.ErrorCode = "CRASH_RECOVERY_INCOMPATIBLE"
			operation.ErrorMessage = "release is not in a recoverable activation state"
		}
	case model.OperationRollback:
		assistant, err := s.Store.GetAssistant(ctx, tenantID, release.AssistantID)
		if err != nil {
			return nil, httperr.New(502, 50200, "assistant lookup failed")
		}
		target, err := s.Store.GetAssistantRelease(ctx, tenantID, operation.TargetReleaseID)
		if err != nil {
			return nil, httperr.New(502, 50200, "rollback target lookup failed")
		}
		if target != nil && target.ReleaseState == model.ReleaseActive && assistant != nil && assistant.CurrentAssistantReleaseID == target.ID {
			operation.OperationState = model.OperationSucceeded
			operation.CurrentStep = "Retire"
			operation.FinishedAt = &now
		} else {
			operation.OperationState = model.OperationFailed
			operation.ErrorCode = "CRASH_RECOVERY_INCOMPATIBLE"
			operation.ErrorMessage = "rollback requires a new operation"
		}
	default:
		operation.OperationState = model.OperationFailed
		operation.ErrorCode = "CRASH_RECOVERY_INCOMPATIBLE"
		operation.ErrorMessage = "operation type is not recoverable"
	}
	operation.Attempt++
	operation.UpdatedAt = now
	if err := s.Store.UpdateReleaseOperation(ctx, operation); err != nil {
		return nil, httperr.New(502, 50200, "release operation recovery failed")
	}
	return operation, nil
}

func (s *Service) CompensateAssistantRelease(ctx context.Context, tenantID, userID, releaseID string, input OperationInput) (*model.AssistantRelease, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	fingerprint := releaseOperationFingerprint(model.OperationCompensate, releaseID, input)
	if existing, err := s.Store.GetReleaseOperationByIdempotencyKey(ctx, tenantID, operationKey(tenantID, input.IdempotencyKey)); err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	} else if existing != nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, httperr.New(409, 40930, "idempotency key reused with different request")
		}
		if existing.OperationState == model.OperationCompensated {
			return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
		}
		return nil, httperr.New(409, 40930, "compensation already terminal")
	}
	if release.ReleaseState != model.ReleaseApplyingFailed &&
		release.ReleaseState != model.ReleaseVerifyFailed &&
		release.ReleaseState != model.ReleaseCompensating {
		return nil, httperr.New(409, 40930, "release compensation is not allowed")
	}
	if input.FencingToken != release.FencingToken {
		return nil, httperr.New(409, 40930, "stale release worker")
	}
	now := time.Now().UTC()
	compensated := release.ReleaseState == model.ReleaseCompensating
	if !compensated {
		if err := s.Store.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseApplyingFailed, model.ReleaseCompensating, 0); err != nil {
			return nil, httperr.New(409, 40930, "release state changed")
		}
	}
	targetState := model.ReleaseCompensated
	operationState := model.OperationCompensated
	errorCode := ""
	if !compensated {
		previous, err := s.Store.GetAssistantRelease(ctx, tenantID, release.PreviousReleaseID)
		if err != nil {
			return nil, httperr.New(502, 50200, "release compensation lookup failed")
		}
		restore := release
		if previous != nil {
			restore = previous
		}
		if err := s.applyAssistantProvider(ctx, tenantID, restore); err != nil {
			targetState = model.ReleaseCompensationFailed
			operationState = model.OperationCompensationFailed
			errorCode = "COMPENSATION_FAILED"
		} else if err := s.verifyAssistantProvider(ctx, tenantID, restore); err != nil {
			targetState = model.ReleaseCompensationFailed
			operationState = model.OperationCompensationFailed
			errorCode = "COMPENSATION_FAILED"
		}
	}
	if err := s.Store.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseCompensating, targetState, 0); err != nil {
		return nil, httperr.New(409, 40930, "release compensation state changed")
	}
	finishedAt := now
	err = s.Store.CreateReleaseOperation(ctx, &model.ReleaseOperation{
		ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
		TargetReleaseID: releaseID, OperationType: model.OperationCompensate,
		IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
		RequestFingerprint: fingerprint,
		FencingToken:       release.FencingToken, OperationState: operationState,
		CurrentStep: "Compensate", ErrorCode: errorCode,
		StartedAt: &now, FinishedAt: &finishedAt, CreatedBy: userID,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "idempotency key conflict")
		}
		return nil, httperr.New(502, 50200, "compensation persistence failed")
	}
	if targetState == model.ReleaseCompensationFailed {
		s.emitAssistantReleaseFailure(ctx, tenantID, userID, releaseID, model.OperationCompensate, "Compensate", errorCode, "provider restore failed")
		return nil, httperr.New(422, 42230, "compensation failed")
	}
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
}
