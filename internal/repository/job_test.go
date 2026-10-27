package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newJobStore(t *testing.T) *store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "job.db")})
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

func mustEnqueue(t *testing.T, s *store, key string, runAfter time.Time, maxRetry int) {
	t.Helper()
	inserted, err := s.EnqueueJob(context.Background(), &model.Job{
		Kind: model.JobKindDocumentSync, Key: key, TenantID: "t1",
		RunAfter: runAfter, MaxRetry: maxRetry,
	})
	if err != nil || !inserted {
		t.Fatalf("enqueue %s: inserted=%v err=%v", key, inserted, err)
	}
}

// findJob returns the first job row for a tenant/kind. Helper: a job's key is
// unique so the list should contain it exactly once.
func findJob(t *testing.T, s *store, tenant, key string) *model.Job {
	t.Helper()
	jobs, _, err := s.ListJobs(context.Background(), tenant, "", "", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := range jobs {
		if jobs[i].Key == key {
			return &jobs[i]
		}
	}
	t.Fatalf("job %s not found for tenant %s", key, tenant)
	return nil
}

func TestEnqueueJobIdempotentByKey(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	inserted, err := s.EnqueueJob(ctx, &model.Job{Kind: model.JobKindDocumentSync, Key: "k1", TenantID: "t1"})
	if err != nil || !inserted {
		t.Fatalf("first enqueue: inserted=%v err=%v", inserted, err)
	}
	// Same key must be a no-op even with different payload.
	inserted, err = s.EnqueueJob(ctx, &model.Job{Kind: model.JobKindDocumentSync, Key: "k1", TenantID: "t1", Payload: "dup"})
	if err != nil || inserted {
		t.Fatalf("duplicate key must be no-op: inserted=%v err=%v", inserted, err)
	}
	jobs, err := s.ClaimJobs(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected exactly one job row, got %d", len(jobs))
	}
}

func TestClaimJobsSkipsFutureAndExcludesRunning(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustEnqueue(t, s, "due", now.Add(-time.Second), 1)
	mustEnqueue(t, s, "future", now.Add(time.Hour), 1)

	jobs, err := s.ClaimJobs(ctx, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Key != "due" {
		t.Fatalf("only the due job should be claimed, got %+v", jobs)
	}
	if jobs[0].Status != model.JobStatusRunning {
		t.Fatalf("claimed job must be running, got %s", jobs[0].Status)
	}
	// A second claim at the same time must not re-claim the already running
	// job, and the future-dated job is still not due.
	again, err := s.ClaimJobs(ctx, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("running job must not be re-claimed, got %+v", again)
	}
}

func TestCompleteAndRetryAndFailLifecycle(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// succeed
	mustEnqueue(t, s, "ok", now, 3)
	claimed, err := s.ClaimJobs(ctx, 10, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim ok: %v %d", err, len(claimed))
	}
	if err := s.CompleteJob(ctx, claimed[0].ID, 1, "done"); err != nil {
		t.Fatal(err)
	}
	j := findJob(t, s, "t1", "ok")
	if j.Status != model.JobStatusSucceeded || j.Result != "done" {
		t.Fatalf("after complete: %+v", j)
	}

	// retry with backoff
	mustEnqueue(t, s, "retry", now, 5)
	claimed, err = s.ClaimJobs(ctx, 10, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim retry: %v %d", err, len(claimed))
	}
	nextRun := now.Add(2 * time.Minute)
	if err := s.RetryJob(ctx, claimed[0].ID, 1, "boom", nextRun); err != nil {
		t.Fatal(err)
	}
	j = findJob(t, s, "t1", "retry")
	if j.Status != model.JobStatusQueued || j.Attempts != 1 || j.LastError != "boom" {
		t.Fatalf("after retry: %+v", j)
	}
	// Not due yet -> cannot be claimed.
	if jobs, _ := s.ClaimJobs(ctx, 10, now.Add(time.Minute)); len(jobs) != 0 {
		t.Fatalf("retried job must respect run_after backoff, got %+v", jobs)
	}
	// Due after backoff -> claimable again.
	if jobs, err := s.ClaimJobs(ctx, 10, now.Add(3*time.Minute)); err != nil || len(jobs) != 1 {
		t.Fatalf("retried job should be claimable after backoff: %v %d", err, len(jobs))
	}

	// fail after max retries
	mustEnqueue(t, s, "fail", now, 2)
	claimed, err = s.ClaimJobs(ctx, 10, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim fail: %v %d", err, len(claimed))
	}
	if err := s.FailJob(ctx, claimed[0].ID, 2, "permanent"); err != nil {
		t.Fatal(err)
	}
	j = findJob(t, s, "t1", "fail")
	if j.Status != model.JobStatusFailed || j.Attempts != 2 || j.LastError != "permanent" {
		t.Fatalf("after fail: %+v", j)
	}
	// A completed/failed job cannot be re-claimed.
	if jobs, _ := s.ClaimJobs(ctx, 10, now.Add(time.Hour)); len(jobs) != 0 {
		t.Fatalf("terminal jobs must not be claimed, got %+v", jobs)
	}
}

func TestRequeueStaleRunningJobs(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustEnqueue(t, s, "stale", now, 3)
	mustEnqueue(t, s, "fresh", now, 3)

	claimed, err := s.ClaimJobs(ctx, 10, now)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim: %v %d", err, len(claimed))
	}
	// Backdate the stale job's heartbeat; keep the fresh one current.
	if err := s.WithContext(ctx).Model(&model.Job{}).
		Where("key = ?", "stale").
		Update("last_heartbeat", now.Add(-2*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	n, err := s.RequeueStaleJobs(ctx, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 stale job requeued, got %d", n)
	}
	stale := findJob(t, s, "t1", "stale")
	if stale.Status != model.JobStatusQueued {
		t.Fatalf("stale job must be requeued, got %s", stale.Status)
	}
	fresh := findJob(t, s, "t1", "fresh")
	if fresh.Status != model.JobStatusRunning {
		t.Fatalf("fresh job must stay running, got %s", fresh.Status)
	}
}

func TestListJobsTenantScopedWithFilters(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustEnqueue(t, s, "t1-k", now, 1)
	if _, err := s.EnqueueJob(ctx, &model.Job{Kind: "other", Key: "t1-other", TenantID: "t1", RunAfter: now}); err != nil {
		t.Fatal(err)
	}
	inserted, err := s.EnqueueJob(ctx, &model.Job{Kind: model.JobKindDocumentSync, Key: "t2-k", TenantID: "t2", RunAfter: now})
	if err != nil || !inserted {
		t.Fatalf("enqueue t2: %v %v", inserted, err)
	}

	// Tenant isolation: tenant t1 must not see t2's job.
	jobs, total, err := s.ListJobs(ctx, "t1", "", "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(jobs) != 2 {
		t.Fatalf("tenant t1 should see 2 jobs, got %d/%d", len(jobs), total)
	}
	for i := range jobs {
		if jobs[i].TenantID != "t1" {
			t.Fatalf("tenant leak: found job of tenant %s", jobs[i].TenantID)
		}
	}

	// Kind filter.
	jobs, total, err = s.ListJobs(ctx, "t1", model.JobKindDocumentSync, "", 1, 20)
	if err != nil || total != 1 || jobs[0].Key != "t1-k" {
		t.Fatalf("kind filter: %d %+v err=%v", total, jobs, err)
	}
	// Status filter.
	jobs, total, err = s.ListJobs(ctx, "t1", "", model.JobStatusQueued, 1, 20)
	if err != nil || total != 2 || len(jobs) != 2 {
		t.Fatalf("status filter: %d/%d err=%v", len(jobs), total, err)
	}
}

func TestCountJobsByStatus(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustEnqueue(t, s, "a", now, 1)
	mustEnqueue(t, s, "b", now, 1)
	claimed, err := s.ClaimJobs(ctx, 10, now)
	if err != nil || len(claimed) != 2 {
		t.Fatal(err)
	}
	if err := s.CompleteJob(ctx, claimed[0].ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.FailJob(ctx, claimed[1].ID, 1, "x"); err != nil {
		t.Fatal(err)
	}
	counts, err := s.CountJobsByStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[model.JobStatusSucceeded] != 1 || counts[model.JobStatusFailed] != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
}
