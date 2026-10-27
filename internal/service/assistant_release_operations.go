package service

import (
	"context"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"

	"gorm.io/gorm"
)

func (s *Service) recordOperationFailure(ctx context.Context, tenantID, userID, releaseID, key, fingerprint string, attempt int64, fencingToken int64, operationType, step, errorCode, message string) {
	now := time.Now().UTC()
	finishedAt := now
	_ = s.Store.WithinTransaction(ctx, func(tx Store) error {
		return tx.CreateReleaseOperation(ctx, &model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			TargetReleaseID: releaseID, OperationType: operationType,
			IdempotencyKey: operationKey(tenantID, key), Attempt: attempt,
			RequestFingerprint: fingerprint,
			FencingToken:       fencingToken, OperationState: model.OperationFailed,
			CurrentStep: step, ErrorCode: errorCode, ErrorMessage: message,
			StartedAt: &now, FinishedAt: &finishedAt, CreatedBy: userID,
			CreatedAt: now, UpdatedAt: now,
		})
	})
	s.emitAssistantReleaseFailure(ctx, tenantID, userID, releaseID, operationType, step, errorCode, message)
}

func (s *Service) emitAssistantReleaseFailure(ctx context.Context, tenantID, userID, releaseID, operation, step, errorCode, message string) {
	detail, _ := canonicalJSON(map[string]string{
		"operation": operation, "step": step,
		"error_code": errorCode, "message": message,
	})
	audit := &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "assistant_release.failed",
		Resource: "assistant-release", ResourceID: releaseID, Result: "FAILED",
		DetailJSON: detail,
	}
	if err := s.RecordAudit(ctx, audit); err != nil {
		logger.Warn("assistant release failure audit write failed", "error", err)
	}
	notify.Emit(ctx, notify.Event{
		ID: audit.ID, Title: "assistant release operation failed", Severity: "error",
		Type: "release.failed", TenantID: tenantID,
		Resource: "assistant-release", ResourceID: releaseID,
		Detail: step + " failed: " + message,
		Fields: map[string]string{
			"operation": operation, "step": step,
			"error_code": errorCode, "audit_id": audit.ID,
		},
	})
}

func (s *Service) TransitionAssistantRelease(ctx context.Context, tenantID, userID, releaseID, fromState, toState string, input OperationInput) (*model.AssistantRelease, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	if release.ReleaseState != fromState || !canTransitionRelease(fromState, toState) {
		return nil, httperr.New(409, 40930, "release state transition is not allowed")
	}
	fingerprint := canonicalJSONHash(map[string]any{
		"operation_type": model.OperationApply, "release_id": releaseID,
		"from_state": fromState, "to_state": toState,
		"expected_current_release_id": input.ExpectedCurrentReleaseID,
	})
	if existing, err := s.Store.GetReleaseOperationByIdempotencyKey(ctx, tenantID, operationKey(tenantID, input.IdempotencyKey)); err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	} else if existing != nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, httperr.New(409, 40930, "idempotency key reused with different request")
		}
		return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	}
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		if toState == model.ReleaseApproved {
			if err := validateReleaseApprovalChain(ctx, tx, release); err != nil {
				return err
			}
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, releaseID, fromState, toState, release.OptimisticVersion); err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.CreateReleaseOperation(ctx, &model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			TargetReleaseID: releaseID, OperationType: model.OperationApply,
			IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
			RequestFingerprint: fingerprint,
			FencingToken:       release.FencingToken + 1, OperationState: model.OperationSucceeded,
			CurrentStep: toState, StartedAt: &now, FinishedAt: &now,
			CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "idempotency key conflict")
		}
		return nil, httperr.New(409, 40930, "release state changed")
	}
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
}

func (s *Service) ApplyAssistantRelease(ctx context.Context, tenantID, releaseID, userID string, input OperationInput) (*model.AssistantRelease, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	fingerprint := releaseOperationFingerprint(model.OperationApply, releaseID, input)
	if existing, err := s.Store.GetReleaseOperationByIdempotencyKey(ctx, tenantID, operationKey(tenantID, input.IdempotencyKey)); err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	} else if existing != nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, httperr.New(409, 40930, "idempotency key reused with different request")
		}
		if existing.OperationState == model.OperationSucceeded {
			return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
		}
		return nil, httperr.New(409, 40930, "release operation already terminal")
	}
	if release.ReleaseState != model.ReleaseApproved {
		return nil, httperr.New(409, 40930, "release must be APPROVED before apply")
	}
	if err := s.validateSnapshot(ctx, tenantID, release); err != nil {
		return nil, err
	}
	if input.FencingToken != release.FencingToken {
		return nil, httperr.New(409, 40930, "stale release worker")
	}
	if err := s.applyAssistantProvider(ctx, tenantID, release); err != nil {
		s.recordOperationFailure(ctx, tenantID, userID, releaseID, input.IdempotencyKey, fingerprint, 1, release.FencingToken, model.OperationApply, "Apply", "PROVIDER_DRIFT", err.Error())
		if updateErr := s.Store.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseApproved, model.ReleaseApplyingFailed, 0); updateErr != nil {
			return nil, httperr.New(502, 50200, "release state persistence failed")
		}
		if updateErr := s.Store.ReconcileAssistantRelease(ctx, &model.AssistantRelease{
			ID: releaseID, TenantID: tenantID, ReconcileStatus: model.ReconcileUnknown,
			ActualStateHash: "", ActualObservedAt: nowPtr(), ProviderVersion: "unknown",
		}); updateErr != nil {
			return nil, httperr.New(502, 50200, "release reconcile persistence failed")
		}
		return nil, err
	}
	if err := s.verifyAssistantProvider(ctx, tenantID, release); err != nil {
		s.recordOperationFailure(ctx, tenantID, userID, releaseID, input.IdempotencyKey, fingerprint, 1, release.FencingToken, model.OperationApply, "Verify", "PROVIDER_DRIFT", err.Error())
		if updateErr := s.Store.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseApproved, model.ReleaseVerifyFailed, 0); updateErr != nil {
			return nil, httperr.New(502, 50200, "release state persistence failed")
		}
		if updateErr := s.Store.ReconcileAssistantRelease(ctx, &model.AssistantRelease{
			ID: releaseID, TenantID: tenantID, ReconcileStatus: model.ReconcileUnknown,
			ActualStateHash: "", ActualObservedAt: nowPtr(), ProviderVersion: "unknown",
		}); updateErr != nil {
			return nil, httperr.New(502, 50200, "release reconcile persistence failed")
		}
		return nil, err
	}
	now := time.Now().UTC()
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		if err := tx.ReconcileAssistantRelease(ctx, &model.AssistantRelease{
			ID: releaseID, TenantID: tenantID, ActualStateJSON: release.DesiredStateJSON,
			ActualStateHash: release.DesiredStateHash, ActualObservedAt: &now,
			ProviderVersion: "verified", ProviderRequestID: input.IdempotencyKey,
			ReconcileStatus: model.ReconcileConsistent,
		}); err != nil {
			return err
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseApproved, model.ReleaseVerified, release.OptimisticVersion); err != nil {
			return err
		}
		operation := &model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			TargetReleaseID: releaseID, OperationType: model.OperationApply,
			IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
			RequestFingerprint: fingerprint,
			FencingToken:       release.FencingToken + 1, OperationState: model.OperationSucceeded,
			CurrentStep: "Verify", ProviderOperationRefsJSON: "[]",
			StartedAt: &now, FinishedAt: &now, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
		}
		return tx.CreateReleaseOperation(ctx, operation)
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "idempotency key conflict")
		}
		return nil, httperr.New(502, 50200, "assistant release apply failed")
	}
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
}
