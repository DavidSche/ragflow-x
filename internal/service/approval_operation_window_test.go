package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_StaleOperationWindowMarksUnknownAtBoundary(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	_, approval, acting := newActingContextApproval(t, svc)
	if claimed, err := svc.Store.TransitionApproval(
		ctx, approval.TenantID, approval.ID, model.ApprovalStatusApproved, model.ApprovalStatusExecuting, nil,
	); err != nil || !claimed {
		t.Fatalf("transition approval: claimed=%t err=%v", claimed, err)
	}

	now := time.Now().UTC()
	staleBefore := now.Add(-approvalOperationPendingWindow)
	operation, err := newApprovalOperation(approval, acting, "worker-stale-boundary", staleBefore, staleBefore)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := svc.Store.ClaimActingContextForOperation(
		ctx, acting.ID, "worker-stale-boundary", staleBefore, staleBefore, operation,
	); err != nil || !claimed {
		t.Fatalf("claim stale boundary operation: claimed=%t err=%v", claimed, err)
	}
	acting, err = svc.Store.GetActingContext(ctx, acting.ID)
	if err != nil || acting == nil {
		t.Fatalf("reload claimed acting context: context=%+v err=%v", acting, err)
	}
	approval, err = svc.Store.GetApproval(ctx, approval.TenantID, approval.ID)
	if err != nil || approval == nil {
		t.Fatalf("reload executing approval: approval=%+v err=%v", approval, err)
	}

	worker := &approvalExecuteWorker{svc: svc}
	job := &model.Job{ID: "job-stale-boundary", Attempts: 0, MaxRetry: 3}
	if _, _, err := worker.resolveApprovalExecution(ctx, job, approval, acting); err != nil {
		t.Fatalf("resolve stale boundary operation: %v", err)
	}
	stored, err := svc.Store.GetApprovalOperationByContext(ctx, acting.ID)
	if err != nil || stored == nil {
		t.Fatalf("reload stale boundary operation: operation=%+v err=%v", stored, err)
	}
	if stored.Status != model.ApprovalOperationUnknown {
		t.Fatalf("operation at pending window boundary = %q, want %q", stored.Status, model.ApprovalOperationUnknown)
	}
}
