package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

const (
	approvalSubmitLimit   = 10
	approvalSubmitWindow  = 10 * time.Minute
	approvalDecisionLimit = 30
	approvalExportLimit   = 10000
)

var approvalIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type ApprovalSubmitRequest struct {
	TenantID       string         `json:"tenant_id"`
	ObjectType     string         `json:"object_type"`
	Action         string         `json:"action"`
	ObjectID       string         `json:"object_id"`
	Title          string         `json:"title"`
	Reason         string         `json:"reason"`
	Payload        map[string]any `json:"payload"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type ApprovalSubmitResult struct {
	Approval *model.Approval
	Created  bool
}

type ApprovalPolicyRequest struct {
	ID          string                   `json:"id"`
	TenantID    string                   `json:"tenant_id"`
	ObjectType  string                   `json:"object_type"`
	Action      string                   `json:"action"`
	Enabled     bool                     `json:"enabled"`
	Priority    int                      `json:"priority"`
	Conditions  model.ApprovalConditions `json:"conditions"`
	ExpireHours int                      `json:"expire_hours"`
	Steps       []model.ApprovalStepSpec `json:"steps"`
}

type ApprovalDelegationRequest struct {
	DelegateID string    `json:"delegate_id"`
	ObjectType string    `json:"object_type"`
	Action     string    `json:"action"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
}

type ApprovalBatchDecisionRequest struct {
	IDs     []string `json:"ids"`
	Comment string   `json:"comment"`
}

type ApprovalBatchDecisionResult struct {
	ID       string          `json:"id"`
	OK       bool            `json:"ok"`
	Approval *model.Approval `json:"approval,omitempty"`
	Error    string          `json:"error,omitempty"`
	Code     int             `json:"code,omitempty"`
}

func (s *Service) SetApprovalConfig(cfg config.Approval) {
	if cfg.DefaultExpireHours <= 0 {
		cfg.DefaultExpireHours = 72
	}
	if cfg.ExecutionMaxRetries <= 0 {
		cfg.ExecutionMaxRetries = 3
	}
	if cfg.ExpireScanIntervalSec <= 0 {
		cfg.ExpireScanIntervalSec = 3600
	}
	if cfg.ReminderBeforeHours <= 0 {
		cfg.ReminderBeforeHours = 24
	}
	if cfg.PolicyCacheTTLSec < 0 {
		cfg.PolicyCacheTTLSec = 0
	}
	s.approvalMu.Lock()
	s.approvalConfig = cfg
	s.approvalCacheRevision = approvalConfigRevision(cfg)
	s.approvalPolicyCache = map[string]approvalPolicyCacheEntry{}
	s.approvalMu.Unlock()
}

func approvalConfigRevision(cfg config.Approval) string {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Service) currentApprovalConfig() config.Approval {
	s.approvalMu.RLock()
	defer s.approvalMu.RUnlock()
	return s.approvalConfig
}

func (s *Service) ApprovalConfig() config.Approval {
	return s.currentApprovalConfig()
}

func (s *Service) SetupApproval(cfg config.Approval) error {
	s.SetApprovalConfig(cfg)
	if len(cfg.Policies) == 0 {
		return nil
	}
	ctx := context.Background()
	for _, spec := range cfg.Policies {
		if spec.ObjectType == "" || spec.Action == "" {
			continue
		}
		policies, err := s.Store.ListApprovalPolicies(ctx, spec.TenantID)
		if err != nil {
			return err
		}
		steps := make([]model.ApprovalStepSpec, 0, len(spec.Steps))
		for _, step := range spec.Steps {
			steps = append(steps, model.ApprovalStepSpec{
				StepNo: step.StepNo, Name: step.Name,
				ApproverType:  step.ApproverType,
				ApproverValue: step.ApproverValue,
				ExpireHours:   step.ExpireHours,
				ApprovalMode:  step.ApprovalMode,
				Approvers: func() []model.ApprovalApproverSpec {
					approvers := make([]model.ApprovalApproverSpec, 0, len(step.Approvers))
					for _, approver := range step.Approvers {
						approvers = append(approvers, model.ApprovalApproverSpec{Type: approver.Type, Value: approver.Value})
					}
					return approvers
				}(),
				RequiredApprovals: step.RequiredApprovals,
			})
		}
		if err := validateApprovalSteps(steps); err != nil {
			return err
		}
		stepsJSON, err := json.Marshal(steps)
		if err != nil {
			return err
		}
		conditionsJSON := spec.Conditions
		if conditionsJSON == "" {
			conditionsJSON = "{}"
		}
		exists := false
		for _, p := range policies {
			if p.TenantID == spec.TenantID && p.ObjectType == spec.ObjectType && p.Action == spec.Action &&
				p.ConditionsJSON == conditionsJSON && p.StepsJSON == string(stepsJSON) {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		priority := spec.Priority
		if priority == 0 {
			priority = 100
		}
		expireHours := spec.ExpireHours
		if expireHours <= 0 {
			expireHours = cfg.DefaultExpireHours
		}
		if err := s.Store.UpsertApprovalPolicy(ctx, &model.ApprovalPolicy{
			ID: id.New(), TenantID: spec.TenantID, ObjectType: spec.ObjectType, Action: spec.Action,
			Enabled: spec.Enabled, Priority: priority, ConditionsJSON: conditionsJSON,
			StepsJSON: string(stepsJSON), ExpireHours: expireHours, Version: 1, CreatedBy: "system",
		}); err != nil {
			return err
		}
		if err := s.invalidateApprovalPolicyCache(spec.TenantID, spec.ObjectType, spec.Action); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) invalidateApprovalPolicyCache(tenantID, objectType, action string) error {
	key, sharedKey, err := s.sharedApprovalCacheKey(tenantID, objectType, action)
	if err != nil {
		return err
	}
	s.approvalMu.Lock()
	if s.approvalPolicyCache != nil {
		delete(s.approvalPolicyCache, key)
	}
	s.approvalMu.Unlock()
	if s.sharedApprovalCacheKeyOrNil() != nil {
		if err := s.approvalSharedCache.Delete(context.Background(), sharedKey); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) sharedApprovalCacheKeyOrNil() ApprovalPolicySharedCache {
	s.approvalMu.RLock()
	defer s.approvalMu.RUnlock()
	return s.approvalSharedCache
}

func (s *Service) sharedApprovalCacheKey(tenantID, objectType, action string) (string, string, error) {
	revision := s.approvalCacheRevisionValue()
	if revision == "" {
		return "", "", fmt.Errorf("approval policy cache is not initialized")
	}
	local := strings.Join([]string{tenantID, objectType, action}, ":")
	return local, revision + ":" + local, nil
}

func (s *Service) approvalCacheRevisionValue() string {
	s.approvalMu.RLock()
	defer s.approvalMu.RUnlock()
	return s.approvalCacheRevision
}

func (s *Service) matchApprovalPolicy(ctx context.Context, tenantID, objectType, action string, attrs map[string]any) (*model.ApprovalPolicy, error) {
	key, sharedKey, err := s.sharedApprovalCacheKey(tenantID, objectType, action)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(s.currentApprovalConfig().PolicyCacheTTLSec) * time.Second
	now := time.Now()
	if ttl > 0 {
		if s.sharedApprovalCacheKeyOrNil() != nil {
			policies, hit, err := s.approvalSharedCache.LoadPolicies(ctx, sharedKey)
			if err != nil {
				return nil, err
			}
			if hit {
				s.approvalMu.Lock()
				s.approvalPolicyCache[key] = approvalPolicyCacheEntry{policies: policies, expireAt: now.Add(ttl)}
				s.approvalMu.Unlock()
				return selectApprovalPolicy(policies, attrs), nil
			}
		}
		s.approvalMu.RLock()
		if entry, ok := s.approvalPolicyCache[key]; ok && now.Before(entry.expireAt) {
			policies := entry.policies
			s.approvalMu.RUnlock()
			return selectApprovalPolicy(policies, attrs), nil
		}
		s.approvalMu.RUnlock()
	}
	policies, err := s.Store.ListApprovalPolicies(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if ttl > 0 {
		if s.approvalSharedCache != nil {
			if err := s.approvalSharedCache.SavePolicies(ctx, sharedKey, policies, ttl); err != nil {
				return nil, err
			}
		}
		s.approvalMu.Lock()
		s.approvalPolicyCache[key] = approvalPolicyCacheEntry{policies: policies, expireAt: now.Add(ttl)}
		s.approvalMu.Unlock()
	}
	return selectApprovalPolicy(policies, attrs), nil
}

func selectApprovalPolicy(policies []model.ApprovalPolicy, attrs map[string]any) *model.ApprovalPolicy {
	var selected *model.ApprovalPolicy
	var tenantMatched bool
	for i := range policies {
		p := &policies[i]
		if !p.Enabled {
			continue
		}
		var conditions model.ApprovalConditions
		if err := json.Unmarshal([]byte(p.ConditionsJSON), &conditions); err != nil {
			continue
		}
		if !matchApprovalConditions(conditions, attrs) {
			continue
		}
		pTenant := p.TenantID != ""
		if selected != nil {
			if tenantMatched && !pTenant {
				continue
			}
			if p.Priority > selected.Priority {
				continue
			}
			if p.Priority == selected.Priority && len(conditions.All) <= selectedConditionCount(selected) {
				continue
			}
		}
		selected = p
		tenantMatched = pTenant
	}
	return selected
}

func selectedConditionCount(p *model.ApprovalPolicy) int {
	var conditions model.ApprovalConditions
	_ = json.Unmarshal([]byte(p.ConditionsJSON), &conditions)
	return len(conditions.All)
}

func matchApprovalConditions(conditions model.ApprovalConditions, attrs map[string]any) bool {
	for _, condition := range conditions.All {
		value, ok := attrs[condition.Field]
		if !matchApprovalCondition(condition, value, ok) {
			return false
		}
	}
	return true
}

func matchApprovalCondition(condition model.ApprovalCondition, value any, exists bool) bool {
	switch condition.Op {
	case "exists":
		return exists
	case "eq":
		return exists && fmt.Sprint(value) == decodeApprovalScalar(condition.Value)
	case "neq":
		return !exists || fmt.Sprint(value) != decodeApprovalScalar(condition.Value)
	case "prefix":
		return exists && strings.HasPrefix(fmt.Sprint(value), decodeApprovalScalar(condition.Value))
	case "gt", "gte", "lt", "lte":
		threshold, ok := decodeApprovalNumber(condition.Value)
		number, numberOK := approvalNumber(value)
		if !ok || !numberOK {
			return false
		}
		switch condition.Op {
		case "gt":
			return number > threshold
		case "gte":
			return number >= threshold
		case "lt":
			return number < threshold
		case "lte":
			return number <= threshold
		}
	case "in":
		var values []string
		if err := json.Unmarshal(condition.Value, &values); err != nil {
			return false
		}
		text := fmt.Sprint(value)
		for _, candidate := range values {
			if text == candidate {
				return true
			}
		}
		return false
	default:
		return false
	}
	return false
}

func decodeApprovalScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return fmt.Sprint(value)
}

func decodeApprovalNumber(raw json.RawMessage) (float64, bool) {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

func approvalNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func (s *Service) ListApprovalPolicies(ctx context.Context, actorID, actorTenantID string) ([]model.ApprovalPolicy, error) {
	if err := s.Authorize(ctx, actorID, "read", "approval-policy"); err != nil {
		return nil, err
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if ac.platform {
		return s.Store.ListAllApprovalPolicies(ctx)
	}
	return s.Store.ListApprovalPolicies(ctx, actorTenantID)
}

func (s *Service) SaveApprovalPolicy(ctx context.Context, actorID, actorTenantID string, req ApprovalPolicyRequest) (*model.ApprovalPolicy, error) {
	if err := s.Authorize(ctx, actorID, "manage", "approval-policy"); err != nil {
		return nil, err
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return nil, err
	}
	tenantID := req.TenantID
	if !ac.platform {
		tenantID = actorTenantID
	} else {
		if req.TenantID != "" {
			targetTenant, err := s.Store.GetTenant(ctx, req.TenantID)
			if err != nil {
				return nil, err
			}
			if targetTenant == nil {
				return nil, httperr.BadRequest(40102, "approval policy tenant target must exist")
			}
		}
	}
	if ac.platform && req.ID != "" {
		existing, err := s.getApprovalPolicyByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, httperr.NotFound("approval policy not found")
		}
		if req.TenantID != "" && req.TenantID != existing.TenantID {
			return nil, httperr.New(409, 40910, "approval policy tenant target cannot change")
		}
		tenantID = existing.TenantID
	}
	if req.ObjectType == "" || req.Action == "" {
		return nil, httperr.BadRequest(40080, "object_type and action are required")
	}
	if len(req.Steps) == 0 {
		return nil, httperr.BadRequest(40081, "at least one approval step is required")
	}
	steps := append([]model.ApprovalStepSpec(nil), req.Steps...)
	if err := validateApprovalSteps(steps); err != nil {
		return nil, err
	}
	conditionsJSON, err := json.Marshal(req.Conditions)
	if err != nil {
		return nil, err
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, err
	}
	policy := &model.ApprovalPolicy{
		ID: req.ID, TenantID: tenantID, ObjectType: req.ObjectType, Action: req.Action,
		Enabled: req.Enabled, Priority: req.Priority, ConditionsJSON: string(conditionsJSON),
		StepsJSON: string(stepsJSON), ExpireHours: req.ExpireHours, CreatedBy: actorID,
	}
	if policy.Priority <= 0 {
		policy.Priority = 100
	}
	if policy.ExpireHours <= 0 {
		policy.ExpireHours = s.currentApprovalConfig().DefaultExpireHours
	}
	if policy.ID != "" {
		existing, err := s.getApprovalPolicyByID(ctx, policy.ID)
		if err != nil {
			return nil, err
		}
		if existing == nil || (!ac.platform && existing.TenantID != actorTenantID) {
			return nil, httperr.NotFound("approval policy not found")
		}
		policy.Version = existing.Version + 1
		policy.CreatedAt = existing.CreatedAt
	} else {
		policy.ID = id.New()
		policy.Version = 1
	}
	if err := s.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		return nil, err
	}
	if err := s.invalidateApprovalPolicyCache(policy.TenantID, policy.ObjectType, policy.Action); err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *Service) getApprovalPolicyByID(ctx context.Context, policyID string) (*model.ApprovalPolicy, error) {
	return s.Store.GetApprovalPolicyByID(ctx, policyID)
}

func validateApprovalSteps(steps []model.ApprovalStepSpec) error {
	const maxApprovalsPerStep = 50
	for i := range steps {
		step := &steps[i]
		if len(step.Approvers) == 0 {
			if step.ApproverType == "" || step.ApproverValue == "" {
				return httperr.BadRequest(40083, "approval step name and approver are required")
			}
			step.Approvers = []model.ApprovalApproverSpec{{Type: step.ApproverType, Value: step.ApproverValue}}
		}
		if len(step.Approvers) > maxApprovalsPerStep {
			return httperr.BadRequest(40083, "too many approvers in approval step")
		}
		if step.ApprovalMode == "" {
			step.ApprovalMode = model.ApprovalModeAny
		}
		if step.ApprovalMode != model.ApprovalModeAny && step.ApprovalMode != model.ApprovalModeAll {
			return httperr.BadRequest(40083, "invalid approval mode")
		}
		seen := map[string]bool{}
		validApprovals := 0
		for _, approver := range step.Approvers {
			if approver.Type != model.ApprovalApproverRole && approver.Type != model.ApprovalApproverUser && approver.Type != model.ApprovalApproverTeam {
				return httperr.BadRequest(40084, "invalid approval approver type")
			}
			if approver.Value == "" {
				return httperr.BadRequest(40083, "approval approver value is required")
			}
			key := approver.Type + ":" + approver.Value
			if seen[key] {
				return httperr.BadRequest(40083, "approval approvers must be unique")
			}
			seen[key] = true
			validApprovals++
		}
		if validApprovals == 0 {
			return httperr.BadRequest(40083, "approval approvers are required")
		}
		step.Approvers = step.Approvers[:validApprovals]
		if step.ApprovalMode == model.ApprovalModeAll {
			if step.RequiredApprovals == 0 {
				step.RequiredApprovals = len(step.Approvers)
			}
			if step.RequiredApprovals > len(step.Approvers) {
				return httperr.BadRequest(40083, "required approvals exceed approver count")
			}
		} else {
			step.RequiredApprovals = 1
		}
		step.ApproverType = step.Approvers[0].Type
		step.ApproverValue = step.Approvers[0].Value
	}
	for i, step := range steps {
		if step.StepNo != i+1 {
			return httperr.BadRequest(40082, "approval step numbers must be sequential")
		}
		if step.Name == "" {
			return httperr.BadRequest(40083, "approval step name is required")
		}
		if i == 0 {
			continue
		}
		for _, current := range step.Approvers {
			if current.Type != model.ApprovalApproverUser {
				continue
			}
			for _, previous := range steps[i-1].Approvers {
				if previous.Type == model.ApprovalApproverUser && previous.Value == current.Value {
					return httperr.BadRequest(40085, "the same user cannot approve adjacent steps")
				}
			}
		}
	}
	return nil
}

func (s *Service) SubmitApproval(ctx context.Context, actorID string, req ApprovalSubmitRequest) (*model.Approval, error) {
	if !s.currentApprovalConfig().Enabled {
		return nil, httperr.BadRequest(40086, "approval workflow is disabled")
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return nil, err
	}
	tenantID := ac.user.TenantID
	if ac.platform && req.TenantID != "" {
		tenantID = req.TenantID
	}
	if req.IdempotencyKey == "" || !approvalIDPattern.MatchString(req.IdempotencyKey) {
		return nil, httperr.BadRequest(40087, "idempotency_key must be 1-128 URL-safe characters")
	}
	if req.ObjectType == "" || req.Action == "" {
		return nil, httperr.BadRequest(40088, "object_type and action are required")
	}
	if existing, err := s.Store.GetApprovalByIdempotencyKey(ctx, tenantID, req.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	target, attrs, snapshot, err := s.resolveApprovalTarget(ctx, tenantID, actorID, req)
	if err != nil {
		return nil, err
	}
	policy, err := s.matchApprovalPolicy(ctx, tenantID, req.ObjectType, req.Action, attrs)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, nil
	}
	if count, err := s.approvalSubmitLimiter.Count(ctx, actorID, approvalSubmitWindow); err != nil {
		return nil, err
	} else if count >= approvalSubmitLimit {
		return nil, httperr.New(429, 42940, "too many approval submissions; try later")
	}
	if _, err := s.approvalSubmitLimiter.Incr(ctx, actorID, approvalSubmitWindow); err != nil {
		return nil, err
	}
	if req.Title == "" {
		req.Title = fmt.Sprintf("%s %s", req.Action, req.ObjectID)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(policy.ExpireHours) * time.Hour)
	steps, err := s.approvalStepsFromPolicy(tenantID, policy, now)
	if err != nil {
		return nil, err
	}
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var credential *model.ApprovalCredential
	if apiKey := strings.TrimSpace(fmt.Sprint(req.Payload["api_key"])); apiKey != "" {
		ciphertext, cerr := crypto.Encrypt(s.EncryptKey, apiKey)
		if cerr != nil {
			return nil, httperr.Internal("failed to encrypt approval credential")
		}
		credential = &model.ApprovalCredential{
			TenantID: tenantID, ApprovalID: "pending", Name: "provider_api_key", Ciphertext: ciphertext,
		}
		delete(req.Payload, "api_key")
		payloadBytes, err = json.Marshal(req.Payload)
		if err != nil {
			return nil, err
		}
	}
	_ = target
	fingerprintDraft := &model.Approval{
		TenantID: tenantID, ObjectType: req.ObjectType, ObjectID: req.ObjectID, Action: req.Action,
		PayloadJSON: string(payloadBytes), SnapshotJSON: string(snapshotBytes),
	}
	targetTenantID, err := approvalTargetTenant(fingerprintDraft)
	if err != nil {
		return nil, err
	}
	resourceVersion, err := approvalResourceVersion(fingerprintDraft)
	if err != nil {
		return nil, err
	}
	approvalActionHash, err := computeApprovalActionHash(fingerprintDraft)
	if err != nil {
		return nil, err
	}
	approval := &model.Approval{
		ID: id.New(), TenantID: tenantID, RequestNo: fmt.Sprintf("APR-%s-%s", now.Format("20060102"), id.New()[:8]),
		ObjectType: req.ObjectType, ObjectID: req.ObjectID, Action: req.Action, Title: req.Title, Reason: req.Reason,
		PayloadJSON: string(payloadBytes), SnapshotJSON: string(snapshotBytes),
		TargetTenantID: targetTenantID, ResourceVersion: resourceVersion,
		ApprovalActionHash: approvalActionHash, Status: model.ApprovalStatusPendingApproval,
		PolicyID: policy.ID, PolicyVersion: policy.Version, CurrentStep: 1, RequesterID: actorID,
		IdempotencyKey: req.IdempotencyKey, ExpiresAt: expiresAt, SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if credential != nil {
		credential.ApprovalID = approval.ID
	}
	auditDetail, _ := json.Marshal(map[string]any{
		"request_no": approval.RequestNo, "object_type": req.ObjectType, "action": req.Action,
		"policy_id": policy.ID, "policy_version": policy.Version,
	})
	if err := s.Store.CreateApprovalWithAudit(ctx, approval, steps, credential, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "approval.submitted", Resource: "approval",
		ResourceID: approval.ID, DetailJSON: string(auditDetail), At: now,
		ActorTenantID: ac.user.TenantID, TargetTenantID: targetTenantID,
		ApprovalID: approval.ID, ApprovalActionHash: approval.ApprovalActionHash,
		Result: "PENDING_APPROVAL", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "approval:" + req.ObjectType + ":" + req.Action,
	}); err != nil {
		return nil, err
	}
	obs.Get().IncApprovalSubmitted(req.ObjectType, req.Action)
	s.emitApprovalEvent(ctx, "approval.pending", "pending approval", approval, "info")
	return approval, nil
}

func (s *Service) ApprovalGateEvaluateAndHold(ctx context.Context, actorID, objectType, action, objectID string, payload map[string]any) (*model.Approval, bool, error) {
	return s.ApprovalGate(ctx, actorID, objectType, action, objectID, payload, "")
}

// ApprovalGateForTenant is the only cross-tenant write entry point. It accepts
// the exact server-resolved target tenant and never derives it from raw input.
func (s *Service) ApprovalGateForTenant(ctx context.Context, actorID, targetTenantID, objectType, action, objectID string, payload map[string]any, idempotencyKey string) (*model.Approval, bool, error) {
	if targetTenantID == "" {
		return nil, false, httperr.BadRequest(40000, "target tenant is required")
	}
	if !s.currentApprovalConfig().Enabled {
		return nil, false, httperr.BadRequest(40086, "approval workflow must be enabled for cross-tenant writes")
	}
	if idempotencyKey == "" {
		idempotencyKey = id.New()
	}
	approval, err := s.SubmitApproval(ctx, actorID, ApprovalSubmitRequest{
		TenantID: targetTenantID, ObjectType: objectType, Action: action, ObjectID: objectID,
		Payload: payload, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, false, err
	}
	return approval, approval != nil, nil
}

func (s *Service) ApprovalGate(ctx context.Context, actorID, objectType, action, objectID string, payload map[string]any, idempotencyKey string) (*model.Approval, bool, error) {
	if approvalAlwaysRequired(objectType) {
		if !s.currentApprovalConfig().Enabled {
			return nil, false, httperr.BadRequest(40086, "approval workflow must be enabled for governed operations")
		}
	}
	if !s.currentApprovalConfig().Enabled {
		return nil, false, nil
	}
	if idempotencyKey == "" {
		idempotencyKey = id.New()
	}
	approval, err := s.SubmitApproval(ctx, actorID, ApprovalSubmitRequest{
		ObjectType: objectType, Action: action, ObjectID: objectID, Payload: payload,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, false, err
	}
	if approval == nil && approvalAlwaysRequired(objectType) {
		return nil, false, httperr.BadRequest(40089, "approval policy is required for governed operations")
	}
	return approval, approval != nil, nil
}

func approvalAlwaysRequired(objectType string) bool {
	return objectType == model.ApprovalObjectEnterpriseConnection ||
		objectType == model.ApprovalObjectEnterpriseBinding
}

type approvalTarget struct {
	Snapshot map[string]any
}

func (s *Service) resolveApprovalTarget(ctx context.Context, tenantID, actorID string, req ApprovalSubmitRequest) (*approvalTarget, map[string]any, map[string]any, error) {
	target := &approvalTarget{Snapshot: map[string]any{}}
	attrs := map[string]any{}
	switch req.ObjectType {
	case model.ApprovalObjectDataset:
		if req.Action == model.ApprovalActionCreate {
			if err := s.Authorize(ctx, actorID, "manage", "dataset"); err != nil {
				return nil, nil, nil, err
			}
			name, _ := req.Payload["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, nil, nil, httperr.BadRequest(40089, "dataset name is required")
			}
			req.ObjectID = "new:" + name
			attrs["dataset_name"] = name
			return target, attrs, target.Snapshot, nil
		}
		dataset, err := s.Store.GetDatasetLink(ctx, tenantID, req.ObjectID)
		if err != nil {
			return nil, nil, nil, err
		}
		if dataset == nil {
			return nil, nil, nil, httperr.NotFound("dataset not found")
		}
		if err := s.AuthorizeABAC(ctx, actorID, "manage", "dataset", tenantID, dataset.ProjectID, ""); err != nil {
			return nil, nil, nil, err
		}
		target.Snapshot = map[string]any{"name": dataset.Name, "ragflow_dataset_id": dataset.RAGFlowDatasetID, "project_id": dataset.ProjectID}
		attrs["dataset_name"] = dataset.Name
		attrs["dataset_project_id"] = dataset.ProjectID
		if req.Action == model.ApprovalActionUpdate {
			updateReq, updateErr := normalizeDatasetUpdateApproval(req.Payload)
			if updateErr != nil {
				return nil, nil, nil, httperr.BadRequest(40089, "invalid dataset update payload")
			}
			if updateReq.Name == "" && updateReq.ProjectID == "" && updateReq.Config == nil {
				return nil, nil, nil, httperr.BadRequest(40089, "dataset name, project or config is required")
			}
			if updateReq.ProjectID != "" {
				project, err := s.Store.GetProject(ctx, tenantID, updateReq.ProjectID)
				if err != nil {
					return nil, nil, nil, err
				}
				if project == nil {
					return nil, nil, nil, httperr.NotFound("project not found")
				}
			}
			if updateReq.Config != nil {
				currentConfig, err := s.GetDatasetConfig(ctx, tenantID, req.ObjectID)
				if err != nil {
					return nil, nil, nil, err
				}
				target.Snapshot["config"] = currentConfig
			}
			attrs["new_dataset_name"] = updateReq.Name
		}
	case model.ApprovalObjectDocument, model.ApprovalObjectDocumentChunk:
		if req.Action == model.ApprovalActionCreate {
			return nil, nil, nil, httperr.BadRequest(40089, "cross-tenant document upload is not supported")
		}
		if req.Action != model.ApprovalActionUpdate &&
			req.Action != model.ApprovalActionDelete &&
			req.Action != model.ApprovalActionParse &&
			req.Action != model.ApprovalActionStop &&
			req.Action != model.ApprovalActionEnable &&
			req.Action != model.ApprovalActionDisable {
			return nil, nil, nil, httperr.BadRequest(40089, "unsupported document action")
		}
		if err := s.Authorize(ctx, actorID, "execute", "document"); err != nil {
			return nil, nil, nil, err
		}
		datasetID, _ := req.Payload["dataset_id"].(string)
		chunkID, _ := req.Payload["chunk_id"].(string)
		if strings.TrimSpace(datasetID) == "" {
			return nil, nil, nil, httperr.BadRequest(40089, "dataset_id is required")
		}
		if chunkID != "" {
			if req.Action != model.ApprovalActionDelete && req.Action != model.ApprovalActionEnable && req.Action != model.ApprovalActionDisable {
				return nil, nil, nil, httperr.BadRequest(40089, "unsupported chunk action")
			}
			documentID := payloadString(req.Payload, "document_id")
			if strings.TrimSpace(documentID) == "" {
				return nil, nil, nil, httperr.BadRequest(40089, "document_id is required")
			}
			dataset, snapshot, attrs, err := s.approvalChunkSnapshot(ctx, tenantID, datasetID, documentID, chunkID)
			if err != nil {
				return nil, nil, nil, err
			}
			target.Snapshot = snapshot
			attrs["dataset_name"] = dataset.Name
			attrs["document_id"] = snapshot["document_id"]
			attrs["chunk_id"] = req.ObjectID
			return target, attrs, target.Snapshot, nil
		}
		if req.ObjectType == model.ApprovalObjectDocumentChunk {
			return nil, nil, nil, httperr.BadRequest(40089, "chunk_id is required")
		}
		documentID := req.ObjectID
		if strings.TrimSpace(documentID) == "" {
			return nil, nil, nil, httperr.BadRequest(40089, "document_id is required")
		}
		dataset, snapshot, attrs, err := s.approvalDocumentSnapshot(ctx, tenantID, datasetID, documentID)
		if err != nil {
			return nil, nil, nil, err
		}
		target.Snapshot = snapshot
		attrs["dataset_name"] = dataset.Name
		attrs["document_name"] = snapshot["name"]
	case model.ApprovalObjectAgent:
		if req.Action == model.ApprovalActionCreate {
			if err := s.Authorize(ctx, actorID, "manage", "agent"); err != nil {
				return nil, nil, nil, err
			}
			title, _ := req.Payload["title"].(string)
			dsl, _ := req.Payload["dsl"].(map[string]any)
			if strings.TrimSpace(title) == "" || len(dsl) == 0 {
				return nil, nil, nil, httperr.BadRequest(40091, "agent title and dsl are required")
			}
			req.ObjectID = "new:" + title
			attrs["agent_title"] = title
			return target, attrs, target.Snapshot, nil
		}
		if req.Action != model.ApprovalActionUpdate && req.Action != model.ApprovalActionDelete {
			return nil, nil, nil, httperr.BadRequest(40091, "unsupported agent action")
		}
		agent, agentErr := s.Store.GetAgentShadow(ctx, tenantID, req.ObjectID, false)
		if agentErr != nil {
			return nil, nil, nil, agentErr
		}
		if agent == nil {
			return nil, nil, nil, httperr.NotFound("agent not found")
		}
		if err := s.Authorize(ctx, actorID, "manage", "agent"); err != nil {
			return nil, nil, nil, err
		}
		rollbackVersionID, _ := req.Payload["rollback_version_id"].(string)
		target.Snapshot = map[string]any{
			"title": agent.Title, "status": agent.Status, "release": agent.Release,
			"owner_id": agent.OwnerID, "agent_updated_at": agent.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
		if rollbackVersionID != "" {
			target.Snapshot["rollback_version_id"] = rollbackVersionID
		}
		attrs["agent_title"] = agent.Title
	case model.ApprovalObjectChat:
		if req.Action == model.ApprovalActionCreate {
			if err := s.Authorize(ctx, actorID, "manage", "chat"); err != nil {
				return nil, nil, nil, err
			}
			name, _ := req.Payload["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, nil, nil, httperr.BadRequest(40090, "chat name is required")
			}
			req.ObjectID = "new:" + name
			attrs["chat_name"] = name
			return target, attrs, target.Snapshot, nil
		}
		if req.Action != model.ApprovalActionUpdate && req.Action != model.ApprovalActionDelete {
			return nil, nil, nil, httperr.BadRequest(40090, "unsupported chat action")
		}
		chat, chatErr := s.Store.GetChatShadow(ctx, tenantID, req.ObjectID, false)
		if chatErr != nil {
			return nil, nil, nil, chatErr
		}
		if chat == nil {
			return nil, nil, nil, httperr.NotFound("chat not found")
		}
		if err := s.Authorize(ctx, actorID, "manage", "chat"); err != nil {
			return nil, nil, nil, err
		}
		target.Snapshot = map[string]any{
			"name": chat.Name, "status": chat.Status, "dataset_ids": chat.DatasetIDs,
			"owner_id": chat.OwnerID, "chat_updated_at": chat.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
		attrs["chat_name"] = chat.Name
	case model.ApprovalObjectAPIKey:
		if req.Action == model.ApprovalActionCreate {
			if err := s.Authorize(ctx, actorID, "manage", "api-key"); err != nil {
				return nil, nil, nil, err
			}
			name, _ := req.Payload["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, nil, nil, httperr.BadRequest(40090, "key name is required")
			}
			req.ObjectID = "new:" + name
			attrs["key_name"] = name
			attrs["key_quota"] = approvalAnyToFloat(req.Payload["token_quota"])
			attrs["key_request_quota"] = approvalAnyToFloat(req.Payload["request_quota"])
		} else if req.Action == model.ApprovalActionRevoke {
			key, err := s.Store.GetAPIKey(ctx, tenantID, req.ObjectID)
			if err != nil {
				return nil, nil, nil, err
			}
			if key == nil {
				return nil, nil, nil, httperr.NotFound("api key not found")
			}
			if err := s.AuthorizeABAC(ctx, actorID, "manage", "api-key", tenantID, "", key.UserID); err != nil {
				return nil, nil, nil, err
			}
			target.Snapshot = map[string]any{"name": key.Name, "enabled": key.Enabled}
			attrs["key_name"] = key.Name
		}
	case model.ApprovalObjectModelProvider:
		if req.Action != model.ApprovalActionCreate && req.Action != model.ApprovalActionDelete {
			return nil, nil, nil, httperr.BadRequest(40094, "unsupported model provider action")
		}
		if req.Action == model.ApprovalActionCreate {
			if err := s.Authorize(ctx, actorID, "manage", "model-provider"); err != nil {
				return nil, nil, nil, err
			}
			var providerReq CreateModelProviderRequest
			if err := decodeApprovalPayload(req.Payload, &providerReq); err != nil {
				return nil, nil, nil, httperr.BadRequest(40091, "invalid model provider payload")
			}
			if strings.TrimSpace(providerReq.ProviderName) != "" {
				req.ObjectID = "new:" + providerReq.ProviderName
				attrs["provider_type"] = providerReq.ProviderName
				break
			}
			if err := ValidateProviderBaseURL(providerReq.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
				return nil, nil, nil, err
			}
			req.ObjectID = "new:" + providerReq.Name
			attrs["provider_type"] = providerReq.ProviderType
			attrs["base_url"] = providerReq.BaseURL
		} else {
			provider, providerErr := s.Store.GetModelProvider(ctx, tenantID, req.ObjectID)
			if providerErr != nil {
				return nil, nil, nil, providerErr
			}
			if provider == nil {
				return nil, nil, nil, httperr.NotFound("model provider not found")
			}
			if authorizeErr := s.Authorize(ctx, actorID, "manage", "model-provider"); authorizeErr != nil {
				return nil, nil, nil, authorizeErr
			}
			target.Snapshot = map[string]any{
				"provider_id": provider.ID, "name": provider.Name, "provider_type": provider.ProviderType,
				"base_url": provider.BaseURL, "status": provider.Status,
				"updated_at": provider.UpdatedAt.UTC().Format(time.RFC3339Nano),
			}
			attrs["provider_name"] = provider.Name
		}
	case model.ApprovalObjectModelInstance:
		if req.Action != model.ApprovalActionCreate && req.Action != model.ApprovalActionUpdate && req.Action != model.ApprovalActionDelete {
			return nil, nil, nil, httperr.BadRequest(40094, "unsupported model instance action")
		}
		var instanceReq CreateProviderInstanceRequest
		if err := decodeApprovalPayload(req.Payload, &instanceReq); err != nil {
			return nil, nil, nil, httperr.BadRequest(40092, "invalid provider instance payload")
		}
		if err := s.Authorize(ctx, actorID, "manage", "model-provider"); err != nil {
			return nil, nil, nil, err
		}
		if req.Action == model.ApprovalActionCreate {
			if strings.TrimSpace(instanceReq.InstanceName) == "" {
				return nil, nil, nil, httperr.BadRequest(40092, "instance_name is required")
			}
			if err := ValidateProviderBaseURL(instanceReq.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
				return nil, nil, nil, err
			}
			providerID, _ := req.Payload["provider_id"].(string)
			provider, providerErr := s.Store.GetModelProvider(ctx, tenantID, providerID)
			if providerErr != nil {
				return nil, nil, nil, providerErr
			}
			if provider == nil {
				return nil, nil, nil, httperr.NotFound("model provider not found")
			}
			req.ObjectID = "new:" + provider.ID + ":" + instanceReq.InstanceName
			target.Snapshot = map[string]any{
				"provider_id": provider.ID, "provider_type": provider.ProviderType,
				"instance_name": instanceReq.InstanceName, "base_url": instanceReq.BaseURL, "region": instanceReq.Region,
			}
			attrs["provider_name"] = provider.Name
			attrs["instance_name"] = instanceReq.InstanceName
			attrs["base_url"] = instanceReq.BaseURL
			break
		}
		providerID, instanceID, ok := splitApprovalObjectID(req.ObjectID)
		if !ok {
			return nil, nil, nil, httperr.BadRequest(40093, "provider instance object_id must be providerId:instanceId")
		}
		provider, providerErr := s.Store.GetModelProvider(ctx, tenantID, providerID)
		if providerErr != nil {
			return nil, nil, nil, providerErr
		}
		if provider == nil {
			return nil, nil, nil, httperr.NotFound("model provider not found")
		}
		instance, instanceErr := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
		if instanceErr != nil {
			return nil, nil, nil, instanceErr
		}
		if instance == nil {
			return nil, nil, nil, httperr.NotFound("provider instance not found")
		}
		if req.Action == model.ApprovalActionUpdate {
			if err := ValidateProviderBaseURL(instanceReq.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
				return nil, nil, nil, err
			}
			attrs["base_url"] = instanceReq.BaseURL
		} else {
			attrs["instance_name"] = instance.InstanceName
		}
		target.Snapshot = map[string]any{
			"provider_id": providerID, "instance_id": instanceID,
			"instance_name": instance.InstanceName, "base_url": instance.BaseURL,
			"region": instance.Region, "status": instance.Status,
			"updated_at": instance.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	case model.ApprovalObjectModelModel:
		if req.Action != model.ApprovalActionCreate && req.Action != model.ApprovalActionUpdate &&
			req.Action != model.ApprovalActionDelete && req.Action != model.ApprovalActionTest {
			return nil, nil, nil, httperr.BadRequest(40094, "unsupported model action")
		}
		if req.Action == model.ApprovalActionCreate {
			var modelReq ModelInfoInput
			if err := decodeApprovalPayload(req.Payload, &modelReq); err != nil {
				return nil, nil, nil, httperr.BadRequest(40092, "invalid model payload")
			}
			if strings.TrimSpace(modelReq.ModelName) == "" {
				return nil, nil, nil, httperr.BadRequest(40092, "model_name is required")
			}
		} else if req.Action == model.ApprovalActionTest {
			if strings.TrimSpace(fmt.Sprint(req.Payload["message"])) == "" {
				return nil, nil, nil, httperr.BadRequest(40092, "message is required")
			}
		}
		if authorizeErr := s.Authorize(ctx, actorID, "manage", "model-provider"); authorizeErr != nil {
			return nil, nil, nil, authorizeErr
		}
		providerID, _ := req.Payload["provider_id"].(string)
		instanceID, _ := req.Payload["instance_id"].(string)
		modelID, _ := req.Payload["model_id"].(string)
		provider, providerErr := s.Store.GetModelProvider(ctx, tenantID, providerID)
		if providerErr != nil {
			return nil, nil, nil, providerErr
		}
		if provider == nil {
			return nil, nil, nil, httperr.NotFound("model provider not found")
		}
		instance, instanceErr := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
		if instanceErr != nil {
			return nil, nil, nil, instanceErr
		}
		if instance == nil {
			return nil, nil, nil, httperr.NotFound("provider instance not found")
		}
		target.Snapshot = map[string]any{
			"provider_id": provider.ID, "provider_type": provider.ProviderType,
			"instance_id": instance.ID, "instance_name": instance.InstanceName,
			"base_url": instance.BaseURL, "region": instance.Region, "status": instance.Status,
			"updated_at": instance.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
		if req.Action == model.ApprovalActionCreate {
			var modelReq ModelInfoInput
			if err := decodeApprovalPayload(req.Payload, &modelReq); err != nil {
				return nil, nil, nil, err
			}
			req.ObjectID = "new:" + provider.ID + ":" + instance.ID + ":" + modelReq.ModelName
			attrs["model_name"] = modelReq.ModelName
		} else {
			if modelID == "" {
				return nil, nil, nil, httperr.BadRequest(40092, "model_id is required")
			}
			mod, modErr := s.Store.GetModelProviderModel(ctx, tenantID, instanceID, modelID)
			if modErr != nil {
				return nil, nil, nil, modErr
			}
			if mod == nil {
				return nil, nil, nil, httperr.NotFound("model not found")
			}
			target.Snapshot["model_id"] = mod.ID
			target.Snapshot["model_name"] = mod.ModelName
			target.Snapshot["model_status"] = mod.Status
			target.Snapshot["model_max_tokens"] = mod.MaxTokens
			target.Snapshot["model_updated_at"] = mod.UpdatedAt.UTC().Format(time.RFC3339Nano)
			req.ObjectID = provider.ID + ":" + instance.ID + ":" + mod.ID
			attrs["model_name"] = mod.ModelName
		}
	case model.ApprovalObjectEnterpriseConnection:
		if req.Action == model.ApprovalActionCreate {
			if err := s.Authorize(ctx, actorID, "manage", "enterprise-connection"); err != nil {
				return nil, nil, nil, err
			}
			providerName, _ := req.Payload["provider_name"].(string)
			providerName = strings.TrimSpace(providerName)
			if providerName == "" {
				return nil, nil, nil, httperr.BadRequest(40060, "provider_name is required")
			}
			displayName, _ := req.Payload["display_name"].(string)
			displayName = strings.TrimSpace(displayName)
			if displayName == "" {
				return nil, nil, nil, httperr.BadRequest(40060, "display_name is required")
			}
			req.ObjectID = "new:" + providerName
			attrs["provider_name"] = providerName
			return target, attrs, target.Snapshot, nil
		}
		enterpriseTarget, enterpriseErr := s.resolveEnterpriseConnectionApprovalTarget(ctx, actorID, &model.Approval{
			ObjectType: req.ObjectType, ObjectID: req.ObjectID, Action: req.Action,
			PayloadJSON: encodeApprovalPayload(req.Payload), RequesterID: actorID,
		})
		if enterpriseErr != nil {
			return nil, nil, nil, enterpriseErr
		}
		target.Snapshot = enterpriseTarget.Snapshot
		attrs = enterpriseTarget.Attrs
	case model.ApprovalObjectEnterpriseBinding:
		enterpriseTarget, enterpriseErr := s.resolveEnterpriseBindingApprovalTarget(ctx, actorID, &model.Approval{
			ObjectType: req.ObjectType, ObjectID: req.ObjectID, Action: req.Action,
			PayloadJSON: encodeApprovalPayload(req.Payload), RequesterID: actorID,
		})
		if enterpriseErr != nil {
			return nil, nil, nil, enterpriseErr
		}
		target.Snapshot = enterpriseTarget.Snapshot
		attrs = enterpriseTarget.Attrs
	default:
		return nil, nil, nil, httperr.BadRequest(40094, "unsupported approval object")
	}
	return target, attrs, target.Snapshot, nil
}

func (s *Service) approvalStepsFromPolicy(tenantID string, policy *model.ApprovalPolicy, now time.Time) ([]model.ApprovalStep, error) {
	var specs []model.ApprovalStepSpec
	if err := json.Unmarshal([]byte(policy.StepsJSON), &specs); err != nil {
		return nil, httperr.Internal("invalid approval policy steps")
	}
	if len(specs) == 0 {
		return nil, httperr.Internal("approval policy has no steps")
	}
	if err := validateApprovalSteps(specs); err != nil {
		return nil, httperr.Internal("invalid approval policy steps")
	}
	steps := make([]model.ApprovalStep, 0, len(specs))
	for i, spec := range specs {
		hours := spec.ExpireHours
		if hours <= 0 {
			hours = policy.ExpireHours
		}
		dueAt := now.Add(time.Duration(hours) * time.Hour)
		status := model.ApprovalStepPending
		if i == 0 {
			status = model.ApprovalStepCurrent
		}
		approversJSON, err := json.Marshal(spec.Approvers)
		if err != nil {
			return nil, httperr.Internal("invalid approval policy approvers")
		}
		steps = append(steps, model.ApprovalStep{
			ID: id.New(), TenantID: tenantID, ApprovalID: "pending", StepNo: spec.StepNo,
			Name: spec.Name, ApproverType: spec.ApproverType, ApproverValue: spec.ApproverValue,
			ApprovalMode: spec.ApprovalMode, ApproversJSON: string(approversJSON),
			RequiredApprovals: spec.RequiredApprovals,
			Status:            status, DueAt: &dueAt, CreatedAt: now, UpdatedAt: now,
		})
	}
	return steps, nil
}

func decodeApprovalPayload(payload map[string]any, target any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func splitApprovalObjectID(value string) (string, string, bool) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func approvalAnyToFloat(value any) float64 {
	number, _ := approvalNumber(value)
	return number
}

func (s *Service) emitApprovalEvent(ctx context.Context, eventType, title string, approval *model.Approval, severity string) {
	fields := map[string]string{
		"approval_id":  approval.ID,
		"request_no":   approval.RequestNo,
		"object_type":  approval.ObjectType,
		"action":       approval.Action,
		"status":       approval.Status,
		"current_step": strconv.Itoa(approval.CurrentStep),
		"expire_at":    approval.ExpiresAt.UTC().Format(time.RFC3339),
		"action_url":   "/approvals/" + approval.ID + "/show",
	}
	if approval.Status == model.ApprovalStatusPendingApproval {
		if step, err := s.Store.CurrentApprovalStep(ctx, approval.TenantID, approval.ID); err == nil && step != nil {
			fields["approver_type"] = step.ApproverType
			fields["approver_value"] = step.ApproverValue
			if step.DueAt != nil {
				fields["step_due_at"] = step.DueAt.UTC().Format(time.RFC3339)
			}
			switch step.ApproverType {
			case model.ApprovalApproverUser:
				fields["recipients"] = step.ApproverValue
			case model.ApprovalApproverRole:
				fields["recipients"] = "role:" + step.ApproverValue
			case model.ApprovalApproverTeam:
				fields["recipients"] = "team:" + step.ApproverValue
			}
		}
	}
	notify.Emit(ctx, notify.Event{
		Title: title, Severity: severity, Type: eventType, TenantID: approval.TenantID,
		Resource: "approval", ResourceID: approval.ID,
		Detail: fmt.Sprintf("%s %s (%s)", approval.ObjectType, approval.Action, approval.Status),
		Fields: fields,
	})
}
