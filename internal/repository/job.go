package repository

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// defaultJobMaxRetry is applied when a job is enqueued without an explicit
// max_retry (doc/21 §3: retries with exponential backoff).
const defaultJobMaxRetry = 3

// JobRepo persists durable async jobs (rgx_job). Enqueue is idempotent by the
// unique job Key; Claim is an atomic queued -> running transition so the same
// job can never be executed twice; retries/failures are written exactly once
// per attempt; RequeueStaleJobs recovers running jobs orphaned by a crash.
type JobRepo interface {
	// EnqueueJob inserts a job, returning false (no-op) when a job with the
	// same unique Key already exists so duplicate triggers never double-enqueue.
	EnqueueJob(ctx context.Context, j *model.Job) (bool, error)
	// ClaimJobs atomically transitions up to limit due queued jobs to running
	// and returns the claimed rows for dispatch.
	ClaimJobs(ctx context.Context, limit int, now time.Time) ([]model.Job, error)
	// HeartbeatJob refreshes a running job's lease timestamp.
	HeartbeatJob(ctx context.Context, id string, now time.Time) error
	// CompleteJob marks a claimed job succeeded (exactly once), recording the
	// attempt count of the successful run.
	CompleteJob(ctx context.Context, id string, attempts int, result string) error
	// RetryJob re-queues a claimed job with a bumped attempt count and
	// delayed run_after (exponential backoff).
	RetryJob(ctx context.Context, id string, attempts int, lastError string, runAfter time.Time) error
	// FailJob marks a claimed job failed after max retries.
	FailJob(ctx context.Context, id string, attempts int, lastError string) error
	// RequeueStaleJobs re-queues running jobs whose heartbeat expired (crash
	// recovery), returning how many jobs were recovered.
	RequeueStaleJobs(ctx context.Context, before, now time.Time) (int64, error)
	// CountActiveJobs counts queued/running jobs of a kind (optionally scoped
	// to a tenant; empty tenantID counts across tenants). Used to avoid piling
	// up repeats of a recurring job.
	CountActiveJobs(ctx context.Context, kind, tenantID string) (int64, error)
	// CountActiveJobsExcept counts queued/running jobs of a kind (optionally
	// tenant-scoped) excluding one job id. Recurring jobs use it to re-schedule
	// their next run while the current claim is still running.
	CountActiveJobsExcept(ctx context.Context, kind, tenantID, excludeID string) (int64, error)
	// CancelQueuedJobsWithPayload cancels queued jobs of a kind whose payload
	// contains every filter value. It never touches running, completed, or
	// failed jobs.
	CancelQueuedJobsWithPayload(ctx context.Context, kind, tenantID string, payloadFilter map[string]interface{}) (int64, error)
	// CountActiveJobsByKey counts queued/running jobs by the unique key used
	// by one-time approval executions.
	CountActiveJobsByKey(ctx context.Context, kind, key string) (int64, error)
	// ResetFailedJobByKey re-arms the single failed job for a retryable
	// approval. It never touches queued/running jobs.
	ResetFailedJobByKey(ctx context.Context, kind, key, payload string, maxRetry int) (bool, error)
	// GetJob returns one tenant-scoped job by id.
	GetJob(ctx context.Context, tenantID, id string) (*model.Job, error)
	// ListJobs returns a page of tenant-scoped jobs, optionally filtered by
	// kind and status.
	ListJobs(ctx context.Context, tenantID, kind, status string, page, pageSize int) ([]model.Job, int64, error)
	// CountJobsByStatus returns the process-wide job counts per status (for
	// the observability dashboard and queue-depth metric).
	CountJobsByStatus(ctx context.Context) (map[string]int64, error)
}

// EnqueueJob inserts a job idempotently by its unique Key.
func (s *store) EnqueueJob(ctx context.Context, j *model.Job) (bool, error) {
	if j.ID == "" {
		j.ID = id.New()
	}
	if j.Status == "" {
		j.Status = model.JobStatusQueued
	}
	if j.MaxRetry <= 0 {
		j.MaxRetry = defaultJobMaxRetry
	}
	if j.RunAfter.IsZero() {
		j.RunAfter = time.Now().UTC()
	}
	res := s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoNothing: true,
	}).Create(j)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ClaimJobs atomically claims up to limit due queued jobs.
func (s *store) ClaimJobs(ctx context.Context, limit int, now time.Time) ([]model.Job, error) {
	if limit <= 0 {
		limit = 1
	}
	jobs := make([]model.Job, 0, limit)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&model.Job{}).
			Where("status = ? AND run_after <= ?", model.JobStatusQueued, now).
			Order("run_after ASC").
			Limit(limit).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		// Claim each candidate independently. A concurrent worker may already
		// have transitioned one of the selected rows under PostgreSQL's READ
		// COMMITTED isolation; checking RowsAffected prevents this worker from
		// returning that row as its own claim.
		claimedIDs := make([]string, 0, len(ids))
		for _, jobID := range ids {
			res := tx.Model(&model.Job{}).
				Where("id = ? AND status = ?", jobID, model.JobStatusQueued).
				Updates(map[string]interface{}{
					"status":         model.JobStatusRunning,
					"last_heartbeat": now,
					"updated_at":     now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				claimedIDs = append(claimedIDs, jobID)
			}
		}
		if len(claimedIDs) == 0 {
			return nil
		}
		return tx.Where("id IN ?", claimedIDs).
			Order("run_after ASC").
			Find(&jobs).Error
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// HeartbeatJob refreshes a running job's lease.
func (s *store) HeartbeatJob(ctx context.Context, id string, now time.Time) error {
	return s.WithContext(ctx).Model(&model.Job{}).
		Where("id = ? AND status = ?", id, model.JobStatusRunning).
		Update("last_heartbeat", now).Error
}

// CompleteJob marks a claimed job succeeded exactly once, recording the attempt
// count of the successful run.
func (s *store) CompleteJob(ctx context.Context, id string, attempts int, result string) error {
	return s.WithContext(ctx).Model(&model.Job{}).
		Where("id = ? AND status = ?", id, model.JobStatusRunning).
		Updates(map[string]interface{}{
			"status":     model.JobStatusSucceeded,
			"attempts":   attempts,
			"last_error": "",
			"result":     result,
			"updated_at": time.Now().UTC(),
		}).Error
}

// RetryJob re-queues a claimed job with backoff.
func (s *store) RetryJob(ctx context.Context, id string, attempts int, lastError string, runAfter time.Time) error {
	return s.WithContext(ctx).Model(&model.Job{}).
		Where("id = ? AND status = ?", id, model.JobStatusRunning).
		Updates(map[string]interface{}{
			"status":     model.JobStatusQueued,
			"attempts":   attempts,
			"last_error": lastError,
			"run_after":  runAfter,
			"updated_at": time.Now().UTC(),
		}).Error
}

// FailJob marks a claimed job failed after max retries.
func (s *store) FailJob(ctx context.Context, id string, attempts int, lastError string) error {
	return s.WithContext(ctx).Model(&model.Job{}).
		Where("id = ? AND status = ?", id, model.JobStatusRunning).
		Updates(map[string]interface{}{
			"status":     model.JobStatusFailed,
			"attempts":   attempts,
			"last_error": lastError,
			"updated_at": time.Now().UTC(),
		}).Error
}

// RequeueStaleJobs recovers running jobs orphaned by a crash.
func (s *store) RequeueStaleJobs(ctx context.Context, before, now time.Time) (int64, error) {
	res := s.WithContext(ctx).Model(&model.Job{}).
		Where("status = ? AND last_heartbeat < ?", model.JobStatusRunning, before).
		Updates(map[string]interface{}{
			"status":     model.JobStatusQueued,
			"run_after":  now,
			"last_error": "requeued: runner heartbeat expired",
			"updated_at": now,
		})
	return res.RowsAffected, res.Error
}

// CountActiveJobs counts queued/running jobs of a kind under a tenant (empty
// tenantID counts across all tenants).
func (s *store) CountActiveJobs(ctx context.Context, kind, tenantID string) (int64, error) {
	var n int64
	q := s.WithContext(ctx).Model(&model.Job{}).
		Where("kind = ? AND status IN ?", kind, []string{model.JobStatusQueued, model.JobStatusRunning})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Count(&n).Error
	return n, err
}

// CountActiveJobsExcept counts active jobs of a kind, optionally excluding one
// job id (used by recurring workers to re-arm their own next run).
func (s *store) CountActiveJobsExcept(ctx context.Context, kind, tenantID, excludeID string) (int64, error) {
	var n int64
	q := s.WithContext(ctx).Model(&model.Job{}).
		Where("kind = ? AND status IN ?", kind, []string{model.JobStatusQueued, model.JobStatusRunning})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.Count(&n).Error
	return n, err
}

// CancelQueuedJobsWithPayload cancels only queued jobs matching the decoded
// payload filter. Filtering in Go avoids dialect-specific JSON predicates.
func (s *store) CancelQueuedJobsWithPayload(ctx context.Context, kind, tenantID string, payloadFilter map[string]interface{}) (int64, error) {
	if len(payloadFilter) == 0 {
		return 0, nil
	}
	var candidates []model.Job
	err := s.WithContext(ctx).Model(&model.Job{}).
		Where("kind = ? AND tenant_id = ? AND status = ?", kind, tenantID, model.JobStatusQueued).
		Find(&candidates).Error
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0)
	for _, job := range candidates {
		if job.Payload == "" {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil || len(payload) == 0 {
			continue
		}
		matches := true
		for key, expected := range payloadFilter {
			if !reflect.DeepEqual(payload[key], expected) {
				matches = false
				break
			}
		}
		if matches {
			ids = append(ids, job.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res := s.WithContext(ctx).Model(&model.Job{}).
		Where("id IN ? AND status = ?", ids, model.JobStatusQueued).
		Updates(map[string]interface{}{
			"status":     model.JobStatusCanceled,
			"last_error": "canceled: scheduled reconciliation disabled",
			"updated_at": time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// CountActiveJobsByKey checks the exact unique key for a one-time job.
func (s *store) CountActiveJobsByKey(ctx context.Context, kind, key string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.Job{}).
		Where("kind = ? AND key = ? AND status IN ?", kind, key, []string{model.JobStatusQueued, model.JobStatusRunning}).
		Count(&n).Error
	return n, err
}

// ResetFailedJobByKey re-arms the failed one-time job without creating a new
// key, preserving the single execution lane for an approval retry.
func (s *store) ResetFailedJobByKey(ctx context.Context, kind, key, payload string, maxRetry int) (bool, error) {
	if maxRetry <= 0 {
		maxRetry = defaultJobMaxRetry
	}
	now := time.Now().UTC()
	result := s.WithContext(ctx).Model(&model.Job{}).
		Where("kind = ? AND key = ? AND status = ?", kind, key, model.JobStatusFailed).
		Updates(map[string]interface{}{
			"status": model.JobStatusQueued, "payload": payload, "attempts": 0,
			"last_error": "", "run_after": now, "max_retry": maxRetry,
			"updated_at": now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// GetJob returns a tenant-scoped job by id.
func (s *store) GetJob(ctx context.Context, tenantID, id string) (*model.Job, error) {
	var j model.Job
	err := s.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobs returns a page of tenant-scoped jobs.
func (s *store) ListJobs(ctx context.Context, tenantID, kind, status string, page, pageSize int) ([]model.Job, int64, error) {
	var list []model.Job
	var total int64
	q := s.WithContext(ctx).Model(&model.Job{}).Where("tenant_id = ?", tenantID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// CountJobsByStatus returns the process-wide job count per status.
func (s *store) CountJobsByStatus(ctx context.Context) (map[string]int64, error) {
	rows := []struct {
		Status string `gorm:"column:status"`
		Cnt    int64  `gorm:"column:cnt"`
	}{}
	err := s.WithContext(ctx).Model(&model.Job{}).
		Select("status, count(*) AS cnt").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Cnt
	}
	return out, nil
}
