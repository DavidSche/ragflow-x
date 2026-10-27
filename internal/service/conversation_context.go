package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const conversationContextContractVersion = "phase0-fail-closed-v1"

type ConversationContextPreviewInput struct {
	RequestKind string `json:"request_kind" binding:"required"`
	AssistantID string `json:"assistant_id" binding:"required"`
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
}

type ConversationContextPreview struct {
	Decision       *model.ContextDecision `json:"decision"`
	MemoryEligible bool                   `json:"memory_eligible"`
	Reason         string                 `json:"reason"`
}

func (s *Service) PreviewConversationContext(
	ctx context.Context, tenantID, actorID string, input ConversationContextPreviewInput,
) (*ConversationContextPreview, error) {
	input.RequestKind = strings.TrimSpace(input.RequestKind)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.RequestKind != model.ContextDecisionSearch && input.RequestKind != model.ContextDecisionChat {
		return nil, httperr.BadRequest(40130, "request_kind must be search or chat")
	}
	if input.AssistantID == "" || input.RequestID == "" {
		return nil, httperr.BadRequest(40130, "assistant_id and request_id are required")
	}
	if existing, err := s.Store.GetContextDecisionByRequest(ctx, tenantID, input.RequestID); err != nil {
		return nil, err
	} else if existing != nil {
		return &ConversationContextPreview{Decision: existing, Reason: contextReason(existing.Decision)}, nil
	}
	policies, _, err := s.Store.ListMemoryPolicies(ctx, tenantID, repository.MemoryPolicyFilter{
		Scope: "user", UserID: actorID, AssistantID: input.AssistantID, State: model.MemoryPolicyActive,
	}, 1, 1)
	if err != nil {
		return nil, err
	}
	decision := "memory_denied"
	reason := "no_active_policy"
	memoryPolicyID := ""
	policyGeneration := int64(0)
	consentVersion := int64(0)
	if len(policies) > 0 && policies[0].ReadPolicy != "deny" {
		decision = "memory_allowed"
		reason = "active_policy"
		memoryPolicyID = policies[0].ID
		policyGeneration = policies[0].PolicyGeneration
		consentVersion = policies[0].ConsentVersion
	}
	now := time.Now().UTC()
	detail := map[string]interface{}{
		"contract_version":         conversationContextContractVersion,
		"reason":                   reason,
		"policy_generation":        policyGeneration,
		"consent_version":          consentVersion,
		"search_side_effect":       false,
		"memory_operation_created": false,
	}
	rawDetail, _ := json.Marshal(detail)
	hash := sha256.Sum256([]byte(input.RequestKind + "\x1f" + input.AssistantID + "\x1f" +
		actorID + "\x1f" + reason))
	item := &model.ContextDecision{
		ID: id.New(), TenantID: tenantID, RequestID: input.RequestID,
		RequestKind: input.RequestKind, AssistantID: input.AssistantID,
		SessionID: input.SessionID, UserID: actorID, Decision: decision,
		MemoryPolicyID: memoryPolicyID,
		DecisionHash:   hex.EncodeToString(hash[:]), Detail: string(rawDetail), CreatedAt: now,
	}
	if err := s.Store.CreateContextDecision(ctx, item); err != nil {
		return nil, err
	}
	return &ConversationContextPreview{
		Decision: item, MemoryEligible: decision == "memory_allowed", Reason: reason,
	}, nil
}

func (s *Service) GetConversationContextDecision(
	ctx context.Context, tenantID, actorID, decisionID string,
) (*model.ContextDecision, error) {
	decision, err := s.Store.GetContextDecision(ctx, tenantID, decisionID)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		return nil, httperr.NotFound("context decision not found")
	}
	if !s.canViewContextDecision(ctx, actorID, decision) {
		return nil, httperr.NotFound("context decision not found")
	}
	return decision, nil
}

func (s *Service) GetConversationContextDecisionByRequest(
	ctx context.Context, tenantID, actorID, requestID string,
) (*model.ContextDecision, error) {
	decision, err := s.Store.GetContextDecisionByRequest(ctx, tenantID, requestID)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		return nil, httperr.NotFound("context decision not found")
	}
	if !s.canViewContextDecision(ctx, actorID, decision) {
		return nil, httperr.NotFound("context decision not found")
	}
	return decision, nil
}

func (s *Service) canViewContextDecision(
	ctx context.Context, actorID string, decision *model.ContextDecision,
) bool {
	if decision == nil {
		return false
	}
	if decision.UserID == actorID {
		return true
	}
	roles, err := s.Store.ListRolesByUser(ctx, actorID)
	if err != nil {
		return false
	}
	for _, role := range roles {
		switch role.ID {
		case model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator:
			return true
		}
	}
	return false
}

func (s *Service) ListConversationContextDecisions(
	ctx context.Context, tenantID, actorID string, filter repository.ContextDecisionFilter, page, pageSize int,
) ([]model.ContextDecision, int64, error) {
	roles, err := s.Store.ListRolesByUser(ctx, actorID)
	if err != nil {
		return nil, 0, err
	}
	governance := false
	for _, role := range roles {
		if role.ID == model.RolePlatformAdmin || role.ID == model.RoleTenantAdmin || role.ID == model.RoleOperator {
			governance = true
			break
		}
	}
	if !governance {
		filter.UserID = actorID
	}
	return s.Store.ListContextDecisions(ctx, tenantID, filter, page, pageSize)
}

func contextReason(decision string) string {
	if decision == "memory_allowed" {
		return "active_policy"
	}
	return "no_active_policy"
}
