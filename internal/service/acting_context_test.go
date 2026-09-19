package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newActingContextApproval(t *testing.T, svc *Service) (*model.User, *model.Approval, *model.ActingContext) {
	t.Helper()
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	now := time.Now().UTC().Add(-time.Second)
	approval := &model.Approval{
		ID: "acting-approval-1", TenantID: admin.TenantID, RequestNo: "APR-ACTING-1",
		ObjectType: model.ApprovalObjectAPIKey, ObjectID: "new:test-key", Action: model.ApprovalActionCreate,
		Title: "create acting context test", Status: model.ApprovalStatusApproved,
		PolicyID: "policy", PolicyVersion: 1, CurrentStep: 1, RequesterID: admin.ID,
		PayloadJSON: `{"name":"test-key"}`, SnapshotJSON: "{}", IdempotencyKey: "acting-idem-1",
		ExpiresAt: now.Add(time.Hour), SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	acting, err := svc.prepareActingContext(ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	return admin, approval, acting
}

func TestActingContextIsAtomicallyClaimedAndValidated(t *testing.T) {
	svc := newAuthzSvc(t)
	admin, approval, acting := newActingContextApproval(t, svc)
	if acting.ApprovalID != approval.ID || acting.ActorID != admin.ID || acting.Status != model.ActingContextActive {
		t.Fatalf("unexpected acting context: %+v", acting)
	}
	if acting.ApprovalActionHash == "" || acting.IdempotencyKey != approval.IdempotencyKey {
		t.Fatalf("fingerprint identity missing: %+v", acting)
	}
	now := time.Now().UTC()
	claimed, ok, err := svc.Store.ClaimActingContext(context.Background(), acting.ID, "worker-1", now, now.Add(time.Minute))
	if err != nil || !ok || claimed.Status != model.ActingContextClaimed || claimed.ClaimedBy != "worker-1" {
		t.Fatalf("first claim failed: ok=%t context=%+v err=%v", ok, claimed, err)
	}
	if _, ok, err := svc.Store.ClaimActingContext(context.Background(), acting.ID, "worker-2", now, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("second claim must lose: ok=%t err=%v", ok, err)
	}
	payload := approvalExecutionPayload{
		ApprovalID: approval.ID, TargetTenantID: acting.TargetTenantID, ActingContextID: acting.ID,
		ApprovalActionHash: acting.ApprovalActionHash, IdempotencyKey: acting.IdempotencyKey,
		RequestID: acting.RequestID,
	}
	if _, err := svc.validateApprovalExecutionPayload(context.Background(), approval, payload); err != nil {
		t.Fatal(err)
	}
	payload.ApprovalActionHash = "sha256:invalid"
	if _, err := svc.validateApprovalExecutionPayload(context.Background(), approval, payload); err == nil {
		t.Fatal("fingerprint mismatch must fail")
	}
}

func TestActingContextDoesNotBlindlyRecoverExpiredClaim(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, acting := newActingContextApproval(t, svc)
	now := time.Now().UTC()
	if _, ok, err := svc.Store.ClaimActingContext(context.Background(), acting.ID, "worker-1", now, now.Add(-time.Second)); err != nil || !ok {
		t.Fatalf("claim context: ok=%t err=%v", ok, err)
	}
	payload := approvalExecutionPayload{
		ApprovalID: approval.ID, TargetTenantID: acting.TargetTenantID, ActingContextID: acting.ID,
		ApprovalActionHash: acting.ApprovalActionHash, IdempotencyKey: acting.IdempotencyKey,
		RequestID: acting.RequestID,
	}
	if _, err := svc.validateApprovalExecutionPayload(context.Background(), approval, payload); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := svc.Store.ClaimActingContext(context.Background(), acting.ID, "worker-2", now, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("expired claim must not be silently reclaimed: ok=%t err=%v", ok, err)
	}
}

func TestActingContextRejectsStaleActorSnapshot(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, acting := newActingContextApproval(t, svc)
	payload := approvalExecutionPayload{
		ApprovalID: approval.ID, TargetTenantID: acting.TargetTenantID, ActingContextID: acting.ID,
		ApprovalActionHash: acting.ApprovalActionHash, IdempotencyKey: acting.IdempotencyKey,
		RequestID: acting.RequestID,
	}
	admin, err := svc.Store.GetUser(context.Background(), approval.RequesterID)
	if err != nil || admin == nil {
		t.Fatalf("load actor: %v", err)
	}
	admin.Role = "viewer"
	if err := svc.Store.UpdateUser(context.Background(), admin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.validateApprovalExecutionPayload(context.Background(), approval, payload); err == nil {
		t.Fatal("stale actor role must fail validation")
	}
}

func TestActingContextRejectsApprovalFingerprintDrift(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, acting := newActingContextApproval(t, svc)
	approval.Action = model.ApprovalActionDelete
	payload := approvalExecutionPayload{
		ApprovalID: approval.ID, TargetTenantID: acting.TargetTenantID, ActingContextID: acting.ID,
		ApprovalActionHash: acting.ApprovalActionHash, IdempotencyKey: acting.IdempotencyKey,
		RequestID: acting.RequestID,
	}
	if _, err := svc.validateApprovalExecutionPayload(context.Background(), approval, payload); err == nil {
		t.Fatal("approval action fingerprint drift must fail")
	}
}

func TestActingContextCreatesImmutableRetryAttempt(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, first := newActingContextApproval(t, svc)
	if _, err := svc.Store.CompleteActingContext(context.Background(), first.ID, model.ActingContextActive, model.ActingContextFailed); err != nil {
		t.Fatal(err)
	}
	second, err := svc.prepareActingContext(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.AttemptNo != first.AttemptNo+1 ||
		second.ApprovalActionHash != first.ApprovalActionHash {
		t.Fatalf("unexpected retry attempt: first=%+v second=%+v", first, second)
	}
}

func TestApprovalOperationClaimAndCompletionAreAtomic(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, acting := newActingContextApproval(t, svc)
	now := time.Now().UTC()
	operation, err := newApprovalOperation(approval, acting, "worker-operation-1", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimedOperation, claimed, err := svc.Store.ClaimActingContextForOperation(
		context.Background(), acting.ID, "worker-operation-1", now, now.Add(time.Minute), operation,
	)
	if err != nil || !claimed {
		t.Fatalf("claim operation: claimed=%t err=%v", claimed, err)
	}
	if claimedOperation.ID != operation.ID || claimedOperation.Status != model.ApprovalOperationRunning ||
		claimedOperation.ApprovalID != approval.ID || claimedOperation.ActingContextID != acting.ID ||
		claimedOperation.OperationFingerprint == "" {
		t.Fatalf("unexpected operation: %+v", claimedOperation)
	}
	if _, claimed, err := svc.Store.ClaimActingContextForOperation(
		context.Background(), acting.ID, "worker-operation-2", now, now.Add(time.Minute), operation,
	); err != nil || claimed {
		t.Fatalf("second operation claim must lose: claimed=%t err=%v", claimed, err)
	}
	if ok, err := svc.Store.CompleteApprovalOperation(
		context.Background(), operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationCompleted,
		`{"recovered":true}`, "", now,
	); err != nil || !ok {
		t.Fatalf("complete operation: ok=%t err=%v", ok, err)
	}
	if ok, err := svc.Store.CompleteApprovalOperation(
		context.Background(), operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed,
		"{}", "must not overwrite terminal result", now,
	); err != nil || ok {
		t.Fatalf("terminal operation must be immutable: ok=%t err=%v", ok, err)
	}
}

func TestApprovalOperationCompletedResultIsReplayedWithoutExecution(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, acting := newActingContextApproval(t, svc)
	now := time.Now().UTC()
	_, err := newApprovalOperation(approval, acting, "worker-recovery-1", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	worker := &approvalExecuteWorker{svc: svc}
	job := &model.Job{ID: "job-recovery-1", Attempts: 0, MaxRetry: 3}
	if _, proceed, err := worker.resolveApprovalExecution(context.Background(), job, approval, acting); err != nil || !proceed {
		t.Fatalf("initial resolve: proceed=%t err=%v", proceed, err)
	}
	var operation *model.ApprovalOperation
	operation, err = svc.Store.GetApprovalOperationByContext(context.Background(), acting.ID)
	if err != nil || operation == nil {
		t.Fatalf("load claimed operation: %v", err)
	}
	if claimed, err := svc.Store.TransitionApproval(
		context.Background(), approval.TenantID, approval.ID,
		model.ApprovalStatusApproved, model.ApprovalStatusExecuting, nil,
	); err != nil || !claimed {
		t.Fatalf("transition approval: claimed=%t err=%v", claimed, err)
	}
	if ok, err := svc.Store.CompleteApprovalOperation(
		context.Background(), operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationCompleted,
		`{"recovered":true}`, "", now,
	); err != nil || !ok {
		t.Fatalf("complete operation: ok=%t err=%v", ok, err)
	}
	approval, err = svc.Store.GetApproval(context.Background(), approval.TenantID, approval.ID)
	if err != nil || approval == nil {
		t.Fatalf("reload approval: %v", err)
	}
	acting, err = svc.Store.GetActingContext(context.Background(), acting.ID)
	if err != nil || acting == nil {
		t.Fatalf("reload acting context: %v", err)
	}
	operation, err = svc.Store.GetApprovalOperationByContext(context.Background(), acting.ID)
	if err != nil || operation == nil {
		t.Fatalf("reload operation: %v", err)
	}
	operation, proceed, err := worker.resolveApprovalExecution(context.Background(), job, approval, acting)
	if err != nil || proceed {
		t.Fatalf("completed operation must be reconciled without execution: proceed=%t err=%v", proceed, err)
	}
	if operation != nil || job.Result != `{"recovered":true}` {
		t.Fatalf("recovery result missing: operation=%+v job=%+v", operation, job)
	}
	completed, err := svc.Store.GetApproval(context.Background(), approval.TenantID, approval.ID)
	if err != nil || completed == nil || completed.Status != model.ApprovalStatusCompleted {
		t.Fatalf("approval must be completed from operation result: %+v err=%v", completed, err)
	}
	consumed, err := svc.Store.GetActingContext(context.Background(), acting.ID)
	if err != nil || consumed == nil || consumed.Status != model.ActingContextConsumed {
		t.Fatalf("acting context must be consumed: %+v err=%v", consumed, err)
	}
}

func TestApprovalOperationExpiredRunningClaimWaitsForOutcome(t *testing.T) {
	svc := newAuthzSvc(t)
	_, approval, acting := newActingContextApproval(t, svc)
	now := time.Now().UTC()
	operation, err := newApprovalOperation(approval, acting, "worker-running-1", now, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	operation, claimed, err := svc.Store.ClaimActingContextForOperation(
		context.Background(), acting.ID, "worker-running-1", now, now.Add(-time.Minute), operation,
	)
	if err != nil || !claimed {
		t.Fatalf("claim expired lease: claimed=%t err=%v", claimed, err)
	}
	acting, err = svc.Store.GetActingContext(context.Background(), acting.ID)
	if err != nil || acting == nil {
		t.Fatalf("reload claimed context: %v", err)
	}
	operation.UpdatedAt = now.Add(-6 * time.Minute)
	worker := &approvalExecuteWorker{svc: svc}
	job := &model.Job{ID: "job-running-1", Attempts: 0, MaxRetry: 3}
	if _, proceed, err := worker.resolveApprovalExecution(context.Background(), job, approval, acting); !errors.Is(err, errApprovalOperationPending) || proceed {
		t.Fatalf("recent running operation must wait: proceed=%t err=%v", proceed, err)
	}
	if _, err := svc.Store.CompleteActingContext(
		context.Background(), acting.ID, model.ActingContextClaimed, model.ActingContextFailed,
	); err != nil {
		t.Fatal(err)
	}
	retryContext, err := svc.prepareActingContext(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	staleOperation, err := newApprovalOperation(approval, retryContext, "worker-running-2", now.Add(-7*time.Minute), now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, claimed, err = svc.Store.ClaimActingContextForOperation(
		context.Background(), retryContext.ID, "worker-running-2", now, now.Add(-time.Minute), staleOperation,
	)
	if err != nil || !claimed {
		t.Fatalf("claim stale operation: claimed=%t err=%v", claimed, err)
	}
	retryContext, err = svc.Store.GetActingContext(context.Background(), retryContext.ID)
	if err != nil || retryContext == nil {
		t.Fatalf("reload stale context: %v", err)
	}
	if claimed, err := svc.Store.TransitionApproval(
		context.Background(), approval.TenantID, approval.ID,
		model.ApprovalStatusApproved, model.ApprovalStatusExecuting, nil,
	); err != nil || !claimed {
		t.Fatalf("transition approval: claimed=%t err=%v", claimed, err)
	}
	approval.Status = model.ApprovalStatusExecuting
	if _, proceed, err := worker.resolveApprovalExecution(context.Background(), job, approval, retryContext); err != nil || proceed {
		t.Fatalf("stale running operation must enter reconciliation without execution: proceed=%t err=%v", proceed, err)
	}
	storedOperation, err := svc.Store.GetApprovalOperationByContext(context.Background(), retryContext.ID)
	if err != nil || storedOperation == nil || storedOperation.Status != model.ApprovalOperationUnknown {
		t.Fatalf("stale operation must be unknown: %+v err=%v", storedOperation, err)
	}
}
