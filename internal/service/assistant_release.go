package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type Store = repository.Store

type CapabilityBindingInput struct {
	BindingID         string `json:"binding_id"`
	Capability        string `json:"capability"`
	Adapter           string `json:"adapter"`
	TargetType        string `json:"target_type"`
	TargetID          string `json:"target_id"`
	TargetVersion     string `json:"target_version"`
	RGXResourceID     string `json:"rgx_resource_id"`
	OwnershipVerified bool   `json:"ownership_verified"`
	PolicyID          string `json:"policy_id"`
	ConfigJSON        string `json:"config_json"`
	Status            string `json:"status"`
}

type DatasetBindingInput struct {
	BindingID         string `json:"binding_id"`
	KnowledgeDomainID string `json:"knowledge_domain_id"`
	DatasetID         string `json:"dataset_id"`
	DatasetVersion    string `json:"dataset_version"`
	FreshnessPolicy   string `json:"freshness_policy"`
	SensitivityLevel  string `json:"sensitivity_level"`
	ConfigJSON        string `json:"config_json"`
	Status            string `json:"status"`
}

type ModelRouteBindingInput struct {
	BindingID          string  `json:"binding_id"`
	ProviderID         string  `json:"provider_id"`
	ModelID            string  `json:"model_id"`
	ModelVersion       string  `json:"model_version"`
	RouteWeight        float64 `json:"route_weight"`
	FallbackModelsJSON string  `json:"fallback_models_json"`
	FallbackPolicyID   string  `json:"fallback_policy_id"`
	ConfigJSON         string  `json:"config_json"`
	Status             string  `json:"status"`
}

type ToolBindingInput struct {
	BindingID        string `json:"binding_id"`
	ToolID           string `json:"tool_id"`
	ToolVersion      string `json:"tool_version"`
	RiskLevel        string `json:"risk_level"`
	ApprovalPolicyID string `json:"approval_policy_id"`
	ConfigJSON       string `json:"config_json"`
	Status           string `json:"status"`
}

type CreateAssistantReleaseInput struct {
	AssistantID          string                   `json:"assistant_id"`
	ProjectID            string                   `json:"project_id"`
	TemplateInstanceID   string                   `json:"template_instance_id"`
	ScenarioTemplateID   string                   `json:"scenario_template_id"`
	TemplateVersionID    string                   `json:"template_version_id"`
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	OwnerID              string                   `json:"owner_id"`
	RiskLevel            string                   `json:"risk_level"`
	ScenarioPackSchema   string                   `json:"scenario_pack_schema"`
	ScenarioPackPayload  string                   `json:"scenario_pack_payload"`
	ExecutionContract    string                   `json:"execution_contract"`
	PolicyVersion        string                   `json:"policy_version"`
	PolicyJSON           string                   `json:"policy_json"`
	RuntimeProfile       string                   `json:"runtime_profile"`
	DesiredState         string                   `json:"desired_state"`
	DesiredTargetType    string                   `json:"desired_target_type"`
	DesiredTargetID      string                   `json:"desired_target_id"`
	DesiredTargetVersion string                   `json:"desired_target_version"`
	CapabilityBindings   []CapabilityBindingInput `json:"capability_bindings"`
	DatasetBindings      []DatasetBindingInput    `json:"dataset_bindings"`
	ModelRouteBindings   []ModelRouteBindingInput `json:"model_route_bindings"`
	ToolBindings         []ToolBindingInput       `json:"tool_bindings"`
	EvidenceBundleID     string                   `json:"evidence_bundle_id"`
	GateDecisionID       string                   `json:"gate_decision_id"`
	GateResult           string                   `json:"gate_result"`
	ApprovalID           string                   `json:"approval_id"`
	IdempotencyKey       string                   `json:"idempotency_key"`
}

type OperationInput struct {
	IdempotencyKey           string `json:"idempotency_key"`
	FencingToken             int64  `json:"fencing_token"`
	ExpectedCurrentReleaseID string `json:"expected_current_release_id"`
}

type ActivationInput struct {
	IdempotencyKey           string         `json:"idempotency_key"`
	FencingToken             int64          `json:"fencing_token"`
	ExpectedCurrentReleaseID string         `json:"expected_current_release_id"`
	Canary                   bool           `json:"canary"`
	RolloutPercentage        float64        `json:"rollout_percentage"`
	RolloutTargeting         map[string]any `json:"rollout_targeting"`
	RollbackCondition        string         `json:"rollback_condition"`
}

type RollbackInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	FencingToken   int64  `json:"fencing_token"`
}

type AssistantPage struct {
	Items []model.Assistant `json:"items"`
	Total int64             `json:"total"`
}

type AssistantReleasePage struct {
	Items []model.AssistantRelease `json:"items"`
	Total int64                    `json:"total"`
}

type ReleaseOperationPage struct {
	Items []model.ReleaseOperation `json:"items"`
	Total int64                    `json:"total"`
}

var assistantReleaseTransitions = map[string][]string{
	model.ReleaseDraft:          {model.ReleaseValidating},
	model.ReleaseValidating:     {model.ReleaseSnapshotted},
	model.ReleaseSnapshotted:    {model.ReleaseEvaluating},
	model.ReleaseEvaluating:     {model.ReleaseGated},
	model.ReleaseGated:          {model.ReleaseApproved},
	model.ReleaseApproved:       {model.ReleaseApplying, model.ReleaseVerifying, model.ReleaseVerified},
	model.ReleaseApplying:       {model.ReleaseVerifyFailed, model.ReleaseApplyingFailed, model.ReleaseVerifying, model.ReleaseVerified},
	model.ReleaseVerifying:      {model.ReleaseVerified, model.ReleaseVerifyFailed},
	model.ReleaseVerifyFailed:   {model.ReleaseCompensating},
	model.ReleaseVerified:       {model.ReleaseCanaryActive, model.ReleaseActive, model.ReleaseRetired},
	model.ReleaseCanaryActive:   {model.ReleaseActive, model.ReleaseRetired},
	model.ReleaseActive:         {model.ReleaseRollingBack, model.ReleaseRetired},
	model.ReleaseApplyingFailed: {model.ReleaseCompensating},
	model.ReleaseCompensating:   {model.ReleaseCompensated, model.ReleaseCompensationFailed},
}

func canTransitionRelease(from, to string) bool {
	for _, next := range assistantReleaseTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

func validateReleaseApprovalChain(ctx context.Context, tx Store, release *model.AssistantRelease) error {
	if release.EvidenceBundleID == "" || release.GateDecisionID == "" || release.ApprovalID == "" {
		return httperr.New(422, 42231, "evidence bundle, gate decision and approval are required")
	}
	bundle, err := tx.GetEvidenceBundle(ctx, release.TenantID, release.EvidenceBundleID)
	if err != nil {
		return httperr.New(502, 50200, "release evidence lookup failed")
	}
	gate, err := tx.GetGateDecision(ctx, release.TenantID, release.GateDecisionID)
	if err != nil {
		return httperr.New(502, 50200, "release gate lookup failed")
	}
	approval, err := tx.GetApproval(ctx, release.TenantID, release.ApprovalID)
	if err != nil {
		return httperr.New(502, 50200, "release approval lookup failed")
	}
	if bundle == nil || gate == nil || approval == nil {
		return httperr.New(422, 42230, "release approval evidence is missing")
	}
	if gate.EvidenceBundleID != release.EvidenceBundleID || !gate.ActiveGate ||
		(gate.Decision != model.GateDecisionPass && gate.Decision != model.GateDecisionPassWithWarning) {
		return httperr.New(422, 42230, "release gate evidence is invalid")
	}
	if approval.Status != model.ApprovalStatusApproved && approval.Status != model.ApprovalStatusCompleted {
		return httperr.New(422, 42230, "release approval is not approved")
	}
	return nil
}

func hashCanonical(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func operationKey(tenantID, key string) string {
	if key == "" {
		return ""
	}
	return tenantID + ":" + key
}

func releaseOperationFingerprint(operationType, releaseID string, input any) string {
	payload := map[string]any{
		"operation_type": operationType,
		"release_id":     releaseID,
	}
	switch typed := input.(type) {
	case OperationInput:
		payload["expected_current_release_id"] = typed.ExpectedCurrentReleaseID
	case ActivationInput:
		payload["expected_current_release_id"] = typed.ExpectedCurrentReleaseID
		payload["canary"] = typed.Canary
		payload["rollout_percentage"] = typed.RolloutPercentage
		payload["rollout_targeting"] = typed.RolloutTargeting
		payload["rollback_condition"] = typed.RollbackCondition
	case RollbackInput:
	}
	return canonicalJSONHash(payload)
}

func nowPtr() *time.Time {
	now := time.Now().UTC()
	return &now
}

func (s *Service) validateProject(ctx context.Context, tenantID, projectID string) error {
	if projectID == "" {
		return httperr.New(422, 42200, "project_id is required")
	}
	project, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return httperr.New(502, 50200, "project lookup failed")
	}
	if project == nil {
		return httperr.New(404, 40400, "project not found")
	}
	return nil
}

func (s *Service) getAssistantReleaseOrNotFound(ctx context.Context, tenantID, releaseID string) (*model.AssistantRelease, error) {
	if releaseID == "" {
		return nil, httperr.New(422, 42200, "release_id is required")
	}
	release, err := s.Store.GetAssistantRelease(ctx, tenantID, releaseID)
	if err != nil {
		return nil, httperr.New(502, 50200, "assistant release lookup failed")
	}
	if release == nil {
		return nil, httperr.NotFound("assistant release not found")
	}
	return release, nil
}

func (s *Service) getAssistantOrNotFound(ctx context.Context, tenantID, assistantID string) (*model.Assistant, error) {
	if assistantID == "" {
		return nil, httperr.New(422, 42200, "assistant_id is required")
	}
	assistant, err := s.Store.GetAssistant(ctx, tenantID, assistantID)
	if err != nil {
		return nil, httperr.New(502, 50200, "assistant lookup failed")
	}
	if assistant == nil {
		return nil, httperr.NotFound("assistant not found")
	}
	return assistant, nil
}

func (s *Service) CreateAssistantRelease(ctx context.Context, tenantID, userID string, input CreateAssistantReleaseInput) (*model.AssistantRelease, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	if err := s.validateProject(ctx, tenantID, input.ProjectID); err != nil {
		return nil, err
	}
	if input.Name == "" || input.OwnerID == "" || input.TemplateVersionID == "" || input.ExecutionContract == "" || input.PolicyVersion == "" {
		return nil, httperr.New(422, 42231, "name, owner, template version, execution contract and policy version are required")
	}
	if len(input.CapabilityBindings) == 0 {
		return nil, httperr.New(422, 42231, "at least one capability binding is required")
	}
	fingerprint := canonicalJSONHash(map[string]any{"operation_type": model.OperationCreate, "input": input})
	if existingOperation, err := s.Store.GetReleaseOperationByIdempotencyKey(ctx, tenantID, operationKey(tenantID, input.IdempotencyKey)); err != nil {
		return nil, httperr.New(502, 50200, "release operation lookup failed")
	} else if existingOperation != nil {
		if existingOperation.RequestFingerprint != fingerprint {
			return nil, httperr.New(409, 40930, "idempotency key reused with different request")
		}
		return s.getAssistantReleaseOrNotFound(ctx, tenantID, existingOperation.ReleaseID)
	}
	checkedCapabilities := make(map[string]bool, len(input.CapabilityBindings))
	for _, binding := range input.CapabilityBindings {
		if binding.Capability == "" || checkedCapabilities[binding.Capability] {
			continue
		}
		if _, err := s.EnsureCapabilityCompatibility(ctx, binding.Capability); err != nil {
			return nil, err
		}
		checkedCapabilities[binding.Capability] = true
	}
	var assistant *model.Assistant
	if input.AssistantID != "" {
		var err error
		assistant, err = s.getAssistantOrNotFound(ctx, tenantID, input.AssistantID)
		if err != nil {
			return nil, err
		}
		if assistant.ProjectID != input.ProjectID {
			return nil, httperr.New(409, 40930, "assistant project mismatch")
		}
	} else if input.DesiredTargetID == "" || input.DesiredTargetType == "" {
		return nil, httperr.New(422, 42231, "capability target is required")
	}
	desiredState := input.DesiredState
	if desiredState == "" {
		desiredState = "{}"
	}
	if _, err := canonicalJSON(json.RawMessage(desiredState)); err != nil {
		return nil, httperr.New(422, 42231, "desired_state must be valid JSON")
	}
	executionContract, err := canonicalJSON(json.RawMessage(input.ExecutionContract))
	if err != nil {
		return nil, httperr.New(422, 42231, "execution contract must be valid JSON")
	}
	scenarioPayload, err := canonicalJSON(json.RawMessage(input.ScenarioPackPayload))
	if err != nil {
		return nil, httperr.New(422, 42231, "scenario pack payload must be valid JSON")
	}
	policyJSON, err := canonicalJSON(json.RawMessage(input.PolicyJSON))
	if err != nil {
		return nil, httperr.New(422, 42231, "policy must be valid JSON")
	}
	runtimeProfile, err := canonicalJSON(json.RawMessage(input.RuntimeProfile))
	if err != nil {
		return nil, httperr.New(422, 42231, "runtime profile must be valid JSON")
	}
	nextVersion := int64(1)
	if assistant != nil {
		releases, total, err := s.Store.ListAssistantReleases(ctx, tenantID, assistant.ID, "", 1, 1)
		if err != nil {
			return nil, httperr.New(502, 50200, "assistant release lookup failed")
		}
		if total > 0 {
			nextVersion = releases[0].ReleaseVersion + 1
		}
	}
	scenarioPackVersionID := id.New()
	assistantVersionID := id.New()
	releaseID := id.New()
	runtimeProfileID := id.New()
	policySnapshotID := id.New()
	manifestID := id.New()
	now := time.Now().UTC()
	manifest := map[string]any{
		"schema_version":           "1",
		"assistant_version_id":     assistantVersionID,
		"scenario_pack_version_id": scenarioPackVersionID,
		"runtime_profile_snapshot": runtimeProfileID,
		"policy_snapshot":          policySnapshotID,
		"evidence_bundle_id":       input.EvidenceBundleID,
		"capability_binding_ids":   input.CapabilityBindings,
		"dataset_binding_ids":      input.DatasetBindings,
		"model_route_binding_ids":  input.ModelRouteBindings,
		"tool_binding_ids":         input.ToolBindings,
		"snapshot_created_at":      now,
		"complete":                 true,
	}
	manifestJSON, err := canonicalJSON(manifest)
	if err != nil {
		return nil, httperr.New(502, 50200, "snapshot manifest serialization failed")
	}
	desiredHash := hashCanonical(desiredState)
	if input.RiskLevel == "" {
		input.RiskLevel = "low"
	}
	err = s.Store.WithinTransaction(ctx, func(tx Store) error {
		if assistant == nil {
			assistant = &model.Assistant{
				ID: id.New(), TenantID: tenantID, ProjectID: input.ProjectID,
				ScenarioTemplateID: input.ScenarioTemplateID, Name: input.Name,
				Description: input.Description, OwnerID: input.OwnerID,
				RiskLevel: input.RiskLevel, LifecycleStatus: model.AssistantLifecycleEnabled,
			}
			if err := tx.CreateAssistant(ctx, assistant); err != nil {
				return err
			}
		}
		if input.TemplateInstanceID == "" {
			input.TemplateInstanceID = id.New()
			if err := tx.CreateTemplateInstance(ctx, &model.TemplateInstance{
				ID: input.TemplateInstanceID, TenantID: tenantID, ProjectID: input.ProjectID,
				TemplateID: input.ScenarioTemplateID, TemplateVersionID: input.TemplateVersionID,
				AssistantID: assistant.ID, GovernanceStatus: "DRAFT", OwnerID: input.OwnerID,
			}); err != nil {
				return err
			}
		} else if instance, err := tx.GetTemplateInstance(ctx, tenantID, input.TemplateInstanceID); err != nil || instance == nil {
			return fmt.Errorf("template instance invalid: %w", err)
		} else if instance.AssistantID == "" {
			// Pre-created by the unified instantiation flow (doc/124 §2.2):
			// bind the instance to its assistant.
			if err := tx.BindTemplateInstanceAssistant(ctx, tenantID, instance.ID, assistant.ID); err != nil {
				return err
			}
		}
		if err := tx.CreateScenarioPackVersion(ctx, &model.ScenarioPackVersion{
			ID: scenarioPackVersionID, TenantID: tenantID, TemplateID: input.ScenarioTemplateID,
			TemplateVersionID: input.TemplateVersionID, SchemaVersion: input.ScenarioPackSchema,
			MinRuntimeSchema: "1", MigrationVersion: "1", CompatibilityLevel: "backward_compatible",
			PayloadJSON: scenarioPayload, PayloadHash: hashCanonical(scenarioPayload),
			HashAlgorithm: model.HashAlgorithmSHA256, CreatedBy: userID, CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.CreateAssistantVersion(ctx, &model.AssistantVersion{
			ID: assistantVersionID, TenantID: tenantID, AssistantID: assistant.ID,
			Version: nextVersion, TemplateVersionID: input.TemplateVersionID,
			ScenarioPackVersionID: scenarioPackVersionID, ExecutionContractSchema: "1",
			ExecutionContractJSON: executionContract, ExecutionContractHash: hashCanonical(executionContract),
			PolicyVersion: input.PolicyVersion, RiskLevel: input.RiskLevel, CreatedBy: userID, CreatedAt: now,
		}); err != nil {
			return err
		}
		release := &model.AssistantRelease{
			ID: releaseID, TenantID: tenantID, AssistantID: assistant.ID,
			AssistantVersionID: assistantVersionID, ReleaseVersion: nextVersion,
			ReleaseState: model.ReleaseDraft, DesiredStateJSON: desiredState,
			DesiredStateHash: desiredHash, ReconcileStatus: model.ReconcileUnknown,
			SnapshotManifestID: manifestID, PreviousReleaseID: assistant.CurrentAssistantReleaseID,
			FencingToken: 0, OptimisticVersion: 1, EvidenceBundleID: input.EvidenceBundleID,
			GateDecisionID: input.GateDecisionID,
			GateResult:     input.GateResult, ApprovalID: input.ApprovalID,
			CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.CreateAssistantRelease(ctx, release); err != nil {
			return err
		}
		if err := tx.CreateRuntimeProfileSnapshot(ctx, &model.RuntimeProfileSnapshot{
			ID: runtimeProfileID, TenantID: tenantID, AssistantReleaseID: releaseID,
			SchemaVersion: "1", ProfileJSON: runtimeProfile, ProfileHash: hashCanonical(runtimeProfile), CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.CreatePolicySnapshot(ctx, &model.PolicySnapshot{
			ID: policySnapshotID, TenantID: tenantID, AssistantReleaseID: releaseID,
			PolicyVersion: input.PolicyVersion, PolicyJSON: policyJSON,
			PolicyHash: hashCanonical(policyJSON), CreatedAt: now,
		}); err != nil {
			return err
		}
		for _, binding := range input.CapabilityBindings {
			if binding.Capability == "" || binding.TargetType == "" || binding.TargetID == "" {
				return httperr.New(422, 42231, "capability binding is incomplete")
			}
			switch binding.TargetType {
			case "chat":
				shadow, err := tx.GetChatShadow(ctx, tenantID, binding.TargetID, false)
				if err != nil {
					return err
				}
				if shadow == nil {
					return httperr.New(422, 42230, "provider ownership is unverified")
				}
			case "agent":
				shadow, err := tx.GetAgentShadow(ctx, tenantID, binding.TargetID, false)
				if err != nil {
					return err
				}
				if shadow == nil {
					return httperr.New(422, 42230, "provider ownership is unverified")
				}
			case "search_app":
				shadow, err := tx.GetSearchAppShadow(ctx, tenantID, binding.TargetID, false)
				if err != nil {
					return err
				}
				if shadow == nil {
					return httperr.New(422, 42230, "provider ownership is unverified")
				}
			default:
				return httperr.New(422, 42231, "unsupported provider target type")
			}
			if err := tx.CreateCapabilityBindingVersion(ctx, &model.CapabilityBindingVersion{
				ID: id.New(), TenantID: tenantID, AssistantReleaseID: releaseID,
				BindingID: binding.BindingID, Version: 1, Capability: binding.Capability,
				Adapter: binding.Adapter, TargetType: binding.TargetType, TargetID: binding.TargetID,
				TargetVersion: binding.TargetVersion, RGXResourceID: binding.RGXResourceID,
				ProjectID: input.ProjectID, OwnershipVerified: true,
				PolicyID: binding.PolicyID, ConfigJSON: binding.ConfigJSON,
				Status: binding.Status, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		for _, binding := range input.DatasetBindings {
			if binding.DatasetID == "" {
				return httperr.New(422, 42231, "dataset binding is incomplete")
			}
			link, err := tx.GetDatasetLink(ctx, tenantID, binding.DatasetID)
			if err != nil {
				return err
			}
			if link == nil {
				return httperr.New(422, 42230, "provider ownership is unverified")
			}
			if err := tx.CreateDatasetBindingVersion(ctx, &model.DatasetBindingVersion{
				ID: id.New(), TenantID: tenantID, AssistantReleaseID: releaseID,
				BindingID: binding.BindingID, Version: 1, KnowledgeDomainID: binding.KnowledgeDomainID,
				DatasetID: binding.DatasetID, DatasetVersion: binding.DatasetVersion,
				FreshnessPolicy: binding.FreshnessPolicy, SensitivityLevel: binding.SensitivityLevel,
				ConfigJSON: binding.ConfigJSON, Status: binding.Status, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		for _, binding := range input.ModelRouteBindings {
			if binding.ModelID == "" || binding.ProviderID == "" {
				return httperr.New(422, 42231, "model route binding is incomplete")
			}
			if err := tx.CreateModelRouteBindingVersion(ctx, &model.ModelRouteBindingVersion{
				ID: id.New(), TenantID: tenantID, AssistantReleaseID: releaseID,
				BindingID: binding.BindingID, Version: 1, ProviderID: binding.ProviderID,
				ModelID: binding.ModelID, ModelVersion: binding.ModelVersion,
				RouteWeight: binding.RouteWeight, FallbackModelsJSON: binding.FallbackModelsJSON,
				FallbackPolicyID: binding.FallbackPolicyID, ConfigJSON: binding.ConfigJSON,
				Status: binding.Status, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		for _, binding := range input.ToolBindings {
			if err := tx.CreateToolBindingVersion(ctx, &model.ToolBindingVersion{
				ID: id.New(), TenantID: tenantID, AssistantReleaseID: releaseID,
				BindingID: binding.BindingID, Version: 1, ToolID: binding.ToolID,
				ToolVersion: binding.ToolVersion, RiskLevel: binding.RiskLevel,
				ApprovalPolicyID: binding.ApprovalPolicyID, ConfigJSON: binding.ConfigJSON,
				Status: binding.Status, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		if err := tx.CreateSnapshotManifest(ctx, &model.SnapshotManifest{
			ID: manifestID, TenantID: tenantID, AssistantReleaseID: releaseID,
			SchemaVersion: "1", AssistantVersionID: assistantVersionID,
			ScenarioPackVersionID: scenarioPackVersionID, ManifestJSON: manifestJSON,
			SnapshotHash: hashCanonical(manifestJSON), Complete: true, CreatedAt: now,
		}); err != nil {
			return err
		}
		startedAt := now
		finishedAt := now
		return tx.CreateReleaseOperation(ctx, &model.ReleaseOperation{
			ID: id.New(), TenantID: tenantID, ReleaseID: releaseID,
			TargetReleaseID: releaseID, OperationType: model.OperationCreate,
			IdempotencyKey: operationKey(tenantID, input.IdempotencyKey), Attempt: 1,
			RequestFingerprint: fingerprint,
			OperationState:     model.OperationSucceeded, CurrentStep: "Validate",
			StartedAt: &startedAt, FinishedAt: &finishedAt, CreatedBy: userID,
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "idempotency key conflict")
		}
		return nil, httperr.New(502, 50200, "assistant release creation failed")
	}
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
}

func (s *Service) ListAssistants(ctx context.Context, tenantID, projectID, lifecycle string, page, pageSize int) (AssistantPage, error) {
	items, total, err := s.Store.ListAssistants(ctx, tenantID, projectID, lifecycle, page, pageSize)
	if err != nil {
		return AssistantPage{}, httperr.New(502, 50200, "assistant list failed")
	}
	return AssistantPage{Items: items, Total: total}, nil
}

func (s *Service) GetAssistant(ctx context.Context, tenantID, assistantID string) (*model.Assistant, error) {
	return s.getAssistantOrNotFound(ctx, tenantID, assistantID)
}

func (s *Service) ListAssistantReleases(ctx context.Context, tenantID, assistantID, state string, page, pageSize int) (AssistantReleasePage, error) {
	items, total, err := s.Store.ListAssistantReleases(ctx, tenantID, assistantID, state, page, pageSize)
	if err != nil {
		return AssistantReleasePage{}, httperr.New(502, 50200, "assistant release list failed")
	}
	return AssistantReleasePage{Items: items, Total: total}, nil
}

func (s *Service) GetAssistantRelease(ctx context.Context, tenantID, releaseID string) (*model.AssistantRelease, error) {
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, releaseID)
}

func (s *Service) GetSnapshotManifest(ctx context.Context, tenantID, manifestID string) (*model.SnapshotManifest, error) {
	if manifestID == "" {
		return nil, httperr.New(422, 42200, "snapshot manifest id is required")
	}
	manifest, err := s.Store.GetSnapshotManifest(ctx, tenantID, manifestID)
	if err != nil {
		return nil, httperr.New(502, 50200, "snapshot manifest lookup failed")
	}
	if manifest == nil {
		return nil, httperr.NotFound("snapshot manifest not found")
	}
	return manifest, nil
}

func (s *Service) ListReleaseOperations(ctx context.Context, tenantID, releaseID, operationType, state string, page, pageSize int) (ReleaseOperationPage, error) {
	items, total, err := s.Store.ListReleaseOperations(ctx, tenantID, releaseID, operationType, state, page, pageSize)
	if err != nil {
		return ReleaseOperationPage{}, httperr.New(502, 50200, "release operation list failed")
	}
	return ReleaseOperationPage{Items: items, Total: total}, nil
}

func (s *Service) GetRuntimeHealth(ctx context.Context, tenantID, releaseID string) (*model.RuntimeHealth, error) {
	health, err := s.Store.GetRuntimeHealth(ctx, tenantID, releaseID)
	if err != nil {
		return nil, httperr.New(502, 50200, "runtime health lookup failed")
	}
	return health, nil
}

func (s *Service) ReportRuntimeHealth(ctx context.Context, tenantID string, health model.RuntimeHealth) (*model.RuntimeHealth, error) {
	if health.AssistantReleaseID == "" || health.Health == "" {
		return nil, httperr.New(422, 42230, "assistant release and health are required")
	}
	switch health.Health {
	case model.AssistantRuntimeHealthHealthy, model.AssistantRuntimeHealthDegraded, model.AssistantRuntimeHealthUnavailable:
	default:
		return nil, httperr.New(422, 42231, "runtime health is invalid")
	}
	if _, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, health.AssistantReleaseID); err != nil {
		return nil, err
	}
	health.TenantID = tenantID
	now := time.Now().UTC()
	health.CheckedAt = now
	health.UpdatedAt = now
	if health.CreatedAt.IsZero() {
		health.CreatedAt = now
	}
	if err := s.Store.UpsertRuntimeHealth(ctx, &health); err != nil {
		return nil, httperr.New(502, 50200, "runtime health persistence failed")
	}
	return &health, nil
}

func (s *Service) RecordAuthorizationSnapshot(ctx context.Context, tenantID string, snapshot model.AuthorizationSnapshot) (*model.AuthorizationSnapshot, error) {
	if tenantID == "" || snapshot.TraceID == "" || snapshot.AssistantReleaseID == "" || snapshot.Decision == "" {
		return nil, httperr.New(422, 42200, "authorization snapshot is incomplete")
	}
	switch snapshot.Decision {
	case model.AuthorizationAllow, model.AuthorizationDeny, model.AuthorizationError:
	default:
		return nil, httperr.New(422, 42200, "authorization decision is invalid")
	}
	if _, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, snapshot.AssistantReleaseID); err != nil {
		return nil, err
	}
	snapshot.TenantID = tenantID
	now := time.Now().UTC()
	snapshot.ID = id.New()
	if snapshot.DecisionTime.IsZero() {
		snapshot.DecisionTime = now
	}
	snapshot.CreatedAt = now
	if err := s.Store.CreateAuthorizationSnapshot(ctx, &snapshot); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, httperr.New(409, 40930, "authorization decision already exists")
		}
		return nil, httperr.New(502, 50200, "authorization snapshot persistence failed")
	}
	return &snapshot, nil
}

func (s *Service) ListAuthorizationSnapshots(ctx context.Context, tenantID, releaseID, traceID string, page, pageSize int) ([]model.AuthorizationSnapshot, int64, error) {
	items, total, err := s.Store.ListAuthorizationSnapshots(ctx, tenantID, releaseID, traceID, page, pageSize)
	if err != nil {
		return nil, 0, httperr.New(502, 50200, "authorization snapshot list failed")
	}
	return items, total, nil
}

func (s *Service) validateSnapshot(ctx context.Context, tenantID string, release *model.AssistantRelease) error {
	if release.SnapshotManifestID == "" {
		return httperr.New(422, 42231, "snapshot manifest is missing")
	}
	manifest, err := s.Store.GetSnapshotManifest(ctx, tenantID, release.SnapshotManifestID)
	if err != nil {
		return httperr.New(502, 50200, "snapshot manifest lookup failed")
	}
	if manifest == nil || !manifest.Complete || manifest.AssistantReleaseID != release.ID {
		return httperr.New(422, 42231, "snapshot manifest is incomplete")
	}
	if manifest.SnapshotHash == "" || manifest.SnapshotHash != hashCanonical(manifest.ManifestJSON) {
		return httperr.New(422, 42231, "snapshot manifest hash mismatch")
	}
	if release.DesiredStateHash == "" || release.DesiredStateHash != hashCanonical(release.DesiredStateJSON) {
		return httperr.New(422, 42231, "desired state hash mismatch")
	}
	return nil
}

func (s *Service) verifyAssistantProvider(ctx context.Context, tenantID string, release *model.AssistantRelease) error {
	capabilities, err := s.Store.ListCapabilityBindingVersions(ctx, tenantID, release.ID)
	if err != nil {
		return httperr.New(502, 50200, "capability binding lookup failed")
	}
	if len(capabilities) == 0 {
		return httperr.New(422, 42231, "release has no capability binding")
	}
	datasets, err := s.Store.ListDatasetBindingVersions(ctx, tenantID, release.ID)
	if err != nil {
		return httperr.New(502, 50200, "dataset binding lookup failed")
	}
	datasetIDs := make([]string, 0, len(datasets))
	for _, binding := range datasets {
		link, err := s.Store.GetDatasetLink(ctx, tenantID, binding.DatasetID)
		if err != nil || link == nil {
			return httperr.New(422, 42230, "provider drift detected")
		}
		if _, err := s.RAGFlow.GetDatasetConfig(ctx, link.RAGFlowDatasetID); err != nil {
			return httperr.New(502, 50230, "ragflow dataset verification failed")
		}
		datasetIDs = append(datasetIDs, link.RAGFlowDatasetID)
	}
	desired := map[string]any{}
	if err := json.Unmarshal([]byte(release.DesiredStateJSON), &desired); err != nil {
		return httperr.New(422, 42231, "desired state must be valid JSON")
	}
	for _, binding := range capabilities {
		if !binding.OwnershipVerified {
			return httperr.New(422, 42230, "provider ownership is unverified")
		}
		switch binding.TargetType {
		case "chat":
			shadow, err := s.Store.GetChatShadow(ctx, tenantID, binding.TargetID, false)
			if err != nil || shadow == nil {
				return httperr.New(422, 42230, "provider drift detected")
			}
			chat, err := s.RAGFlow.GetChat(ctx, binding.TargetID)
			if err != nil {
				return httperr.New(502, 50230, "ragflow chat verification failed")
			}
			if !equalStringSets(chat.DatasetIDs, datasetIDs) {
				return httperr.New(422, 42230, "provider drift detected")
			}
		case "agent":
			shadow, err := s.Store.GetAgentShadow(ctx, tenantID, binding.TargetID, false)
			if err != nil || shadow == nil {
				return httperr.New(422, 42230, "provider drift detected")
			}
			agent, err := s.RAGFlow.GetAgent(ctx, binding.TargetID)
			if err != nil {
				return httperr.New(502, 50230, "ragflow agent verification failed")
			}
			if value, ok := desired["dsl"].(map[string]any); ok {
				expected, err := canonicalJSON(value)
				if err != nil {
					return httperr.New(422, 42231, "agent desired state is invalid")
				}
				actual, err := canonicalJSON(agent.Dsl)
				if err != nil {
					return httperr.New(502, 50230, "ragflow agent verification failed")
				}
				if expected != actual {
					return httperr.New(422, 42230, "provider drift detected")
				}
			}
			if value, ok := desired["release"].(bool); ok && value != agent.Release {
				return httperr.New(422, 42230, "provider drift detected")
			}
			if value, ok := desired["title"].(string); ok && value != agent.Title {
				return httperr.New(422, 42230, "provider drift detected")
			}
		case "search_app":
			shadow, err := s.Store.GetSearchAppShadow(ctx, tenantID, binding.TargetID, false)
			if err != nil || shadow == nil {
				return httperr.New(422, 42230, "provider drift detected")
			}
			searchApp, err := s.RAGFlow.GetSearchApp(ctx, binding.TargetID)
			if err != nil {
				return httperr.New(502, 50230, "ragflow search app verification failed")
			}
			if searchApp.SearchConfig != nil && !equalStringSets(searchApp.SearchConfig.KbIDs, datasetIDs) {
				return httperr.New(422, 42230, "provider drift detected")
			}
			if desiredConfig, ok := desired["search_config"].(map[string]any); ok {
				if searchApp.SearchConfig == nil {
					return httperr.New(422, 42230, "provider drift detected")
				}
				actualConfig, err := canonicalJSON(searchApp.SearchConfig)
				if err != nil {
					return httperr.New(502, 50230, "ragflow search app verification failed")
				}
				actual := map[string]any{}
				if err := json.Unmarshal([]byte(actualConfig), &actual); err != nil {
					return httperr.New(502, 50230, "ragflow search app verification failed")
				}
				expected := maps.Clone(desiredConfig)
				expected["kb_ids"] = datasetIDs
				for key, value := range expected {
					expectedJSON, err := canonicalJSON(value)
					if err != nil {
						return httperr.New(422, 42231, "search_app desired state is invalid")
					}
					actualValue, ok := actual[key]
					if !ok {
						return httperr.New(422, 42230, "provider drift detected")
					}
					actualJSON, err := canonicalJSON(actualValue)
					if err != nil {
						return httperr.New(502, 50230, "ragflow search app verification failed")
					}
					if expectedJSON != actualJSON {
						return httperr.New(422, 42230, "provider drift detected")
					}
				}
			}
		default:
			return httperr.New(422, 42231, "unsupported provider target type")
		}
	}
	return nil
}

func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := make(map[string]struct{}, len(left))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	for _, value := range right {
		if _, exists := leftSet[value]; !exists {
			return false
		}
	}
	return true
}

func (s *Service) applyAssistantProvider(ctx context.Context, tenantID string, release *model.AssistantRelease) error {
	capabilities, err := s.Store.ListCapabilityBindingVersions(ctx, tenantID, release.ID)
	if err != nil {
		return httperr.New(502, 50200, "capability binding lookup failed")
	}
	desired := map[string]any{}
	if err := json.Unmarshal([]byte(release.DesiredStateJSON), &desired); err != nil {
		return httperr.New(422, 42231, "desired state must be valid JSON")
	}
	datasets, err := s.Store.ListDatasetBindingVersions(ctx, tenantID, release.ID)
	if err != nil {
		return httperr.New(502, 50200, "dataset binding lookup failed")
	}
	datasetIDs := make([]string, 0, len(datasets))
	for _, binding := range datasets {
		link, err := s.Store.GetDatasetLink(ctx, tenantID, binding.DatasetID)
		if err != nil || link == nil {
			return httperr.New(422, 42230, "provider drift detected")
		}
		datasetIDs = append(datasetIDs, link.RAGFlowDatasetID)
	}
	for _, binding := range capabilities {
		switch binding.TargetType {
		case "chat":
			shadow, err := s.Store.GetChatShadow(ctx, tenantID, binding.TargetID, false)
			if err != nil || shadow == nil {
				return httperr.New(422, 42230, "provider drift detected")
			}
			request := ragflow.UpdateChatRequest{DatasetIDs: datasetIDs}
			if value, ok := desired["prompt_config"].(map[string]any); ok {
				request.PromptConfig = value
			}
			if value, ok := desired["llm_id"].(string); ok {
				request.LLMID = value
			}
			if value, ok := desired["rerank_id"].(string); ok {
				request.RerankID = value
			}
			if value, ok := desired["top_n"].(float64); ok {
				request.TopN = int(value)
			}
			if value, ok := desired["top_k"].(float64); ok {
				request.TopK = int(value)
			}
			if value, ok := desired["similarity_threshold"].(float64); ok {
				request.SimilarityThreshold = value
			}
			if value, ok := desired["vector_similarity_weight"].(float64); ok {
				request.VectorSimilarityWeight = value
			}
			if value, ok := desired["llm_setting"].(map[string]any); ok {
				request.LLMSetting = value
			}
			if _, err := s.RAGFlow.UpdateChat(ctx, binding.TargetID, request); err != nil {
				return httperr.New(502, 50241, "ragflow chat apply failed")
			}
		case "agent":
			shadow, err := s.Store.GetAgentShadow(ctx, tenantID, binding.TargetID, false)
			if err != nil || shadow == nil {
				return httperr.New(422, 42230, "provider drift detected")
			}
			request := ragflow.UpdateAgentRequest{}
			if value, ok := desired["title"].(string); ok && value != "" {
				request.Title = value
			}
			if value, ok := desired["dsl"].(map[string]any); ok {
				request.Dsl = value
			}
			if value, ok := desired["release"].(bool); ok {
				request.Release = &value
			}
			if request.Dsl == nil && request.Release == nil && request.Title == "" {
				return httperr.New(422, 42231, "agent desired state is empty")
			}
			if err := s.RAGFlow.UpdateAgent(ctx, binding.TargetID, request); err != nil {
				return httperr.New(502, 50241, "ragflow agent apply failed")
			}
		case "search_app":
			shadow, err := s.Store.GetSearchAppShadow(ctx, tenantID, binding.TargetID, false)
			if err != nil || shadow == nil {
				return httperr.New(422, 42230, "provider drift detected")
			}
			config := ragflow.SearchConfig{KbIDs: datasetIDs}
			if value, ok := desired["search_config"]; ok {
				encoded, err := json.Marshal(value)
				if err != nil {
					return httperr.New(422, 42231, "search_app desired state is invalid")
				}
				if err := json.Unmarshal(encoded, &config); err != nil {
					return httperr.New(422, 42231, "search_app desired state is invalid")
				}
			}
			config.KbIDs = datasetIDs
			if _, err := s.RAGFlow.UpdateSearchApp(ctx, binding.TargetID, ragflow.UpdateSearchAppRequest{
				Name: shadow.Name, SearchConfig: &config,
			}); err != nil {
				return httperr.New(502, 50241, "ragflow search app apply failed")
			}
		default:
			return httperr.New(422, 42231, "unsupported provider target type")
		}
	}
	return nil
}
