package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_RetryAuditCarriesApprovalChainEvidence(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Retry Audit Workspace")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "retry-admin", Password: "password123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	actionHash := "sha256:" + id.New()
	approval := &model.Approval{
		ID: id.New(), TenantID: tenant.ID, RequestNo: "APR-" + id.New()[:8],
		ObjectType: model.ApprovalObjectDataset, ObjectID: id.New(), Action: model.ApprovalActionDelete,
		Title: "retry audit", PayloadJSON: "{}", SnapshotJSON: "{}",
		TargetTenantID: tenant.ID, ResourceVersion: "dataset:v1", ApprovalActionHash: actionHash,
		Status: model.ApprovalStatusExecutionFailed, PolicyID: "policy-" + id.New()[:8],
		PolicyVersion: 1, CurrentStep: 1, RequesterID: id.New(), IdempotencyKey: "idem-" + id.New(),
		ExpiresAt: now.Add(48 * time.Hour), SubmittedAt: now, LastError: "previous failure",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	updated, err := svc.RetryApprovalExecution(ctx, admin.ID, tenant.ID, approval.ID, "retry after fixing target")
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || updated.Status != model.ApprovalStatusApproved || updated.LastError != "" {
		t.Fatalf("retry transition mismatch: approval=%+v err=%v", updated, err)
	}

	audits, err := svc.Store.ListAuditsAll(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	var retryAudit *model.AuditLog
	for index := range audits {
		if audits[index].Action == "approval.retry" {
			retryAudit = &audits[index]
			break
		}
	}
	if retryAudit == nil {
		t.Fatal("approval.retry audit is missing")
	}
	if retryAudit.TenantID != tenant.ID || retryAudit.ActorTenantID != tenant.ID ||
		retryAudit.TargetTenantID != tenant.ID || retryAudit.ApprovalID != approval.ID ||
		retryAudit.ApprovalActionHash != actionHash || retryAudit.Result != "RETRY_SCHEDULED" ||
		retryAudit.AuthorizationDecision != "ALLOW" ||
		retryAudit.AuthorizationPermission != "manage:approval" ||
		retryAudit.AuthorizationPolicyVersion != "explicit-rbac-v1" {
		t.Fatalf("approval.retry audit lacks chain evidence: %+v", retryAudit)
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_BatchDecisionCommentFailsClosedBeforeRateLimit(t *testing.T) {
	svc := newAuthzSvc(t)
	limiter := ratelimit.NewMemory()
	svc.approvalDecisionLimiter = limiter
	for _, comment := range []string{"", "   "} {
		_, err := svc.BatchDecideApprovals(t.Context(), "actor", "tenant", "approve", ApprovalBatchDecisionRequest{
			IDs: []string{"approval-1", "approval-2"}, Comment: comment,
		})
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) || httpErr.Status != 400 || httpErr.Code != 40097 {
			t.Fatalf("empty batch comment must fail with 400/40097, got %#v", err)
		}
	}
	_, err := svc.BatchDecideApprovals(t.Context(), "actor", "tenant", "approve", ApprovalBatchDecisionRequest{
		IDs: []string{"approval-1"}, Comment: strings.Repeat("x", 513),
	})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Status != 400 || httpErr.Code != 40098 {
		t.Fatalf("oversized batch comment must fail with 400/40098, got %#v", err)
	}
	if count, err := limiter.Count(t.Context(), "actor", time.Minute); err != nil || count != 0 {
		t.Fatalf("invalid batch comments consumed limiter: count=%d err=%v", count, err)
	}
}
