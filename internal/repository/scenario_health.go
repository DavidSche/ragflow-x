package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// AssistantActivity is the per-assistant answer activity window. The template
// health board (doc/276 §8.6) derives "first run completed" and "active in 7
// days" from it, so both numbers come from the same evidence instead of two
// slightly different queries.
type AssistantActivity struct {
	AssistantID string
	AnswerCount int
	FirstAt     time.Time
	LastAt      time.Time
}

// ListTemplateInstancesForHealth returns the instantiation rows a health board
// needs. It is bounded on purpose: a template with tens of thousands of
// instances must not turn one panel request into a full table scan, so the
// caller gets the newest rows and reports the truncation honestly.
func (s *store) ListTemplateInstancesForHealth(
	ctx context.Context, tenantID, templateID string, limit int,
) ([]model.TemplateInstance, error) {
	if limit <= 0 {
		limit = 2000
	}
	var instances []model.TemplateInstance
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND template_id = ?", tenantID, templateID).
		Order("created_at DESC").Limit(limit).
		Find(&instances).Error
	if err != nil {
		return nil, err
	}
	return instances, nil
}

// AnswerActivityByAssistant aggregates answer runs per assistant. Empty input
// returns an empty map without touching the database.
func (s *store) AnswerActivityByAssistant(
	ctx context.Context, tenantID string, assistantIDs []string, since time.Time,
) (map[string]AssistantActivity, error) {
	out := make(map[string]AssistantActivity, len(assistantIDs))
	if len(assistantIDs) == 0 {
		return out, nil
	}
	var rows []AssistantActivity
	err := s.WithContext(ctx).Model(&model.AnswerRun{}).
		Select("assistant_id, COUNT(*) AS answer_count, MIN(created_at) AS first_at, MAX(created_at) AS last_at").
		Where("tenant_id = ? AND assistant_id IN ? AND created_at >= ?", tenantID, assistantIDs, since).
		Group("assistant_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, item := range rows {
		out[item.AssistantID] = item
	}
	return out, nil
}

// RecordScenarioCapabilityChecks persists one capability-check observation per
// dataset (doc/276 §7.4-7). It is append-only evidence for the health board's
// repair_completion_rate: rows are never updated or deleted, so the metric can
// later distinguish "was repairable and got fixed" from "was always fine".
func (s *store) RecordScenarioCapabilityChecks(ctx context.Context, checks []model.ScenarioCapabilityCheck) error {
	if len(checks) == 0 {
		return nil
	}
	return s.WithContext(ctx).Create(&checks).Error
}

// ListScenarioCapabilityChecksForHealth returns the observation rows the health
// board's repair metric needs for one template, newest first, bounded to the
// metric window by the caller.
func (s *store) ListScenarioCapabilityChecksForHealth(
	ctx context.Context, tenantID, templateID string, since time.Time,
) ([]model.ScenarioCapabilityCheck, error) {
	var checks []model.ScenarioCapabilityCheck
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND template_id = ? AND created_at >= ?", tenantID, templateID, since).
		Order("created_at DESC").
		Find(&checks).Error
	if err != nil {
		return nil, err
	}
	return checks, nil
}
