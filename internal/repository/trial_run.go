package repository

import (
	"context"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// TrialRunFilter narrows trial-run listings.
type TrialRunFilter struct {
	AssistantID        string
	ScenarioTemplateID string
	Status             string
}

// TrialRunRepo persists retrieval-only trial runs and their case results.
type TrialRunRepo interface {
	CreateTrialRun(ctx context.Context, run *model.TrialRun) error
	UpdateTrialRun(ctx context.Context, run *model.TrialRun) error
	GetTrialRun(ctx context.Context, tenantID, runID string) (*model.TrialRun, error)
	GetTrialRunByIdempotencyKey(ctx context.Context, tenantID, key string) (*model.TrialRun, error)
	ListTrialRuns(ctx context.Context, tenantID string, filter TrialRunFilter, limit int) ([]model.TrialRun, error)
	LatestCompletedTrialRun(ctx context.Context, tenantID, templateID string, templateVersion int64, assistantID string) (*model.TrialRun, error)
	CreateTrialCaseResults(ctx context.Context, results []model.TrialCaseResult) error
	ListTrialCaseResults(ctx context.Context, tenantID, runID string) ([]model.TrialCaseResult, error)
	FailInterruptedTrialRuns(ctx context.Context, cutoff time.Time) (int64, error)
}

func (s *store) CreateTrialRun(ctx context.Context, run *model.TrialRun) error {
	return s.WithContext(ctx).Create(run).Error
}

func (s *store) UpdateTrialRun(ctx context.Context, run *model.TrialRun) error {
	return s.WithContext(ctx).Model(&model.TrialRun{}).
		Where("tenant_id = ? AND id = ?", run.TenantID, run.ID).
		Updates(map[string]interface{}{
			"status":              run.Status,
			"failure_reason":      run.FailureReason,
			"summary":             run.Summary,
			"cancel_requested_at": run.CancelRequestedAt,
			"started_at":          run.StartedAt,
			"completed_at":        run.CompletedAt,
			"updated_at":          time.Now().UTC(),
		}).Error
}

func (s *store) GetTrialRun(ctx context.Context, tenantID, runID string) (*model.TrialRun, error) {
	var run model.TrialRun
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, runID).First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *store) GetTrialRunByIdempotencyKey(ctx context.Context, tenantID, key string) (*model.TrialRun, error) {
	var run model.TrialRun
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).
		First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *store) ListTrialRuns(ctx context.Context, tenantID string, filter TrialRunFilter, limit int) ([]model.TrialRun, error) {
	query := s.WithContext(ctx).Model(&model.TrialRun{}).Where("tenant_id = ?", tenantID)
	if filter.AssistantID != "" {
		query = query.Where("assistant_id = ?", filter.AssistantID)
	}
	if filter.ScenarioTemplateID != "" {
		query = query.Where("scenario_template_id = ?", filter.ScenarioTemplateID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var runs []model.TrialRun
	if err := query.Order("created_at DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *store) LatestCompletedTrialRun(ctx context.Context, tenantID, templateID string, templateVersion int64, assistantID string) (*model.TrialRun, error) {
	var run model.TrialRun
	query := s.WithContext(ctx).Model(&model.TrialRun{}).
		Where("tenant_id = ? AND status = ?", tenantID, model.TrialRunCompleted)
	if templateID != "" {
		query = query.Where("scenario_template_id = ?", templateID)
	}
	if templateVersion > 0 {
		query = query.Where("template_version = ?", templateVersion)
	}
	if assistantID != "" {
		query = query.Where("assistant_id = ?", assistantID)
	}
	err := query.Order("completed_at DESC, created_at DESC").First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *store) CreateTrialCaseResults(ctx context.Context, results []model.TrialCaseResult) error {
	if len(results) == 0 {
		return nil
	}
	return s.WithContext(ctx).CreateInBatches(results, 50).Error
}

func (s *store) ListTrialCaseResults(ctx context.Context, tenantID, runID string) ([]model.TrialCaseResult, error) {
	var results []model.TrialCaseResult
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("created_at ASC, id ASC").
		Find(&results).Error
	return results, err
}

// FailInterruptedTrialRuns terminalizes runs left RUNNING by a crash/restart.
// Trial runs are diagnostics and are never resumed (doc/228 §3.1 T7.3′ ①).
func (s *store) FailInterruptedTrialRuns(ctx context.Context, cutoff time.Time) (int64, error) {
	now := time.Now().UTC()
	result := s.WithContext(ctx).Model(&model.TrialRun{}).
		Where("status IN ?", []string{model.TrialRunQueued, model.TrialRunRunning}).
		Where("created_at < ?", cutoff).
		Updates(map[string]interface{}{
			"status":         model.TrialRunFailed,
			"failure_reason": model.TrialFailureInterrupted,
			"completed_at":   now,
			"updated_at":     now,
		})
	return result.RowsAffected, result.Error
}
