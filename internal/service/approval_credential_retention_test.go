package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_RetainedCredentialIsRefreshedOnRetryFailure(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC().Add(-time.Second)
	retainedUntil := now.Add(time.Hour)
	approval := &model.Approval{
		ID: "approval-credential-refresh", TenantID: admin.TenantID, RequestNo: "APR-CREDENTIAL-REFRESH",
		ObjectType: model.ApprovalObjectModelProvider, ObjectID: "new:provider", Action: model.ApprovalActionCreate,
		Title: "provider create retry", Status: model.ApprovalStatusExecuting,
		PolicyID: "policy", PolicyVersion: 1, CurrentStep: 1, RequesterID: admin.ID,
		TargetTenantID: admin.TenantID, PayloadJSON: `{"name":"provider","provider_type":"openai"}`,
		SnapshotJSON: "{}", IdempotencyKey: "credential-refresh-idem",
		ExpiresAt: now.Add(time.Hour), SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	credential := &model.ApprovalCredential{
		ID: "credential-refresh-1", ApprovalID: approval.ID, TenantID: approval.TenantID,
		Name: "provider_api_key", Ciphertext: "cipher", ConsumedAt: &now,
		RetainUntilAt: &retainedUntil, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, nil, credential, nil); err != nil {
		t.Fatal(err)
	}

	execErr := errors.New("provider registration failed")
	worker := &approvalExecuteWorker{svc: svc}
	if err := worker.finishApprovalExecution(
		ctx, "job-credential-refresh", approval, false, nil, execErr, true,
	); err == nil {
		t.Fatal("expected execution failure to propagate")
	}
	retained, err := svc.Store.GetRetainedApprovalCredential(ctx, approval.TenantID, approval.ID)
	if err != nil || retained == nil {
		t.Fatalf("retained credential missing: credential=%+v err=%v", retained, err)
	}
	if retained.RetainUntilAt == nil || retained.RetainUntilAt.Before(time.Now().UTC().Add(23*time.Hour)) {
		t.Fatalf("retry failure must refresh retention, got %+v", retained.RetainUntilAt)
	}
}
