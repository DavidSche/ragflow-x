package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type ReleaseGateRuntimeEvidenceRepo interface {
	GetReleaseGateCostEvidence(
		ctx context.Context, tenantID, assistantReleaseID string,
	) (*model.ReleaseGateRuntimeEvidenceSummary, error)
	GetReleaseGateConversationEvidence(
		ctx context.Context, tenantID, assistantReleaseID, contractVersion string,
	) (*model.ReleaseGateConversationEvidenceSummary, error)
	GetReleaseGateExperienceEvidence(
		ctx context.Context, tenantID, assistantReleaseID string,
	) (*model.ReleaseGateExperienceEvidenceSummary, error)
}

func (s *store) GetReleaseGateCostEvidence(
	ctx context.Context, tenantID, assistantReleaseID string,
) (*model.ReleaseGateRuntimeEvidenceSummary, error) {
	var summary model.ReleaseGateRuntimeEvidenceSummary
	err := s.WithContext(ctx).Raw(`
		WITH candidate_runtime AS (
			SELECT DISTINCT tenant_id, request_id
			FROM rgx_trace_run
			WHERE tenant_id = ? AND assistant_release_id = ?
		)
		SELECT
			COUNT(*) AS runtime_requests,
			SUM(CASE WHEN EXISTS (
				SELECT 1 FROM rgx_cost_metric c
				WHERE c.tenant_id = r.tenant_id AND c.request_id = r.request_id
			) THEN 1 ELSE 0 END) AS cost_requests,
			SUM(CASE WHEN NOT EXISTS (
				SELECT 1 FROM rgx_cost_metric c
				WHERE c.tenant_id = r.tenant_id AND c.request_id = r.request_id
			) THEN 1 ELSE 0 END) AS unmetered_requests,
			COALESCE((SELECT SUM(CASE WHEN c.estimated = false OR c.estimation_policy_version = '' THEN 1 ELSE 0 END)
				FROM rgx_cost_metric c JOIN candidate_runtime r
					ON c.tenant_id = r.tenant_id AND c.request_id = r.request_id), 0) AS unpolicy_requests,
			COALESCE((SELECT SUM(c.tokens_in)
				FROM rgx_cost_metric c JOIN candidate_runtime r
					ON c.tenant_id = r.tenant_id AND c.request_id = r.request_id), 0) AS tokens_in,
			COALESCE((SELECT SUM(c.tokens_out)
				FROM rgx_cost_metric c JOIN candidate_runtime r
					ON c.tenant_id = r.tenant_id AND c.request_id = r.request_id), 0) AS tokens_out,
			COALESCE((SELECT SUM(c.cost)
				FROM rgx_cost_metric c JOIN candidate_runtime r
					ON c.tenant_id = r.tenant_id AND c.request_id = r.request_id), 0) AS estimated_cost
		FROM candidate_runtime r
	`, tenantID, assistantReleaseID).Scan(&summary).Error
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func (s *store) GetReleaseGateConversationEvidence(
	ctx context.Context, tenantID, assistantReleaseID, contractVersion string,
) (*model.ReleaseGateConversationEvidenceSummary, error) {
	var summary model.ReleaseGateConversationEvidenceSummary
	err := s.WithContext(ctx).Raw(`
		WITH candidate_runtime AS (
			SELECT DISTINCT tenant_id, request_id, assistant_id, session_id
			FROM rgx_trace_run
			WHERE tenant_id = ? AND assistant_release_id = ?
		), candidate_decisions AS (
			SELECT d.*
			FROM rgx_context_decision d
			JOIN candidate_runtime r
				ON d.tenant_id = r.tenant_id AND d.request_id = r.request_id
				AND COALESCE(d.assistant_id, '') = COALESCE(r.assistant_id, '')
		)
		SELECT
			(SELECT COUNT(*) FROM candidate_runtime) AS runtime_requests,
			(SELECT COUNT(*) FROM candidate_runtime r WHERE EXISTS (
				SELECT 1 FROM rgx_context_decision d
				WHERE d.tenant_id = r.tenant_id AND d.request_id = r.request_id
					AND COALESCE(d.assistant_id, '') = COALESCE(r.assistant_id, '')
			)) AS decision_requests,
			(SELECT COUNT(*) FROM candidate_decisions) AS decision_count,
			(SELECT COUNT(*) FROM candidate_decisions d
				WHERE d.decision NOT IN ('memory_allowed', 'memory_denied')
					OR COALESCE(d.decision_hash, '') = '') AS invalid_decision_count,
			(SELECT COUNT(*) FROM candidate_decisions d
				WHERE d.detail NOT LIKE '%"contract_version":"' || ? || '"%'
					OR d.detail NOT LIKE '%"search_side_effect":false%'
					OR d.detail NOT LIKE '%"memory_operation_created":false%') AS invalid_contract_count,
			(SELECT COUNT(*) FROM candidate_decisions d
				JOIN candidate_runtime r ON d.tenant_id = r.tenant_id
					AND d.request_id = r.request_id AND COALESCE(d.assistant_id, '') = COALESCE(r.assistant_id, '')
				WHERE d.request_kind = 'search' AND EXISTS (
					SELECT 1 FROM rgx_memory_operation m
					WHERE m.tenant_id = r.tenant_id AND m.request_id = r.request_id
						AND COALESCE(m.assistant_id, '') = COALESCE(r.assistant_id, '')
				)) AS search_memory_operation_count
	`, tenantID, assistantReleaseID, contractVersion).Scan(&summary).Error
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func (s *store) GetReleaseGateExperienceEvidence(
	ctx context.Context, tenantID, assistantReleaseID string,
) (*model.ReleaseGateExperienceEvidenceSummary, error) {
	var summary model.ReleaseGateExperienceEvidenceSummary
	err := s.WithContext(ctx).Raw(`
		WITH candidate_runtime AS (
			SELECT DISTINCT tenant_id, request_id
			FROM rgx_trace_run
			WHERE tenant_id = ? AND assistant_release_id = ?
		), candidate_ledger AS (
			SELECT e.*
			FROM rgx_knowledge_ops_event e
			JOIN candidate_runtime r
				ON e.tenant_id = r.tenant_id AND e.request_id = r.request_id
		)
		SELECT
			(SELECT COUNT(*) FROM candidate_runtime) AS runtime_requests,
			SUM(CASE WHEN EXISTS (
				SELECT 1 FROM rgx_knowledge_ops_event e
				WHERE e.tenant_id = r.tenant_id AND e.request_id = r.request_id
			) THEN 1 ELSE 0 END) AS ledger_requests,
			(SELECT COUNT(*) FROM candidate_ledger WHERE status = ?) AS completed,
			(SELECT COUNT(*) FROM candidate_ledger WHERE status = ?) AS no_answer,
			(SELECT COUNT(*) FROM candidate_ledger WHERE status = ?) AS failed,
			(SELECT COUNT(*) FROM candidate_ledger WHERE feedback_rating = ?) AS negative
		FROM candidate_runtime r
	`, tenantID, assistantReleaseID,
		model.KnowledgeOpsCompleted, model.KnowledgeOpsNoAnswer,
		model.KnowledgeOpsFailed, model.FeedbackNegative,
	).Scan(&summary).Error
	if err != nil {
		return nil, err
	}
	return &summary, nil
}
