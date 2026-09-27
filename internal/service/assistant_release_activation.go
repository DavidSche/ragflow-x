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

func (s *Service) ReconcileAssistantRelease(ctx context.Context, tenantID, userID, releaseID string, input OperationInput) (*model.AssistantRelease, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	fingerprint := releaseOperationFingerprint(model.OperationReconcile, releaseID, input)
	if existing, err := s.Store.GetReleaseOperationByIdempotencyKey(ctx, tenantID, operationKey(tenantID, input.IdempotencyKey)); err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	} else if existing != nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, httperr.New(409, 40930, "idempotency key reused with different request")
		}
		return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	}
	if err := s.validateSnapshot(ctx, tenantID, release); err != nil {
		return nil, err
	}
	if input.FencingToken != release.FencingToken {
		return nil, httperr.New(409, 40930, "stale release worker")
	}
	now := time.Now().UTC()
	status := model.ReconcileConsistent
	actualHash := release.DesiredStateHash
	actualJSON := release.DesiredStateJSON
	providerVersion := "verified"
	if err := s.verifyAssistantProvider(ctx, tenantID, release); err != nil {
		status = model.ReconcileDrifted
		actualHash = ""
		actualJSON = ""
		providerVersion = "unknown"
	}
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		if err := tx.ReconcileAssistantRelease(ctx, &model.AssistantRelease{
			ID: releaseID, TenantID: tenantID, ActualStateJSON: actualJSON,
			ActualStateHash: actualHash, ActualObservedAt: &now,
			ProviderVersion: providerVersion, ProviderRequestID: input.IdempotencyKey,
			ReconcileStatus: status,
		}); err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.CreateReleaseOperation(ctx, &model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			TargetReleaseID: releaseID, OperationType: model.OperationReconcile,
			IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
			RequestFingerprint: fingerprint,
			FencingToken:       release.FencingToken, OperationState: model.OperationSucceeded,
			CurrentStep: "Reconcile", StartedAt: &now, FinishedAt: &now,
			CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "idempotency key conflict")
		}
		return nil, httperr.New(502, 50200, "assistant release reconcile failed")
	}
	if status == model.ReconcileDrifted {
		return nil, httperr.New(422, 42230, "provider drift detected")
	}
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
}

func (s *Service) ActivateAssistantRelease(ctx context.Context, tenantID, userID, releaseID string, input ActivationInput) (*model.ReleaseOperation, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	fingerprint := releaseOperationFingerprint(model.OperationActivate, releaseID, input)
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
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	if release.ReleaseState != model.ReleaseVerified {
		return nil, httperr.New(409, 40930, "stale release worker")
	}
	if err := s.validateSnapshot(ctx, tenantID, release); err != nil {
		return nil, err
	}
	if release.ReconcileStatus != model.ReconcileConsistent || release.DesiredStateHash == "" || release.DesiredStateHash != release.ActualStateHash {
		return nil, httperr.New(422, 42230, "provider drift detected")
	}
	if health, err := s.Store.GetRuntimeHealth(ctx, tenantID, releaseID); err != nil {
		return nil, httperr.New(502, 50200, "runtime health lookup failed")
	} else if health != nil && health.Health == model.AssistantRuntimeHealthUnavailable {
		return nil, httperr.New(422, 42230, "assistant runtime is unavailable")
	}
	if input.Canary && (input.RolloutPercentage <= 0 || input.RolloutPercentage > 100) {
		return nil, httperr.New(422, 42231, "rollout percentage must be between 0 and 100")
	}
	assistantID := release.AssistantID
	targetState := model.ReleaseActive
	if input.Canary {
		targetState = model.ReleaseCanaryActive
	}
	operationType := model.OperationActivate
	now := time.Now().UTC()
	var operation model.ReleaseOperation
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		assistant, err := tx.GetAssistantForUpdate(ctx, tenantID, assistantID)
		if err != nil {
			return err
		}
		if assistant == nil {
			return gorm.ErrRecordNotFound
		}
		if assistant.LifecycleStatus != model.AssistantLifecycleEnabled {
			return httperr.New(409, 40930, "assistant is not enabled")
		}
		if input.FencingToken != release.FencingToken {
			return httperr.New(409, 40930, "stale release worker")
		}
		expectedCurrent := input.ExpectedCurrentReleaseID
		if expectedCurrent == "" {
			if assistant.CurrentAssistantReleaseID != "" {
				return httperr.New(409, 40930, "expected current release id is required")
			}
		}
		if assistant.CurrentAssistantReleaseID != expectedCurrent {
			return httperr.New(409, 40930, "stale release worker")
		}
		if !input.Canary {
			canaries, _, err := tx.ListAssistantReleases(ctx, tenantID, assistantID, model.ReleaseCanaryActive, 1, 1)
			if err != nil {
				return err
			}
			if len(canaries) > 0 {
				return httperr.New(409, 40930, "canary release must be retired before stable activation")
			}
			old, err := tx.GetAssistantRelease(ctx, tenantID, assistant.CurrentAssistantReleaseID)
			if err != nil {
				return err
			}
			if old != nil && old.ID != releaseID && old.ReleaseState == model.ReleaseActive {
				if err := tx.UpdateAssistantReleaseState(ctx, tenantID, old.ID, model.ReleaseActive, model.ReleaseRetired, 0); err != nil {
					return err
				}
			}
		}
		if input.Canary {
			stable, err := tx.GetAssistantRelease(ctx, tenantID, assistant.CurrentAssistantReleaseID)
			if err != nil {
				return err
			}
			if stable == nil || stable.ReleaseState != model.ReleaseActive {
				return httperr.New(409, 40930, "stable release is missing")
			}
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseVerified, targetState, 0); err != nil {
			return err
		}
		if !input.Canary {
			if err := tx.TransitionAssistantCurrentRelease(ctx, tenantID, assistantID, assistant.CurrentAssistantReleaseID, releaseID); err != nil {
				return err
			}
			if policy, err := tx.GetActiveRolloutPolicy(ctx, tenantID, assistantID); err != nil {
				return err
			} else if policy != nil {
				policy.Status = model.RolloutStageCancelled
				if err := tx.UpdateRolloutPolicy(ctx, policy, policy.OptimisticVersion); err != nil {
					return err
				}
			}
		} else {
			if policy, err := tx.GetActiveRolloutPolicy(ctx, tenantID, assistantID); err != nil {
				return err
			} else if policy != nil {
				if policy.CanaryReleaseID != releaseID {
					return httperr.New(409, 40930, "another canary rollout is active")
				}
				policy.Stage = model.RolloutStageRunning
				policy.Percentage = input.RolloutPercentage
				policy.Status = model.RolloutStageRunning
				if err := tx.UpdateRolloutPolicy(ctx, policy, policy.OptimisticVersion); err != nil {
					return err
				}
			} else {
				targeting := "{}"
				if input.RolloutTargeting != nil {
					encoded, err := canonicalJSON(input.RolloutTargeting)
					if err != nil {
						return err
					}
					targeting = encoded
				}
				if err := tx.CreateRolloutPolicy(ctx, &model.RolloutPolicy{
					ID: id.New(), TenantID: tenantID, AssistantID: assistantID,
					CanaryReleaseID: releaseID, Stage: model.RolloutStageRunning,
					Percentage: input.RolloutPercentage, TargetingJSON: targeting,
					RollbackCondition: input.RollbackCondition, Status: model.RolloutStageRunning,
					OptimisticVersion: 1, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					return err
				}
			}
		}
		startedAt := now
		finishedAt := now
		operation = model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			SourceReleaseID: assistant.CurrentAssistantReleaseID, TargetReleaseID: releaseID,
			OperationType: operationType, IdempotencyKey: operationKey(tenantID, input.IdempotencyKey),
			Attempt: 1, FencingToken: release.FencingToken,
			RequestFingerprint:       fingerprint,
			ExpectedCurrentReleaseID: assistant.CurrentAssistantReleaseID,
			OperationState:           model.OperationSucceeded, CurrentStep: "Activate",
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
		return nil, httperr.New(409, 40930, "release activation conflict")
	}
	return &operation, nil
}

func (s *Service) PromoteCanaryRelease(ctx context.Context, tenantID, userID, releaseID string, input OperationInput) (*model.ReleaseOperation, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	fingerprint := releaseOperationFingerprint(model.OperationPromoteCanary, releaseID, input)
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
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	if release.ReleaseState != model.ReleaseCanaryActive {
		return nil, httperr.New(409, 40930, "stale release worker")
	}
	if err := s.validateSnapshot(ctx, tenantID, release); err != nil {
		return nil, err
	}
	if release.ReconcileStatus != model.ReconcileConsistent || release.DesiredStateHash != release.ActualStateHash {
		return nil, httperr.New(422, 42230, "provider drift detected")
	}
	now := time.Now().UTC()
	var operation model.ReleaseOperation
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		assistant, err := tx.GetAssistantForUpdate(ctx, tenantID, release.AssistantID)
		if err != nil {
			return err
		}
		if assistant == nil {
			return gorm.ErrRecordNotFound
		}
		if input.FencingToken != release.FencingToken {
			return httperr.New(409, 40930, "stale release worker")
		}
		expectedCurrent := input.ExpectedCurrentReleaseID
		if expectedCurrent == "" {
			if assistant.CurrentAssistantReleaseID == "" {
				return httperr.New(409, 40930, "expected current release id is required")
			}
			expectedCurrent = assistant.CurrentAssistantReleaseID
		}
		if assistant.CurrentAssistantReleaseID != expectedCurrent || assistant.CurrentAssistantReleaseID == releaseID {
			return httperr.New(409, 40930, "stale release worker")
		}
		old, err := tx.GetAssistantRelease(ctx, tenantID, assistant.CurrentAssistantReleaseID)
		if err != nil {
			return err
		}
		if old == nil || old.ReleaseState != model.ReleaseActive {
			return httperr.New(409, 40930, "stable release is missing")
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, old.ID, model.ReleaseActive, model.ReleaseRetired, 0); err != nil {
			return err
		}
		if err := tx.UpdateAssistantReleaseState(ctx, tenantID, releaseID, model.ReleaseCanaryActive, model.ReleaseActive, 0); err != nil {
			return err
		}
		if err := tx.TransitionAssistantCurrentRelease(ctx, tenantID, release.AssistantID, assistant.CurrentAssistantReleaseID, releaseID); err != nil {
			return err
		}
		if policy, err := tx.GetActiveRolloutPolicy(ctx, tenantID, release.AssistantID); err != nil {
			return err
		} else if policy == nil || policy.CanaryReleaseID != releaseID {
			return httperr.New(409, 40930, "canary rollout policy is missing")
		} else {
			policy.Stage = model.RolloutStageCompleted
			policy.Percentage = 100
			policy.Status = model.RolloutStageCompleted
			if err := tx.UpdateRolloutPolicy(ctx, policy, policy.OptimisticVersion); err != nil {
				return err
			}
		}
		startedAt := now
		finishedAt := now
		operation = model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			SourceReleaseID: assistant.CurrentAssistantReleaseID, TargetReleaseID: releaseID,
			OperationType:  model.OperationPromoteCanary,
			IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
			RequestFingerprint: fingerprint,
			FencingToken:       release.FencingToken, ExpectedCurrentReleaseID: assistant.CurrentAssistantReleaseID,
			OperationState: model.OperationSucceeded, CurrentStep: "Activate",
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
		return nil, httperr.New(409, 40930, "canary promotion conflict")
	}
	return &operation, nil
}
