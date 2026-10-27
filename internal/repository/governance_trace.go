package repository

import (
	"context"
	"time"

	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type TraceRunFilter struct {
	TraceID     string
	RequestID   string
	SessionID   string
	AssistantID string
	AppType     string
	Status      string
}

func (s *store) UpsertTraceRun(ctx context.Context, run *model.TraceRun) error {
	if run.ID == "" {
		run.ID = id.New()
	}
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	err := s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "trace_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"span_id", "request_id", "session_id", "user_id", "project_id", "assistant_id",
			"assistant_release_id", "app_type", "app_id", "channel", "status", "route_summary_json",
			"retrieval_summary_json", "model_summary_json", "tool_summary_json", "governance_summary_json",
			"quality_summary_json", "evidence_pointers_json", "updated_at",
		}),
	}).Create(run).Error
	if err != nil {
		return err
	}

	var persisted model.TraceRun
	if err := s.WithContext(ctx).Where("tenant_id = ? AND trace_id = ?", run.TenantID, run.TraceID).First(&persisted).Error; err != nil {
		return err
	}
	*run = persisted
	return nil
}

func (s *store) GetTraceRunByTraceID(ctx context.Context, tenantID, traceID string, scopeAll bool) (*model.TraceRun, error) {
	var run model.TraceRun
	q := s.WithContext(ctx)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Where("trace_id = ?", traceID).First(&run).Error
	return &run, err
}

func (s *store) ListTraceRuns(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter TraceRunFilter) ([]model.TraceRun, int64, error) {
	var list []model.TraceRun
	var total int64
	q := s.WithContext(ctx).Model(&model.TraceRun{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.TraceID != "" {
		q = q.Where("trace_id = ?", filter.TraceID)
	}
	if filter.RequestID != "" {
		q = q.Where("request_id = ?", filter.RequestID)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if filter.AssistantID != "" {
		q = q.Where("assistant_id = ?", filter.AssistantID)
	}
	if filter.AppType != "" {
		q = q.Where("app_type = ?", filter.AppType)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}
