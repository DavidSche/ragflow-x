package service

import (
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func approvalOperationFingerprint(approval *model.Approval, context *model.ActingContext) (string, error) {
	if approval == nil || context == nil {
		return "", httperr.BadRequest(40073, "approval operation identity is incomplete")
	}
	fingerprint := approvalActionFingerprint{
		TargetTenantID:      context.TargetTenantID,
		ResourceType:        context.ResourceType,
		ResourceID:          context.ResourceID,
		ResourceVersion:     context.ResourceVersion,
		Action:              context.Action,
		RequestedChangeHash: actingSHA256Hex(approval.PayloadJSON),
	}
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return "", httperr.Internal("failed to encode approval operation fingerprint")
	}
	return actingSHA256Hex(string(raw)), nil
}

func newApprovalOperation(approval *model.Approval, context *model.ActingContext, claimedBy string, now, claimExpiresAt time.Time) (*model.ApprovalOperation, error) {
	fingerprint, err := approvalOperationFingerprint(approval, context)
	if err != nil {
		return nil, err
	}
	return &model.ApprovalOperation{
		ID: id.New(), TenantID: context.TargetTenantID, ApprovalID: approval.ID,
		ActingContextID: context.ID, AttemptNo: context.AttemptNo,
		PrincipalID: context.ActorID, IdempotencyKey: context.IdempotencyKey,
		RequestID: context.RequestID, OperationFingerprint: fingerprint,
		ApprovalActionHash: context.ApprovalActionHash, Status: model.ApprovalOperationRunning,
		ResultJSON: "{}", ClaimedBy: claimedBy, ClaimedAt: &now, ClaimExpiresAt: &claimExpiresAt,
		StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func decodeApprovalOperationResult(raw string) map[string]any {
	result := map[string]any{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	return result
}
