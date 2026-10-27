package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ExpirationAuditCarriesApprovalChainEvidence(t *testing.T) {
	store := newApprovalStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	approval := createTestApproval(t, store, "tenant-expired", "expired-evidence")
	actionHash := "sha256:expiration-evidence"
	if err := store.WithContext(ctx).Model(&model.Approval{}).Where("id = ?", approval.ID).Updates(map[string]interface{}{
		"target_tenant_id":     "tenant-expired",
		"approval_action_hash": actionHash,
		"expires_at":           now.Add(-time.Minute),
		"created_at":           now,
		"submitted_at":         now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	expired, err := store.ExpireApprovals(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}

	audits, err := store.ListAuditsAll(ctx, "tenant-expired")
	if err != nil {
		t.Fatal(err)
	}
	var expirationAudit *model.AuditLog
	for index := range audits {
		if audits[index].Action == "approval.expired" && audits[index].ResourceID == approval.ID {
			expirationAudit = &audits[index]
			break
		}
	}
	if expirationAudit == nil {
		t.Fatal("approval.expired audit is missing")
	}
	if expirationAudit.UserID != "system" ||
		expirationAudit.ActorTenantID != "tenant-expired" ||
		expirationAudit.TargetTenantID != "tenant-expired" ||
		expirationAudit.ApprovalID != approval.ID ||
		expirationAudit.ApprovalActionHash != actionHash ||
		expirationAudit.Result != "EXPIRED" ||
		expirationAudit.AuthorizationDecision != "ALLOW" ||
		expirationAudit.AuthorizationPermission != "system:approval-maintenance" ||
		expirationAudit.AuthorizationPolicyVersion != "explicit-rbac-v1" {
		t.Fatalf("expiration audit lacks chain evidence: %+v", expirationAudit)
	}
}

func TestExpireApprovalsMarksOnlyOverduePending(t *testing.T) {
	store := newApprovalStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)

	overdue := createTestApproval(t, store, "t1", "overdue")
	future := createTestApproval(t, store, "t1", "future")
	if err := store.WithContext(ctx).Model(&model.Approval{}).Where("id = ?", overdue.ID).Updates(map[string]interface{}{
		"expires_at": now.Add(-time.Minute), "created_at": now, "submitted_at": now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	expired, err := store.ExpireApprovals(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	updatedOverdue, err := store.GetApproval(ctx, "t1", overdue.ID)
	if err != nil {
		t.Fatal(err)
	}
	updatedFuture, err := store.GetApproval(ctx, "t1", future.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedOverdue.Status != model.ApprovalStatusExpired {
		t.Fatalf("overdue status = %q, want expired", updatedOverdue.Status)
	}
	if updatedFuture.Status != model.ApprovalStatusPendingApproval {
		t.Fatalf("future status = %q, want pending_approval", updatedFuture.Status)
	}
}

func TestListApprovedApprovalsUsesDecidedAtCutoff(t *testing.T) {
	store := newApprovalStore(t)
	older := createTestApproval(t, store, "t1", "cutoff-older")
	newer := createTestApproval(t, store, "t1", "cutoff-newer")
	ctx := context.Background()
	cutoff := time.Now().UTC().Add(-time.Minute)
	olderDecidedAt := cutoff.Add(-time.Minute)
	newerDecidedAt := cutoff.Add(time.Minute)
	if _, _, err := store.DecideApprovalStep(ctx, "t1", older.ID, "approver", "", "approve", "ok", olderDecidedAt, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.DecideApprovalStep(ctx, "t1", newer.ID, "approver", "", "approve", "ok", newerDecidedAt, nil); err != nil {
		t.Fatal(err)
	}

	approvals, err := store.ListApprovedApprovals(ctx, 10, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 1 || approvals[0].ID != older.ID {
		t.Fatalf("only approval decided before cutoff must be included, got %v", approvals)
	}
}

func TestGetApprovalPolicyByIDReturnsExactPolicy(t *testing.T) {
	store := newApprovalStore(t)
	policy := &model.ApprovalPolicy{
		ID: "policy-by-id", TenantID: "t1", ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100,
		ConditionsJSON: "{}", StepsJSON: `[{"step_no":1,"name":"one","approver_type":"role","approver_value":"tenant_admin"}]`,
		ExpireHours: 1, Version: 1,
	}
	if err := store.UpsertApprovalPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetApprovalPolicyByID(context.Background(), policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != policy.ID {
		t.Fatalf("got policy %+v, want exact policy", got)
	}
	missing, err := store.GetApprovalPolicyByID(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("missing policy must be nil, got %+v", missing)
	}
}
