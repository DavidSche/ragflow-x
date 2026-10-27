package service

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// RolloutPolicyInput drives POST/PUT /assistants/:id/rollout-policies
// (doc/124 §3.3). It only manages the canary targeting/traffic policy; the
// stable release fact stays on Assistant.current_assistant_release_id.
type RolloutPolicyInput struct {
	CanaryReleaseID   string         `json:"canary_release_id" binding:"required"`
	Percentage        float64        `json:"percentage"`
	Targeting         map[string]any `json:"targeting"`
	StartAt           *time.Time     `json:"start_at"`
	EndAt             *time.Time     `json:"end_at"`
	RollbackCondition string         `json:"rollback_condition"`
	OptimisticVersion int64          `json:"optimistic_version"`
}

// GetAssistantRolloutPolicies returns the active (PENDING/RUNNING) policy plus
// the policy history for the assistant.
func (s *Service) GetAssistantRolloutPolicies(ctx context.Context, tenantID, assistantID string, page, pageSize int) ([]model.RolloutPolicy, *model.RolloutPolicy, int64, error) {
	if _, err := s.getAssistantOrNotFound(ctx, tenantID, assistantID); err != nil {
		return nil, nil, 0, err
	}
	items, total, err := s.Store.ListRolloutPolicies(ctx, tenantID, assistantID, page, pageSize)
	if err != nil {
		return nil, nil, 0, httperr.New(502, 50200, "rollout policy list failed")
	}
	active, err := s.Store.GetActiveRolloutPolicy(ctx, tenantID, assistantID)
	if err != nil {
		return nil, nil, 0, httperr.New(502, 50200, "rollout policy lookup failed")
	}
	return items, active, total, nil
}

// CreateAssistantRolloutPolicy pre-creates a PENDING canary policy. At most
// one PENDING/RUNNING policy may exist per assistant (doc/124 §3.3 rule 3).
func (s *Service) CreateAssistantRolloutPolicy(ctx context.Context, tenantID, userID, assistantID string, input RolloutPolicyInput) (*model.RolloutPolicy, error) {
	if _, err := s.getAssistantOrNotFound(ctx, tenantID, assistantID); err != nil {
		return nil, err
	}
	if _, err := s.validateRolloutPolicyInput(ctx, tenantID, assistantID, input); err != nil {
		return nil, err
	}
	if active, err := s.Store.GetActiveRolloutPolicy(ctx, tenantID, assistantID); err != nil {
		return nil, httperr.New(502, 50200, "rollout policy lookup failed")
	} else if active != nil {
		return nil, httperr.New(409, 40930, "another rollout policy is active")
	}
	targeting := "{}"
	if input.Targeting != nil {
		encoded, err := canonicalJSON(input.Targeting)
		if err != nil {
			return nil, httperr.New(422, 42231, "targeting must be valid JSON")
		}
		targeting = encoded
	}
	now := time.Now().UTC()
	created := &model.RolloutPolicy{
		ID: id.New(), TenantID: tenantID, AssistantID: assistantID,
		CanaryReleaseID: input.CanaryReleaseID, Stage: model.RolloutStagePending,
		Percentage: input.Percentage, TargetingJSON: targeting,
		StartAt: input.StartAt, EndAt: input.EndAt,
		RollbackCondition: input.RollbackCondition, Status: model.RolloutStagePending,
		OptimisticVersion: 1, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateRolloutPolicy(ctx, created); err != nil {
		return nil, httperr.New(502, 50200, "rollout policy persistence failed")
	}
	return created, nil
}

// UpdateAssistantRolloutPolicy edits a PENDING policy with optimistic locking.
// RUNNING/terminal policies are immutable (doc/124 §3.3 rule 2).
func (s *Service) UpdateAssistantRolloutPolicy(ctx context.Context, tenantID, userID, assistantID, policyID string, input RolloutPolicyInput) (*model.RolloutPolicy, error) {
	if _, err := s.getAssistantOrNotFound(ctx, tenantID, assistantID); err != nil {
		return nil, err
	}
	existing, err := s.Store.GetRolloutPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, httperr.New(502, 50200, "rollout policy lookup failed")
	}
	if existing == nil || existing.AssistantID != assistantID {
		return nil, httperr.NotFound("rollout policy not found")
	}
	if existing.Stage != model.RolloutStagePending {
		return nil, httperr.New(409, 40930, "rollout policy is not editable in stage "+existing.Stage)
	}
	if input.OptimisticVersion <= 0 || input.OptimisticVersion != existing.OptimisticVersion {
		return nil, httperr.New(409, 40930, "stale rollout policy version")
	}
	policy, err := s.validateRolloutPolicyInput(ctx, tenantID, assistantID, input)
	if err != nil {
		return nil, err
	}
	targeting := existing.TargetingJSON
	if input.Targeting != nil {
		encoded, err := canonicalJSON(input.Targeting)
		if err != nil {
			return nil, httperr.New(422, 42231, "targeting must be valid JSON")
		}
		targeting = encoded
	}
	existing.CanaryReleaseID = policy.CanaryReleaseID
	existing.Percentage = policy.Percentage
	existing.TargetingJSON = targeting
	existing.StartAt = input.StartAt
	existing.EndAt = input.EndAt
	existing.RollbackCondition = input.RollbackCondition
	if err := s.Store.UpdateRolloutPolicy(ctx, existing, input.OptimisticVersion); err != nil {
		return nil, httperr.New(409, 40930, "rollout policy version conflict")
	}
	return s.Store.GetRolloutPolicy(ctx, tenantID, policyID)
}

// validateRolloutPolicyInput applies the fail-closed checks from doc/124 §3.3.
func (s *Service) validateRolloutPolicyInput(ctx context.Context, tenantID, assistantID string, input RolloutPolicyInput) (*RolloutPolicyInput, error) {
	if input.CanaryReleaseID == "" {
		return nil, httperr.New(422, 42231, "canary_release_id is required")
	}
	if input.Percentage <= 0 || input.Percentage > 100 {
		return nil, httperr.New(422, 42231, "rollout percentage must be between 0 and 100")
	}
	if input.StartAt != nil && input.EndAt != nil && !input.StartAt.Before(*input.EndAt) {
		return nil, httperr.New(422, 42231, "start_at must be before end_at")
	}
	release, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, input.CanaryReleaseID)
	if err != nil {
		return nil, err
	}
	if release.AssistantID != assistantID {
		return nil, httperr.New(422, 42230, "canary release belongs to another assistant")
	}
	return &input, nil
}
