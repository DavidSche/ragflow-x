package db

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ScenarioID: SC-PG-001
func TestP0_PG_001_PostgresApprovalOrganizationAdaptability(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL baseline")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	defer sqlDB.Close()

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var version int
	if err := gdb.Raw("SELECT COALESCE(MAX(version), 0) FROM rgx_schema_version").Scan(&version).Error; err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version < 52 {
		t.Fatalf("schema version = %d, want >= 52", version)
	}
	expected := []string{
		"idx_rgx_approval_policy_tenant_object",
		"uk_rgx_approval_step_actor",
		"idx_rgx_approval_delegation_active",
		"idx_active_gate_decision",
	}
	for _, indexName := range expected {
		var count int64
		err := gdb.Raw("SELECT COUNT(*) FROM pg_indexes WHERE indexname = ?", indexName).Scan(&count).Error
		if err != nil {
			t.Fatalf("check index %s: %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("index %s was not created", indexName)
		}
	}

	var migrationLocks int64
	if err := gdb.Raw(`SELECT COUNT(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = hashtext('ragflow_x_schema_migrations') AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&migrationLocks).Error; err != nil {
		t.Fatalf("check migration lock: %v", err)
	}
	if migrationLocks != 0 {
		t.Fatalf("migration advisory locks = %d, want 0", migrationLocks)
	}

	store := repository.NewStore(gdb)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	approvalID := id.New()
	stepID := id.New()
	tenantID := "pg-" + id.New()[:8]
	runID := id.New()
	pass := true
	terminalRun := &model.EvaluationRun{
		ID: runID, TenantID: tenantID, ReleaseCandidateID: id.New(), CandidateVersion: 1,
		EvalSetID: "pg-set", EvalSetVersion: 1, EvalSetHash: "hash",
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "hash",
		ExecutionSnapshotID: id.New(), Status: model.EvaluationRunCompleted,
		Metrics: "{}", Pass: &pass, Actor: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(terminalRun).Error; err != nil {
		t.Fatalf("create terminal run: %v", err)
	}
	if err := gdb.Model(&model.EvaluationRun{}).Where("id = ?", runID).
		Update("evaluation_policy_hash", "tampered").Error; err == nil {
		t.Fatal("expected terminal evaluation run update to fail")
	}
	if err := gdb.Delete(&model.EvaluationRun{}, "id = ?", runID).Error; err == nil {
		t.Fatal("expected terminal evaluation run delete to fail")
	}
	result := &model.EvaluationCaseResult{
		ID: id.New(), TenantID: tenantID, RunID: runID, CaseID: "case",
		CaseVersionID: id.New(), CaseVersionHash: "hash", ActualAnswer: "answer",
		References: "[]", Metrics: "{}", Pass: &pass, CreatedAt: now,
	}
	if err := gdb.Create(result).Error; err != nil {
		t.Fatalf("create terminal case result: %v", err)
	}
	if err := gdb.Model(result).Update("actual_answer", "tampered").Error; err == nil {
		t.Fatal("expected terminal case result update to fail")
	}
	if err := gdb.Delete(result).Error; err == nil {
		t.Fatal("expected terminal case result delete to fail")
	}
	approval := &model.Approval{
		ID: approvalID, TenantID: tenantID, RequestNo: "APR-PG-VERIFY",
		ObjectType: model.ApprovalObjectDataset, ObjectID: "verify", Action: model.ApprovalActionDelete,
		Title: "PostgreSQL approval baseline", Status: model.ApprovalStatusPendingApproval,
		PolicyID: id.New(), PolicyVersion: 1, CurrentStep: 1, RequesterID: "requester",
		IdempotencyKey: "pg-verify-" + id.New(), ExpiresAt: now.Add(time.Hour),
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	due := now.Add(time.Hour)
	steps := []model.ApprovalStep{{
		ID: stepID, TenantID: tenantID, ApprovalID: approvalID, StepNo: 1,
		Name: "PostgreSQL concurrent decision", ApproverType: model.ApprovalApproverUser,
		ApproverValue: "approver-1", ApprovalMode: model.ApprovalModeAny,
		ApproversJSON: `[{"type":"user","value":"approver-1"}]`, RequiredApprovals: 1,
		Status: model.ApprovalStepCurrent, DueAt: &due, CreatedAt: now, UpdatedAt: now,
	}}
	if err := store.CreateApprovalWithAudit(ctx, approval, steps, nil, &model.AuditLog{
		TenantID: tenantID, UserID: "requester", Action: "approval.submitted", Resource: "approval",
		ResourceID: approvalID, DetailJSON: `{"verify":"postgres"}`, At: now,
	}); err != nil {
		t.Fatalf("create approval: %v", err)
	}

	const attempts = 4
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.DecideApprovalStep(ctx, tenantID, approvalID, "approver-1", "",
				model.ApprovalDecisionApprove, "concurrent baseline", now, &model.AuditLog{
					TenantID: tenantID, UserID: "approver-1", Action: "approval.approved", Resource: "approval",
					ResourceID: approvalID, DetailJSON: `{"verify":"concurrent"}`, At: now,
				})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded, duplicated := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, gorm.ErrDuplicatedKey) {
			duplicated++
			continue
		}
		t.Fatalf("concurrent decision failed: %v", err)
	}
	if succeeded != 1 || duplicated != attempts-1 {
		t.Fatalf("concurrent decisions succeeded=%d duplicated=%d, want 1/%d", succeeded, duplicated, attempts-1)
	}

	jobKey := model.ApprovalExecutionJobKeyPrefix + approvalID
	inserted, err := store.EnqueueJob(ctx, &model.Job{
		Kind: model.JobKindApprovalExecute, Key: jobKey, TenantID: tenantID,
		Payload: `{"approval_id":"` + approvalID + `"}`, RunAfter: now,
	})
	if err != nil || !inserted {
		t.Fatalf("enqueue approval execution job: inserted=%v err=%v", inserted, err)
	}
	claimedJobs, err := store.ClaimJobs(ctx, 50, now)
	if err != nil {
		t.Fatalf("claim approval execution job: err=%v", err)
	}
	var claimed *model.Job
	for index := range claimedJobs {
		if claimedJobs[index].ID != "" && claimedJobs[index].Key == jobKey {
			claimed = &claimedJobs[index]
			break
		}
	}
	if claimed == nil {
		t.Fatalf("claim approval execution job: job %s not found among %d jobs", jobKey, len(claimedJobs))
	}
	if err := store.RetryJob(ctx, claimed.ID, 1, "verify retry", now); err != nil {
		t.Fatalf("retry approval execution job: %v", err)
	}
	reclaimedJobs, err := store.ClaimJobs(ctx, 50, now)
	if err != nil {
		t.Fatalf("reclaim approval execution job: err=%v", err)
	}
	var reclaimed *model.Job
	for index := range reclaimedJobs {
		if reclaimedJobs[index].ID == claimed.ID {
			reclaimed = &reclaimedJobs[index]
			break
		}
	}
	if reclaimed == nil || reclaimed.Attempts != 1 {
		t.Fatalf("reclaim retried approval execution job: found=%v attempts=%d", reclaimed != nil, func() int {
			if reclaimed != nil {
				return reclaimed.Attempts
			}
			return 0
		}())
	}
}
