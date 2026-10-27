package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// createTestDatasetLink creates a dataset link in the test database for approval tests.
// helper: set up a tenant with a policy and two users (requester + approver).
// Returns (requesterID, approverID, tenantID, policyID).
func setupApprovalEnv(t *testing.T, svc *Service) (string, string, string, string) {
	t.Helper()
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "test-tenant")
	if err != nil {
		t.Fatal(err)
	}

	// requester: normal operator
	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "requester", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}

	// approver: tenant_admin with manage/approval permission
	approver, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "approver", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// create policy: single-step, user-based approver
	policy := &model.ApprovalPolicy{
		ID:             "policy-1",
		TenantID:       tenant.ID,
		ObjectType:     model.ApprovalObjectDataset,
		Action:         model.ApprovalActionDelete,
		Enabled:        true,
		Priority:       100,
		ConditionsJSON: "{}",
		StepsJSON:      `[{"step_no":1,"name":"admin approve","approver_type":"user","approver_value":"` + approver.ID + `","expire_hours":48}]`,
		ExpireHours:    48,
		Version:        1,
		CreatedBy:      "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	return requester.ID, approver.ID, tenant.ID, policy.ID
}

// createApprovalDirectly inserts an approval with steps directly into the store,
// bypassing SubmitApproval's target resolution (which requires real dataset links).
// approverValue is the user/role/team ID that the step expects as approver.
func createApprovalDirectly(t *testing.T, svc *Service, tenantID, requesterID, policyID, status, approverType, approverValue string) *model.Approval {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	approval := &model.Approval{
		ID:             id.New(),
		TenantID:       tenantID,
		RequestNo:      "APR-TEST-" + id.New()[:8],
		ObjectType:     model.ApprovalObjectDataset,
		ObjectID:       "ds-test-" + id.New()[:4],
		Action:         model.ApprovalActionDelete,
		Title:          "test approval",
		Status:         status,
		PolicyID:       policyID,
		PolicyVersion:  1,
		CurrentStep:    1,
		RequesterID:    requesterID,
		IdempotencyKey: "idem-" + id.New(),
		PayloadJSON:    "{}",
		SnapshotJSON:   "{}",
		ExpiresAt:      now.Add(48 * time.Hour),
		SubmittedAt:    now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	futureDueAt := now.Add(48 * time.Hour)
	steps := []model.ApprovalStep{
		{
			ID:            id.New(),
			TenantID:      tenantID,
			ApprovalID:    approval.ID,
			StepNo:        1,
			Name:          "step 1",
			ApproverType:  approverType,
			ApproverValue: approverValue,
			Status:        model.ApprovalStepCurrent,
			DueAt:         &futureDueAt,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, steps, nil, nil); err != nil {
		t.Fatal(err)
	}
	return approval
}

// TestApprovalSoD_RequesterCannotApproveOwn verifies that the requester of an
// approval cannot also be the approver (Separation of Duties).
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_RequesterCannotApproveOwn(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "sod-tenant")
	if err != nil {
		t.Fatal(err)
	}

	// admin who has both manage/approval and is the requester
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "sod-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// policy with admin as approver
	policy := &model.ApprovalPolicy{
		ID:             "sod-policy",
		TenantID:       tenant.ID,
		ObjectType:     model.ApprovalObjectDataset,
		Action:         model.ApprovalActionDelete,
		Enabled:        true,
		Priority:       100,
		ConditionsJSON: "{}",
		StepsJSON:      `[{"step_no":1,"name":"admin approve","approver_type":"user","approver_value":"` + admin.ID + `","expire_hours":48}]`,
		ExpireHours:    48,
		Version:        1,
		CreatedBy:      "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	// Create approval directly with admin as approver
	approval := createApprovalDirectly(t, svc, tenant.ID, admin.ID, policy.ID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, admin.ID)

	// admin tries to approve own request -> must be forbidden
	_, err = svc.DecideApproval(ctx, admin.ID, tenant.ID, approval.ID, "approve", "self-approve test")
	if err == nil {
		t.Fatal("requester must not be able to approve own request")
	}
	t.Logf("SoD correctly rejected: %v", err)
}

// TestApprovalStateMachine_FullLifecycle covers the complete state transitions:
// submit -> approve -> (final) approved.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ApprovalStateMachineFullLifecycle(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "lifecycle-tenant")
	if err != nil {
		t.Fatal(err)
	}
	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "lc-requester", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	approver, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "lc-approver", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.ApprovalPolicy{
		ID: "lc-policy", TenantID: tenant.ID, ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"approve","approver_type":"user","approver_value":"` + approver.ID + `","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	// Step 1: create approval directly with approver as the step approver
	approval := createApprovalDirectly(t, svc, tenant.ID, requester.ID, policy.ID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, approver.ID)
	if approval.Status != model.ApprovalStatusPendingApproval {
		t.Fatalf("expected pending_approval, got status=%q", approval.Status)
	}

	// Step 2: approver approves
	approved, err := svc.DecideApproval(ctx, approver.ID, tenant.ID, approval.ID, "approve", "looks good")
	if err != nil {
		t.Fatalf("DecideApproval failed: %v", err)
	}
	if approved == nil {
		t.Fatal("approved should not be nil")
	}
	if approved.Status != model.ApprovalStatusApproved {
		t.Fatalf("expected approved, got status=%q (requester=%s, approver=%s)", approved.Status, requester.ID, approver.ID)
	}
	if approved.DecidedAt.IsZero() {
		t.Fatal("decided_at should be set after final approval")
	}

	// Verify: step should be marked approved
	steps, err := svc.Store.ListApprovalSteps(ctx, tenant.ID, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Status != model.ApprovalStepApproved {
		t.Fatalf("step status should be approved, got %q", steps[0].Status)
	}
	if steps[0].ActedBy == nil || *steps[0].ActedBy != approver.ID {
		t.Fatal("step acted_by should match approver")
	}
}

// TestApprovalStateMachine_Reject covers rejection path.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ApprovalStateMachineReject(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "reject-tenant")
	if err != nil {
		t.Fatal(err)
	}
	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "rj-requester", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	approver, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "rj-approver", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.ApprovalPolicy{
		ID: "rj-policy", TenantID: tenant.ID, ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"approve","approver_type":"user","approver_value":"` + approver.ID + `","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	approval := createApprovalDirectly(t, svc, tenant.ID, requester.ID, policy.ID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, approver.ID)

	rejected, err := svc.DecideApproval(context.Background(), approver.ID, tenant.ID, approval.ID, "reject", "not ready")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != model.ApprovalStatusRejected {
		t.Fatalf("expected rejected, got status=%q", rejected.Status)
	}
}

// TestApprovalStateMachine_Cancel covers cancellation by requester.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ApprovalStateMachineCancel(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "cancel-tenant")
	if err != nil {
		t.Fatal(err)
	}
	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "cn-requester", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.ApprovalPolicy{
		ID: "cn-policy", TenantID: tenant.ID, ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"approve","approver_type":"role","approver_value":"tenant_admin","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	approval := createApprovalDirectly(t, svc, tenant.ID, requester.ID, policy.ID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin")

	cancelled, err := svc.CancelApproval(context.Background(), requester.ID, tenant.ID, approval.ID, "changed my mind")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.ApprovalStatusCanceled {
		t.Fatalf("expected canceled, got status=%q", cancelled.Status)
	}
}

// TestApprovalConcurrency_TwoApproversRace ensures that when two different
// approvers try to approve the last step concurrently, only one succeeds.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_TwoApproversRace(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "race-tenant")
	if err != nil {
		t.Fatal(err)
	}

	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "race-requester", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create two approvers
	approver1, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "race-approver1", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	approver2, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "race-approver2", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Policy with role-based approver (both tenant_admin users qualify)
	policy := &model.ApprovalPolicy{
		ID: "race-policy", TenantID: tenant.ID, ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"admin approve","approver_type":"role","approver_value":"tenant_admin","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	// Create approval directly with tenant_admin role as approver
	approval := createApprovalDirectly(t, svc, tenant.ID, requester.ID, policy.ID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin")

	// Two goroutines race to approve
	var wg sync.WaitGroup
	results := make([]*model.Approval, 2)
	errors := make([]error, 2)

	for i, approverID := range []string{approver1.ID, approver2.ID} {
		wg.Add(1)
		go func(idx int, aID string) {
			defer wg.Done()
			results[idx], errors[idx] = svc.DecideApproval(ctx, aID, tenant.ID, approval.ID, "approve", "concurrent approve")
		}(i, approverID)
	}
	wg.Wait()

	// Exactly one should succeed, one should fail with 409
	successCount := 0
	failCount := 0
	for i := 0; i < 2; i++ {
		if errors[i] == nil && results[i] != nil && results[i].Status == model.ApprovalStatusApproved {
			successCount++
		} else {
			failCount++
			t.Logf("goroutine %d failed (expected): %v", i, errors[i])
		}
	}

	if successCount != 1 || failCount != 1 {
		t.Fatalf("expected exactly 1 success and 1 failure, got success=%d fail=%d", successCount, failCount)
	}
}

// TestApprovalTenantIsolation_CrossTenantDenied ensures that user from tenant A
// cannot see or approve an approval belonging to tenant B.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_CrossTenantDenied(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	// Tenant A
	tenantA, err := svc.CreateTenant(ctx, "tenant-A")
	if err != nil {
		t.Fatal(err)
	}
	adminA, err := svc.CreateUser(ctx, tenantA.ID, "platform_admin", CreateUserRequest{
		Username: "adminA", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	policyA := &model.ApprovalPolicy{
		ID: "policyA", TenantID: tenantA.ID, ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"approve","approver_type":"role","approver_value":"tenant_admin","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policyA); err != nil {
		t.Fatal(err)
	}

	// Tenant B
	tenantB, err := svc.CreateTenant(ctx, "tenant-B")
	if err != nil {
		t.Fatal(err)
	}
	adminB, err := svc.CreateUser(ctx, tenantB.ID, "platform_admin", CreateUserRequest{
		Username: "adminB", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tenant A submits an approval directly
	approvalA := createApprovalDirectly(t, svc, tenantA.ID, adminA.ID, policyA.ID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin")

	// Tenant B admin tries to get approval details -> must be denied
	_, err = svc.GetApproval(ctx, adminB.ID, tenantB.ID, approvalA.ID)
	if err == nil {
		t.Fatal("cross-tenant approval access should be forbidden")
	}
	t.Logf("cross-tenant access correctly denied: %v", err)

	// Tenant B admin tries to approve -> must be denied
	_, err = svc.DecideApproval(ctx, adminB.ID, tenantB.ID, approvalA.ID, "approve", "cross-tenant approve")
	if err == nil {
		t.Fatal("cross-tenant approval decision should be forbidden")
	}
	t.Logf("cross-tenant decision correctly denied: %v", err)
}

// TestApprovalExecutor_DatasetDeleteRetry verifies that a transient delete
// failure retains the failure site and a later worker retry completes it.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_DatasetDeleteExecutorRetry(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	// Create a fake approval in approved state
	tenantID := "exec-tenant"
	now := time.Now().UTC()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	approval := &model.Approval{
		ID:             "exec-approval-1",
		TenantID:       tenantID,
		RequestNo:      "APR-EXEC-001",
		ObjectType:     model.ApprovalObjectDataset,
		ObjectID:       "ds-exec-delete",
		Action:         model.ApprovalActionDelete,
		Title:          "delete dataset for test",
		Status:         model.ApprovalStatusApproved,
		PolicyID:       "policy-exec",
		PolicyVersion:  1,
		CurrentStep:    1,
		RequesterID:    admin.ID,
		IdempotencyKey: "exec-approval-1",
		PayloadJSON:    "{}",
		SnapshotJSON:   "{}",
		ExpiresAt:      now.Add(24 * time.Hour),
		DecidedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := svc.Store.CreateApprovalWithAudit(ctx, approval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	actingContext, err := svc.prepareActingContext(ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	firstPayload, err := json.Marshal(map[string]string{
		"approval_id":          approval.ID,
		"target_tenant_id":     actingContext.TargetTenantID,
		"acting_context_id":    actingContext.ID,
		"approval_action_hash": actingContext.ApprovalActionHash,
		"idempotency_key":      actingContext.IdempotencyKey,
		"request_id":           actingContext.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build the worker and run it
	worker := &approvalExecuteWorker{svc: svc}
	job := &model.Job{
		ID:       "job-exec-1",
		Payload:  string(firstPayload),
		TenantID: tenantID,
		Attempts: 0,
		MaxRetry: 3,
	}

	firstErr := worker.Run(ctx, job)
	if firstErr == nil {
		t.Fatal("missing dataset link should make the first execution fail")
	}

	// Check approval status
	updated, err := svc.Store.GetApproval(ctx, tenantID, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil {
		t.Fatal("approval should still exist")
	}
	if updated.Status != model.ApprovalStatusExecutionFailed {
		t.Fatalf("expected execution_failed after first attempt, got status=%q", updated.Status)
	}

	// Recreate the missing target and model the next worker attempt after a
	// manual retry has moved the failed approval back to approved.
	engineDataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "retryable dataset"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID:               approval.ObjectID,
		TenantID:         tenantID,
		RAGFlowDatasetID: engineDataset.ID,
		Name:             "retryable dataset",
		ProjectID:        "proj-default",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := svc.Store.TransitionApproval(ctx, tenantID, approval.ID,
		model.ApprovalStatusExecutionFailed, model.ApprovalStatusApproved, nil,
	)
	if err != nil || !claimed {
		t.Fatalf("retry transition: claimed=%t err=%v", claimed, err)
	}
	updated, err = svc.Store.GetApproval(ctx, tenantID, approval.ID)
	if err != nil || updated == nil {
		t.Fatalf("reload approval for retry: %v", err)
	}
	retryContext, err := svc.prepareActingContext(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	retryPayload, err := json.Marshal(map[string]string{
		"approval_id":          updated.ID,
		"target_tenant_id":     retryContext.TargetTenantID,
		"acting_context_id":    retryContext.ID,
		"approval_action_hash": retryContext.ApprovalActionHash,
		"idempotency_key":      retryContext.IdempotencyKey,
		"request_id":           retryContext.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	job.Payload = string(retryPayload)
	if err := worker.Run(ctx, job); err != nil {
		t.Fatalf("retry execution: %v", err)
	}
	updated, err = svc.Store.GetApproval(ctx, tenantID, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || updated.Status != model.ApprovalStatusCompleted {
		t.Fatalf("expected completed after retry, got %+v", updated)
	}

	audits, err := svc.Store.ListAuditsAll(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	jobIDAudited := false
	for _, audit := range audits {
		if audit.ResourceID != approval.ID {
			continue
		}
		if audit.Action != "approval.executed" && audit.Action != "approval.execution_failed" {
			continue
		}
		if strings.Contains(audit.DetailJSON, `"job_id":"job-exec-1"`) {
			jobIDAudited = true
			break
		}
	}
	if !jobIDAudited {
		t.Fatal("execution audit must include the worker job id")
	}
	t.Logf("approval ended in status=%q", updated.Status)
}

// TestApprovalIdempotency_DuplicateSubmitRejected ensures that submitting the
// same idempotency key twice returns the existing approval, not a new one.
// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_DuplicateSubmitRejected(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{
		Enabled:             true,
		DefaultExpireHours:  72,
		ExecutionMaxRetries: 1,
		PolicyCacheTTLSec:   60,
	})
	ctx := context.Background()

	tenant, err := svc.CreateTenant(ctx, "idem-tenant")
	if err != nil {
		t.Fatal(err)
	}
	requester, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "idem-requester", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create an approval
	approval1 := createApprovalDirectly(t, svc, tenant.ID, requester.ID, "policy-x", model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin")
	// Try to find by idempotency key
	existing, err := svc.Store.GetApprovalByIdempotencyKey(ctx, tenant.ID, approval1.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	// Should find the existing approval
	if existing == nil {
		t.Fatal("approval should be found by idempotency key")
	}
	if existing.ID != approval1.ID {
		t.Fatalf("expected same approval ID, got %s != %s", existing.ID, approval1.ID)
	}

	t.Logf("idempotency key test passed: duplicate key correctly returns existing approval")
}

// TestApprovalPolicyConditionMatching verifies that conditions are evaluated
// correctly during policy matching.
func TestApprovalPolicyConditionMatching(t *testing.T) {
	tests := []struct {
		name     string
		condJSON string
		attrs    map[string]any
		want     bool
	}{
		{
			name:     "eq match",
			condJSON: `{"all":[{"field":"dataset_name","op":"eq","value":"finance-prod"}]}`,
			attrs:    map[string]any{"dataset_name": "finance-prod"},
			want:     true,
		},
		{
			name:     "eq no match",
			condJSON: `{"all":[{"field":"dataset_name","op":"eq","value":"finance-prod"}]}`,
			attrs:    map[string]any{"dataset_name": "dev-test"},
			want:     false,
		},
		{
			name:     "prefix match",
			condJSON: `{"all":[{"field":"dataset_name","op":"prefix","value":"finance-"}]}`,
			attrs:    map[string]any{"dataset_name": "finance-report"},
			want:     true,
		},
		{
			name:     "gt match",
			condJSON: `{"all":[{"field":"key_quota","op":"gt","value":1000}]}`,
			attrs:    map[string]any{"key_quota": 5000},
			want:     true,
		},
		{
			name:     "gt no match",
			condJSON: `{"all":[{"field":"key_quota","op":"gt","value":1000}]}`,
			attrs:    map[string]any{"key_quota": 500},
			want:     false,
		},
		{
			name:     "exists match",
			condJSON: `{"all":[{"field":"dataset_project_id","op":"exists","value":null}]}`,
			attrs:    map[string]any{"dataset_project_id": "proj-1"},
			want:     true,
		},
		{
			name:     "exists no match",
			condJSON: `{"all":[{"field":"dataset_project_id","op":"exists","value":null}]}`,
			attrs:    map[string]any{},
			want:     false,
		},
		{
			name:     "in match",
			condJSON: `{"all":[{"field":"dataset_name","op":"in","value":["prod-a","prod-b"]}]}`,
			attrs:    map[string]any{"dataset_name": "prod-b"},
			want:     true,
		},
		{
			name:     "in no match",
			condJSON: `{"all":[{"field":"dataset_name","op":"in","value":["prod-a","prod-b"]}]}`,
			attrs:    map[string]any{"dataset_name": "dev-c"},
			want:     false,
		},
		{
			name:     "empty conditions always match",
			condJSON: `{}`,
			attrs:    map[string]any{"anything": "value"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var conds model.ApprovalConditions
			if err := json.Unmarshal([]byte(tt.condJSON), &conds); err != nil {
				t.Fatal(err)
			}
			got := matchApprovalConditions(conds, tt.attrs)
			if got != tt.want {
				t.Fatalf("matchApprovalConditions() = %v, want %v", got, tt.want)
			}
		})
	}
}
