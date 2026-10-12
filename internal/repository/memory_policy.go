package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type MemoryPolicyFilter struct {
	Scope       string
	ProjectID   string
	UserID      string
	AssistantID string
	MemoryID    string
	State       string
}

func MemoryDispatchIdentity(tenantID, userID string) (string, string) {
	return "ragflow-tenant:" + tenantID, "rgx:" + tenantID + ":" + userID
}

type MemoryPolicyRepo interface {
	CreateMemoryPolicy(ctx context.Context, policy *model.MemoryPolicy) error
	CreateMemoryOperation(ctx context.Context, operation *model.MemoryOperation) error
	CreateMemoryOperationWithOutbox(ctx context.Context, operation *model.MemoryOperation, event *model.OutboxEvent) error
	GetMemoryPolicy(ctx context.Context, tenantID, policyID string) (*model.MemoryPolicy, error)
	UpdateMemoryPolicy(ctx context.Context, policy *model.MemoryPolicy) error
	DeleteMemoryPolicy(ctx context.Context, tenantID, policyID string) error
	ListMemoryPolicies(ctx context.Context, tenantID string, filter MemoryPolicyFilter, page, pageSize int) ([]model.MemoryPolicy, int64, error)
	ListMemoryOperations(ctx context.Context, tenantID string, filter MemoryOperationFilter, page, pageSize int) ([]model.MemoryOperation, int64, error)
	GetMemoryOperation(ctx context.Context, tenantID, operationID string) (*model.MemoryOperation, error)
	AdmitMemoryDispatchOperation(
		ctx context.Context, tenantID, operationID, policyID, outboxEventID string,
		policyGeneration, consentVersion int64, now time.Time,
	) (*model.MemoryOperation, error)
	MarkMemoryDispatched(
		ctx context.Context, tenantID, operationID, policyID, outboxEventID string, now time.Time,
	) error
	FinishMemoryDispatchOperation(
		ctx context.Context, tenantID, operationID, policyID, outboxEventID string,
		state, reasonCode string, now time.Time,
	) error
	ClaimDueMemoryCompensationOperations(
		ctx context.Context, visibleBefore, now, claimExpiresAt time.Time, owner string, limit int,
	) ([]model.MemoryOperation, error)
	AttachMemoryDispatchExternalMessage(
		ctx context.Context, tenantID, operationID, claimOwner, messageID string, now time.Time,
	) error
	CompleteMemoryCompensationOperation(
		ctx context.Context, tenantID, operationID, claimOwner, messageID, reasonCode string, now time.Time,
	) (bool, error)
	TombstoneMemoryOperation(
		ctx context.Context, tenantID, operationID, scopeHash, payloadHash, messageHash string, now time.Time,
	) (*model.MemoryOperation, bool, error)
}

type MemoryOperationFilter struct {
	ProjectID   string
	ProjectIDs  []string
	UserID      string
	AssistantID string
	MemoryID    string
	Operation   string
	State       string
	Tombstone   *bool
}

func (s *store) CreateMemoryPolicy(ctx context.Context, policy *model.MemoryPolicy) error {
	return s.WithContext(ctx).Create(policy).Error
}

func (s *store) CreateMemoryOperation(ctx context.Context, operation *model.MemoryOperation) error {
	return s.WithContext(ctx).Create(operation).Error
}

func (s *store) CreateMemoryOperationWithOutbox(
	ctx context.Context, operation *model.MemoryOperation, event *model.OutboxEvent,
) error {
	if operation == nil || event == nil {
		return errors.New("memory operation and outbox event are required")
	}
	if operation.TenantID != event.TenantID || operation.OutboxEventID != event.ID ||
		event.EventType != model.EventTypeMemoryOperationDispatch ||
		event.AggregateType != model.AggregateTypeMemoryOperation ||
		event.AggregateID != operation.ID {
		return errors.New("memory operation and outbox event references do not match")
	}
	if operation.State != model.MemoryOperationAccepted && operation.State != model.MemoryOperationAdmitted {
		return errors.New("memory dispatch operation must be accepted or admitted")
	}
	if operation.OperationType == "write" {
		if operation.PayloadCiphertext == "" || operation.PayloadKeyID == "" ||
			!strings.HasPrefix(operation.PayloadHash, "hmac-sha256:v1:") ||
			operation.ExternalMessageHash == "" ||
			operation.RagflowAuthPrincipalID == "" || operation.RagflowAttributionUserID == "" {
			return errors.New("memory write operation requires sealed payload and principal mapping")
		}
	} else if operation.PayloadCiphertext != "" || operation.PayloadKeyID != "" ||
		operation.RagflowAuthPrincipalID != "" || operation.RagflowAttributionUserID != "" {
		return errors.New("memory non-write operation must not carry payload or principal mapping")
	}
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(operation).Error; err != nil {
			return err
		}
		return tx.Create(event).Error
	})
}

func (s *store) GetMemoryPolicy(ctx context.Context, tenantID, policyID string) (*model.MemoryPolicy, error) {
	var policy model.MemoryPolicy
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, policyID).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *store) UpdateMemoryPolicy(ctx context.Context, policy *model.MemoryPolicy) error {
	return s.WithContext(ctx).Save(policy).Error
}

func (s *store) DeleteMemoryPolicy(ctx context.Context, tenantID, policyID string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, policyID).Delete(&model.MemoryPolicy{}).Error
}

func (s *store) ListMemoryPolicies(
	ctx context.Context, tenantID string, filter MemoryPolicyFilter, page, pageSize int,
) ([]model.MemoryPolicy, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.Scope != "" {
		query += " AND (CASE WHEN user_id = '' THEN 'project' ELSE 'user' END) = ?"
		args = append(args, filter.Scope)
	}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if filter.UserID != "" {
		query += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.AssistantID != "" {
		query += " AND assistant_id = ?"
		args = append(args, filter.AssistantID)
	}
	if filter.MemoryID != "" {
		query += " AND ragflow_memory_id = ?"
		args = append(args, filter.MemoryID)
	}
	if filter.State != "" {
		query += " AND state = ?"
		args = append(args, filter.State)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.MemoryPolicy{}, query,
		"created_at DESC, id DESC", page, pageSize, args...,
	)
}

func (s *store) ListMemoryOperations(
	ctx context.Context, tenantID string, filter MemoryOperationFilter, page, pageSize int,
) ([]model.MemoryOperation, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if len(filter.ProjectIDs) > 0 {
		query += " AND project_id IN ?"
		args = append(args, filter.ProjectIDs)
	}
	if filter.UserID != "" {
		query += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.AssistantID != "" {
		query += " AND assistant_id = ?"
		args = append(args, filter.AssistantID)
	}
	if filter.MemoryID != "" {
		query += " AND memory_id = ?"
		args = append(args, filter.MemoryID)
	}
	if filter.Operation != "" {
		query += " AND operation_type = ?"
		args = append(args, filter.Operation)
	}
	if filter.State != "" {
		query += " AND state = ?"
		args = append(args, filter.State)
	}
	if filter.Tombstone != nil {
		query += " AND tombstone_active = ?"
		args = append(args, *filter.Tombstone)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.MemoryOperation{}, query,
		"created_at DESC, id DESC", page, pageSize, args...,
	)
}

func (s *store) GetMemoryOperation(ctx context.Context, tenantID, operationID string) (*model.MemoryOperation, error) {
	var operation model.MemoryOperation
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

func (s *store) AdmitMemoryDispatchOperation(
	ctx context.Context, tenantID, operationID, policyID, outboxEventID string,
	policyGeneration, consentVersion int64, now time.Time,
) (*model.MemoryOperation, error) {
	var result *model.MemoryOperation
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var operation model.MemoryOperation
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.PolicyID != policyID || operation.OutboxEventID != outboxEventID {
			return errors.New("memory operation dispatch aggregate mismatch")
		}

		switch operation.State {
		case model.MemoryOperationAdmitted:
			result = &operation
			return nil
		case model.MemoryOperationDispatched:
			updated, err := recoverDispatchedMemoryOperation(ctx, tx, &operation, now)
			if err != nil {
				return err
			}
			result = updated
			return nil
		case model.MemoryOperationUnknown, model.MemoryOperationExecuted,
			model.MemoryOperationFailed, model.MemoryOperationReconciled,
			model.MemoryOperationBlocked, model.MemoryOperationSkipped:
			if err := closeMemoryDispatchOutbox(tx, tenantID, outboxEventID, operation.State, now); err != nil {
				return err
			}
			result = &operation
			return nil
		}

		if operation.State != model.MemoryOperationAccepted {
			return errors.New("unsupported memory operation dispatch state " + operation.State)
		}

		var policy model.MemoryPolicy
		policyErr := tx.Where("tenant_id = ? AND id = ?", tenantID, policyID).First(&policy).Error
		blockedReason := ""
		if errors.Is(policyErr, gorm.ErrRecordNotFound) {
			blockedReason = "memory_dispatch_policy_not_found"
		} else if policyErr != nil {
			return policyErr
		}
		if blockedReason == "" {
			blockedReason = memoryDispatchAdmissionDenial(operation, policy, policyGeneration, consentVersion)
		}
		if blockedReason == "" {
			var principal model.User
			err := tx.Where("id = ? AND tenant_id = ?", operation.UserID, tenantID).First(&principal).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				blockedReason = "memory_dispatch_principal_not_active"
			} else if err != nil {
				return err
			} else if principal.Status != model.UserStatusActive {
				blockedReason = "memory_dispatch_principal_not_active"
			} else {
				expectedAuthPrincipalID, expectedAttributionUserID := MemoryDispatchIdentity(tenantID, principal.ID)
				if operation.RagflowAuthPrincipalID != expectedAuthPrincipalID ||
					operation.RagflowAttributionUserID != expectedAttributionUserID {
					blockedReason = "memory_dispatch_identity_drift"
				}
			}
		}
		if blockedReason == "" && policy.UserID == "" {
			var membership int64
			if err := tx.Model(&model.ProjectMember{}).
				Where("user_id = ? AND project_id = ?", operation.UserID, policy.ProjectID).
				Count(&membership).Error; err != nil {
				return err
			}
			if membership == 0 {
				blockedReason = "memory_dispatch_principal_not_project_member"
			}
		}

		if blockedReason != "" {
			state := model.MemoryOperationBlocked
			if operation.TombstoneActive {
				state = model.MemoryOperationSkipped
			}
			if err := terminateNonDispatchedMemoryOperation(
				ctx, tx, &operation, state, blockedReason, now,
			); err != nil {
				return err
			}
			result = &operation
			return nil
		}

		operationID := operation.ID
		admission := tx.Model(&model.MemoryOperation{}).
			Where(
				"tenant_id = ? AND id = ? AND outbox_event_id = ? AND state = ? AND tombstone_active = ? AND policy_generation = ? AND consent_version = ?",
				tenantID, operationID, outboxEventID, model.MemoryOperationAccepted, false, policyGeneration, consentVersion,
			).
			Updates(map[string]interface{}{
				"state":                       model.MemoryOperationAdmitted,
				"admission_policy_generation": policyGeneration,
				"admission_consent_version":   consentVersion,
				"dispatch_admitted_at":        now,
				"claim_owner":                 "memory-dispatch-worker",
				"claim_expires_at":            nil,
				"updated_at":                  now,
				"record_version":              gorm.Expr("record_version + 1"),
			})
		if admission.Error != nil {
			return admission.Error
		}
		if admission.RowsAffected != 1 {
			return errors.New("memory operation admission race detected")
		}
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error; err != nil {
			return err
		}
		if err := createMemoryDispatchAudit(
			ctx, tx, &operation, "memory_operation.dispatch_admitted", "memory_dispatch_admitted", now,
		); err != nil {
			return err
		}

		result = &operation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func memoryDispatchAdmissionDenial(
	operation model.MemoryOperation, policy model.MemoryPolicy,
	policyGeneration, consentVersion int64,
) string {
	if operation.OperationType != "write" {
		return "memory_dispatch_only_write_supported"
	}
	if operation.TombstoneActive {
		return "memory_dispatch_tombstone_active"
	}
	if policy.State != model.MemoryPolicyActive {
		return "memory_dispatch_policy_not_active"
	}
	if policy.PolicyGeneration != policyGeneration {
		return "memory_dispatch_policy_generation_stale"
	}
	if policy.ConsentVersion != consentVersion || policy.ConsentVersion <= 0 {
		return "memory_dispatch_consent_not_current"
	}
	if policy.UserID != operation.UserID || policy.AssistantID != operation.AssistantID ||
		policy.ProjectID != operation.ProjectID {
		return "memory_dispatch_scope_mismatch"
	}
	if policy.WritePolicy == "deny" {
		return "memory_dispatch_write_policy_denied"
	}
	if policy.RagflowMemoryID != operation.MemoryID {
		return "memory_dispatch_memory_binding_mismatch"
	}
	return ""
}

func recoverDispatchedMemoryOperation(
	ctx context.Context, tx *gorm.DB, operation *model.MemoryOperation, now time.Time,
) (*model.MemoryOperation, error) {
	result := tx.Model(&model.MemoryOperation{}).
		Where(
			"tenant_id = ? AND id = ? AND outbox_event_id = ? AND state = ?",
			operation.TenantID, operation.ID, operation.OutboxEventID, model.MemoryOperationDispatched,
		).
		Updates(map[string]interface{}{
			"state":              model.MemoryOperationUnknown,
			"reason_code":        "memory_dispatch_provider_unconfirmed",
			"claim_owner":        "",
			"claim_expires_at":   nil,
			"payload_ciphertext": "",
			"payload_key_id":     "",
			"updated_at":         now,
			"record_version":     gorm.Expr("record_version + 1"),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("dispatched memory operation recovery race detected")
	}
	if err := tx.Where("tenant_id = ? AND id = ?", operation.TenantID, operation.ID).First(operation).Error; err != nil {
		return nil, err
	}
	if err := createMemoryDispatchAudit(
		ctx, tx, operation, "memory_operation.dispatch_unknown", "memory_dispatch_provider_unconfirmed", now,
	); err != nil {
		return nil, err
	}
	if err := closeMemoryDispatchOutbox(
		tx, operation.TenantID, operation.OutboxEventID, model.MemoryOperationUnknown, now,
	); err != nil {
		return nil, err
	}
	return operation, nil
}

func terminateNonDispatchedMemoryOperation(
	ctx context.Context, tx *gorm.DB, operation *model.MemoryOperation,
	state, reasonCode string, now time.Time,
) error {
	result := tx.Model(&model.MemoryOperation{}).
		Where(
			"tenant_id = ? AND id = ? AND outbox_event_id = ? AND state = ?",
			operation.TenantID, operation.ID, operation.OutboxEventID, model.MemoryOperationAccepted,
		).
		Updates(map[string]interface{}{
			"state":              state,
			"reason_code":        reasonCode,
			"claim_owner":        "",
			"claim_expires_at":   nil,
			"payload_ciphertext": "",
			"payload_key_id":     "",
			"updated_at":         now,
			"record_version":     gorm.Expr("record_version + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("memory operation terminal race detected")
	}
	operation.State = state
	operation.ReasonCode = reasonCode
	operation.PayloadCiphertext = ""
	operation.PayloadKeyID = ""
	action := "memory_operation.dispatch_blocked"
	if state == model.MemoryOperationSkipped {
		action = "memory_operation.dispatch_skipped"
	}
	if err := createMemoryDispatchAudit(ctx, tx, operation, action, reasonCode, now); err != nil {
		return err
	}
	return closeMemoryDispatchOutbox(
		tx, operation.TenantID, operation.OutboxEventID, state, now,
	)
}

func createMemoryDispatchAudit(
	ctx context.Context, tx *gorm.DB, operation *model.MemoryOperation, action, reason string, now time.Time,
) error {
	detail, err := json.Marshal(map[string]interface{}{
		"operation_id": operation.ID, "request_id": operation.RequestID,
		"operation": operation.OperationType, "policy_id": operation.PolicyID,
		"policy_generation": operation.PolicyGeneration, "consent_version": operation.ConsentVersion,
		"state": operation.State, "reason_code": reason,
		"outbox_event_id": operation.OutboxEventID,
	})
	if err != nil {
		return err
	}
	audit := &model.AuditLog{
		ID: id.New(), TenantID: operation.TenantID,
		ActorTenantID: operation.TenantID, TargetTenantID: operation.TenantID,
		UserID: operation.UserID, Action: action, Resource: "memory-operation",
		ResourceID: operation.ID, DetailJSON: string(detail), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission: "execute:memory-policy", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
	return (&store{DB: tx}).CreateAudit(ctx, audit)
}

func closeMemoryDispatchOutbox(tx *gorm.DB, tenantID, eventID, state string, now time.Time) error {
	resultJSON, err := json.Marshal(map[string]interface{}{"status": state})
	if err != nil {
		return err
	}
	publish := tx.Model(&model.OutboxEvent{}).
		Where(
			"tenant_id = ? AND id = ? AND event_type = ? AND aggregate_type = ? AND published_at IS NULL",
			tenantID, eventID, model.EventTypeMemoryOperationDispatch, model.AggregateTypeMemoryOperation,
		).
		Updates(map[string]interface{}{
			"published_at":     now,
			"result":           string(resultJSON),
			"claimed_at":       nil,
			"claim_expires_at": nil,
			"updated_at":       now,
		})
	if publish.Error != nil {
		return publish.Error
	}
	if publish.RowsAffected != 1 {
		return errors.New("memory dispatch outbox race detected for event " + eventID)
	}
	return nil
}

func (s *store) MarkMemoryDispatched(
	ctx context.Context, tenantID, operationID, policyID, outboxEventID string, now time.Time,
) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var operation model.MemoryOperation
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.PolicyID != policyID || operation.OutboxEventID != outboxEventID {
			return errors.New("memory operation dispatch aggregate mismatch")
		}
		if operation.State != model.MemoryOperationAdmitted {
			return errors.New("memory operation is not admitted for dispatch")
		}
		result := tx.Model(&model.MemoryOperation{}).
			Where(
				"tenant_id = ? AND id = ? AND outbox_event_id = ? AND state = ?",
				tenantID, operationID, outboxEventID, model.MemoryOperationAdmitted,
			).
			Updates(map[string]interface{}{
				"state":            model.MemoryOperationDispatched,
				"claim_owner":      "memory-dispatch-worker",
				"claim_expires_at": nil,
				"updated_at":       now,
				"record_version":   gorm.Expr("record_version + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("memory operation dispatch race detected")
		}
		operation.State = model.MemoryOperationDispatched
		return createMemoryDispatchAudit(
			ctx, tx, &operation, "memory_operation.dispatched", "memory_dispatch_external_call_pending", now,
		)
	})
}

func (s *store) FinishMemoryDispatchOperation(
	ctx context.Context, tenantID, operationID, policyID, outboxEventID string,
	state, reasonCode string, now time.Time,
) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var operation model.MemoryOperation
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.PolicyID != policyID || operation.OutboxEventID != outboxEventID {
			return errors.New("memory operation dispatch aggregate mismatch")
		}
		if operation.State != model.MemoryOperationDispatched {
			return closeMemoryDispatchOutbox(tx, tenantID, outboxEventID, operation.State, now)
		}
		if state != model.MemoryOperationExecuted && state != model.MemoryOperationUnknown {
			return errors.New("invalid memory dispatch completion state")
		}
		if reasonCode == "" {
			reasonCode = "memory_dispatch_external_accepted"
		}
		if state == model.MemoryOperationExecuted && operation.TombstoneActive {
			reasonCode = "memory_tombstone_compensation_required"
		}
		result := tx.Model(&model.MemoryOperation{}).
			Where(
				"tenant_id = ? AND id = ? AND outbox_event_id = ? AND state = ?",
				tenantID, operationID, outboxEventID, model.MemoryOperationDispatched,
			).
			Updates(map[string]interface{}{
				"state":              state,
				"reason_code":        reasonCode,
				"claim_owner":        "",
				"claim_expires_at":   nil,
				"payload_ciphertext": "",
				"payload_key_id":     "",
				"updated_at":         now,
				"record_version":     gorm.Expr("record_version + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("memory operation completion race detected")
		}
		operation.State = state
		operation.ReasonCode = reasonCode
		action := "memory_operation.dispatch_unknown"
		if state == model.MemoryOperationExecuted {
			action = "memory_operation.dispatch_executed"
		}
		if err := createMemoryDispatchAudit(ctx, tx, &operation, action, reasonCode, now); err != nil {
			return err
		}
		return closeMemoryDispatchOutbox(tx, tenantID, outboxEventID, state, now)
	})
}

func (s *store) ClaimDueMemoryCompensationOperations(
	ctx context.Context, visibleBefore, now, claimExpiresAt time.Time, owner string, limit int,
) ([]model.MemoryOperation, error) {
	if limit <= 0 {
		limit = 1
	}
	if owner == "" {
		return nil, errors.New("memory compensation claim owner is required")
	}
	states := []string{
		model.MemoryOperationDispatched,
		model.MemoryOperationExecuted, model.MemoryOperationUnknown,
	}
	operations := make([]model.MemoryOperation, 0, limit)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&model.MemoryOperation{}).
			Where(
				"tombstone_active = ? AND operation_type = ? AND state IN (?) AND tombstone_created_at IS NOT NULL AND tombstone_created_at <= ? AND external_message_hash <> '' AND (claim_expires_at IS NULL OR claim_expires_at <= ?)",
				true, "write", states, visibleBefore, now,
			).
			Order("created_at ASC, id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		claimedIDs := make([]string, 0, len(ids))
		for _, operationID := range ids {
			result := tx.Model(&model.MemoryOperation{}).
				Where(
					"id = ? AND tombstone_active = ? AND operation_type = ? AND state IN (?) AND tombstone_created_at <= ? AND external_message_hash <> '' AND (claim_expires_at IS NULL OR claim_expires_at <= ?)",
					operationID, true, "write", states, visibleBefore, now,
				).
				Updates(map[string]interface{}{
					"claim_owner":      owner,
					"claim_expires_at": claimExpiresAt,
					"updated_at":       now,
					"record_version":   gorm.Expr("record_version + 1"),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				claimedIDs = append(claimedIDs, operationID)
			}
		}
		if len(claimedIDs) == 0 {
			return nil
		}
		return tx.Where("id IN ?", claimedIDs).Order("created_at ASC, id ASC").Find(&operations).Error
	})
	if err != nil {
		return nil, err
	}
	return operations, nil
}

func (s *store) AttachMemoryDispatchExternalMessage(
	ctx context.Context, tenantID, operationID, claimOwner, messageID string, now time.Time,
) error {
	if messageID == "" {
		return nil
	}
	result := s.WithContext(ctx).Model(&model.MemoryOperation{}).
		Where(
			"tenant_id = ? AND id = ? AND state = ? AND claim_owner = ?",
			tenantID, operationID, model.MemoryOperationDispatched, claimOwner,
		).
		Updates(map[string]interface{}{
			"ragflow_message_id": messageID,
			"updated_at":         now,
			"record_version":     gorm.Expr("record_version + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 1 {
		return errors.New("memory dispatch external message attach matched multiple operations")
	}
	return nil
}

func (s *store) CompleteMemoryCompensationOperation(
	ctx context.Context, tenantID, operationID, claimOwner, messageID, reasonCode string, now time.Time,
) (bool, error) {
	var completed bool
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var operation model.MemoryOperation
		err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			completed = false
			return nil
		}
		if err != nil {
			return err
		}
		states := []string{
			model.MemoryOperationDispatched,
			model.MemoryOperationExecuted, model.MemoryOperationUnknown,
		}
		result := tx.Model(&model.MemoryOperation{}).
			Where(
				"tenant_id = ? AND id = ? AND claim_owner = ? AND tombstone_active = ? AND state IN (?)",
				tenantID, operationID, claimOwner, true, states,
			).
			Updates(map[string]interface{}{
				"state":              model.MemoryOperationReconciled,
				"reason_code":        reasonCode,
				"ragflow_message_id": messageID,
				"claim_owner":        "",
				"claim_expires_at":   nil,
				"payload_ciphertext": "",
				"payload_key_id":     "",
				"updated_at":         now,
				"record_version":     gorm.Expr("record_version + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			completed = false
			return nil
		}
		operation.State = model.MemoryOperationReconciled
		operation.ReasonCode = reasonCode
		if err := createMemoryCompensationAudit(ctx, tx, &operation, messageID, reasonCode, now); err != nil {
			return err
		}
		completed = true
		return nil
	})
	return completed, err
}

func createMemoryCompensationAudit(
	ctx context.Context, tx *gorm.DB, operation *model.MemoryOperation,
	messageID, reasonCode string, now time.Time,
) error {
	detail, err := json.Marshal(map[string]interface{}{
		"operation_id": operation.ID, "request_id": operation.RequestID,
		"operation": operation.OperationType, "policy_id": operation.PolicyID,
		"state": operation.State, "reason_code": reasonCode,
		"ragflow_message_id": messageID,
	})
	if err != nil {
		return err
	}
	audit := &model.AuditLog{
		ID: id.New(), TenantID: operation.TenantID,
		ActorTenantID: operation.TenantID, TargetTenantID: operation.TenantID,
		UserID: "system:memory-compensation", Action: "memory_operation.tombstone_compensated",
		Resource: "memory-operation", ResourceID: operation.ID,
		DetailJSON: string(detail), At: now, Result: "SUCCESS",
		AuthorizationDecision: "SYSTEM", AuthorizationPermission: "system:memory-compensation",
		AuthorizationPolicyVersion: "system-worker-v1",
	}
	return (&store{DB: tx}).CreateAudit(ctx, audit)
}

func (s *store) TombstoneMemoryOperation(
	ctx context.Context, tenantID, operationID, scopeHash, payloadHash, messageHash string, now time.Time,
) (*model.MemoryOperation, bool, error) {
	var operation model.MemoryOperation
	changed := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.TombstoneActive {
			repair := tx.Model(&model.MemoryOperation{}).
				Where(
					"tenant_id = ? AND id = ? AND (tombstone_scope_hash = '' OR tombstone_payload_hash = '' OR tombstone_message_hash = '' OR tombstone_created_at IS NULL)",
					tenantID, operationID,
				).
				Updates(map[string]interface{}{
					"tombstone_scope_hash":   scopeHash,
					"tombstone_payload_hash": payloadHash,
					"tombstone_message_hash": messageHash,
					"tombstone_created_at":   now,
					"updated_at":             now,
				})
			if repair.Error != nil {
				return repair.Error
			}
			changed = repair.RowsAffected == 1
			return tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error
		}

		reason := "memory_tombstone_compensation_required"
		switch operation.State {
		case model.MemoryOperationAccepted, model.MemoryOperationSkipped, model.MemoryOperationBlocked:
			reason = "memory_tombstone_cancelled"
		}

		if operation.State == model.MemoryOperationAccepted {
			cancel := tx.Model(&model.MemoryOperation{}).
				Where(
					"tenant_id = ? AND id = ? AND outbox_event_id = ? AND state = ? AND tombstone_active = ?",
					tenantID, operationID, operation.OutboxEventID, operation.State, false,
				).
				Updates(map[string]interface{}{
					"state":                  model.MemoryOperationSkipped,
					"tombstone_active":       true,
					"tombstone_scope_hash":   scopeHash,
					"tombstone_payload_hash": payloadHash,
					"tombstone_message_hash": messageHash,
					"tombstone_created_at":   now,
					"reason_code":            reason,
					"claim_owner":            "",
					"claim_expires_at":       nil,
					"payload_ciphertext":     "",
					"payload_key_id":         "",
					"updated_at":             now,
					"record_version":         gorm.Expr("record_version + 1"),
				})
			if cancel.Error != nil {
				return cancel.Error
			}
			if cancel.RowsAffected == 1 {
				operation.State = model.MemoryOperationSkipped
				changed = true
			} else if err := tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error; err != nil {
				return err
			}
		}

		if !changed {
			preserve := tx.Model(&model.MemoryOperation{}).
				Where("tenant_id = ? AND id = ? AND tombstone_active = ?", tenantID, operationID, false).
				Updates(map[string]interface{}{
					"tombstone_active":       true,
					"tombstone_scope_hash":   scopeHash,
					"tombstone_payload_hash": payloadHash,
					"tombstone_message_hash": messageHash,
					"tombstone_created_at":   now,
					"reason_code":            reason,
					"updated_at":             now,
					"record_version":         gorm.Expr("record_version + 1"),
				})
			if preserve.Error != nil {
				return preserve.Error
			}
			if preserve.RowsAffected != 1 {
				return errors.New("memory operation tombstone lost an update")
			}
			changed = true
		}

		if operation.State == model.MemoryOperationAccepted ||
			(changed && operation.State == model.MemoryOperationSkipped) {
			closeEvent := tx.Model(&model.OutboxEvent{}).
				Where(
					"tenant_id = ? AND id = ? AND aggregate_id = ? AND aggregate_type = ? AND event_type = ? AND published_at IS NULL",
					tenantID, operation.OutboxEventID, operation.ID, model.AggregateTypeMemoryOperation, model.EventTypeMemoryOperationDispatch,
				).
				Updates(map[string]interface{}{
					"published_at":     now,
					"result":           `{"status":"skipped","reason":"memory_tombstone_cancelled"}`,
					"claimed_at":       nil,
					"claim_expires_at": nil,
					"updated_at":       now,
				})
			if closeEvent.Error != nil {
				return closeEvent.Error
			}
		}

		return tx.Where("tenant_id = ? AND id = ?", tenantID, operationID).First(&operation).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &operation, changed, nil
}
