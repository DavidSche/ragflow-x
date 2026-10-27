package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_ConcurrentExecutionEnqueueHasOneContextAndJob(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC().Add(-time.Second)
	approval := &model.Approval{
		ID: "approval-enqueue-race", TenantID: admin.TenantID, RequestNo: "APR-ENQUEUE-RACE",
		ObjectType: model.ApprovalObjectAPIKey, ObjectID: "new:test-key", Action: model.ApprovalActionCreate,
		Title: "create execution race test", Status: model.ApprovalStatusApproved,
		PolicyID: "policy", PolicyVersion: 1, CurrentStep: 1, RequesterID: admin.ID,
		PayloadJSON: `{"name":"test-key"}`, SnapshotJSON: "{}", IdempotencyKey: "enqueue-race",
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
	svc.Runner = NewRunner(svc.Store, DefaultWorkerConfig())

	const callerCount = 8
	inserted := make([]bool, callerCount)
	enqueues := make([]error, callerCount)
	var wg sync.WaitGroup
	wg.Add(callerCount)
	for index := range callerCount {
		go func(index int) {
			defer wg.Done()
			inserted[index], enqueues[index] = svc.enqueueApprovalExecution(ctx, approval)
		}(index)
	}
	wg.Wait()

	for index := range callerCount {
		if enqueues[index] != nil {
			t.Fatalf("enqueue caller %d: %v", index, enqueues[index])
		}
	}
	contexts, err := svc.Store.GetActingContextByApproval(ctx, approval.ID)
	if err != nil || contexts == nil {
		t.Fatalf("get acting context: context=%+v err=%v", contexts, err)
	}
	jobs, jobTotal, err := svc.Store.ListJobs(ctx, approval.TenantID, model.JobKindApprovalExecute, "", 1, 20)
	if err != nil || jobTotal != 1 || len(jobs) != 1 {
		t.Fatalf("execution jobs: total=%d jobs=%+v err=%v", jobTotal, jobs, err)
	}
	var payload approvalExecutionPayload
	if err := json.Unmarshal([]byte(jobs[0].Payload), &payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload.ActingContextID != contexts.ID || payload.ApprovalActionHash != actionHash {
		t.Fatalf("execution payload points outside signed context: payload=%+v context=%+v", payload, contexts)
	}
	contextCount, err := svc.Store.CountActingContextsByApproval(ctx, approval.ID)
	if err != nil || contextCount != 1 {
		t.Fatalf("acting contexts = %d err=%v, want 1", contextCount, err)
	}
}
