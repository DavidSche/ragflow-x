package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_PostgresOperationClaimAndRecoveryIsFenced(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	tenant := mustCreatePostgresTenant(t, store, "approval-operation-contract")
	approvalID := "pg-approval-operation"

	claimContext := &model.ActingContext{
		ID: "pg-acting-context-claim", ActorID: "actor", ActorTenantID: tenant.ID,
		ActorRole: model.ApprovalApproverUser, ActingMode: "acting", TargetTenantID: tenant.ID,
		ResourceType: model.ApprovalObjectDataset, ResourceID: "dataset-1", ResourceVersion: "1",
		Action: model.ApprovalActionDelete, ApprovalID: approvalID, AttemptNo: 1,
		ApprovalActionHash: "hash", IdempotencyKey: "idempotency-claim", RequestID: "request-claim",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Status: model.ActingContextActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateActingContext(ctx, claimContext); err != nil {
		t.Fatalf("create PostgreSQL acting context: %v", err)
	}

	const claimantCount = 8
	won := make([]bool, claimantCount)
	claimErrors := make([]error, claimantCount)
	var wg sync.WaitGroup
	wg.Add(claimantCount)
	for index := range claimantCount {
		go func(index int) {
			defer wg.Done()
			claimedBy := fmt.Sprintf("worker-%d", index)
			operation := &model.ApprovalOperation{
				ID:                   fmt.Sprintf("pg-approval-operation-%d", index),
				TenantID:             tenant.ID,
				ApprovalID:           approvalID,
				ActingContextID:      claimContext.ID,
				AttemptNo:            claimContext.AttemptNo,
				PrincipalID:          claimContext.ActorID,
				IdempotencyKey:       claimContext.IdempotencyKey,
				RequestID:            claimContext.RequestID,
				OperationFingerprint: "fingerprint-claim",
				ApprovalActionHash:   claimContext.ApprovalActionHash,
				Status:               model.ApprovalOperationRunning,
				ClaimedBy:            claimedBy,
				StartedAt:            now,
				CreatedAt:            now,
				UpdatedAt:            now,
			}
			_, claimed, err := store.ClaimActingContextForOperation(
				ctx, claimContext.ID, claimedBy, now, now.Add(15*time.Minute), operation,
			)
			won[index] = claimed
			claimErrors[index] = err
		}(index)
	}
	wg.Wait()

	winner := -1
	for index := range claimantCount {
		if claimErrors[index] != nil {
			t.Fatalf("claimant %d: %v", index, claimErrors[index])
		}
		if won[index] && winner != -1 {
			t.Fatalf("multiple PostgreSQL operation claim winners: %d and %d", winner, index)
		}
		if won[index] {
			winner = index
		}
	}
	if winner == -1 {
		t.Fatal("no PostgreSQL operation claim winner")
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_acting_context WHERE approval_id = ? AND status = ?`, approvalID, model.ActingContextClaimed); count != 1 {
		t.Fatalf("claimed PostgreSQL contexts = %d, want 1", count)
	}
	if count := postgresCount(t, store, `SELECT COUNT(*) FROM rgx_approval_operation WHERE approval_id = ?`, approvalID); count != 1 {
		t.Fatalf("PostgreSQL operations = %d, want 1", count)
	}
	winnerOperation, err := store.GetApprovalOperationByContext(ctx, claimContext.ID)
	if err != nil || winnerOperation == nil {
		t.Fatalf("get winner PostgreSQL operation: operation=%+v err=%v", winnerOperation, err)
	}
	if winnerOperation.Status != model.ApprovalOperationRunning || winnerOperation.ClaimedBy != fmt.Sprintf("worker-%d", winner) {
		t.Fatalf("winner PostgreSQL operation: %+v", winnerOperation)
	}

	completed, err := store.CompleteApprovalOperation(
		ctx, winnerOperation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed,
		`{"reason":"contract-test"}`, "", now.Add(time.Minute),
	)
	if err != nil || !completed {
		t.Fatalf("complete RUNNING PostgreSQL operation: completed=%v err=%v", completed, err)
	}
	for _, transition := range [][2]string{
		{model.ApprovalOperationRunning, model.ApprovalOperationUnknown},
		{model.ApprovalOperationFailed, model.ApprovalOperationCompleted},
		{model.ApprovalOperationRunning, model.ApprovalOperationFailed},
	} {
		completed, err = store.CompleteApprovalOperation(
			ctx, winnerOperation.ID, transition[0], transition[1], "{}", "terminal fence", now.Add(2*time.Minute),
		)
		if err != nil {
			t.Fatalf("terminal transition %s->%s: %v", transition[0], transition[1], err)
		}
		if completed {
			t.Fatalf("terminal transition %s->%s was accepted", transition[0], transition[1])
		}
	}
	if stale, err := store.MarkStaleApprovalOperationUnknown(
		ctx, winnerOperation.ID, now.Add(-time.Second), now.Add(2*time.Minute),
	); err != nil || stale {
		t.Fatalf("stale terminal PostgreSQL operation recovered: stale=%v err=%v", stale, err)
	}

	staleContext := &model.ActingContext{
		ID: "pg-acting-context-stale", ActorID: claimContext.ActorID, ActorTenantID: tenant.ID,
		ActorRole: claimContext.ActorRole, ActingMode: claimContext.ActingMode,
		TargetTenantID: tenant.ID, ResourceType: claimContext.ResourceType,
		ResourceID: claimContext.ResourceID, ResourceVersion: claimContext.ResourceVersion,
		Action: claimContext.Action, ApprovalID: approvalID, AttemptNo: 2,
		ApprovalActionHash: claimContext.ApprovalActionHash,
		IdempotencyKey:     "idempotency-stale", RequestID: "request-stale",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Status: model.ActingContextActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateActingContext(ctx, staleContext); err != nil {
		t.Fatalf("create stale PostgreSQL acting context: %v", err)
	}
	staleOperation := &model.ApprovalOperation{
		ID: "pg-approval-operation-stale", TenantID: tenant.ID, ApprovalID: approvalID,
		ActingContextID: staleContext.ID, AttemptNo: 2, PrincipalID: staleContext.ActorID,
		IdempotencyKey: staleContext.IdempotencyKey, RequestID: staleContext.RequestID,
		OperationFingerprint: "fingerprint-stale", ApprovalActionHash: staleContext.ApprovalActionHash,
		Status: model.ApprovalOperationRunning, ClaimedBy: "stale-worker",
		StartedAt: now.Add(-time.Hour), CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if _, claimed, err := store.ClaimActingContextForOperation(
		ctx, staleContext.ID, staleOperation.ClaimedBy, now, now.Add(15*time.Minute), staleOperation,
	); err != nil || !claimed {
		t.Fatalf("claim stale PostgreSQL operation: claimed=%v err=%v", claimed, err)
	}
	if recovered, err := store.MarkStaleApprovalOperationUnknown(
		ctx, staleOperation.ID, now, now.Add(time.Minute),
	); err != nil || !recovered {
		t.Fatalf("recover stale RUNNING PostgreSQL operation: recovered=%v err=%v", recovered, err)
	}
	if completed, err := store.CompleteApprovalOperation(
		ctx, staleOperation.ID, model.ApprovalOperationRunning, model.ApprovalOperationCompleted,
		"{}", "unknown fence", now.Add(2*time.Minute),
	); err != nil || completed {
		t.Fatalf("complete unknown PostgreSQL operation: completed=%v err=%v", completed, err)
	}
}
