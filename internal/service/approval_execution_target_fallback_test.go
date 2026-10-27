package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ExecutionAuditFallsBackToApprovalTenantTarget(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC().Add(-time.Second)
	approval := &model.Approval{
		ID: "approval-target-fallback", TenantID: admin.TenantID, RequestNo: "APR-TARGET-FALLBACK",
		ObjectType: model.ApprovalObjectAPIKey, ObjectID: "new:test-key", Action: model.ApprovalActionCreate,
		Title: "approval target fallback", Status: model.ApprovalStatusApproved,
		PolicyID: "policy", PolicyVersion: 1, CurrentStep: 1, RequesterID: admin.ID,
		PayloadJSON: `{"name":"test-key"}`, SnapshotJSON: "{}", IdempotencyKey: "target-fallback-idem",
		ExpiresAt: now.Add(time.Hour), SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	actionHash, err := computeApprovalActionHash(approval)
	if err != nil {
		t.Fatal(err)
	}
	approval.ApprovalActionHash = actionHash
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	acting, err := svc.prepareActingContext(ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := newApprovalOperation(approval, acting, "worker-target-fallback", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := svc.Store.ClaimActingContextForOperation(
		ctx, acting.ID, "worker-target-fallback", now, now.Add(time.Minute), operation,
	); err != nil || !claimed {
		t.Fatalf("claim operation: claimed=%t err=%v", claimed, err)
	}
	if claimed, err := svc.Store.TransitionApproval(
		ctx, approval.TenantID, approval.ID, model.ApprovalStatusApproved, model.ApprovalStatusExecuting, nil,
	); err != nil || !claimed {
		t.Fatalf("transition approval: claimed=%t err=%v", claimed, err)
	}

	worker := &approvalExecuteWorker{svc: svc}
	if err := worker.finishApprovalExecution(
		ctx, "job-target-fallback", approval, true, map[string]any{"key_id": "key-1"}, nil, true,
	); err != nil {
		t.Fatalf("finish execution: %v", err)
	}
	audits, err := svc.Store.ListAuditsAll(ctx, approval.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	var executionAudit *model.AuditLog
	for index := range audits {
		if audits[index].Action == "approval.executed" && audits[index].ApprovalID == approval.ID {
			executionAudit = &audits[index]
			break
		}
	}
	if executionAudit == nil {
		t.Fatal("approval.executed audit is missing")
	}
	if executionAudit.TargetTenantID != admin.TenantID || executionAudit.ActingContextID != acting.ID {
		t.Fatalf("execution audit lacks tenant fallback and acting context: %+v", executionAudit)
	}
}
