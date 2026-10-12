package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type MemoryDispatchOperationInput struct {
	ProjectID      string `json:"project_id"`
	PolicyID       string `json:"policy_id" binding:"required"`
	MemoryID       string `json:"memory_id" binding:"required"`
	OperationType  string `json:"operation_type" binding:"required"`
	RequestID      string `json:"request_id" binding:"required"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	UserID         string `json:"user_id" binding:"required"`
	AssistantID    string `json:"assistant_id" binding:"required"`
	SessionID      string `json:"session_id"`
	UserInput      string `json:"user_input"`
	AgentResponse  string `json:"agent_response"`
}

type MemoryOperationDispatchPayload struct {
	OperationID      string `json:"x_operation_id"`
	RequestID        string `json:"request_id"`
	PolicyID         string `json:"x_policy_id"`
	PolicyGeneration int64  `json:"policy_generation"`
	ConsentVersion   int64  `json:"consent_version"`
}

type MemoryOperationPayloadEnvelope struct {
	SchemaVersion int                       `json:"schema_version"`
	TenantID      string                    `json:"tenant_id"`
	OperationID   string                    `json:"operation_id"`
	RequestID     string                    `json:"request_id"`
	PolicyID      string                    `json:"policy_id"`
	OperationType string                    `json:"operation_type"`
	MemoryID      string                    `json:"memory_id"`
	AssistantID   string                    `json:"assistant_id"`
	SessionID     string                    `json:"session_id"`
	UserID        string                    `json:"user_id"`
	Provider      ragflow.AddMessageRequest `json:"provider_request"`
}

func (s *Service) CreateMemoryDispatchOperation(
	ctx context.Context, tenantID, actorID string, input MemoryDispatchOperationInput,
) (*model.MemoryOperation, error) {
	tenantID = strings.TrimSpace(tenantID)
	actorID = strings.TrimSpace(actorID)
	input.PolicyID = strings.TrimSpace(input.PolicyID)
	input.MemoryID = strings.TrimSpace(input.MemoryID)
	input.OperationType = strings.ToLower(strings.TrimSpace(input.OperationType))
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.UserID = strings.TrimSpace(input.UserID)
	input.AssistantID = strings.TrimSpace(input.AssistantID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.UserInput = strings.TrimSpace(input.UserInput)
	input.AgentResponse = strings.TrimSpace(input.AgentResponse)
	if tenantID == "" || actorID == "" || input.PolicyID == "" || input.MemoryID == "" ||
		input.RequestID == "" || input.IdempotencyKey == "" || input.UserID == "" || input.AssistantID == "" {
		return nil, httperr.BadRequest(40142, "policy_id, memory_id, request_id, idempotency_key, user_id and assistant_id are required")
	}
	if input.UserID != actorID {
		return nil, httperr.Forbidden("memory dispatch target principal must be the current user")
	}
	_, authPrincipalID, attributionUserID, err := s.resolveMemoryDispatchPrincipal(ctx, tenantID, input.UserID)
	if err != nil {
		return nil, err
	}
	switch input.OperationType {
	case "write", "forget", "status":
	default:
		return nil, httperr.BadRequest(40142, "operation_type must be write, forget or status")
	}
	if input.OperationType == "write" && (input.UserInput == "" || input.AgentResponse == "") {
		return nil, httperr.BadRequest(40142, "write operation requires user_input and agent_response")
	}
	if input.OperationType != "write" && (input.UserInput != "" || input.AgentResponse != "") {
		return nil, httperr.BadRequest(40142, "only write operation accepts payload content")
	}
	if len(input.UserInput) > 16384 || len(input.AgentResponse) > 16384 {
		return nil, httperr.BadRequest(40142, "memory payload exceeds 16384 characters")
	}
	if len(input.RequestID) > 128 || len(input.IdempotencyKey) > 128 || len(input.SessionID) > 128 {
		return nil, httperr.BadRequest(40142, "request identity exceeds 128 characters")
	}
	policy, err := s.Store.GetMemoryPolicy(ctx, tenantID, input.PolicyID)
	if err != nil {
		return nil, err
	}
	if policy == nil || policy.State != model.MemoryPolicyActive {
		return nil, httperr.New(409, 40954, "memory policy is not active")
	}
	if policy.UserID != "" {
		if policy.UserID != input.UserID {
			return nil, httperr.Forbidden("user memory policy belongs to another principal")
		}
		if input.ProjectID != "" {
			return nil, httperr.Forbidden("user memory policy does not accept a project scope")
		}
	} else {
		if policy.ProjectID == "" || policy.ProjectID != input.ProjectID {
			return nil, httperr.Forbidden("memory dispatch project does not match policy scope")
		}
		member, err := s.Store.IsProjectMember(ctx, input.UserID, policy.ProjectID)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, httperr.Forbidden("memory dispatch target principal is not a project member")
		}
	}
	if policy.AssistantID != input.AssistantID {
		return nil, httperr.BadRequest(40142, "assistant_id does not match memory policy")
	}
	if policy.ProjectID != input.ProjectID {
		return nil, httperr.Forbidden("memory dispatch project does not match policy scope")
	}
	if policy.ConsentVersion <= 0 {
		return nil, httperr.New(409, 40955, "memory dispatch requires target principal consent")
	}

	now := time.Now().UTC()
	operationID := id.New()
	eventID := id.New()
	envelope := MemoryOperationPayloadEnvelope{
		SchemaVersion: 1, TenantID: tenantID, OperationID: operationID,
		RequestID: input.RequestID, PolicyID: policy.ID,
		OperationType: input.OperationType, MemoryID: input.MemoryID,
		AssistantID: input.AssistantID, SessionID: input.SessionID,
		UserID: input.UserID,
		Provider: ragflow.AddMessageRequest{
			MemoryIDs: []string{input.MemoryID}, AgentID: input.AssistantID,
			SessionID: input.SessionID, UserID: attributionUserID,
			UserInput: input.UserInput, AgentResponse: input.AgentResponse,
		},
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil, httperr.Internal("unable to serialize memory payload envelope")
	}
	ciphertext, err := crypto.Encrypt(s.EncryptKey, string(envelopeJSON))
	if err != nil {
		return nil, httperr.Internal("unable to seal memory payload envelope")
	}
	payloadHash, err := memoryPayloadHash(s.HMACKey, tenantID, envelope)
	if err != nil {
		return nil, httperr.Internal("unable to hash memory payload envelope")
	}
	externalMessageHash := memoryExternalContentHash(
		s.HMACKey, envelope.MemoryID,
		envelope.Provider.UserInput, envelope.Provider.AgentResponse,
	)
	payload, err := json.Marshal(MemoryOperationDispatchPayload{
		OperationID: operationID, RequestID: input.RequestID, PolicyID: policy.ID,
		PolicyGeneration: policy.PolicyGeneration, ConsentVersion: policy.ConsentVersion,
	})
	if err != nil {
		return nil, err
	}
	operation := &model.MemoryOperation{
		ID: operationID, TenantID: tenantID, ProjectID: policy.ProjectID,
		PolicyID: policy.ID, MemoryID: input.MemoryID, AssistantID: input.AssistantID,
		SessionID: input.SessionID, UserID: input.UserID, OutboxEventID: eventID,
		RequestID: input.RequestID, OperationType: input.OperationType,
		IdempotencyKey: input.IdempotencyKey, State: model.MemoryOperationAccepted,
		PolicyVersion: policy.PolicyVersion, PolicyGeneration: policy.PolicyGeneration,
		ConsentVersion: policy.ConsentVersion, MemoryType: "raw", RecordVersion: 1,
		PayloadCiphertext: ciphertext, PayloadKeyID: memoryPayloadKeyID(s.HMACKey),
		PayloadHash:              payloadHash,
		ExternalMessageHash:      externalMessageHash,
		RagflowAuthPrincipalID:   authPrincipalID,
		RagflowAttributionUserID: attributionUserID,
		CreatedAt:                now, UpdatedAt: now,
	}
	event := &model.OutboxEvent{
		ID: eventID, TenantID: tenantID, EventType: model.EventTypeMemoryOperationDispatch,
		AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
		Payload: string(payload), OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateMemoryOperationWithOutbox(ctx, operation, event); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForMemoryDispatch(operation, actorID, now))
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

func auditForMemoryDispatch(operation *model.MemoryOperation, actorID string, at time.Time) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: operation.TenantID, ActorTenantID: operation.TenantID,
		TargetTenantID: operation.TenantID, UserID: actorID,
		Action: "memory_operation.dispatch_accepted", Resource: "memory-operation",
		ResourceID: operation.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"operation_id": operation.ID, "request_id": operation.RequestID,
			"operation": operation.OperationType, "policy_id": operation.PolicyID,
			"policy_generation": operation.PolicyGeneration, "state": operation.State,
			"outbox_event_id": operation.OutboxEventID,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "execute:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

func memoryPayloadKeyID(key []byte) string {
	fingerprint := hmacSha256(key, []byte("ragflow-x:memory-payload-key:v1"))
	return "rgx-memory-payload-v1-" + hex.EncodeToString(fingerprint[:8])
}

func memoryPayloadHash(key []byte, tenantID string, envelope MemoryOperationPayloadEnvelope) (string, error) {
	if len(key) == 0 || tenantID == "" {
		return "", errors.New("memory payload hash key is not configured")
	}
	contentHash := func(value string) [32]byte {
		digest := hmacSha256(key, []byte("ragflow-x:memory-content:v1:"+tenantID+":"+value))
		var result [32]byte
		copy(result[:], digest[:32])
		return result
	}
	input := make([]byte, 0, 128)
	appendValue := func(value string) {
		input = append(input, []byte(value)...)
		input = append(input, 0)
	}
	appendValue(strconv.FormatInt(int64(envelope.SchemaVersion), 10))
	appendValue(envelope.TenantID)
	appendValue(envelope.OperationID)
	appendValue(envelope.RequestID)
	appendValue(envelope.PolicyID)
	appendValue(envelope.OperationType)
	appendValue(envelope.MemoryID)
	appendValue(envelope.AssistantID)
	appendValue(envelope.SessionID)
	appendValue(envelope.UserID)
	firstContentHash := contentHash(envelope.Provider.UserInput)
	secondContentHash := contentHash(envelope.Provider.AgentResponse)
	input = append(input, firstContentHash[:]...)
	input = append(input, secondContentHash[:]...)
	digest := hmacSha256(key, append([]byte("ragflow-x:memory-payload-hash:v1:"+tenantID+":"), input...))
	return "hmac-sha256:v1:" + hex.EncodeToString(digest), nil
}

func memoryTombstoneHash(key []byte, kind string, parts ...string) string {
	plaintext := append([]byte("ragflow-x:memory-tombstone-v1:"+kind), 0)
	for index, part := range parts {
		if index > 0 {
			plaintext = append(plaintext, 0)
		}
		plaintext = append(plaintext, part...)
	}
	return hex.EncodeToString(hmacSha256(key, plaintext))
}

func (s *Service) TombstoneMemoryOperation(
	ctx context.Context, tenantID, actorID, operationID string,
) (*model.MemoryOperation, error) {
	tenantID = strings.TrimSpace(tenantID)
	actorID = strings.TrimSpace(actorID)
	operationID = strings.TrimSpace(operationID)
	if tenantID == "" || actorID == "" || operationID == "" {
		return nil, httperr.BadRequest(40142, "operation id is required")
	}
	operation, err := s.GetMemoryOperation(ctx, tenantID, actorID, operationID)
	if err != nil {
		return nil, err
	}
	if operation.UserID != actorID {
		return nil, httperr.Forbidden("only the memory operation owner can tombstone it")
	}

	scopeHash := memoryTombstoneHash(
		s.HMACKey, "scope", operation.TenantID, operation.ProjectID,
		operation.UserID, operation.AssistantID, operation.MemoryID,
	)
	payloadHash := memoryTombstoneHash(
		s.HMACKey, "payload", operation.PolicyID, operation.RequestID,
		operation.IdempotencyKey, operation.OperationType, operation.PayloadHash,
	)
	messageHash := memoryTombstoneHash(s.HMACKey, "message", operation.MemoryID, operation.RagflowMessageID)
	now := time.Now().UTC()

	var tombstoned *model.MemoryOperation
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		item, changed, err := tx.TombstoneMemoryOperation(
			ctx, tenantID, operationID, scopeHash, payloadHash, messageHash, now,
		)
		if err != nil {
			return err
		}
		if item == nil {
			return httperr.NotFound("memory operation not found")
		}
		if changed {
			reason := item.ReasonCode
			if reason == "" {
				reason = "memory_tombstone_active"
			}
			if err := tx.CreateAudit(ctx, &model.AuditLog{
				ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
				TargetTenantID: tenantID, UserID: actorID,
				Action: "memory_operation.tombstone", Resource: "memory-forget",
				ResourceID: operation.ID,
				DetailJSON: encodeAuditDetail(map[string]interface{}{
					"operation_id": operation.ID, "request_id": operation.RequestID,
					"operation": operation.OperationType, "policy_id": operation.PolicyID,
					"state": item.State, "reason_code": reason,
				}),
				At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
				AuthorizationPermission: "execute:memory-forget", AuthorizationPolicyVersion: "explicit-rbac-v1",
			}); err != nil {
				return err
			}
		}
		tombstoned = item
		return nil
	})
	if err != nil {
		return nil, err
	}
	tombstoned.Detail = ""
	return tombstoned, nil
}

func (s *Service) dispatchMemoryOperationEvent(ctx context.Context, event *model.OutboxEvent) error {
	var payload MemoryOperationDispatchPayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return err
	}
	if payload.OperationID == "" || payload.RequestID == "" || payload.PolicyID == "" {
		return errors.New("invalid memory operation dispatch payload")
	}
	if event.AggregateID != payload.OperationID {
		return errors.New("memory operation dispatch aggregate mismatch")
	}
	operation, err := s.Store.AdmitMemoryDispatchOperation(
		ctx, event.TenantID, payload.OperationID, payload.PolicyID, event.ID,
		payload.PolicyGeneration, payload.ConsentVersion, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	if operation == nil || operation.State != model.MemoryOperationAdmitted {
		return nil
	}

	now := time.Now().UTC()
	if err := s.Store.MarkMemoryDispatched(
		ctx, event.TenantID, operation.ID, operation.PolicyID, operation.OutboxEventID, now,
	); err != nil {
		return err
	}
	plaintext, err := crypto.Decrypt(s.EncryptKey, operation.PayloadCiphertext)
	if err != nil {
		return s.finishMemoryDispatch(
			ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
			operation.OutboxEventID,
			model.MemoryOperationUnknown, "memory_dispatch_envelope_unreadable", now,
		)
	}
	var envelope MemoryOperationPayloadEnvelope
	if err := json.Unmarshal([]byte(plaintext), &envelope); err != nil {
		return s.finishMemoryDispatch(
			ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
			operation.OutboxEventID,
			model.MemoryOperationUnknown, "memory_dispatch_envelope_unreadable", now,
		)
	}
	if err := validateMemoryDispatchEnvelope(operation, &envelope); err != nil {
		return s.finishMemoryDispatch(
			ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
			operation.OutboxEventID,
			model.MemoryOperationUnknown, "memory_dispatch_envelope_invalid", now,
		)
	}
	if err := s.RAGFlow.AddMemoryMessage(ctx, envelope.Provider); err != nil {
		return s.finishMemoryDispatch(
			ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
			operation.OutboxEventID,
			model.MemoryOperationUnknown, "memory_dispatch_provider_unconfirmed", now,
		)
	}
	s.recordMemoryDispatchCostMetric(ctx, operation, envelope.Provider)
	messageID, err := s.attachMemoryExternalMessage(ctx, event.TenantID, operation, envelope.Provider)
	if err != nil {
		obs.Get().AddMemoryDispatchExternalBind("unconfirmed")
		return s.finishMemoryDispatch(
			ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
			operation.OutboxEventID,
			model.MemoryOperationUnknown, "memory_dispatch_external_message_unconfirmed", now,
		)
	}
	if messageID == "" {
		obs.Get().AddMemoryDispatchExternalBind("absent")
		return s.finishMemoryDispatch(
			ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
			operation.OutboxEventID,
			model.MemoryOperationUnknown, "memory_dispatch_external_message_absent", now,
		)
	}
	obs.Get().AddMemoryDispatchExternalBind("bound")
	return s.finishMemoryDispatch(
		ctx, event.TenantID, operation.ID, operation.RequestID, operation.PolicyID,
		operation.OutboxEventID,
		model.MemoryOperationExecuted, "", time.Now().UTC(),
	)
}

func (s *Service) attachMemoryExternalMessage(
	ctx context.Context, tenantID string, operation *model.MemoryOperation,
	provider ragflow.AddMessageRequest,
) (string, error) {
	expectedHash := memoryExternalContentHash(
		s.HMACKey, operation.MemoryID, provider.UserInput, provider.AgentResponse,
	)
	matches, _, err := s.findMemoryExternalMessagesByHash(ctx, operation, provider, expectedHash)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	messageID := strconv.FormatInt(latestMemoryMessage(matches).MessageID, 10)
	if err := s.Store.AttachMemoryDispatchExternalMessage(
		ctx, tenantID, operation.ID, "memory-dispatch-worker", messageID, time.Now().UTC(),
	); err != nil {
		return "", err
	}
	return messageID, nil
}

func latestMemoryMessage(messages []ragflow.MemoryMessage) ragflow.MemoryMessage {
	selected := messages[0]
	for _, message := range messages[1:] {
		if message.CreateTime > selected.CreateTime ||
			(message.CreateTime == selected.CreateTime && message.MessageID > selected.MessageID) {
			selected = message
		}
	}
	return selected
}

func (s *Service) findMemoryExternalMessagesByHash(
	ctx context.Context, operation *model.MemoryOperation,
	provider ragflow.AddMessageRequest, expectedHash string,
) ([]ragflow.MemoryMessage, bool, error) {
	matches := make([]ragflow.MemoryMessage, 0, 1)
	for page := 1; page <= memoryCompensationMaxPages; page++ {
		messages, err := s.RAGFlow.ListMemoryMessagesFiltered(ctx, operation.MemoryID, ragflow.MemoryMessageListFilter{
			AgentID: provider.AgentID, SessionID: provider.SessionID,
			Page: page, PageSize: memoryCompensationPageSize,
		})
		if err != nil {
			return nil, false, err
		}
		for _, message := range messages {
			if message.MemoryID != operation.MemoryID || message.UserID != provider.UserID {
				continue
			}
			if !memoryMessageActive(message) {
				continue
			}
			hash, err := s.memoryMessageExternalHash(ctx, operation.MemoryID, message)
			if err != nil {
				return nil, false, err
			}
			if hash == expectedHash {
				matches = append(matches, message)
			}
		}
		if len(messages) < memoryCompensationPageSize {
			return matches, true, nil
		}
	}
	return nil, false, errors.New("memory external message scan incomplete")
}

func validateMemoryDispatchEnvelope(operation *model.MemoryOperation, envelope *MemoryOperationPayloadEnvelope) error {
	if envelope.SchemaVersion != 1 || envelope.OperationID != operation.ID ||
		envelope.TenantID != operation.TenantID || envelope.RequestID != operation.RequestID ||
		envelope.PolicyID != operation.PolicyID || envelope.OperationType != operation.OperationType ||
		envelope.MemoryID != operation.MemoryID || envelope.AssistantID != operation.AssistantID ||
		envelope.SessionID != operation.SessionID || envelope.UserID != operation.UserID {
		return errors.New("memory dispatch envelope mismatch")
	}
	if envelope.Provider.AgentID != operation.AssistantID ||
		envelope.Provider.SessionID != operation.SessionID ||
		envelope.Provider.UserID != operation.RagflowAttributionUserID ||
		len(envelope.Provider.MemoryIDs) != 1 || envelope.Provider.MemoryIDs[0] != operation.MemoryID {
		return errors.New("memory dispatch provider envelope mismatch")
	}
	return nil
}

func (s *Service) finishMemoryDispatch(
	ctx context.Context, tenantID, operationID, requestID, policyID, outboxEventID,
	state, reasonCode string, now time.Time,
) error {
	if state == model.MemoryOperationUnknown && reasonCode != "" {
		notify.Emit(context.Background(), notify.Event{
			ID:       "memory-compensation-required:" + operationID,
			TenantID: tenantID, Resource: "memory-operation", ResourceID: operationID,
			Type: "memory.compensation.required", Severity: "warning",
			Title:  "Memory dispatch requires compensation",
			Detail: "The external write result is uncertain; no content is included",
			Fields: map[string]string{
				"operation_id": operationID, "request_id": requestID,
				"policy_id": policyID, "state": state, "reason": reasonCode,
			},
		})
	}
	return s.Store.FinishMemoryDispatchOperation(
		ctx, tenantID, operationID, policyID, outboxEventID, state, reasonCode, now,
	)
}
