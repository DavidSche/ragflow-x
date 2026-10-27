package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type OperationalReportFilter struct {
	TenantID           string
	ProjectID          string
	AssistantID        string
	AssistantReleaseID string
	Scenario           string
	DateFrom           string
	DateTo             string
}

func (s *store) SummarizeOperationsByAttribution(ctx context.Context, tenantID string, scopeAll bool, filter OperationalReportFilter) ([]model.OperationalAttributionRow, error) {
	effective := filter
	if !scopeAll {
		effective.TenantID = tenantID
	}
	query := `
		SELECT
			e.tenant_id,
			COALESCE(t.project_id, '') AS project_id,
			COALESCE(t.assistant_id, '') AS assistant_id,
			COALESCE(t.assistant_release_id, '') AS assistant_release_id,
			e.app_type AS scenario,
			COUNT(*) AS requests,
			SUM(CASE WHEN e.status = ? THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN e.status = ? THEN 1 ELSE 0 END) AS no_answer,
			SUM(e.tokens_in) AS tokens_in,
			SUM(e.tokens_out) AS tokens_out,
			COALESCE(SUM(c.cost), 0) AS estimated_cost,
			COALESCE(AVG(NULLIF(e.duration_ms, 0)), 0) AS avg_latency_ms,
			COALESCE(SUM(c.tokens_in + c.tokens_out), 0) AS quota_consumed_tokens
		FROM rgx_knowledge_ops_event e
		LEFT JOIN rgx_trace_run t
			ON t.tenant_id = e.tenant_id AND t.trace_id = e.request_id
		LEFT JOIN rgx_cost_metric c
			ON c.tenant_id = e.tenant_id AND c.request_id = e.request_id
		WHERE 1 = 1`
	args := []any{model.KnowledgeOpsFailed, model.KnowledgeOpsNoAnswer}
	if effective.TenantID != "" {
		query += " AND e.tenant_id = ?"
		args = append(args, effective.TenantID)
	}
	if effective.ProjectID != "" {
		query += " AND COALESCE(t.project_id, '') = ?"
		args = append(args, effective.ProjectID)
	}
	if effective.AssistantID != "" {
		query += " AND COALESCE(t.assistant_id, '') = ?"
		args = append(args, effective.AssistantID)
	}
	if effective.AssistantReleaseID != "" {
		query += " AND COALESCE(t.assistant_release_id, '') = ?"
		args = append(args, effective.AssistantReleaseID)
	}
	if effective.Scenario != "" {
		query += " AND e.app_type = ?"
		args = append(args, effective.Scenario)
	}
	if t, ok := parseFilterDate(effective.DateFrom); ok {
		query += " AND e.created_at >= ?"
		args = append(args, t)
	}
	if t, ok := parseFilterDate(effective.DateTo); ok {
		if effective.DateTo != t.Format(time.RFC3339) {
			t = t.AddDate(0, 0, 1)
		}
		query += " AND e.created_at < ?"
		args = append(args, t)
	}
	query += `
		GROUP BY e.tenant_id, t.project_id, t.assistant_id, t.assistant_release_id, e.app_type
		ORDER BY requests DESC, e.tenant_id ASC, scenario ASC`
	var rows []model.OperationalAttributionRow
	if err := s.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
