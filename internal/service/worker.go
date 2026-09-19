package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Worker processes claims of a single job kind (doc/21 §3). Register one per
// kind before the runner starts; an unregistered kind fails the job so a
// missing consumer is visible instead of silently dropped.
type Worker interface {
	// Kind returns the job kind this worker consumes.
	Kind() string
	// Run executes one claimed job. Any returned error is retried with
	// exponential backoff up to MaxRetry, then the job is failed.
	Run(ctx context.Context, job *model.Job) error
}

// WorkerConfig tunes the in-process runner (doc/21 §3: process-internal queue
// entry + durable DB job table; Redis queue is the multi-replica upgrade path).
type WorkerConfig struct {
	// PollInterval is how often due jobs are claimed.
	PollInterval time.Duration
	// BatchSize bounds how many jobs are claimed per poll.
	BatchSize int
	// HeartbeatInterval refreshes running jobs' lease timestamps.
	HeartbeatInterval time.Duration
	// HeartbeatTimeout marks a running job stale (crash recovery) after this
	// much inactivity.
	HeartbeatTimeout time.Duration
	// RequeueInterval is how often stale running jobs are recovered.
	RequeueInterval time.Duration
	// JobTimeout bounds a single job execution.
	JobTimeout time.Duration
	// BaseBackoff is the first retry delay; later retries double it.
	BaseBackoff time.Duration
	// MaxBackoff caps the exponential backoff.
	MaxBackoff time.Duration
}

// DefaultWorkerConfig returns sensible in-process defaults.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval:      1 * time.Second,
		BatchSize:         10,
		HeartbeatInterval: 10 * time.Second,
		HeartbeatTimeout:  5 * time.Minute,
		RequeueInterval:   1 * time.Minute,
		JobTimeout:        10 * time.Minute,
		BaseBackoff:       2 * time.Second,
		MaxBackoff:        5 * time.Minute,
	}
}

// Runner is the in-process async worker: it claims due jobs from the durable
// rgx_job table and dispatches them to registered Workers, then reconciles the
// outcome (succeed / retry with backoff / fail) exactly once per claim.
type Runner struct {
	store   repository.Store
	cfg     WorkerConfig
	workers map[string]Worker

	mu       sync.Mutex
	running  map[string]struct{} // job IDs claimed but not yet settled (for Stop)
	inflight sync.WaitGroup
	started  bool
	stopCh   chan struct{}
	stopped  chan struct{}
}

// NewRunner builds a runner over the given store.
func NewRunner(store repository.Store, cfg WorkerConfig) *Runner {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.HeartbeatTimeout <= 0 {
		cfg.HeartbeatTimeout = 5 * time.Minute
	}
	if cfg.RequeueInterval <= 0 {
		cfg.RequeueInterval = time.Minute
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 10 * time.Minute
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 2 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Minute
	}
	return &Runner{
		store:   store,
		cfg:     cfg,
		workers: map[string]Worker{},
		running: map[string]struct{}{},
	}
}

// Register binds a Worker to its kind. It returns false when the kind already
// has a worker, so a duplicate registration is detected instead of overwritten.
func (r *Runner) Register(w Worker) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.workers[w.Kind()]; ok {
		return false
	}
	r.workers[w.Kind()] = w
	return true
}

// Enqueue persists a durable job idempotently by key. Duplicate keys are a
// no-op (returns inserted=false). runAfter == zero means "as soon as possible".
func (r *Runner) Enqueue(ctx context.Context, kind, key, tenantID, payload string, runAfter time.Time, maxRetry int) (bool, error) {
	inserted, err := r.store.EnqueueJob(ctx, &model.Job{
		Kind: kind, Key: key, TenantID: tenantID, Payload: payload,
		RunAfter: runAfter, MaxRetry: maxRetry, Status: model.JobStatusQueued,
	})
	if err != nil {
		return false, err
	}
	if inserted {
		obs.Get().IncJobEnqueued(kind)
	}
	return inserted, nil
}

// Start launches the claim and crash-recovery loops. Start is idempotent.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.stopCh = make(chan struct{})
	r.stopped = make(chan struct{})
	r.mu.Unlock()

	go r.pollLoop(ctx)
	go r.requeueLoop(ctx)
	logger.Info("async worker started", "poll_interval", r.cfg.PollInterval.String())
}

// Stop gracefully shuts the runner down: it stops claiming new jobs and waits
// for in-flight jobs to settle, bounding the wait by the passed context. A
// cancelled context forces a return while in-flight jobs finish in the
// background (rgx_job heartbeats cover crash recovery on restart).
func (r *Runner) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	r.started = false
	close(r.stopCh)
	r.mu.Unlock()

	select {
	case <-r.stopped:
	case <-ctx.Done():
		return nil
	}

	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("async worker stopped")
		return nil
	case <-ctx.Done():
		return nil
	}
}

func (r *Runner) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			r.closeStopped()
			return
		case <-ctx.Done():
			r.closeStopped()
			return
		case <-ticker.C:
			r.pollOnce(ctx)
		}
	}
}

func (r *Runner) requeueLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RequeueInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			cutoff := now.Add(-r.cfg.HeartbeatTimeout)
			n, err := r.store.RequeueStaleJobs(ctx, cutoff, now)
			if err != nil {
				logger.Warn("worker requeue stale failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Warn("worker recovered stale running jobs", "count", n)
			}
			r.refreshQueueMetrics(ctx)
		}
	}
}

// closeStopped signals Stop that both loops have exited.
func (r *Runner) closeStopped() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped != nil {
		close(r.stopped)
		r.stopped = nil
	}
}

func (r *Runner) pollOnce(ctx context.Context) {
	now := time.Now().UTC()
	jobs, err := r.store.ClaimJobs(ctx, r.cfg.BatchSize, now)
	if err != nil {
		logger.Warn("worker claim failed", "error", err)
		return
	}
	for i := range jobs {
		j := &jobs[i]
		if err := r.ensureJobTenantActive(ctx, j); err != nil {
			if failErr := r.store.FailJob(context.Background(), j.ID, j.Attempts+1, err.Error()); failErr != nil {
				logger.Warn("worker fail-disabled-tenant job", "job_id", j.ID, "error", failErr)
			}
			r.observe(j.Kind, model.JobStatusFailed)
			continue
		}
		worker := r.workers[j.Kind]
		if worker == nil {
			// No consumer registered: fail fast so the missing consumer is
			// visible on the observation endpoint and in metrics.
			if err := r.store.FailJob(context.Background(), j.ID, j.Attempts+1, "no worker registered for kind "+j.Kind); err != nil {
				logger.Warn("worker fail-unregistered job", "job_id", j.ID, "error", err)
			}
			r.observe(j.Kind, model.JobStatusFailed)
			continue
		}
		r.mu.Lock()
		r.running[j.ID] = struct{}{}
		r.mu.Unlock()
		r.inflight.Add(1)
		go r.runClaim(ctx, worker, j)
	}
}

func (r *Runner) ensureJobTenantActive(ctx context.Context, j *model.Job) error {
	tenant, err := r.store.GetTenant(ctx, j.TenantID)
	if err != nil {
		return fmt.Errorf("workspace lifecycle check failed: %w", err)
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return fmt.Errorf("workspace is disabled or missing")
	}
	return nil
}

// runClaim executes one claimed job, refreshing its heartbeat lease while it
// runs, then reconciles success/retry/failure exactly once.
func (r *Runner) runClaim(parent context.Context, w Worker, j *model.Job) {
	defer r.inflight.Done()
	defer func() {
		r.mu.Lock()
		delete(r.running, j.ID)
		r.mu.Unlock()
	}()
	start := time.Now()

	runCtx, cancel := context.WithTimeout(parent, r.cfg.JobTimeout)
	defer cancel()

	hbCtx, hbCancel := context.WithCancel(parent)
	defer hbCancel()
	if r.cfg.HeartbeatInterval > 0 {
		go r.heartbeatLoop(hbCtx, j.ID)
	}

	err := w.Run(runCtx, j)
	r.settle(j, err, start)
}

func (r *Runner) heartbeatLoop(ctx context.Context, jobID string) {
	ticker := time.NewTicker(r.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.store.HeartbeatJob(context.Background(), jobID, time.Now().UTC()); err != nil {
				logger.Warn("worker heartbeat failed", "job_id", jobID, "error", err)
			}
		}
	}
}

// settle reconciles a finished claim: success -> complete, transient error ->
// retry with backoff, exhausted retries -> fail + alert + metrics. It uses a
// detached context so the outcome is persisted even when the runner context is
// cancelled during shutdown.
func (r *Runner) settle(j *model.Job, runErr error, start time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	attempts := j.Attempts + 1
	if runErr == nil {
		if err := r.store.CompleteJob(ctx, j.ID, attempts, j.Result); err != nil {
			logger.Warn("worker complete failed", "job_id", j.ID, "error", err)
		}
		r.observe(j.Kind, model.JobStatusSucceeded)
		logger.Info("worker job succeeded", "job_id", j.ID, "kind", j.Kind, "attempts", attempts,
			"duration_ms", time.Since(start).Milliseconds())
		return
	}

	backoff := r.backoff(attempts)
	if attempts < j.MaxRetry {
		nextRun := time.Now().UTC().Add(backoff)
		if err := r.store.RetryJob(ctx, j.ID, attempts, runErr.Error(), nextRun); err != nil {
			logger.Warn("worker retry failed", "job_id", j.ID, "error", err)
		}
		r.observe(j.Kind, model.JobStatusQueued)
		logger.Warn("worker job scheduled for retry", "job_id", j.ID, "kind", j.Kind, "attempts", attempts,
			"delay_ms", backoff.Milliseconds(), "error", runErr)
		return
	}

	if err := r.store.FailJob(ctx, j.ID, attempts, runErr.Error()); err != nil {
		logger.Warn("worker fail failed", "job_id", j.ID, "error", err)
	}
	r.observe(j.Kind, model.JobStatusFailed)
	notify.Emit(context.Background(), notify.Event{
		Title: "async job failed", Severity: "error",
		TenantID: j.TenantID, Resource: "job", Type: "job_failed", ResourceID: j.ID,
		Detail: fmt.Sprintf("kind=%s attempts=%d error=%s", j.Kind, attempts, runErr),
	})
	logger.Error("worker job failed after retries", "job_id", j.ID, "kind", j.Kind, "attempts", attempts, "error", runErr)
}

// backoff returns the exponential delay for a given attempt number (1-based).
func (r *Runner) backoff(attempt int) time.Duration {
	d := r.cfg.BaseBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= r.cfg.MaxBackoff {
			return r.cfg.MaxBackoff
		}
	}
	if d > r.cfg.MaxBackoff {
		return r.cfg.MaxBackoff
	}
	return d
}

func (r *Runner) observe(kind, status string) {
	obs.Get().IncJobProcessed(kind, status)
}

// refreshQueueMetrics publishes queue-depth and failed counts to the metrics
// registry (best-effort, throttled by the requeue loop).
func (r *Runner) refreshQueueMetrics(ctx context.Context) {
	counts, err := r.store.CountJobsByStatus(ctx)
	if err != nil {
		logger.Warn("worker job stats failed", "error", err)
		return
	}
	depth := counts[model.JobStatusQueued] + counts[model.JobStatusRunning]
	obs.Get().SetJobQueueDepth(depth)
	obs.Get().SetJobFailed(counts[model.JobStatusFailed])
}

// documentSyncWorker is the first worker consumer (doc/33 A2): it polls
// RAGFlow's real document parsing state and projects it onto the operator
// queue. Its Kind is bound to EnqueueDocumentSync.
type documentSyncWorker struct {
	svc *Service
}

// Kind implements Worker.
func (w *documentSyncWorker) Kind() string { return model.JobKindDocumentSync }

// Run implements Worker.
func (w *documentSyncWorker) Run(ctx context.Context, job *model.Job) error {
	return w.svc.SyncTaskProgress(ctx, job.TenantID)
}

type resourceReconciliationWorker struct {
	svc *Service
}

func (w *resourceReconciliationWorker) Kind() string {
	return model.JobKindResourceReconciliation
}

func (w *resourceReconciliationWorker) Run(ctx context.Context, job *model.Job) error {
	return w.svc.ReconcileExternalResources(ctx, job.TenantID)
}

type alertDeliveryCompensationWorker struct {
	svc *Service
}

func (w *alertDeliveryCompensationWorker) Kind() string {
	return model.JobKindAlertDeliveryCompensation
}

func (w *alertDeliveryCompensationWorker) Run(ctx context.Context, job *model.Job) error {
	summary, err := w.svc.RetryDueAlertDeliveries(ctx, 20)
	if err != nil {
		logger.Warn("alert delivery compensation failed", "job_id", job.ID, "error", err)
		return err
	}
	obs.Get().AddAlertDeliveryCompensation(int64(summary.Scanned), int64(summary.Compensated), int64(summary.Skipped),
		int64(summary.Abandoned), int64(summary.Fenced))
	if _, err := w.svc.ScheduleAlertDeliveryCompensation(ctx, job.ID); err != nil {
		logger.Warn("alert delivery compensation reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	logger.Info("alert delivery compensation completed", "scanned", summary.Scanned, "compensated", summary.Compensated,
		"skipped", summary.Skipped, "abandoned", summary.Abandoned, "fenced", summary.Fenced)
	return nil
}

// SetupWorker builds the in-process runner for this Service, registers the
// document-sync consumer (the first A2 worker) and stores it on s.Runner for
// lifecycle management by the caller.
func (s *Service) SetupWorker(cfg WorkerConfig) *Runner {
	runner := NewRunner(s.Store, cfg)
	runner.Register(&documentSyncWorker{svc: s})
	runner.Register(&resourceReconciliationWorker{svc: s})
	runner.Register(&ragflowImportWorker{svc: s})
	runner.Register(&ragflowReconcileWorker{svc: s})
	runner.Register(&alertDeliveryCompensationWorker{svc: s})
	s.SetupApprovalWorker(runner)
	s.Runner = runner
	return runner
}

// EnqueueDocumentSync schedules a document parsing progress-sync job scoped to
// tenantID. It is a no-op when such a job is already queued/running for the
// tenant, so recurring triggers (the task poller and the manual sync endpoint)
// cannot pile up jobs.
func (s *Service) EnqueueDocumentSync(ctx context.Context, tenantID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobs(ctx, model.JobKindDocumentSync, tenantID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	key := "document_sync:" + tenantID + ":" + time.Now().UTC().Format("2006-01-02T15:04")
	return s.Runner.Enqueue(ctx, model.JobKindDocumentSync, key, tenantID, "", time.Time{}, 0)
}

// ScheduleAlertDeliveryCompensation arms the platform-wide recurring scan for
// persisted pending/failed webhook deliveries. It never keeps two active jobs.
func (s *Service) ScheduleAlertDeliveryCompensation(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	interval := time.Duration(s.CurrentAlertingConfig().CompensationIntervalSec) * time.Second
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindAlertDeliveryCompensation, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	key := "alert_delivery_compensation:" + time.Now().UTC().Format("20060102T150405Z")
	return s.Runner.Enqueue(ctx, model.JobKindAlertDeliveryCompensation, key, SystemTenantID, "", time.Now().UTC().Add(interval), 0)
}

// ListJobs returns a page of a tenant's async job execution records.
func (s *Service) ListJobs(ctx context.Context, tenantID, kind, status string, page, pageSize int) ([]model.Job, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListJobs(ctx, tenantID, kind, status, page, pageSize)
}
