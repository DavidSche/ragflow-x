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
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// supportedMemoryTypes are the memory kinds RAGFlow accepts. A usable memory
// must include the raw type as its baseline store.
var supportedMemoryTypes = map[string]struct{}{
	"raw": {}, "semantic": {}, "episodic": {}, "procedural": {},
}

// ListMemories returns the tenant's memories (or all tenants for scopeAll).
func (s *Service) ListMemories(ctx context.Context, tenantID string, scopeAll bool, filter repository.MemoryFilter, page, pageSize int) ([]model.MemoryShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListMemoryShadows(ctx, tenantID, scopeAll, filter, page, pageSize)
}

// GetMemory returns a memory the caller may access.
func (s *Service) GetMemory(ctx context.Context, tenantID, memoryID string, scopeAll bool) (*model.MemoryShadow, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	return mem, nil
}

// GetMemoryConfig returns the live RAGFlow-side memory config for the editor.
func (s *Service) GetMemoryConfig(ctx context.Context, tenantID, memoryID string, scopeAll bool) (*ragflow.Memory, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	live, err := s.RAGFlow.GetMemoryConfig(ctx, memoryID)
	if err != nil {
		return nil, httperr.New(502, 50270, "ragflow get memory config failed")
	}
	if live == nil {
		return nil, httperr.NotFound("memory not found")
	}
	return live, nil
}

// validateMemoryCreate enforces the same baseline rules RAGFlow's own create
// dialog applies: a non-empty name, at least the "raw" memory type, and both a
// chat (LLM) and embedding model so the memory is actually usable.
func validateMemoryCreate(name string, memoryType []string, embdID, llmID string) error {
	if strings.TrimSpace(name) == "" {
		return httperr.BadRequest(40095, "memory name is required")
	}
	if len(name) > 100 {
		return httperr.BadRequest(40095, "memory name exceeds the 100 character limit")
	}
	if len(memoryType) == 0 {
		return httperr.BadRequest(40097, "memory type is required")
	}
	hasRaw := false
	for _, mt := range memoryType {
		if _, ok := supportedMemoryTypes[mt]; !ok {
			return httperr.BadRequest(40097, "unsupported memory type: "+mt)
		}
		if mt == "raw" {
			hasRaw = true
		}
	}
	if !hasRaw {
		return httperr.BadRequest(40097, "memory type must include raw")
	}
	if strings.TrimSpace(embdID) == "" {
		return httperr.BadRequest(40098, "embedding model is required")
	}
	if strings.TrimSpace(llmID) == "" {
		return httperr.BadRequest(40099, "llm model is required")
	}
	return nil
}

// CreateMemory creates a memory in RAGFlow and records its ownership.
func (s *Service) CreateMemory(ctx context.Context, tenantID, name string, memoryType []string, embdID, llmID string) (*model.MemoryShadow, error) {
	if err := validateMemoryCreate(name, memoryType, embdID, llmID); err != nil {
		return nil, err
	}
	id, err := s.RAGFlow.CreateMemory(ctx, ragflow.CreateMemoryRequest{Name: name, MemoryType: memoryType, EmbdID: embdID, LLMID: llmID})
	if err != nil {
		return nil, httperr.New(502, 50271, "ragflow create memory failed")
	}
	now := time.Now().UTC()
	mem := &model.MemoryShadow{
		ID: id, TenantID: tenantID, Name: name, MemoryType: strings.Join(memoryType, ","),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.UpsertMemoryShadow(ctx, mem); err != nil {
		return nil, err
	}
	return mem, nil
}

// UpdateMemory updates a memory the caller owns.
func (s *Service) UpdateMemory(ctx context.Context, tenantID, memoryID string, req ragflow.UpdateMemoryRequest, scopeAll bool) (*model.MemoryShadow, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return nil, httperr.BadRequest(40095, "memory name is required")
	}
	if req.MemoryType != nil && len(req.MemoryType) == 0 {
		return nil, httperr.BadRequest(40097, "memory type is required")
	}
	if _, err := s.RAGFlow.UpdateMemory(ctx, memoryID, req); err != nil {
		return nil, httperr.New(502, 50272, "ragflow update memory failed")
	}
	if req.Name != nil {
		mem.Name = *req.Name
	}
	if req.MemoryType != nil {
		mem.MemoryType = strings.Join(req.MemoryType, ",")
	}
	mem.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpsertMemoryShadow(ctx, mem); err != nil {
		return nil, err
	}
	return mem, nil
}

// DeleteMemory deletes a memory the caller owns.
func (s *Service) DeleteMemory(ctx context.Context, tenantID, memoryID string, scopeAll bool) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	if err := s.RAGFlow.DeleteMemory(ctx, memoryID); err != nil {
		return httperr.New(502, 50273, "ragflow delete memory failed")
	}
	return s.Store.DeleteMemoryShadow(ctx, memoryID)
}

// ListMemoryMessages lists the messages of an owned memory.
func (s *Service) ListMemoryMessages(ctx context.Context, tenantID, memoryID string, scopeAll bool) ([]ragflow.MemoryMessage, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	msgs, err := s.RAGFlow.ListMemoryMessages(ctx, memoryID)
	if err != nil {
		return nil, httperr.New(502, 50274, "ragflow list memory messages failed")
	}
	return msgs, nil
}

// AddMemoryMessage stores a message into an owned memory.
func (s *Service) AddMemoryMessage(ctx context.Context, tenantID, memoryID, agentID, sessionID, userInput, agentResponse string, scopeAll bool) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	if strings.TrimSpace(userInput) == "" || strings.TrimSpace(agentResponse) == "" {
		return httperr.BadRequest(40096, "user_input and agent_response are required")
	}
	if err := s.RAGFlow.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: agentID, SessionID: sessionID,
		UserInput: userInput, AgentResponse: agentResponse,
	}); err != nil {
		return httperr.New(502, 50275, "ragflow add memory message failed")
	}
	return nil
}

// DeleteMemoryMessage forgets a message of an owned memory.
// DeleteMemoryMessage forgets a message through the legacy RAGFlow endpoint,
// behind the MEM-014 policy/ownership/tombstone gate.
func (s *Service) DeleteMemoryMessage(ctx context.Context, tenantID, memoryID string, messageID int64, scopeAll bool) error {
	if err := s.enforceLegacyMemoryMessageGate(ctx, tenantID, memoryID, messageID, scopeAll); err != nil {
		return err
	}
	if err := s.RAGFlow.DeleteMemoryMessage(ctx, memoryID, messageID); err != nil {
		return httperr.New(502, 50276, "ragflow forget memory message failed")
	}
	return nil
}

// ComplianceDeleteMemoryMessage implements the 137 plan section 8.2 admin
// compliance delete (POST /memories/:memoryId/messages/:messageId/
// compliance-delete, manage @ memory-forget). It is the governed counterpart
// of the legacy forget endpoint: active-policy-governed memories stay
// deletable for tenant/platform admins, a live tombstone still owns the
// external deletion, the provider delete runs before the forget stamp so a
// failure leaves both sides unchanged, and the caller records the audit entry
// with the compliance reason. It is never a substitute for ordinary user
// withdraw (execute @ memory-forget).
func (s *Service) ComplianceDeleteMemoryMessage(
	ctx context.Context, tenantID, memoryID string, messageID int64, reason string, scopeAll bool,
) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 256 {
		return httperr.BadRequest(40099, "compliance delete reason exceeds 256 characters")
	}
	operation, err := s.Store.FindMemoryOperationForExternalMessage(ctx, tenantID, memoryID, strconv.FormatInt(messageID, 10))
	if err != nil {
		return err
	}
	if operation != nil && operation.TombstoneActive {
		return httperr.New(409, 40959,
			"memory message tombstone is active; the compensation worker owns the external deletion")
	}
	if err := s.RAGFlow.DeleteMemoryMessage(ctx, memoryID, messageID); err != nil {
		return httperr.New(502, 50279, "ragflow compliance delete memory message failed")
	}
	if operation != nil && operation.ForgetAt == nil {
		if _, err := s.Store.MarkMemoryOperationForgotten(ctx, tenantID, operation.ID, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// UpdateMemoryMessageStatus sets a message's remember/forget status.
// enforceLegacyMemoryMessageGate implements the MEM-014 gate shared by the
// legacy status/forget message endpoints: the caller must be the memory owner
// (or a tenant admin via scopeAll), the memory must not be governed by an
// active memory policy, and the target message must not be tombstoned or
// already forgotten through the memory write path.
func (s *Service) enforceLegacyMemoryMessageGate(
	ctx context.Context, tenantID, memoryID string, messageID int64, scopeAll bool,
) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	policies, _, err := s.Store.ListMemoryPolicies(ctx, tenantID, repository.MemoryPolicyFilter{MemoryID: memoryID}, 1, 1)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		if policy.State == model.MemoryPolicyActive {
			return httperr.New(409, 40957,
				"memory is governed by an active memory policy; use the governed memory operation endpoints")
		}
	}
	operation, err := s.Store.FindMemoryOperationForExternalMessage(ctx, tenantID, memoryID, strconv.FormatInt(messageID, 10))
	if err != nil {
		return err
	}
	if operation != nil {
		if operation.TombstoneActive || operation.ForgetAt != nil {
			return httperr.New(409, 40958, "memory message is tombstoned or already forgotten")
		}
		if _, err := s.Store.MarkMemoryOperationForgotten(ctx, tenantID, operation.ID, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// UpdateMemoryMessageStatus sets a message's status through the legacy RAGFlow
// endpoint, behind the MEM-014 policy/ownership/tombstone gate.
func (s *Service) UpdateMemoryMessageStatus(ctx context.Context, tenantID, memoryID string, messageID int64, status bool, scopeAll bool) error {
	if err := s.enforceLegacyMemoryMessageGate(ctx, tenantID, memoryID, messageID, scopeAll); err != nil {
		return err
	}
	if err := s.RAGFlow.UpdateMemoryMessageStatus(ctx, memoryID, messageID, status); err != nil {
		return httperr.New(502, 50277, "ragflow update memory message failed")
	}
	return nil
}

// MemorySearchInput carries explicit RAGFlow attribution and assistant filters.
type MemorySearchInput struct {
	Query                    string
	AgentID                  string
	SessionID                string
	UserID                   string
	SimilarityThreshold      float64
	KeywordsSimilarityWeight float64
	TopN                     int
}

// SearchMemoryMessages runs a semantic retrieval against an owned memory.
func (s *Service) SearchMemoryMessages(
	ctx context.Context, tenantID, memoryID string, input MemorySearchInput, scopeAll bool,
) ([]ragflow.MemoryMessage, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	rows, err := s.RAGFlow.SearchMemoryMessages(ctx, memoryID, ragflow.MemorySearchParams{
		Query: input.Query, AgentID: input.AgentID, SessionID: input.SessionID, UserID: input.UserID,
		SimilarityThreshold: input.SimilarityThreshold, KeywordsSimilarityWeight: input.KeywordsSimilarityWeight, TopN: input.TopN,
	})
	if err != nil {
		return nil, httperr.New(502, 50278, "ragflow search memory messages failed")
	}
	return rows, nil
}

// GetMemoryMessageContent returns an owned memory message's full content.
func (s *Service) GetMemoryMessageContent(ctx context.Context, tenantID, memoryID string, messageID int64, scopeAll bool) (json.RawMessage, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	content, err := s.RAGFlow.GetMemoryMessageContent(ctx, memoryID, messageID)
	if err != nil {
		return nil, httperr.New(502, 50279, "ragflow get memory message content failed")
	}
	return content, nil
}

type GovernedMemorySearchInput struct {
	MemoryID                 string
	Query                    string
	AssistantID              string
	SessionID                string
	RequestID                string
	SimilarityThreshold      float64
	KeywordsSimilarityWeight float64
	TopN                     int
	// KnowledgeChunks optionally carries the top-k knowledge chunk bodies of the
	// same answer run (doc 137 §6.3 #8). When provided, deterministic conflict
	// evaluation runs against knowledge fact annotations; when absent, every
	// Stable Fact is fail-closed downgraded as memory_conflict_unknown.
	KnowledgeChunks []string
}

type GovernedMemorySearchResult struct {
	Decision *model.ContextDecision  `json:"decision"`
	Messages []ragflow.MemoryMessage `json:"messages"`
	// Conflict records the deterministic Memory/Knowledge conflict-downgrade
	// evaluation (doc 137 §6.3 #8). Nil only for legacy call sites.
	Conflict *MemoryConflictSummary `json:"conflict,omitempty"`
}

func (s *Service) SearchGovernedMemory(
	ctx context.Context, tenantID, actorID, memoryID string, input GovernedMemorySearchInput,
) (*GovernedMemorySearchResult, error) {
	started := time.Now()
	memoryID = strings.TrimSpace(memoryID)
	input.Query = strings.TrimSpace(input.Query)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.MemoryID = memoryID
	if tenantID == "" || actorID == "" || memoryID == "" || input.Query == "" ||
		input.MemoryID == "" || input.AssistantID == "" || input.SessionID == "" || input.RequestID == "" {
		return nil, httperr.BadRequest(40147, "memory_id, assistant_id, session_id, request_id and query are required")
	}
	if input.TopN <= 0 {
		input.TopN = 5
	}
	if input.TopN > 100 || input.SimilarityThreshold < 0 || input.SimilarityThreshold > 1 ||
		input.KeywordsSimilarityWeight < 0 || input.KeywordsSimilarityWeight > 1 {
		return nil, httperr.BadRequest(40148, "memory search bounds are invalid")
	}

	policies, _, err := s.Store.ListMemoryPolicies(ctx, tenantID, repository.MemoryPolicyFilter{
		MemoryID: memoryID, AssistantID: input.AssistantID, State: model.MemoryPolicyActive,
	}, 1, 2)
	if err != nil {
		return nil, err
	}
	if len(policies) != 1 {
		_, decisionErr := s.createMemorySearchDecision(ctx, tenantID, actorID, input, nil, "no_active_policy_binding")
		if decisionErr != nil {
			return nil, decisionErr
		}
		return nil, httperr.Forbidden("memory retrieval is not authorized by an active policy binding")
	}
	policy := policies[0]

	principal, err := s.Store.GetUser(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if principal == nil || principal.TenantID != tenantID || principal.Status != model.UserStatusActive {
		obs.Get().AddMemoryPrincipalMapping("failure", "principal_inactive")
		_, decisionErr := s.createMemorySearchDecision(ctx, tenantID, actorID, input, &policy, "principal_inactive")
		if decisionErr != nil {
			return nil, decisionErr
		}
		obs.Get().ObserveContextDecisionDuration(model.ContextDecisionSearch, time.Since(started).Seconds())
		return nil, httperr.Forbidden("memory retrieval principal is not active")
	}
	if policyScope(&policy) == "user" {
		if policy.UserID != actorID {
			_, decisionErr := s.createMemorySearchDecision(ctx, tenantID, actorID, input, &policy, "owner_scope_denied")
			if decisionErr != nil {
				return nil, decisionErr
			}
			return nil, httperr.NotFound("memory retrieval is outside the caller scope")
		}
		if policy.ReadPolicy != "owner" {
			_, decisionErr := s.createMemorySearchDecision(ctx, tenantID, actorID, input, &policy, "read_policy_denied")
			if decisionErr != nil {
				return nil, decisionErr
			}
			return nil, httperr.Forbidden("memory read policy denies retrieval")
		}
	} else {
		member, err := s.Store.IsProjectMember(ctx, actorID, policy.ProjectID)
		if err != nil {
			return nil, err
		}
		if !member {
			_, decisionErr := s.createMemorySearchDecision(ctx, tenantID, actorID, input, &policy, "project_membership_denied")
			if decisionErr != nil {
				return nil, decisionErr
			}
			return nil, httperr.NotFound("memory retrieval is outside the caller scope")
		}
		if policy.ReadPolicy != "project_member" {
			_, decisionErr := s.createMemorySearchDecision(ctx, tenantID, actorID, input, &policy, "read_policy_denied")
			if decisionErr != nil {
				return nil, decisionErr
			}
			return nil, httperr.Forbidden("memory read policy denies retrieval")
		}
	}
	memory, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, false)
	if err != nil {
		return nil, err
	}
	if memory == nil {
		return nil, httperr.NotFound("memory not found")
	}
	decision, err := s.createMemorySearchDecision(ctx, tenantID, actorID, input, &policy, "active_policy_binding")
	if err != nil {
		return nil, err
	}
	var attributionUserID string
	if policyScope(&policy) == "user" {
		attributionUserID = "rgx:" + tenantID + ":" + actorID
	}
	// 137 §11.1: the outbound search carries the enforced policy scope
	// (memory id, assistant/session binding and attribution identity). Every
	// outbound search is counted as authorized; an unauthorized label is
	// reserved for any future path that reaches the provider without those
	// filters and must stay at zero.
	obs.Get().AddMemorySearchAuthorizedFilter("authorized")
	obs.Get().AddMemoryPrincipalMapping("success", "")
	rows, err := s.RAGFlow.SearchMemoryMessages(ctx, memoryID, ragflow.MemorySearchParams{
		Query: input.Query, AgentID: input.AssistantID, SessionID: input.SessionID,
		UserID: attributionUserID, SimilarityThreshold: input.SimilarityThreshold,
		KeywordsSimilarityWeight: input.KeywordsSimilarityWeight, TopN: input.TopN,
	})
	if err != nil {
		obs.Get().ObserveContextDecisionDuration(model.ContextDecisionSearch, time.Since(started).Seconds())
		return nil, httperr.New(502, 50278, "ragflow search memory messages failed")
	}
	obs.Get().ObserveContextDecisionDuration(model.ContextDecisionSearch, time.Since(started).Seconds())
	return &GovernedMemorySearchResult{
		Decision: decision,
		Messages: rows,
		Conflict: evaluateMemoryConflicts(rows, input.KnowledgeChunks),
	}, nil
}

func (s *Service) createMemorySearchDecision(
	ctx context.Context, tenantID, actorID string, input GovernedMemorySearchInput,
	policy *model.MemoryPolicy, reason string,
) (*model.ContextDecision, error) {
	queryHash := sha256.Sum256([]byte(input.Query))
	queryHashHex := hex.EncodeToString(queryHash[:])
	if existing, err := s.Store.GetContextDecisionByRequest(ctx, tenantID, input.RequestID); err != nil {
		return nil, err
	} else if existing != nil {
		var existingDetail map[string]interface{}
		_ = json.Unmarshal([]byte(existing.Detail), &existingDetail)
		existingMemoryID, _ := existingDetail["memory_id"].(string)
		existingQueryHash, _ := existingDetail["query_hash"].(string)
		if existing.TenantID != tenantID || existing.UserID != actorID ||
			existing.AssistantID != input.AssistantID || existing.SessionID != input.SessionID ||
			existingMemoryID != input.MemoryID || existingQueryHash != queryHashHex {
			return nil, httperr.New(409, 40958, "memory search request id conflicts with an existing decision")
		}
		return existing, nil
	}
	detail := map[string]interface{}{
		"contract_version": "memory-governed-read-v1", "reason": reason,
		"memory_id": input.MemoryID, "assistant_id": input.AssistantID,
		"session_id": input.SessionID, "principal_mapping": "tenant_prefixed_attribution",
		"query_hash": queryHashHex, "search_side_effect": false,
		"memory_operation_created": false,
		// Deterministic Memory/Knowledge conflict-downgrade rule version
		// (doc 137 §6.3 #8); results are recorded on the trace surface, the
		// decision snapshot itself stays immutable.
		"context_policy_version": memoryConflictPolicyVersion,
	}
	if policy != nil {
		detail["policy_id"] = policy.ID
		detail["policy_scope"] = policyScope(policy)
		detail["policy_generation"] = policy.PolicyGeneration
		detail["consent_version"] = policy.ConsentVersion
		detail["read_policy"] = policy.ReadPolicy
	}
	rawDetail, err := json.Marshal(detail)
	if err != nil {
		return nil, httperr.Internal("unable to serialize memory search decision")
	}
	filterHash := sha256.Sum256([]byte(strings.Join([]string{
		"memory-governed-read-v1", tenantID, actorID, input.MemoryID,
		input.AssistantID, input.SessionID, reason, queryHashHex,
	}, "\x1f")))
	decisionValue := "memory_denied"
	memoryPolicyID := ""
	if policy != nil && reason == "active_policy_binding" && policy.ReadPolicy != "deny" {
		decisionValue = "memory_allowed"
		memoryPolicyID = policy.ID
	}
	now := time.Now().UTC()
	decision := &model.ContextDecision{
		ID: id.New(), TenantID: tenantID, RequestID: input.RequestID,
		// Governed memory search decisions are always search-kind rows; the
		// chat kind belongs to the pre-model conversation context path only.
		RequestKind: model.ContextDecisionSearch, AssistantID: input.AssistantID,
		SessionID: input.SessionID, UserID: actorID, Decision: decisionValue,
		MemoryPolicyID: memoryPolicyID, DecisionHash: hex.EncodeToString(filterHash[:]),
		Detail: string(rawDetail), CreatedAt: now,
	}
	if err := s.Store.CreateContextDecision(ctx, decision); err != nil {
		return nil, err
	}
	return decision, nil
}
