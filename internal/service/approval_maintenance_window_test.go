package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func createMaintenanceWindowApproval(t *testing.T, svc *Service, id string, decidedAt time.Time) *model.Approval {
	t.Helper()
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC().Add(-time.Second)
	approval := &model.Approval{
		ID: id, TenantID: admin.TenantID, RequestNo: "APR-" + id,
		ObjectType: model.ApprovalObjectAPIKey, ObjectID: "new:test-key", Action: model.ApprovalActionCreate,
		Title: "approval maintenance window", Status: model.ApprovalStatusApproved,
		PolicyID: "policy", PolicyVersion: 1, CurrentStep: 1, RequesterID: admin.ID,
		TargetTenantID: admin.TenantID, PayloadJSON: `{"name":"test-key"}`, SnapshotJSON: "{}",
		IdempotencyKey: id + "-idem", ExpiresAt: now.Add(time.Hour),
		DecidedAt: decidedAt, SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	actionHash, err := computeApprovalActionHash(approval)
	if err != nil {
		t.Fatal(err)
	}
	approval.ApprovalActionHash = actionHash
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return approval
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_MaintenanceOnlyRecoversApprovedBeforeCutoff(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.Runner = NewRunner(svc.Store, DefaultWorkerConfig())
	ctx := context.Background()
	now := time.Now().UTC()
	older := createMaintenanceWindowApproval(t, svc, "approval-maintenance-old", now.Add(-2*time.Minute))
	newer := createMaintenanceWindowApproval(t, svc, "approval-maintenance-newer", now)

	if err := svc.RunApprovalMaintenance(ctx); err != nil {
		t.Fatal(err)
	}

	olderContexts, err := svc.Store.CountActingContextsByApproval(ctx, older.ID)
	if err != nil || olderContexts != 1 {
		t.Fatalf("older acting contexts = %d err=%v, want 1", olderContexts, err)
	}
	newerContexts, err := svc.Store.CountActingContextsByApproval(ctx, newer.ID)
	if err != nil || newerContexts != 0 {
		t.Fatalf("newer acting contexts = %d err=%v, want 0", newerContexts, err)
	}
	jobs, total, err := svc.Store.ListJobs(ctx, older.TenantID, model.JobKindApprovalExecute, "", 1, 20)
	if err != nil || total != 1 || len(jobs) != 1 || jobs[0].Key != model.ApprovalExecutionJobKeyPrefix+older.ID {
		t.Fatalf("maintenance jobs: total=%d jobs=%+v err=%v", total, jobs, err)
	}
}
