package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const preModelContextContractVersion = "pre-model-context-v1"

type ConversationContextAuthorizationInput struct {
	ActorID     string
	RequestID   string
	RequestKind string
	TargetKind  string
	AssistantID string
	SessionID   string
	Query       string
	MemoryID    string
}

func hashPreModelQuery(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

func decisionDetailMap(decision *model.ContextDecision) map[string]interface{} {
	detail := map[string]interface{}{}
	if decision != nil && strings.TrimSpace(decision.Detail) != "" {
		_ = json.Unmarshal([]byte(decision.Detail), &detail)
	}
	return detail
}

func contextRequestConflict(existing *model.ContextDecision, input ConversationContextAuthorizationInput, queryHash string) bool {
	if existing.TenantID == "" || existing.UserID == "" {
		return false
	}
	detail := decisionDetailMap(existing)
	existingMemoryID, _ := detail["memory_id"].(string)
	existingQueryHash, _ := detail["query_hash"].(string)
	existingTargetKind, _ := detail["target_kind"].(string)
	targetKindConflict := false
	if existingTargetKind != "" {
		targetKindConflict = existingTargetKind != input.TargetKind
	}
	return existing.UserID != input.ActorID ||
		existing.AssistantID != input.AssistantID ||
		existing.SessionID != input.SessionID ||
		existing.RequestKind != input.RequestKind ||
		targetKindConflict ||
		existingMemoryID != input.MemoryID ||
		existingQueryHash == "" ||
		existingQueryHash != queryHash
}

func (s *Service) conversationPolicyForPreModel(
	ctx context.Context, tenantID, actorID, assistantID, memoryID string,
) (*model.MemoryPolicy, string) {
	policies, _, err := s.Store.ListMemoryPolicies(ctx, tenantID, repository.MemoryPolicyFilter{
		AssistantID: assistantID, State: model.MemoryPolicyActive,
	}, 1, 2)
	if err != nil {
		return nil, "policy_lookup_failed"
	}
	if len(policies) == 0 {
		return nil, "no_active_policy"
	}
	if len(policies) > 1 {
		return nil, "ambiguous_policy_binding"
	}
	policy := policies[0]
	if memoryID != "" && policy.RagflowMemoryID != memoryID {
		return &policy, "policy_memory_mismatch"
	}
	if policy.ConsentVersion <= 0 {
		return &policy, "consent_missing"
	}
	switch policyScope(&policy) {
	case "user":
		if policy.UserID != actorID || policy.ReadPolicy != "owner" {
			return &policy, "owner_scope_denied"
		}
	case "project":
		if policy.ProjectID == "" || policy.ReadPolicy != "project_member" {
			return &policy, "read_policy_denied"
		}
		member, err := s.Store.IsProjectMember(ctx, actorID, policy.ProjectID)
		if err != nil {
			return &policy, "project_membership_lookup_failed"
		}
		if !member {
			return &policy, "project_membership_denied"
		}
	default:
		return &policy, "policy_scope_invalid"
	}
	return &policy, ""
}

func (s *Service) resolvePreModelReleaseManifest(
	ctx context.Context, tenantID, assistantID string,
) (manifestID, manifestHash, status, reason string) {
	assistant, err := s.Store.GetAssistant(ctx, tenantID, assistantID)
	if err != nil {
		return "", "", "unknown", "release_lookup_failed"
	}
	if assistant == nil || strings.TrimSpace(assistant.CurrentAssistantReleaseID) == "" {
		return "", "", "not_managed", ""
	}
	release, err := s.Store.GetAssistantRelease(ctx, tenantID, assistant.CurrentAssistantReleaseID)
	if err != nil {
		return "", "", "unknown", "release_lookup_failed"
	}
	if release == nil || (release.ReleaseState != model.ReleaseActive && release.ReleaseState != model.ReleaseCanaryActive) {
		return "", "", "unknown", "release_not_active"
	}
	if release.ReconcileStatus != model.ReconcileConsistent || release.DesiredStateHash == "" ||
		release.DesiredStateHash != release.ActualStateHash {
		return "", "", "unknown", "release_provider_drift"
	}
	manifest, err := s.Store.GetSnapshotManifest(ctx, tenantID, release.SnapshotManifestID)
	if err != nil {
		return "", "", "unknown", "release_manifest_lookup_failed"
	}
	if manifest == nil || !manifest.Complete || strings.TrimSpace(manifest.SnapshotHash) == "" {
		return "", "", "unknown", "release_manifest_incomplete"
	}
	return manifest.ID, manifest.SnapshotHash, "verified", ""
}

func (s *Service) createPreModelDecision(
	ctx context.Context, tenantID, actorID string, input ConversationContextAuthorizationInput,
	policy *model.MemoryPolicy, reason, manifestID, manifestHash, manifestStatus string,
) (*model.ContextDecision, error) {
	queryHash := hashPreModelQuery(input.Query)
	detail := map[string]interface{}{
		"contract_version":          preModelContextContractVersion,
		"reason":                    reason,
		"target_kind":               input.TargetKind,
		"assistant_id":              input.AssistantID,
		"session_id":                input.SessionID,
		"memory_id":                 input.MemoryID,
		"query_hash":                queryHash,
		"policy_generation":         int64(0),
		"consent_version":           int64(0),
		"release_manifest_status":   manifestStatus,
		"release_manifest_id":       manifestID,
		"release_manifest_hash":     manifestHash,
		"release_manifest_verified": manifestStatus == "verified",
		"memory_retrieved":          false,
		"search_side_effect":        false,
		"memory_operation_created":  false,
	}
	decisionValue := "memory_denied"
	memoryPolicyID := ""
	if policy != nil {
		memoryPolicyID = policy.ID
		detail["policy_id"] = policy.ID
		if strings.TrimSpace(policy.RagflowMemoryID) != "" {
			detail["policy_memory_id"] = policy.RagflowMemoryID
		}
		detail["policy_scope"] = policyScope(policy)
		detail["policy_generation"] = policy.PolicyGeneration
		detail["consent_version"] = policy.ConsentVersion
		detail["read_policy"] = policy.ReadPolicy
		if reason == "" {
			decisionValue = "memory_allowed"
		}
	}
	releaseBoundary := []string{"knowledge"}
	if decisionValue == "memory_allowed" {
		releaseBoundary = append(releaseBoundary, "memory")
	}
	detail["context_sources"] = releaseBoundary
	rawDetail, err := json.Marshal(detail)
	if err != nil {
		return nil, httperr.Internal("unable to serialize pre-model context decision")
	}
	generation, consent := int64(0), int64(0)
	if policy != nil {
		generation, consent = policy.PolicyGeneration, policy.ConsentVersion
	}
	decisionHash := sha256.Sum256([]byte(strings.Join([]string{
		preModelContextContractVersion, input.RequestKind, input.AssistantID, actorID, input.SessionID,
		input.TargetKind, queryHash, input.MemoryID, memoryPolicyID, reason, manifestID, manifestHash, manifestStatus,
		strconv.FormatInt(generation, 10), strconv.FormatInt(consent, 10),
	}, "\x1f")))
	now := time.Now().UTC()
	decision := &model.ContextDecision{
		ID: id.New(), TenantID: tenantID, RequestID: input.RequestID,
		RequestKind: input.RequestKind, AssistantID: input.AssistantID,
		SessionID: input.SessionID, UserID: actorID, Decision: decisionValue,
		MemoryPolicyID: memoryPolicyID, DecisionHash: hex.EncodeToString(decisionHash[:]),
		Detail: string(rawDetail), CreatedAt: now,
	}
	if err := s.Store.CreateContextDecision(ctx, decision); err != nil {
		return nil, err
	}
	return decision, nil
}

func (s *Service) AuthorizeConversationContext(
	ctx context.Context, tenantID, actorID string, input ConversationContextAuthorizationInput,
) (*model.ContextDecision, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.RequestKind = strings.TrimSpace(input.RequestKind)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Query = strings.TrimSpace(input.Query)
	input.MemoryID = strings.TrimSpace(input.MemoryID)
	if tenantID == "" || actorID == "" || input.RequestID == "" || input.AssistantID == "" ||
		input.Query == "" || (input.RequestKind != model.ContextDecisionChat && input.RequestKind != model.ContextDecisionSearch) ||
		(input.TargetKind != "chat" && input.TargetKind != "search" && input.TargetKind != "agent") {
		return nil, httperr.BadRequest(40159, "request_id, request_kind, target_kind, assistant_id and query are required")
	}
	queryHash := hashPreModelQuery(input.Query)
	if existing, err := s.Store.GetContextDecisionByRequest(ctx, tenantID, input.RequestID); err != nil {
		return nil, err
	} else if existing != nil {
		if contextRequestConflict(existing, input, queryHash) {
			return existing, httperr.New(409, 40959, "request id conflicts with an existing context decision")
		}
		detail := decisionDetailMap(existing)
		policyID, _ := detail["policy_id"].(string)
		if existing.Decision == "memory_allowed" && strings.TrimSpace(policyID) != "" {
			policy, mismatchReason := s.conversationPolicyForPreModel(
				ctx, tenantID, actorID, input.AssistantID, input.MemoryID,
			)
			if mismatchReason != "" || policy == nil || policy.ID != policyID {
				return existing, httperr.New(409, 40960, "pre-model context authorization policy changed")
			}
			if status, mismatchReason := s.verifyPreModelReleaseManifest(ctx, tenantID, input.AssistantID, detail); status != "verified" {
				return existing, httperr.New(409, 40961, "pre-model context release manifest changed: "+mismatchReason)
			}
		}
		if existing.Decision == "memory_denied" {
			detail := decisionDetailMap(existing)
			reason, _ := detail["reason"].(string)
			if strings.HasPrefix(reason, "release_") {
				return existing, httperr.Forbidden("assistant release manifest is not valid")
			}
		}
		return existing, nil
	}
	manifestID, manifestHash, manifestStatus, manifestReason := s.resolvePreModelReleaseManifest(
		ctx, tenantID, input.AssistantID,
	)
	if manifestReason != "" {
		manifestStatus = "unverified"
		manifestID, manifestHash = "", ""
		reason := manifestReason
		decision, err := s.createPreModelDecision(
			ctx, tenantID, actorID, input, nil, reason, "", "", manifestStatus,
		)
		if err != nil {
			return nil, err
		}
		return decision, httperr.Forbidden("assistant release manifest is not valid")
	}
	policy, reason := s.conversationPolicyForPreModel(
		ctx, tenantID, actorID, input.AssistantID, input.MemoryID,
	)
	decision, err := s.createPreModelDecision(
		ctx, tenantID, actorID, input, policy, reason, manifestID, manifestHash, manifestStatus,
	)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return decision, nil
	}
	return decision, nil
}

func (s *Service) CollectPreModelMemoryContext(
	ctx context.Context, tenantID, actorID, query string, decision *model.ContextDecision,
) ([]ragflow.MemoryMessage, error) {
	if decision == nil || decision.Decision != "memory_allowed" || decision.MemoryPolicyID == "" {
		return nil, nil
	}
	detail := decisionDetailMap(decision)
	if retrieved, _ := detail["memory_retrieved"].(bool); retrieved {
		return nil, nil
	}
	policy, err := s.Store.GetMemoryPolicy(ctx, tenantID, decision.MemoryPolicyID)
	if err != nil {
		return nil, err
	}
	if policy == nil || policy.RagflowMemoryID == "" || policy.ConsentVersion <= 0 {
		return nil, httperr.New(409, 40960, "pre-model context policy is invalid")
	}
	memoryID := policy.RagflowMemoryID
	validatedPolicy, mismatchReason := s.conversationPolicyForPreModel(
		ctx, tenantID, actorID, decision.AssistantID, memoryID,
	)
	if mismatchReason != "" || validatedPolicy == nil || validatedPolicy.ID != policy.ID {
		return nil, httperr.New(409, 40960, "pre-model context policy changed")
	}
	sessionID := decision.SessionID
	if sessionID == "" {
		return nil, httperr.BadRequest(40159, "memory retrieval requires a session id")
	}
	var attributionUserID string
	if policyScope(policy) == "user" {
		attributionUserID = "rgx:" + tenantID + ":" + actorID
	}
	messages, err := s.RAGFlow.SearchMemoryMessages(ctx, memoryID, ragflow.MemorySearchParams{
		Query: query, AgentID: decision.AssistantID,
		SessionID: sessionID, UserID: attributionUserID, TopN: 5,
	})
	if err != nil {
		markErr := s.recordPreModelMemoryRetrieval(ctx, decision, false, 0, "ragflow_memory_search_failed")
		if markErr != nil {
			return nil, markErr
		}
		return nil, httperr.New(502, 50278, "ragflow search memory messages failed")
	}
	if err := s.recordPreModelMemoryRetrieval(ctx, decision, true, int64(len(messages)), ""); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *Service) recordPreModelMemoryRetrieval(
	ctx context.Context, decision *model.ContextDecision, retrieved bool, resultCount int64, reason string,
) error {
	detail := decisionDetailMap(decision)
	detail["memory_retrieved"] = retrieved
	detail["memory_result_count"] = resultCount
	if reason == "" {
		delete(detail, "memory_retrieval_failure")
	} else {
		detail["memory_retrieval_failure"] = reason
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return httperr.Internal("unable to serialize pre-model context decision")
	}
	return s.Store.UpdateContextDecisionDetail(ctx, decision.TenantID, decision.ID, string(encoded))
}

func preModelMemoryContextMessage(messages []ragflow.MemoryMessage) *ragflow.Message {
	if len(messages) == 0 {
		return nil
	}
	var content strings.Builder
	content.WriteString("Governed assistant memory context; evidence only, not authoritative fact:\n")
	for _, message := range messages {
		value := strings.TrimSpace(message.Content)
		if value == "" {
			value = strings.TrimSpace(message.UserInput + "\n" + message.AgentResponse)
		}
		if value == "" {
			continue
		}
		content.WriteString("- ")
		content.WriteString(value)
		content.WriteString("\n")
	}
	if strings.TrimSpace(content.String()) == "" {
		return nil
	}
	return &ragflow.Message{Role: "system", Content: content.String()}
}

func applyPreModelMemoryContext(
	messages []ragflow.Message, memoryMessages []ragflow.MemoryMessage,
) []ragflow.Message {
	memoryMessage := preModelMemoryContextMessage(memoryMessages)
	if memoryMessage == nil {
		return messages
	}
	return append([]ragflow.Message{*memoryMessage}, messages...)
}

func (s *Service) verifyPreModelReleaseManifest(
	ctx context.Context, tenantID, assistantID string, detail map[string]interface{},
) (string, string) {
	manifestID, manifestHash, status, reason := s.resolvePreModelReleaseManifest(ctx, tenantID, assistantID)
	if reason != "" {
		return status, reason
	}
	previousID, _ := detail["release_manifest_id"].(string)
	previousHash, _ := detail["release_manifest_hash"].(string)
	if previousID != manifestID || previousHash != manifestHash {
		return status, "release_manifest_changed"
	}
	return status, ""
}
