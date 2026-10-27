package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_TerminalOperationUpdatesAreFenced(t *testing.T) {
	store := newApprovalStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	tenant := &model.Tenant{
		ID: "tenant-operation", Name: "operation", Type: model.TenantTypeWorkspace,
		Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	actingContext := &model.ActingContext{
		ID: "context-operation", ActorID: "actor", ActorTenantID: tenant.ID,
		ActorRole: model.ApprovalApproverUser, ActingMode: "acting", TargetTenantID: tenant.ID,
		ResourceType: model.ApprovalObjectDataset, ResourceID: "dataset-1", ResourceVersion: "1",
		Action: model.ApprovalActionDelete, ApprovalID: "approval-operation", AttemptNo: 1,
		ApprovalActionHash: "hash", IdempotencyKey: "idempotency", RequestID: "request",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Status: model.ActingContextActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateActingContext(ctx, actingContext); err != nil {
		t.Fatalf("create acting context: %v", err)
	}
	operation := &model.ApprovalOperation{
		ID: "operation-terminal", TenantID: tenant.ID, ApprovalID: actingContext.ApprovalID,
		ActingContextID: actingContext.ID, AttemptNo: 1, PrincipalID: actingContext.ActorID,
		IdempotencyKey: actingContext.IdempotencyKey, RequestID: actingContext.RequestID,
		OperationFingerprint: "fingerprint", ApprovalActionHash: actingContext.ApprovalActionHash,
		Status: model.ApprovalOperationRunning, ClaimedBy: "worker", StartedAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, claimed, err := store.ClaimActingContextForOperation(
		ctx, actingContext.ID, operation.ClaimedBy, now, now.Add(15*time.Minute), operation,
	); err != nil || !claimed {
		t.Fatalf("claim operation: claimed=%v err=%v", claimed, err)
	}

	completed, err := store.CompleteApprovalOperation(
		ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed,
		`{"reason":"contract-test"}`, "", now.Add(time.Minute),
	)
	if err != nil || !completed {
		t.Fatalf("complete RUNNING operation: completed=%v err=%v", completed, err)
	}
	for _, transition := range [][2]string{
		{model.ApprovalOperationFailed, model.ApprovalOperationCompleted},
		{model.ApprovalOperationFailed, model.ApprovalOperationUnknown},
		{model.ApprovalOperationRunning, model.ApprovalOperationCompleted},
	} {
		completed, err = store.CompleteApprovalOperation(
			ctx, operation.ID, transition[0], transition[1], "{}", "terminal fence", now.Add(2*time.Minute),
		)
		if err != nil {
			t.Fatalf("terminal transition %s->%s: %v", transition[0], transition[1], err)
		}
		if completed {
			t.Fatalf("terminal transition %s->%s was accepted", transition[0], transition[1])
		}
	}
	if stale, err := store.MarkStaleApprovalOperationUnknown(
		ctx, operation.ID, now.Add(-time.Second), now.Add(2*time.Minute),
	); err != nil || stale {
		t.Fatalf("stale terminal operation recovered: stale=%v err=%v", stale, err)
	}
}
