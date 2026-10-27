package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

var errFakeWorker = errors.New("fake worker error")

// fakeWorker is a controllable in-proc worker for runner tests.
type fakeWorker struct {
	kind           string
	alwaysFail     bool
	remainingFails int32 // fail this many times, then succeed
	Runs           int32
}

func (w *fakeWorker) Kind() string { return w.kind }

func (w *fakeWorker) Run(ctx context.Context, job *model.Job) error {
	atomic.AddInt32(&w.Runs, 1)
	if w.alwaysFail {
		return errFakeWorker
	}
	if atomic.AddInt32(&w.remainingFails, -1) >= 0 {
		return errFakeWorker
	}
	return nil
}

// fastWorkerConfig returns a runner config with aggressive intervals so tests
// settle quickly without waiting on the production defaults.
func fastWorkerConfig() WorkerConfig {
	cfg := DefaultWorkerConfig()
	cfg.PollInterval = 5 * time.Millisecond
	cfg.HeartbeatInterval = 2 * time.Millisecond
	cfg.HeartbeatTimeout = 50 * time.Millisecond
	cfg.RequeueInterval = 10 * time.Millisecond
	cfg.JobTimeout = 5 * time.Second
	cfg.BaseBackoff = 2 * time.Millisecond
	cfg.MaxBackoff = 10 * time.Millisecond
	return cfg
}

// waitJobStatus polls the store until a job of kind reaches want, returning it.
func waitJobStatus(t *testing.T, store repository.Store, tenant, kind, want string, timeout time.Duration) *model.Job {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		jobs, _, err := store.ListJobs(ctx, tenant, kind, "", 1, 50)
		if err != nil {
			t.Fatal(err)
		}
		for i := range jobs {
			if jobs[i].Status == want {
				return &jobs[i]
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s job reached status %s within %s", kind, want, timeout)
	return nil
}

func newRunnerSvc(t *testing.T) *Service {
	t.Helper()
	svc := newAuthzSvc(t)
	return svc
}

func TestRunnerEnqueueAndExecuteSuccess(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	w := &fakeWorker{kind: "test-ok", remainingFails: 0}
	runner := NewRunner(svc.Store, fastWorkerConfig())
	if !runner.Register(w) {
		t.Fatal("register should succeed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	defer func() { _ = runner.Stop(context.Background()) }()

	inserted, err := runner.Enqueue(ctx, "test-ok", "k1", tenantID, "", time.Time{}, 1)
	if err != nil || !inserted {
		t.Fatalf("enqueue: inserted=%v err=%v", inserted, err)
	}
	job := waitJobStatus(t, svc.Store, tenantID, "test-ok", model.JobStatusSucceeded, 3*time.Second)
	if job.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", job.Attempts)
	}
	if atomic.LoadInt32(&w.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", w.Runs)
	}
}

func TestRunnerRetriesThenSucceeds(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	w := &fakeWorker{kind: "test-retry", remainingFails: 1}
	runner := NewRunner(svc.Store, fastWorkerConfig())
	runner.Register(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	defer func() { _ = runner.Stop(context.Background()) }()

	if _, err := runner.Enqueue(ctx, "test-retry", "k2", tenantID, "{}", time.Time{}, 3); err != nil {
		t.Fatal(err)
	}
	job := waitJobStatus(t, svc.Store, tenantID, "test-retry", model.JobStatusSucceeded, 3*time.Second)
	if job.Attempts != 2 {
		t.Fatalf("expected 2 attempts (1 failure + 1 success), got %d", job.Attempts)
	}
	if job.LastError != "" {
		t.Fatalf("last_error must be cleared on success, got %q", job.LastError)
	}
	if atomic.LoadInt32(&w.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", w.Runs)
	}
}

func TestRunnerFailsAfterMaxRetries(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	w := &fakeWorker{kind: "test-fail", alwaysFail: true}
	runner := NewRunner(svc.Store, fastWorkerConfig())
	runner.Register(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	defer func() { _ = runner.Stop(context.Background()) }()

	if _, err := runner.Enqueue(ctx, "test-fail", "k3", tenantID, "", time.Time{}, 2); err != nil {
		t.Fatal(err)
	}
	job := waitJobStatus(t, svc.Store, tenantID, "test-fail", model.JobStatusFailed, 3*time.Second)
	if job.Attempts != 2 {
		t.Fatalf("expected attempts == max_retry (2), got %d", job.Attempts)
	}
	if job.LastError == "" {
		t.Fatal("failed job must record last_error")
	}
	if atomic.LoadInt32(&w.Runs) != 2 {
		t.Fatalf("expected 2 runs total, got %d", w.Runs)
	}
}

func TestRunnerStopStopsClaiming(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	w := &fakeWorker{kind: "test-stop", remainingFails: 0}
	runner := NewRunner(svc.Store, fastWorkerConfig())
	runner.Register(w)
	ctx := context.Background()
	runner.Start(ctx)
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.Stop(stopCtx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Stop must be idempotent.
	if err := runner.Stop(stopCtx); err != nil {
		t.Fatalf("double stop: %v", err)
	}
	// Jobs enqueued after stop must not be executed.
	if _, err := runner.Enqueue(context.Background(), "test-stop", "k4", tenantID, "", time.Time{}, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt32(&w.Runs); n != 0 {
		t.Fatalf("stopped runner must not execute jobs, runs=%d", n)
	}
}

func TestRunnerUnknownKindFails(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	// No worker registered for kind "ghost".
	runner := NewRunner(svc.Store, fastWorkerConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	defer func() { _ = runner.Stop(context.Background()) }()
	if _, err := runner.Enqueue(ctx, "ghost", "k5", tenantID, "", time.Time{}, 1); err != nil {
		t.Fatal(err)
	}
	job := waitJobStatus(t, svc.Store, tenantID, "ghost", model.JobStatusFailed, 3*time.Second)
	if job.LastError == "" {
		t.Fatal("unknown-kind job must record a failure reason")
	}
}

func TestRunnerRejectsDisabledTenantJob(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	w := &fakeWorker{kind: "test-disabled", remainingFails: 0}
	runner := NewRunner(svc.Store, fastWorkerConfig())
	runner.Register(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	if err := svc.BatchUpdateTenantStatus(ctx, admin.ID, []string{tenantID}, model.TenantStatusDisabled); err != nil {
		t.Fatal(err)
	}
	runner.Start(ctx)
	defer func() { _ = runner.Stop(context.Background()) }()
	if _, err := runner.Enqueue(ctx, "test-disabled", "disabled-1", tenantID, "", time.Time{}, 1); err != nil {
		t.Fatal(err)
	}
	job := waitJobStatus(t, svc.Store, tenantID, "test-disabled", model.JobStatusFailed, 3*time.Second)
	if job.LastError != "workspace is disabled or missing" {
		t.Fatalf("disabled workspace job must fail closed, got %+v", job)
	}
	if runs := atomic.LoadInt32(&w.Runs); runs != 0 {
		t.Fatalf("disabled workspace job must not execute, runs=%d", runs)
	}
}

func TestRunnerRequeuesStaleRunningJob(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	// No worker -> we deliberately leave the job claimed and backdate it to
	// simulate a crashed process, then let the requeue loop recover it.
	runner := NewRunner(svc.Store, fastWorkerConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inserted, err := runner.Enqueue(ctx, "zombie", "k6", tenantID, "", time.Time{}, 1)
	if err != nil || !inserted {
		t.Fatalf("enqueue: %v %v", inserted, err)
	}
	// First claim (simulated by a direct claim) -> running with crash-stale
	// heartbeat after backdate.
	for {
		jobs, err := svc.Store.ClaimJobs(ctx, 10, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) > 0 {
			if err := svc.Store.HeartbeatJob(ctx, jobs[0].ID, time.Now().UTC().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	runner.Start(ctx)
	defer func() { _ = runner.Stop(context.Background()) }()
	// The requeue loop must recover it into a claimable queued job, which then
	// fails fast for the missing kind and reaches failed.
	job := waitJobStatus(t, svc.Store, tenantID, "zombie", model.JobStatusFailed, 3*time.Second)
	if job.Status != model.JobStatusFailed {
		t.Fatalf("zombie job should end failed after requeue, got %+v", job)
	}
}

func TestEnqueueDocumentSyncDedup(t *testing.T) {
	svc := newRunnerSvc(t)
	tenantID := newRunnerTenant(t, svc)
	svc.SetupWorker(fastWorkerConfig())
	ctx := context.Background()
	first, err := svc.EnqueueDocumentSync(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("first document sync enqueue should insert")
	}
	// A second enqueue while one is queued/running must be a no-op.
	second, err := svc.EnqueueDocumentSync(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("second document sync enqueue should be de-duplicated")
	}
	n, err := svc.Store.CountActiveJobs(ctx, model.JobKindDocumentSync, tenantID)
	if err != nil || n != 1 {
		t.Fatalf("expected 1 active document sync job, got %d err=%v", n, err)
	}
}

func newRunnerTenant(t *testing.T, svc *Service) string {
	t.Helper()
	tenant, err := svc.CreateTenant(context.Background(), "Runner Workspace")
	if err != nil {
		t.Fatal(err)
	}
	return tenant.ID
}
