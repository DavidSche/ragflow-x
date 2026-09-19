// Data retention for the async worker janitor (A5, doc/33 Sprint P1-2).
package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// retentionKeyPrefix is the durable recurring-job key namespace of the
// data-retention janitor. Every run schedules the next one with a timestamped
// key so the durable rgx_job ledger keeps at most one active janitor while
// still surviving restarts (the queued next-run is claimed after a crash).
const retentionKeyPrefix = "data_retention:main:"

// defaultRetentionInterval is the cadence used when Retention.IntervalSec <= 0.
const defaultRetentionInterval = 24 * time.Hour

// RetentionSummary reports what one purge cycle removed, grouped per tenant
// across the retention classes. It is returned for logging and tests alike.
type RetentionSummary struct {
	Enabled  bool             `json:"enabled"`
	Audit    map[string]int64 `json:"audit"`    // tenant -> deleted audit rows
	Usage    map[string]int64 `json:"usage"`    // cost_metric+quota_usage+meter_request
	Feedback map[string]int64 `json:"feedback"` // message_feedback
	Job      map[string]int64 `json:"job"`      // terminal job/task ledgers
}

// SetRetentionPolicy stores the A5 retention policy (enabled + per-class TTLs).
func (s *Service) SetRetentionPolicy(cfg config.Retention) {
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	s.retentionPolicy = cfg
}

// currentRetentionPolicy returns the last policy set by SetRetentionPolicy.
func (s *Service) currentRetentionPolicy() config.Retention {
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	return s.retentionPolicy
}

// retentionInterval resolves the janitor cadence, falling back to 24h.
func (s *Service) retentionInterval(p config.Retention) time.Duration {
	if p.IntervalSec > 0 {
		return time.Duration(p.IntervalSec) * time.Second
	}
	return defaultRetentionInterval
}

// SetupRetention stores the policy and registers the data-retention worker on
// the async runner so data_retention jobs can be consumed. When enabled the
// caller should ScheduleRetention next to arm the recurring cycle.
func (s *Service) SetupRetention(cfg config.Retention) {
	s.SetRetentionPolicy(cfg)
	if s.Runner != nil {
		s.Runner.Register(&retentionWorker{svc: s})
	}
}

// ScheduleRetention arms (or re-arms) the recurring data-retention janitor: it
// enqueues one queued data_retention job due one interval out. It is a no-op
// when retention is disabled or a janitor job (other than excludeID) is already
// queued/running, so repeated calls (startup, a finished run) never pile up
// duplicates. excludeID lets a finishing worker exclude its own (still running)
// claim so it can re-arm the next cycle. The job has no tenant scope because the
// purge is platform-wide.
func (s *Service) ScheduleRetention(ctx context.Context, excludeID string) (bool, error) {
	p := s.currentRetentionPolicy()
	if !p.Enabled {
		return false, nil
	}
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindDataRetention, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	key := retentionKeyPrefix + time.Now().UTC().Format("20060102T150405Z")
	runAfter := time.Now().UTC().Add(s.retentionInterval(p))
	// The janitor is a platform-wide job: it is owned by the reserved system
	// tenant so platform admins can observe it on GET /tasks/jobs.
	return s.Runner.Enqueue(ctx, model.JobKindDataRetention, key, SystemTenantID, "", runAfter, 0)
}

// retentionWorker is the A5 consumer registered on the async runner. It runs
// one bounded purge then self-schedules the next run through the durable job
// table (crash-safe recurring execution, observable on GET /tasks/jobs).
type retentionWorker struct {
	svc *Service
}

// Kind implements Worker.
func (w *retentionWorker) Kind() string { return model.JobKindDataRetention }

// Run implements Worker: purge + re-arm. Any error is retried by the runner
// with exponential backoff; the purge is idempotent so a retry is safe.
func (w *retentionWorker) Run(ctx context.Context, job *model.Job) error {
	summary, err := w.svc.RunRetention(ctx)
	if err != nil {
		logger.Warn("data retention purge failed", "job_id", job.ID, "error", err)
		return err
	}
	if summary != nil && summary.Enabled {
		// Exclude this claim (still marked running until the runner settles it)
		// so re-arming is not blocked by the job we are finishing.
		if _, err := w.svc.ScheduleRetention(ctx, job.ID); err != nil {
			logger.Warn("data retention reschedule failed", "job_id", job.ID, "error", err)
			return err
		}
	}
	return nil
}

// RunRetention executes one bounded TTL purge across the configured classes.
// Deletion is off unless Retention.Enabled; each class only runs when its day
// count is positive. The audit purge re-anchors affected chains and, after all
// classes finish, one "retention.purge" audit record with the deleted counts is
// appended per affected tenant ("有删除量审计", doc/33 A5 验收).
func (s *Service) RunRetention(ctx context.Context) (*RetentionSummary, error) {
	p := s.currentRetentionPolicy()
	if err := s.convergeResourceSyncArtifactLifecycles(ctx); err != nil {
		return nil, err
	}
	if !p.Enabled {
		logger.Info("data retention disabled; purge skipped")
		return &RetentionSummary{Enabled: false}, nil
	}
	now := time.Now().UTC()
	summary := &RetentionSummary{
		Enabled: true, Audit: map[string]int64{}, Usage: map[string]int64{},
		Feedback: map[string]int64{}, Job: map[string]int64{},
	}
	// perTenant accumulates class -> deleted count for the audit record.
	perTenant := map[string]map[string]int64{}
	accumulate := func(counts map[string]int64, class string) {
		for tenantID, n := range counts {
			if n <= 0 {
				continue
			}
			m := perTenant[tenantID]
			if m == nil {
				m = map[string]int64{}
				perTenant[tenantID] = m
			}
			m[class] += n
		}
	}
	obs.Get().IncRetentionRun()

	// 1) Audit ledger: purge + re-anchor so the retained chain stays verifiable.
	if p.AuditDays > 0 {
		cutoff := now.AddDate(0, 0, -p.AuditDays)
		deleted, err := s.Store.PurgeAuditBefore(ctx, cutoff)
		if err != nil {
			return summary, err
		}
		summary.Audit = deleted
		tenantIDs := make([]string, 0, len(deleted))
		for tenantID, n := range deleted {
			tenantIDs = append(tenantIDs, tenantID)
			accumulate(map[string]int64{tenantID: n}, "audit")
			obs.Get().IncRetentionPurged("audit", n)
		}
		if err := s.Store.ReanchorAuditChains(ctx, tenantIDs); err != nil {
			return summary, err
		}
	}

	// 2) Usage detail + aggregates + idempotency ledger.
	if p.UsageDays > 0 {
		cutoff := now.AddDate(0, 0, -p.UsageDays)
		for _, purge := range []struct {
			fn func(context.Context, time.Time) (map[string]int64, error)
		}{
			{s.Store.PurgeCostMetricBefore},
			{s.Store.PurgeQuotaUsageBefore},
			{s.Store.PurgeMeterRequestBefore},
			{s.Store.PurgeGatewayIdempotencyBefore},
		} {
			deleted, err := purge.fn(ctx, cutoff)
			if err != nil {
				return summary, err
			}
			for tenantID, n := range deleted {
				summary.Usage[tenantID] += n
				obs.Get().IncRetentionPurged("usage", n)
			}
			accumulate(deleted, "usage")
		}
	}

	// 3) Message feedback (消息/反馈).
	if p.FeedbackDays > 0 {
		cutoff := now.AddDate(0, 0, -p.FeedbackDays)
		deleted, err := s.Store.PurgeMessageFeedbackBefore(ctx, cutoff)
		if err != nil {
			return summary, err
		}
		summary.Feedback = deleted
		for _, n := range deleted {
			obs.Get().IncRetentionPurged("feedback", n)
		}
		accumulate(deleted, "feedback")
	}

	// 4) Async job + task terminal ledgers (keeps the A2 queue bounded).
	if p.JobDays > 0 {
		cutoff := now.AddDate(0, 0, -p.JobDays)
		for _, purge := range []struct {
			fn func(context.Context, time.Time) (map[string]int64, error)
		}{
			{s.Store.PurgeTerminalJobsBefore},
			{s.Store.PurgeTerminalTasksBefore},
		} {
			deleted, err := purge.fn(ctx, cutoff)
			if err != nil {
				return summary, err
			}
			for tenantID, n := range deleted {
				summary.Job[tenantID] += n
				obs.Get().IncRetentionPurged("job", n)
			}
			accumulate(deleted, "job")
		}
	}

	// 5) Audit the purge itself: one record per affected tenant, appended after
	// the (possibly re-anchored) tail so chains stay verifiable.
	if len(perTenant) > 0 {
		for tenantID, counts := range perTenant {
			detail, err := json.Marshal(counts)
			if err != nil {
				return summary, err
			}
			if err := s.RecordAudit(ctx, &model.AuditLog{
				TenantID:   tenantID,
				Action:     "retention.purge",
				Resource:   "retention",
				ResourceID: "ttl",
				DetailJSON: string(detail),
			}); err != nil {
				logger.Warn("data retention audit write failed", "tenant_id", tenantID, "error", err)
			}
		}
	}

	logger.Info("data retention purge complete",
		"audit", len(summary.Audit), "usage", len(summary.Usage),
		"feedback", len(summary.Feedback), "job", len(summary.Job))
	return summary, nil
}
