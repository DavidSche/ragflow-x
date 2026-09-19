package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type countingApprovalExecutor struct {
	executions atomic.Int64
	executeErr error
}

func (e *countingApprovalExecutor) Healthy(context.Context) error { return nil }

func (e *countingApprovalExecutor) Validate(context.Context, *model.Approval) error { return nil }

func (e *countingApprovalExecutor) Execute(context.Context, *model.Approval) (map[string]any, error) {
	e.executions.Add(1)
	if e.executeErr != nil {
		return nil, e.executeErr
	}
	return map[string]any{"provider_side_effect": "applied"}, nil
}

func newApprovalOperationLifecycleApproval(t *testing.T, svc *Service, id string) (*model.Approval, *model.ActingContext) {
	t.Helper()
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	now := time.Now().UTC().Add(-time.Second)
	approval := &model.Approval{
		ID: id, TenantID: admin.TenantID, RequestNo: "APR-" + id,
		ObjectType: model.ApprovalObjectAPIKey, ObjectID: "new:test-key", Action: model.ApprovalActionCreate,
		Title: "approval operation lifecycle", Status: model.ApprovalStatusApproved,
		PolicyID: "policy", PolicyVersion: 1, CurrentStep: 1, RequesterID: admin.ID,
		TargetTenantID: admin.TenantID, PayloadJSON: `{"name":"test-key"}`, SnapshotJSON: "{}", IdempotencyKey: id + "-idem",
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
	return approval, acting
}

func approvalOperationJob(t *testing.T, approval *model.Approval, acting *model.ActingContext, attempts, maxRetry int) *model.Job {
	t.Helper()
	payload, err := json.Marshal(approvalExecutionPayload{
		ApprovalID: approval.ID, TargetTenantID: acting.TargetTenantID,
		ActingContextID: acting.ID, ApprovalActionHash: acting.ApprovalActionHash,
		IdempotencyKey: acting.IdempotencyKey, RequestID: acting.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &model.Job{
		ID: "job-" + approval.ID, Payload: string(payload), TenantID: approval.TenantID,
		Attempts: attempts, MaxRetry: maxRetry,
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_CompletedOperationReplayDoesNotExecuteProviderAgain(t *testing.T) {
	svc := newAuthzSvc(t)
	approval, acting := newApprovalOperationLifecycleApproval(t, svc, "approval-operation-success")
	executor := &countingApprovalExecutor{}
	registry := NewApprovalExecutorRegistry()
	if !registry.Register(approval.ObjectType+"."+approval.Action, executor) {
		t.Fatal("failed to register test executor")
	}
	svc.approvalExecutorRegistryOverride = registry
	worker := &approvalExecuteWorker{svc: svc}
	ctx := context.Background()
	job := approvalOperationJob(t, approval, acting, 0, 3)
	if err := worker.Run(ctx, job); err != nil {
		t.Fatalf("first execution: %v", err)
	}
	if executor.executions.Load() != 1 {
		t.Fatalf("provider executions = %d, want 1", executor.executions.Load())
	}
	completed, err := svc.Store.GetApproval(ctx, approval.TenantID, approval.ID)
	if err != nil || completed == nil || completed.Status != model.ApprovalStatusCompleted {
		t.Fatalf("approval must complete: %+v err=%v", completed, err)
	}
	storedContext, err := svc.Store.GetActingContext(ctx, acting.ID)
	if err != nil || storedContext == nil || storedContext.Status != model.ActingContextConsumed {
		t.Fatalf("acting context must be consumed: %+v err=%v", storedContext, err)
	}
	operation, err := svc.Store.GetApprovalOperationByContext(ctx, acting.ID)
	if err != nil || operation == nil || operation.Status != model.ApprovalOperationCompleted {
		t.Fatalf("operation must be completed: %+v err=%v", operation, err)
	}
	if err := worker.Run(ctx, job); err != nil {
		t.Fatalf("completed operation replay must be a no-op: %v", err)
	}
	if executor.executions.Load() != 1 {
		t.Fatalf("completed operation replay executed provider %d times", executor.executions.Load())
	}
	if job.Result != operation.ResultJSON {
		t.Fatalf("job result = %q, want operation result %q", job.Result, operation.ResultJSON)
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ExecutionAuditIsExactlyOnceAndChainAnchored(t *testing.T) {
	svc := newAuthzSvc(t)
	approval, acting := newApprovalOperationLifecycleApproval(t, svc, "approval-execution-audit")
	executor := &countingApprovalExecutor{}
	registry := NewApprovalExecutorRegistry()
	if !registry.Register(approval.ObjectType+"."+approval.Action, executor) {
		t.Fatal("failed to register test executor")
	}
	svc.approvalExecutorRegistryOverride = registry
	worker := &approvalExecuteWorker{svc: svc}
	ctx := context.Background()
	job := approvalOperationJob(t, approval, acting, 0, 3)

	if err := worker.Run(ctx, job); err != nil {
		t.Fatalf("first execution: %v", err)
	}
	if err := worker.Run(ctx, job); err != nil {
		t.Fatalf("completed operation replay: %v", err)
	}
	if executor.executions.Load() != 1 {
		t.Fatalf("provider executions = %d, want 1", executor.executions.Load())
	}

	audits, err := svc.Store.ListAuditsAll(ctx, approval.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	var executionAudits []model.AuditLog
	for _, audit := range audits {
		if audit.ResourceID == approval.ID &&
			(audit.Action == "approval.executed" || audit.Action == "approval.execution_failed") {
			executionAudits = append(executionAudits, audit)
		}
	}
	if len(executionAudits) != 1 {
		t.Fatalf("execution audits = %d, want exactly 1: %+v", len(executionAudits), executionAudits)
	}
	audit := executionAudits[0]
	if audit.UserID != "system" || audit.ActorTenantID != model.PlatformTenantID ||
		audit.TargetTenantID != approval.TargetTenantID || audit.ActingContextID != acting.ID ||
		audit.ApprovalActionHash != approval.ApprovalActionHash || audit.Result != "SUCCESS" ||
		audit.AuthorizationDecision != "ALLOW" ||
		audit.AuthorizationPermission != "system:approval-executor" ||
		audit.AuthorizationPolicyVersion != "explicit-rbac-v1" ||
		!strings.Contains(audit.DetailJSON, `"job_id":"job-`+approval.ID+`"`) ||
		!strings.Contains(audit.DetailJSON, `"object_type":"`+approval.ObjectType+`"`) {
		t.Fatalf("execution audit lacks chain evidence: %+v", audit)
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ExecutorFailureSettlesOperationBeforeReplay(t *testing.T) {
	svc := newAuthzSvc(t)
	approval, acting := newApprovalOperationLifecycleApproval(t, svc, "approval-operation-failure")
	expectedErr := errors.New("provider business action failed")
	executor := &countingApprovalExecutor{executeErr: expectedErr}
	registry := NewApprovalExecutorRegistry()
	if !registry.Register(approval.ObjectType+"."+approval.Action, executor) {
		t.Fatal("failed to register test executor")
	}
	svc.approvalExecutorRegistryOverride = registry
	worker := &approvalExecuteWorker{svc: svc}
	ctx := context.Background()
	job := approvalOperationJob(t, approval, acting, 0, 3)
	if err := worker.Run(ctx, job); !errors.Is(err, expectedErr) {
		t.Fatalf("first execution error = %v, want %v", err, expectedErr)
	}
	if executor.executions.Load() != 1 {
		t.Fatalf("provider executions = %d, want 1", executor.executions.Load())
	}
	failed, err := svc.Store.GetApproval(ctx, approval.TenantID, approval.ID)
	if err != nil || failed == nil || failed.Status != model.ApprovalStatusExecutionFailed {
		t.Fatalf("approval must settle as execution_failed: %+v err=%v", failed, err)
	}
	storedContext, err := svc.Store.GetActingContext(ctx, acting.ID)
	if err != nil || storedContext == nil || storedContext.Status != model.ActingContextFailed {
		t.Fatalf("acting context must fail: %+v err=%v", storedContext, err)
	}
	operation, err := svc.Store.GetApprovalOperationByContext(ctx, acting.ID)
	if err != nil || operation == nil || operation.Status != model.ApprovalOperationFailed {
		t.Fatalf("operation must fail: %+v err=%v", operation, err)
	}
	if err := worker.Run(ctx, job); err != nil {
		t.Fatalf("failed operation replay must be a no-op: %v", err)
	}
	if executor.executions.Load() != 1 {
		t.Fatalf("failed operation replay executed provider %d times", executor.executions.Load())
	}
}
