package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

const actingContextTTL = 5 * time.Minute
const actingContextClaimTTL = time.Minute

type approvalActionFingerprint struct {
	TargetTenantID      string `json:"target_tenant_id"`
	ResourceType        string `json:"resource_type"`
	ResourceID          string `json:"resource_id"`
	ResourceVersion     string `json:"resource_version"`
	Action              string `json:"action"`
	RequestedChangeHash string `json:"requested_change_hash"`
}

func actingSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func approvalTargetTenant(approval *model.Approval) (string, error) {
	if approval == nil {
		return "", httperr.BadRequest(40072, "approval is required")
	}
	if approval.TargetTenantID != "" {
		return approval.TargetTenantID, nil
	}
	if approval.ObjectType == model.ApprovalObjectEnterpriseBinding {
		payload := map[string]any{}
		if approval.PayloadJSON != "" && json.Unmarshal([]byte(approval.PayloadJSON), &payload) == nil {
			if tenantID, _ := payload["tenant_id"].(string); tenantID != "" {
				return tenantID, nil
			}
		}
	}
	return approval.TenantID, nil
}

func approvalResourceVersion(approval *model.Approval) (string, error) {
	if approval == nil {
		return "", httperr.BadRequest(40072, "approval is required")
	}
	if approval.ResourceVersion != "" {
		return approval.ResourceVersion, nil
	}
	if approval.ObjectType == model.ApprovalObjectEnterpriseConnection ||
		approval.ObjectType == model.ApprovalObjectEnterpriseBinding {
		snapshot := map[string]any{}
		if approval.SnapshotJSON != "" && json.Unmarshal([]byte(approval.SnapshotJSON), &snapshot) == nil {
			if approval.ObjectType == model.ApprovalObjectEnterpriseBinding {
				if bindingID, _ := snapshot["binding_id"].(string); bindingID != "" {
					return fmt.Sprintf("binding:%s:v%s", bindingID, fmt.Sprint(snapshot["binding_version"])), nil
				}
			}
			if connectionID, _ := snapshot["connection_id"].(string); connectionID != "" {
				return fmt.Sprintf("connection:%s:v%s", connectionID, fmt.Sprint(snapshot["connection_version"])), nil
			}
		}
	}
	if approval.SnapshotJSON == "" || approval.SnapshotJSON == "{}" {
		return "new", nil
	}
	return "snapshot:" + actingSHA256Hex(approval.SnapshotJSON), nil
}

func computeApprovalActionHash(approval *model.Approval) (string, error) {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return "", err
	}
	resourceVersion, err := approvalResourceVersion(approval)
	if err != nil {
		return "", err
	}
	fingerprint := approvalActionFingerprint{
		TargetTenantID:      targetTenantID,
		ResourceType:        approval.ObjectType,
		ResourceID:          approval.ObjectID,
		ResourceVersion:     resourceVersion,
		Action:              approval.Action,
		RequestedChangeHash: actingSHA256Hex(approval.PayloadJSON),
	}
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return "", httperr.Internal("failed to encode approval action fingerprint")
	}
	return actingSHA256Hex(string(raw)), nil
}

func (s *Service) prepareActingContext(ctx context.Context, approval *model.Approval) (*model.ActingContext, error) {
	if approval == nil {
		return nil, httperr.BadRequest(40072, "approval is required")
	}
	existing, err := s.Store.GetActingContextByApproval(ctx, approval.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		switch existing.Status {
		case model.ActingContextActive:
			if existing.ExpiresAt.After(time.Now().UTC()) {
				return existing, nil
			}
			if _, err := s.Store.CompleteActingContext(ctx, existing.ID, model.ActingContextActive, model.ActingContextExpired); err != nil {
				return nil, err
			}
		case model.ActingContextClaimed:
			return nil, httperr.New(409, 40972, "acting context is already claimed")
		case model.ActingContextConsumed, model.ActingContextFailed, model.ActingContextRevoked:
			// A new immutable attempt is created only by an explicit approved
			// execution/retry. It inherits the original approval fingerprint.
		case model.ActingContextExpired:
		default:
			return nil, httperr.BadRequest(40072, "acting context status is invalid")
		}
	}
	actor, err := s.Store.GetUser(ctx, approval.RequesterID)
	if err != nil {
		return nil, err
	}
	if actor == nil || actor.Status != model.UserStatusActive {
		return nil, httperr.Unauthorized("approval requester is disabled or missing")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	resourceVersion, err := approvalResourceVersion(approval)
	if err != nil {
		return nil, err
	}
	actionHash, err := computeApprovalActionHash(approval)
	if err != nil {
		return nil, err
	}
	if approval.ApprovalActionHash != "" && approval.ApprovalActionHash != actionHash {
		return nil, httperr.New(409, 40972, "approval action fingerprint has changed")
	}
	attemptNo := int64(1)
	if existing != nil {
		attemptNo = existing.AttemptNo + 1
	}
	now := time.Now().UTC()
	context := &model.ActingContext{
		ID: id.New(), ActorID: actor.ID, ActorTenantID: actor.TenantID, ActorRole: actor.Role,
		ActingMode: "TARGET_WORKSPACE", TargetTenantID: targetTenantID,
		ResourceType: approval.ObjectType, ResourceID: approval.ObjectID,
		ResourceVersion: resourceVersion, Action: approval.Action, ApprovalID: approval.ID,
		AttemptNo: attemptNo, ApprovalActionHash: actionHash, IdempotencyKey: approval.IdempotencyKey,
		RequestID: approval.RequestNo, RiskLevel: "high", Reason: approval.Reason,
		IssuedAt: now, ExpiresAt: now.Add(actingContextTTL), Status: model.ActingContextActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateActingContext(ctx, context); err != nil {
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, err
		}
		winner, err := s.Store.GetActingContextByApproval(ctx, approval.ID)
		if err != nil {
			return nil, err
		}
		if winner == nil || winner.ApprovalActionHash != actionHash || winner.IdempotencyKey != approval.IdempotencyKey {
			return nil, httperr.New(409, 40972, "concurrent approval acting context identity mismatch")
		}
		switch winner.Status {
		case model.ActingContextActive:
			if winner.ExpiresAt.After(time.Now().UTC()) {
				return winner, nil
			}
		case model.ActingContextClaimed:
			return nil, httperr.New(409, 40972, "acting context is already claimed")
		}
		return nil, httperr.New(409, 40972, "acting context state changed concurrently")
	}
	return context, nil
}

type approvalExecutionPayload struct {
	ApprovalID         string `json:"approval_id"`
	TargetTenantID     string `json:"target_tenant_id"`
	ActingContextID    string `json:"acting_context_id"`
	ApprovalActionHash string `json:"approval_action_hash"`
	IdempotencyKey     string `json:"idempotency_key"`
	RequestID          string `json:"request_id"`
}

func (s *Service) validateApprovalExecutionPayload(ctx context.Context, approval *model.Approval, payload approvalExecutionPayload) (*model.ActingContext, error) {
	if payload.ApprovalID == "" || payload.ApprovalID != approval.ID {
		return nil, httperr.BadRequest(40073, "acting context approval identity is invalid")
	}
	if payload.ActingContextID == "" || payload.ApprovalActionHash == "" ||
		payload.IdempotencyKey == "" || payload.RequestID == "" {
		return nil, httperr.BadRequest(40073, "acting context payload is incomplete")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if payload.TargetTenantID != targetTenantID {
		return nil, httperr.New(409, 40972, "acting context target tenant mismatch")
	}
	actionHash, err := computeApprovalActionHash(approval)
	if err != nil {
		return nil, err
	}
	if approval.ApprovalActionHash != "" && approval.ApprovalActionHash != actionHash {
		return nil, httperr.New(409, 40972, "approval action fingerprint has changed")
	}
	if payload.ApprovalActionHash != actionHash {
		return nil, httperr.New(409, 40972, "acting context action fingerprint mismatch")
	}
	if payload.IdempotencyKey != approval.IdempotencyKey || payload.RequestID != approval.RequestNo {
		return nil, httperr.New(409, 40972, "acting context idempotency identity mismatch")
	}
	actor, err := s.Store.GetUser(ctx, approval.RequesterID)
	if err != nil {
		return nil, err
	}
	if actor == nil || actor.Status != model.UserStatusActive {
		return nil, httperr.Unauthorized("approval requester is disabled or missing")
	}
	context, err := s.Store.GetActingContext(ctx, payload.ActingContextID)
	if err != nil {
		return nil, err
	}
	if context == nil {
		return nil, httperr.NotFound("acting context not found")
	}
	expected := []struct {
		name, actual, expected string
	}{
		{"approval", context.ApprovalID, approval.ID},
		{"resource type", context.ResourceType, approval.ObjectType},
		{"resource id", context.ResourceID, approval.ObjectID},
		{"action", context.Action, approval.Action},
		{"actor", context.ActorID, actor.ID},
		{"actor tenant", context.ActorTenantID, actor.TenantID},
		{"actor role", context.ActorRole, actor.Role},
		{"target tenant", context.TargetTenantID, targetTenantID},
		{"approval hash", context.ApprovalActionHash, actionHash},
		{"idempotency key", context.IdempotencyKey, approval.IdempotencyKey},
		{"request id", context.RequestID, approval.RequestNo},
	}
	for _, item := range expected {
		if item.actual != item.expected {
			return nil, httperr.New(409, 40972, "acting context "+item.name+" mismatch")
		}
	}
	if err := s.AuthorizeABAC(ctx, actor.ID, "manage", approvalAuthorizationResource(approval.ObjectType), targetTenantID, "", ""); err != nil {
		return nil, err
	}
	if context.Status != model.ActingContextActive && context.Status != model.ActingContextClaimed &&
		context.Status != model.ActingContextConsumed && context.Status != model.ActingContextFailed &&
		context.Status != model.ActingContextExpired {
		return nil, httperr.New(409, 40972, "acting context is not executable")
	}
	return context, nil
}

func approvalAuthorizationResource(objectType string) string {
	if objectType == model.ApprovalObjectDocumentChunk {
		return "document"
	}
	return objectType
}

func (s *Service) validateApprovalResource(ctx context.Context, approval *model.Approval) error {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return err
	}
	var payload map[string]any
	if approval.PayloadJSON != "" {
		if err := json.Unmarshal([]byte(approval.PayloadJSON), &payload); err != nil {
			return httperr.BadRequest(40073, "invalid approval payload")
		}
	}
	req := ApprovalSubmitRequest{
		TenantID: targetTenantID, ObjectType: approval.ObjectType, Action: approval.Action,
		ObjectID: approval.ObjectID, Payload: payload,
	}
	_, _, currentSnapshot, err := s.resolveApprovalTarget(ctx, targetTenantID, approval.RequesterID, req)
	if err != nil {
		return err
	}
	stored := map[string]any{}
	if approval.SnapshotJSON != "" {
		if err := json.Unmarshal([]byte(approval.SnapshotJSON), &stored); err != nil {
			return httperr.BadRequest(40073, "invalid approval snapshot")
		}
	}
	for key, approvedValue := range stored {
		currentValue, exists := currentSnapshot[key]
		if !exists || fmt.Sprint(approvedValue) != fmt.Sprint(currentValue) {
			return httperr.New(409, 40972, "approval resource snapshot has changed")
		}
	}
	return nil
}
