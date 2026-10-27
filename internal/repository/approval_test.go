package repository

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newApprovalStore(t *testing.T) *store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "approval.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := &store{DB: gdb}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func createTestApproval(t *testing.T, s *store, tenantID, idempotencyKey string) *model.Approval {
	t.Helper()
	now := time.Now().UTC()
	approval := &model.Approval{
		ID: "apr_" + tenantID + "_" + idempotencyKey, TenantID: tenantID, RequestNo: "APR-TEST-" + idempotencyKey,
		ObjectType: model.ApprovalObjectDataset, ObjectID: "ds-1", Action: model.ApprovalActionDelete,
		Title: "delete", PayloadJSON: "{}", SnapshotJSON: "{}", Status: model.ApprovalStatusPendingApproval,
		PolicyID: "policy", CurrentStep: 1, RequesterID: "user", IdempotencyKey: idempotencyKey,
		ExpiresAt: now.Add(time.Hour), SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	steps := []model.ApprovalStep{{
		ID: approval.ID + "-s1", TenantID: tenantID, ApprovalID: approval.ID, StepNo: 1,
		Name: "first", ApproverType: model.ApprovalApproverUser, ApproverValue: "approver",
		Status: model.ApprovalStepCurrent, DueAt: &approval.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}}
	err := s.CreateApprovalWithAudit(context.Background(), approval, steps, nil, &model.AuditLog{
		TenantID: tenantID, UserID: "user", Action: "approval.submitted", Resource: "approval",
		ResourceID: approval.ID, DetailJSON: "{}", At: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestApprovalTenantIsolation(t *testing.T) {
	s := newApprovalStore(t)
	ctx := context.Background()
	createTestApproval(t, s, "t1", "one")
	createTestApproval(t, s, "t2", "two")

	approval, err := s.GetApproval(ctx, "t1", "apr_t2_two")
	if err != nil {
		t.Fatal(err)
	}
	if approval != nil {
		t.Fatal("tenant query must not return another tenant's approval")
	}
}

func TestApprovalApproverFilterMatchesCurrentStep(t *testing.T) {
	s := newApprovalStore(t)
	ctx := context.Background()
	createTestApproval(t, s, "t1", "approver")

	items, total, err := s.ListApprovals(ctx, "t1", ApprovalFilter{
		Approver: &ApprovalApproverFilter{UserID: "approver"},
	}, ApprovalListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected current approver to match one approval, got total=%d items=%d", total, len(items))
	}

	items, total, err = s.ListApprovals(ctx, "t1", ApprovalFilter{
		Approver: &ApprovalApproverFilter{UserID: "other"},
	}, ApprovalListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("expected other user to match no approvals, got total=%d items=%d", total, len(items))
	}
}

// ScenarioID: SC-IDEM-001
func TestP0_IDEM_001_ApprovalIdempotencyIsTenantScoped(t *testing.T) {
	s := newApprovalStore(t)
	ctx := context.Background()
	createTestApproval(t, s, "t1", "same")
	createTestApproval(t, s, "t2", "same")

	first, err := s.GetApprovalByIdempotencyKey(ctx, "t1", "same")
	if err != nil || first == nil {
		t.Fatalf("tenant one key lookup: approval=%+v err=%v", first, err)
	}
	second, err := s.GetApprovalByIdempotencyKey(ctx, "t2", "same")
	if err != nil || second == nil {
		t.Fatalf("tenant two key lookup: approval=%+v err=%v", second, err)
	}
}

func TestApprovalTransitionIsConcurrentSafe(t *testing.T) {
	s := newApprovalStore(t)
	approval := createTestApproval(t, s, "t1", "concurrent")
	ctx := context.Background()
	var mu sync.Mutex
	successes := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.TransitionApproval(ctx, "t1", approval.ID, model.ApprovalStatusPendingApproval, model.ApprovalStatusApproved, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("expected one transition success, got %d", successes)
	}
	updated, err := s.GetApproval(ctx, "t1", approval.ID)
	if err != nil || updated == nil {
		t.Fatalf("get approval: %+v err=%v", updated, err)
	}
	if updated.Status != model.ApprovalStatusApproved {
		t.Fatalf("status = %s", updated.Status)
	}
}

func TestCreateApprovalWithAuditRebindsPendingSteps(t *testing.T) {
	s := newApprovalStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, idempotencyKey := range []string{"first", "second"} {
		approvalID := "apr-" + idempotencyKey
		approval := &model.Approval{
			ID: approvalID, TenantID: "t1", RequestNo: "APR-" + idempotencyKey,
			ObjectType: model.ApprovalObjectDataset, ObjectID: "ds-1", Action: model.ApprovalActionDelete,
			Title: "delete", Status: model.ApprovalStatusPendingApproval, PolicyID: "policy",
			CurrentStep: 1, RequesterID: "user", IdempotencyKey: idempotencyKey,
			ExpiresAt: now.Add(time.Hour), SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
		}
		steps := []model.ApprovalStep{{
			ID: approvalID + "-step", TenantID: "t1", ApprovalID: "pending", StepNo: 1,
			Name: "first", ApproverType: model.ApprovalApproverUser, ApproverValue: "approver",
			Status: model.ApprovalStepCurrent, DueAt: &approval.ExpiresAt, CreatedAt: now, UpdatedAt: now,
		}}
		if err := s.CreateApprovalWithAudit(ctx, approval, steps, nil, nil); err != nil {
			t.Fatalf("create approval %d: %v", i+1, err)
		}
		step, err := s.CurrentApprovalStep(ctx, "t1", approval.ID)
		if err != nil || step == nil {
			t.Fatalf("current step for approval %d: step=%+v err=%v", i+1, step, err)
		}
		if step.ApprovalID != approval.ID {
			t.Fatalf("step approval id = %s, want %s", step.ApprovalID, approval.ID)
		}
	}
}

func TestApprovalCredentialLifecycle(t *testing.T) {
	s := newApprovalStore(t)
	ctx := context.Background()
	approval := createTestApproval(t, s, "t1", "credential")
	retainUntil := time.Now().UTC().Add(time.Hour)
	if err := s.WithContext(ctx).Create(&model.ApprovalCredential{
		ID: "credential-1", ApprovalID: approval.ID, TenantID: "t1", Name: "api_key",
		Ciphertext: "cipher", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if ok, err := s.TransitionApproval(ctx, "t1", approval.ID, model.ApprovalStatusPendingApproval, model.ApprovalStatusExecuting, nil); err != nil || !ok {
		t.Fatalf("claim executing: ok=%t err=%v", ok, err)
	}
	err := s.SettleApprovalExecution(ctx, "t1", approval.ID, false, "{}", "boom", "credential-1", retainUntil, &model.AuditLog{
		TenantID: "t1", UserID: "system", Action: "approval.execution_failed", Resource: "approval",
		ResourceID: approval.ID, DetailJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential, err := s.GetRetainedApprovalCredential(ctx, "t1", approval.ID); err != nil || credential == nil {
		t.Fatalf("failed credential must remain consumable for retry: %+v err=%v", credential, err)
	}
	if err := s.WithContext(ctx).Model(&model.ApprovalCredential{}).
		Where("id = ?", "credential-1").
		Update("retain_until_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteExpiredApprovalCredentials(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("expired failed credential should be deleted")
	}
}
