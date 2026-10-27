package service

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type MemoryPolicyInput struct {
	Scope           string `json:"scope" binding:"required"`
	ProjectID       string `json:"project_id"`
	UserID          string `json:"user_id"`
	AssistantID     string `json:"assistant_id" binding:"required"`
	RagflowMemoryID string `json:"ragflow_memory_id"`
	State           string `json:"state"`
	ReadPolicy      string `json:"read_policy"`
	WritePolicy     string `json:"write_policy"`
	Config          string `json:"config"`
}

type SelfMemoryPolicyTemplateInput struct {
	Template string `json:"template" binding:"required"`
}

const MemoryPolicyQualityGateNotEvaluated = "NOT_EVALUATED"

type MemoryPolicyQualityGateEvidence struct {
	ID              string `json:"id"`
	EvaluationRunID string `json:"evaluation_run_id"`
	EvaluationScope string `json:"evaluation_scope"`
	EvalSetID       string `json:"eval_set_id"`
	EvalSetVersion  int64  `json:"eval_set_version"`
	EvalSetHash     string `json:"eval_set_hash"`
	SnapshotID      string `json:"snapshot_id"`
	SnapshotHash    string `json:"snapshot_hash"`
	CreatedAt       string `json:"created_at"`
}

type MemoryPolicyQualityGateSummary struct {
	PolicyID       string                           `json:"policy_id"`
	RunID          string                           `json:"run_id"`
	RunStatus      string                           `json:"run_status"`
	GateStatus     string                           `json:"gate_status"`
	GateReason     string                           `json:"gate_reason"`
	Pass           *bool                            `json:"pass"`
	Metrics        map[string]interface{}           `json:"metrics"`
	EvalSetID      string                           `json:"eval_set_id"`
	EvalSetVersion int64                            `json:"eval_set_version"`
	EvalSetHash    string                           `json:"eval_set_hash"`
	Capability     string                           `json:"capability"`
	ExecutionMode  string                           `json:"execution_mode"`
	CreatedAt      string                           `json:"created_at"`
	CompletedAt    *string                          `json:"completed_at"`
	Evidence       *MemoryPolicyQualityGateEvidence `json:"evidence,omitempty"`
	EvidenceURL    string                           `json:"evidence_url,omitempty"`
}

func (s *Service) GetMemoryPolicyQualityGate(
	ctx context.Context, tenantID, policyID string,
) (*MemoryPolicyQualityGateSummary, error) {
	policy, err := s.Store.GetMemoryPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, httperr.NotFound("memory policy not found")
	}
	summary := &MemoryPolicyQualityGateSummary{
		PolicyID: policy.ID, GateStatus: MemoryPolicyQualityGateNotEvaluated,
		Metrics: map[string]interface{}{},
	}
	runs, _, err := s.Store.ListEvaluationRuns(ctx, tenantID, repository.EvaluationScopeFilter{
		Scope: model.EvaluationScopeMemoryPolicy, MemoryPolicyID: policy.ID,
	}, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return summary, nil
	}
	run := runs[0]
	summary.RunID = run.ID
	summary.RunStatus = run.Status
	summary.GateStatus = run.GateStatus
	if summary.GateStatus == "" {
		summary.GateStatus = "UNREPORTED"
	}
	summary.GateReason = run.GateReason
	summary.Pass = run.Pass
	summary.EvalSetID = run.EvalSetID
	summary.EvalSetVersion = run.EvalSetVersion
	summary.EvalSetHash = run.EvalSetHash
	summary.Capability = run.Capability
	summary.ExecutionMode = run.ExecutionMode
	summary.CreatedAt = run.CreatedAt.UTC().Format(time.RFC3339)
	if run.CompletedAt != nil {
		completedAt := run.CompletedAt.UTC().Format(time.RFC3339)
		summary.CompletedAt = &completedAt
	}
	if run.Metrics != "" {
		metrics := map[string]interface{}{}
		if err := json.Unmarshal([]byte(run.Metrics), &metrics); err != nil {
			return nil, httperr.New(500, 50081, "memory policy quality metrics are invalid")
		}
		summary.Metrics = metrics
	}
	bundles, _, err := s.Store.ListEvidenceBundles(ctx, tenantID, repository.EvaluationScopeFilter{
		Scope:           model.EvaluationScopeMemoryPolicy,
		MemoryPolicyID:  policy.ID,
		EvaluationRunID: run.ID,
	}, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(bundles) > 0 {
		summary.Evidence = &MemoryPolicyQualityGateEvidence{
			ID: bundles[0].ID, EvaluationRunID: bundles[0].EvaluationRunID,
			EvaluationScope: bundles[0].EvaluationScope, EvalSetID: bundles[0].EvalSetID,
			EvalSetVersion: bundles[0].EvalSetVersion, EvalSetHash: bundles[0].EvalSetHash,
			SnapshotID: bundles[0].SnapshotID, SnapshotHash: bundles[0].SnapshotHash,
			CreatedAt: bundles[0].CreatedAt.UTC().Format(time.RFC3339),
		}
		query := url.Values{}
		query.Set("evaluation_scope", model.EvaluationScopeMemoryPolicy)
		query.Set("x_memory_policy_id", policy.ID)
		query.Set("evaluation_run_id", run.ID)
		summary.EvidenceURL = "/api/v1/evidence-bundles?" + query.Encode()
	}
	return summary, nil
}

func validateMemoryPolicyConfig(value string) (map[string]interface{}, error) {
	trimmed := strings.TrimSpace(value)
	if !json.Valid([]byte(trimmed)) || !strings.HasPrefix(trimmed, "{") {
		return nil, httperr.BadRequest(40090, "config must be a JSON object")
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &config); err != nil {
		return nil, httperr.BadRequest(40090, "config must be a JSON object")
	}
	purpose, _ := config["purpose"].(string)
	readPolicy, _ := config["read_policy"].(string)
	writePolicy, _ := config["write_policy"].(string)
	retentionDays, _ := config["retention_days"].(float64)
	if strings.TrimSpace(purpose) == "" || len(purpose) > 256 {
		return nil, httperr.BadRequest(40091, "config.purpose is required and must be at most 256 characters")
	}
	if !isValidMemoryBoundaryPolicy(readPolicy) || !isValidMemoryBoundaryPolicy(writePolicy) ||
		retentionDays <= 0 || retentionDays > 3650 {
		return nil, httperr.BadRequest(40092, "config must contain valid read_policy, write_policy and retention_days")
	}
	return config, nil
}

func isValidMemoryBoundaryPolicy(value string) bool {
	return value == "allow" || value == "deny" || value == "project_member" || value == "owner"
}

func buildMemoryPolicy(
	tenantID, userID string, input MemoryPolicyInput, state string, now time.Time,
) (*model.MemoryPolicy, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.RagflowMemoryID = strings.TrimSpace(input.RagflowMemoryID)
	input.ReadPolicy = strings.TrimSpace(input.ReadPolicy)
	input.WritePolicy = strings.TrimSpace(input.WritePolicy)
	config, err := validateMemoryPolicyConfig(input.Config)
	if err != nil {
		return nil, err
	}
	if input.Scope != "project" && input.Scope != "user" {
		return nil, httperr.BadRequest(40093, "scope must be project or user")
	}
	if input.AssistantID == "" {
		return nil, httperr.BadRequest(40093, "assistant_id is required")
	}
	if input.Scope == "project" {
		if input.ProjectID == "" || input.UserID != "" {
			return nil, httperr.BadRequest(40093, "project policy requires project_id and must not target a user")
		}
	} else if input.UserID == "" {
		return nil, httperr.BadRequest(40093, "user policy requires user_id")
	}
	readPolicy := config["read_policy"].(string)
	writePolicy := config["write_policy"].(string)
	if input.ReadPolicy != "" {
		readPolicy = input.ReadPolicy
	}
	if input.WritePolicy != "" {
		writePolicy = input.WritePolicy
	}
	if !isValidMemoryBoundaryPolicy(readPolicy) || !isValidMemoryBoundaryPolicy(writePolicy) {
		return nil, httperr.BadRequest(40092, "read_policy and write_policy are invalid")
	}
	if state == "" {
		state = model.MemoryPolicyPaused
	}
	return &model.MemoryPolicy{
		ID: id.New(), TenantID: tenantID, ProjectID: input.ProjectID,
		AssistantID: input.AssistantID, UserID: input.UserID, RagflowMemoryID: input.RagflowMemoryID,
		State:      state,
		ReadPolicy: readPolicy, WritePolicy: writePolicy, PolicyGeneration: 1,
		Config: strings.TrimSpace(input.Config), CreatedBy: userID,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Service) validateMemoryPolicyBinding(
	ctx context.Context, policy *model.MemoryPolicy,
) error {
	if policy.RagflowMemoryID == "" {
		if policy.State == model.MemoryPolicyActive {
			return httperr.BadRequest(40093, "active memory policy requires ragflow_memory_id")
		}
		return nil
	}
	if policy.State != model.MemoryPolicyActive {
		return httperr.BadRequest(40093, "ragflow_memory_id can only bind an active policy")
	}
	memory, err := s.Store.GetMemoryShadow(ctx, policy.TenantID, policy.RagflowMemoryID, false)
	if err != nil {
		return err
	}
	if memory == nil {
		return httperr.BadRequest(40093, "ragflow_memory_id must reference a tenant memory")
	}
	if policyScope(policy) == "user" && memory.OwnerID != policy.UserID {
		return httperr.Forbidden("user memory policy must bind an owned memory")
	}
	if policyScope(policy) == "project" {
		member, err := s.Store.IsProjectMember(ctx, memory.OwnerID, policy.ProjectID)
		if err != nil {
			return err
		}
		if !member {
			return httperr.Forbidden("project memory policy must bind a project member memory")
		}
	}
	if policyScope(policy) == "user" && policy.ConsentVersion <= 0 {
		return httperr.BadRequest(40093, "active user memory policy requires consent")
	}
	return nil
}

func (s *Service) CreateMemoryPolicy(
	ctx context.Context, tenantID, actorID string, input MemoryPolicyInput,
) (*model.MemoryPolicy, error) {
	policy, err := buildMemoryPolicy(tenantID, actorID, input, "", time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if policy.ProjectID != "" {
		project, err := s.Store.GetProject(ctx, tenantID, policy.ProjectID)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, httperr.BadRequest(40093, "project_id must reference a tenant project")
		}
	}
	if err := s.validateMemoryPolicyBinding(ctx, policy); err != nil {
		return nil, err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, policy.ProjectID); err != nil {
		return nil, err
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateMemoryPolicy(ctx, policy); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForMemoryPolicy(policy, "memory_policy.created", actorID, policy.CreatedAt))
	})
	if err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *Service) ListMemoryPolicies(
	ctx context.Context, tenantID, actorID string,
	filter repository.MemoryPolicyFilter, page, pageSize int,
) ([]model.MemoryPolicy, int64, error) {
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.UserID = strings.TrimSpace(filter.UserID)
	filter.AssistantID = strings.TrimSpace(filter.AssistantID)
	if filter.ProjectID != "" {
		if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, filter.ProjectID); err != nil {
			return nil, 0, err
		}
	}
	return s.Store.ListMemoryPolicies(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetMemoryPolicy(ctx context.Context, tenantID, policyID string) (*model.MemoryPolicy, error) {
	policy, err := s.Store.GetMemoryPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, httperr.NotFound("memory policy not found")
	}
	return policy, nil
}

func (s *Service) UpdateMemoryPolicy(
	ctx context.Context, tenantID, actorID, policyID string, input MemoryPolicyInput,
) (*model.MemoryPolicy, error) {
	existing, err := s.GetMemoryPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	state := strings.TrimSpace(input.State)
	if state != "" && state != model.MemoryPolicyActive && state != model.MemoryPolicyPaused && state != model.MemoryPolicyRetired {
		return nil, httperr.BadRequest(40094, "state must be active, paused or retired")
	}
	input.Scope = policyScope(existing)
	input.ProjectID = existing.ProjectID
	input.UserID = existing.UserID
	input.AssistantID = existing.AssistantID
	if strings.TrimSpace(input.RagflowMemoryID) == "" {
		input.RagflowMemoryID = existing.RagflowMemoryID
	}
	policy, err := buildMemoryPolicy(tenantID, existing.CreatedBy, input, state, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, policy.ProjectID); err != nil {
		return nil, err
	}
	if err := s.validateMemoryPolicyBinding(ctx, policy); err != nil {
		return nil, err
	}
	policy.ID = existing.ID
	policy.CreatedBy = existing.CreatedBy
	policy.CreatedAt = existing.CreatedAt
	if policy.State != existing.State || policy.ReadPolicy != existing.ReadPolicy ||
		policy.WritePolicy != existing.WritePolicy {
		policy.PolicyGeneration = existing.PolicyGeneration + 1
	} else {
		policy.PolicyGeneration = existing.PolicyGeneration
	}
	policy.ConsentVersion = existing.ConsentVersion
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.UpdateMemoryPolicy(ctx, policy); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForMemoryPolicy(policy, "memory_policy.updated", actorID, policy.UpdatedAt))
	}); err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *Service) DeleteMemoryPolicy(ctx context.Context, tenantID, actorID, policyID string) error {
	policy, err := s.GetMemoryPolicy(ctx, tenantID, policyID)
	if err != nil {
		return err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, policy.ProjectID); err != nil {
		return err
	}
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.DeleteMemoryPolicy(ctx, tenantID, policy.ID); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForMemoryPolicy(policy, "memory_policy.deleted", actorID, time.Now().UTC()))
	})
}

func (s *Service) SetMemoryPolicyConsent(
	ctx context.Context, tenantID, actorID, policyID string, consent bool,
) (*model.MemoryPolicy, error) {
	policy, err := s.GetMemoryPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	if policyScope(policy) == "project" {
		// MEM-012: personal consent is per member. Project enablement (the
		// active policy) never implies personal consent, and one member's
		// opt-in/out never moves the shared policy version or generation.
		member, err := s.Store.IsProjectMember(ctx, actorID, policy.ProjectID)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, httperr.Forbidden("project membership is required")
		}
		if consent && policy.State != model.MemoryPolicyActive {
			return nil, httperr.New(409, 40950, "project policy must be active before opt-in")
		}
		if err := s.upsertProjectMemberConsent(ctx, tenantID, actorID, policy, consent); err != nil {
			return nil, err
		}
		return policy, nil
	}
	if policy.UserID != actorID {
		return nil, httperr.Forbidden("own memory policy is required")
	}
	if consent && policy.RagflowMemoryID == "" {
		if err := s.createSelfMemoryBinding(ctx, policy); err != nil {
			return nil, err
		}
	}
	policy.ConsentVersion++
	policy.PolicyGeneration++
	policy.UpdatedAt = time.Now().UTC()
	if !consent {
		policy.State = model.MemoryPolicyPaused
	} else {
		policy.State = model.MemoryPolicyActive
	}
	if policy.State == model.MemoryPolicyActive {
		if err := s.validateMemoryPolicyBinding(ctx, policy); err != nil {
			return nil, err
		}
	}
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.UpdateMemoryPolicy(ctx, policy); err != nil {
			return err
		}
		action := "memory_policy.opt_out"
		if consent {
			action = "memory_policy.opt_in"
		} else {
			// 137 §11.1: sample the owner revocation event so the pending
			// invalidation and revocation effectiveness alerts can compare
			// events against user_action admission rejections.
			obs.Get().AddMemoryRevocationEvent("owner_opt_out")
		}
		return tx.CreateAudit(ctx, auditForMemoryPolicy(policy, action, actorID, policy.UpdatedAt))
	}); err != nil {
		return nil, err
	}
	return policy, nil
}

// upsertProjectMemberConsent records one member's personal consent for a
// project shared memory policy and audits the switch.
func (s *Service) upsertProjectMemberConsent(
	ctx context.Context, tenantID, actorID string, policy *model.MemoryPolicy, consent bool,
) error {
	now := time.Now().UTC()
	existing, err := s.Store.GetMemoryConsent(ctx, tenantID, policy.ID, actorID)
	if err != nil {
		return err
	}
	version := int64(0)
	if existing != nil {
		version = existing.ConsentVersion
	}
	version++
	state := model.MemoryConsentWithdrawn
	if consent {
		state = model.MemoryConsentActive
	}
	consentRow := &model.MemoryConsent{
		ID: id.New(), TenantID: tenantID, PolicyID: policy.ID,
		ProjectID: policy.ProjectID, UserID: actorID,
		ConsentVersion: version, State: state, CreatedBy: actorID,
		CreatedAt: now, UpdatedAt: now,
	}
	if existing != nil {
		consentRow.ID = existing.ID
		consentRow.CreatedAt = existing.CreatedAt
		consentRow.CreatedBy = existing.CreatedBy
	}
	if err := s.Store.UpsertMemoryConsent(ctx, consentRow, now); err != nil {
		return err
	}
	action := "memory_policy.personal_opt_out"
	if consent {
		action = "memory_policy.personal_opt_in"
	} else {
		// 137 §11.1: sample the member's personal revocation event.
		obs.Get().AddMemoryRevocationEvent("personal_opt_out")
	}
	return s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: actorID,
		Action: action, Resource: "memory-policy", ResourceID: policy.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"policy_id": policy.ID, "project_id": policy.ProjectID,
			"consent_version": version, "state": state,
		}),
		At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "execute:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	})
}

// MemoryPolicyConsentView is the governance-visible consent state of one
// project member. It carries identity and version only, never content.
type MemoryPolicyConsentView struct {
	UserID         string `json:"user_id"`
	State          string `json:"state"`
	ConsentVersion int64  `json:"consent_version"`
	WithdrawReason string `json:"withdraw_reason,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

// ListMemoryPolicyConsents exposes per-member consent state for a project
// memory policy. It is read-only governance evidence.
func (s *Service) ListMemoryPolicyConsents(
	ctx context.Context, tenantID, policyID string,
) ([]MemoryPolicyConsentView, error) {
	policy, err := s.GetMemoryPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	if policyScope(policy) != "project" {
		return nil, httperr.BadRequest(40097, "consent listing only applies to project memory policies")
	}
	rows, err := s.Store.ListMemoryConsents(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	views := make([]MemoryPolicyConsentView, 0, len(rows))
	for _, row := range rows {
		views = append(views, MemoryPolicyConsentView{
			UserID: row.UserID, State: row.State, ConsentVersion: row.ConsentVersion,
			WithdrawReason: row.WithdrawReason,
			UpdatedAt:      row.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return views, nil
}

func (s *Service) createSelfMemoryBinding(ctx context.Context, policy *model.MemoryPolicy) error {
	config, err := validateMemoryPolicyConfig(policy.Config)
	if err != nil {
		return err
	}
	embeddingModel, _ := config["embedding_model"].(string)
	llmModel, _ := config["llm_model"].(string)
	if strings.TrimSpace(embeddingModel) == "" || strings.TrimSpace(llmModel) == "" {
		return httperr.New(409, 40957, "self memory template requires embedding_model and llm_model")
	}
	memoryTypes := []string{"raw"}
	if rawTypes, ok := config["memory_types"].([]interface{}); ok && len(rawTypes) > 0 {
		memoryTypes = make([]string, 0, len(rawTypes))
		for _, rawType := range rawTypes {
			value, _ := rawType.(string)
			memoryTypes = append(memoryTypes, strings.TrimSpace(value))
		}
	}
	memory, err := s.CreateMemory(ctx, policy.TenantID, "rgx-memory-"+policy.ID, memoryTypes, embeddingModel, llmModel)
	if err != nil {
		return err
	}
	if err := s.SetMemoryOwner(ctx, memory, policy.UserID); err != nil {
		return err
	}
	policy.RagflowMemoryID = memory.ID
	return nil
}

func (s *Service) GetSelfMemoryPolicyTemplate(ctx context.Context, tenantID string) (*model.Tenant, error) {
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, httperr.NotFound("tenant not found")
	}
	return tenant, nil
}

func (s *Service) UpdateSelfMemoryPolicyTemplate(
	ctx context.Context, tenantID, actorID string, input SelfMemoryPolicyTemplateInput,
) (*model.Tenant, error) {
	if _, err := validateMemoryPolicyConfig(input.Template); err != nil {
		return nil, err
	}
	tenant, err := s.GetSelfMemoryPolicyTemplate(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	tenant.SelfMemoryTemplate = strings.TrimSpace(input.Template)
	tenant.SelfMemoryTemplateVersion++
	if err := s.Store.UpdateTenant(ctx, tenant); err != nil {
		return nil, err
	}
	audit := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
		UserID: actorID, Action: "memory_policy.self_template.updated", Resource: "memory-policy",
		ResourceID: tenantID, DetailJSON: `{"template_version":` + strconv.FormatInt(tenant.SelfMemoryTemplateVersion, 10) + `}`,
		At: time.Now().UTC(), Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "manage:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
	if err := s.Store.CreateAudit(ctx, audit); err != nil {
		return nil, err
	}
	return tenant, nil
}

func (s *Service) ListSelfMemoryPolicies(
	ctx context.Context, tenantID, actorID string, page, pageSize int,
) ([]model.MemoryPolicy, int64, error) {
	return s.Store.ListMemoryPolicies(ctx, tenantID, repository.MemoryPolicyFilter{
		Scope: "user", UserID: actorID,
	}, page, pageSize)
}

func (s *Service) CreateSelfMemoryPolicy(
	ctx context.Context, tenantID, actorID, assistantID string,
) (*model.MemoryPolicy, error) {
	tenant, err := s.GetSelfMemoryPolicyTemplate(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(tenant.SelfMemoryTemplate) == "" || tenant.SelfMemoryTemplateVersion == 0 {
		return nil, httperr.New(409, 40951, "self memory policy template is missing")
	}
	existing, total, err := s.ListSelfMemoryPolicies(ctx, tenantID, actorID, 1, 200)
	if err != nil {
		return nil, err
	}
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" {
		return nil, httperr.BadRequest(40093, "assistant_id is required")
	}
	for _, policy := range existing {
		if policy.AssistantID == assistantID && policy.State != model.MemoryPolicyRetired {
			existingPolicy := policy
			return &existingPolicy, nil
		}
	}
	if total >= 50 {
		return nil, httperr.New(409, 40952, "self memory policy limit reached")
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(tenant.SelfMemoryTemplate), &config); err != nil {
		return nil, httperr.New(409, 40953, "self memory policy template is invalid")
	}
	input := MemoryPolicyInput{
		Scope: "user", ProjectID: "", UserID: actorID, AssistantID: assistantID,
		Config: tenant.SelfMemoryTemplate,
	}
	policy, err := buildMemoryPolicy(tenantID, actorID, input, model.MemoryPolicyPaused, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateMemoryPolicy(ctx, policy); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForMemoryPolicy(policy, "memory_policy.self_created", actorID, policy.CreatedAt))
	}); err != nil {
		return nil, err
	}
	return policy, nil
}

func policyScope(policy *model.MemoryPolicy) string {
	if policy.UserID == "" {
		return "project"
	}
	return "user"
}

func auditForMemoryPolicy(
	policy *model.MemoryPolicy, action, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: policy.TenantID, ActorTenantID: policy.TenantID,
		TargetTenantID: policy.TenantID, UserID: userID, Action: action,
		Resource: "memory-policy", ResourceID: policy.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"policy_id": policy.ID, "scope": policyScope(policy),
			"project_id": policy.ProjectID, "user_id": policy.UserID,
			"assistant_id": policy.AssistantID, "state": policy.State,
			"policy_generation": policy.PolicyGeneration, "consent_version": policy.ConsentVersion,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "manage:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}
