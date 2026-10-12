package service

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const KnowledgeStrategyQualityGateNotEvaluated = "NOT_EVALUATED"

type KnowledgeStrategyQualityGateEvidence struct {
	ID              string `json:"id"`
	EvaluationRunID string `json:"evaluation_run_id"`
	EvaluationScope string `json:"evaluation_scope"`
	EvalSetID       string `json:"eval_set_id"`
	EvalSetVersion  int64  `json:"eval_set_version"`
	EvalSetHash     string `json:"eval_set_hash"`
	SnapshotID      string `json:"snapshot_id"`
	SnapshotHash    string `json:"snapshot_hash"`
	CreatedAt       string `json:"created_at"`
}

type KnowledgeStrategyQualityGateSummary struct {
	StrategyID     string                                `json:"strategy_id"`
	RunID          string                                `json:"run_id"`
	RunStatus      string                                `json:"run_status"`
	GateStatus     string                                `json:"gate_status"`
	GateReason     string                                `json:"gate_reason"`
	Pass           *bool                                 `json:"pass"`
	Metrics        map[string]interface{}                `json:"metrics"`
	EvalSetID      string                                `json:"eval_set_id"`
	EvalSetVersion int64                                 `json:"eval_set_version"`
	EvalSetHash    string                                `json:"eval_set_hash"`
	Capability     string                                `json:"capability"`
	ExecutionMode  string                                `json:"execution_mode"`
	CreatedAt      string                                `json:"created_at"`
	CompletedAt    *string                               `json:"completed_at"`
	Evidence       *KnowledgeStrategyQualityGateEvidence `json:"evidence,omitempty"`
	EvidenceURL    string                                `json:"evidence_url,omitempty"`
}

func (s *Service) GetKnowledgeStrategyQualityGate(
	ctx context.Context, tenantID, strategyID string,
) (*KnowledgeStrategyQualityGateSummary, error) {
	strategy, err := s.GetKnowledgeStrategy(ctx, tenantID, strategyID)
	if err != nil {
		return nil, err
	}
	summary := &KnowledgeStrategyQualityGateSummary{
		StrategyID: strategy.ID, GateStatus: KnowledgeStrategyQualityGateNotEvaluated,
		Metrics: map[string]interface{}{},
	}
	runs, _, err := s.Store.ListEvaluationRuns(ctx, tenantID, repository.EvaluationScopeFilter{
		Scope: model.EvaluationScopeCompilationStrategy, StrategyID: strategy.ID,
	}, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return summary, nil
	}
	run := runs[0]
	summary.RunID = run.ID
	summary.RunStatus = run.Status
	summary.GateStatus = run.GateStatus
	if summary.GateStatus == "" {
		summary.GateStatus = "UNREPORTED"
	}
	summary.GateReason = run.GateReason
	summary.Pass = run.Pass
	summary.EvalSetID = run.EvalSetID
	summary.EvalSetVersion = run.EvalSetVersion
	summary.EvalSetHash = run.EvalSetHash
	summary.Capability = run.Capability
	summary.ExecutionMode = run.ExecutionMode
	summary.CreatedAt = run.CreatedAt.UTC().Format(time.RFC3339)
	if run.CompletedAt != nil {
		completedAt := run.CompletedAt.UTC().Format(time.RFC3339)
		summary.CompletedAt = &completedAt
	}
	if run.Metrics != "" {
		metrics := map[string]interface{}{}
		if err := json.Unmarshal([]byte(run.Metrics), &metrics); err != nil {
			return nil, httperr.New(500, 50082, "knowledge strategy quality metrics are invalid")
		}
		summary.Metrics = metrics
	}
	bundles, _, err := s.Store.ListEvidenceBundles(ctx, tenantID, repository.EvaluationScopeFilter{
		Scope:           model.EvaluationScopeCompilationStrategy,
		StrategyID:      strategy.ID,
		EvaluationRunID: run.ID,
	}, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(bundles) > 0 {
		summary.Evidence = &KnowledgeStrategyQualityGateEvidence{
			ID: bundles[0].ID, EvaluationRunID: bundles[0].EvaluationRunID,
			EvaluationScope: bundles[0].EvaluationScope, EvalSetID: bundles[0].EvalSetID,
			EvalSetVersion: bundles[0].EvalSetVersion, EvalSetHash: bundles[0].EvalSetHash,
			SnapshotID: bundles[0].SnapshotID, SnapshotHash: bundles[0].SnapshotHash,
			CreatedAt: bundles[0].CreatedAt.UTC().Format(time.RFC3339),
		}
		query := url.Values{}
		query.Set("evaluation_scope", model.EvaluationScopeCompilationStrategy)
		query.Set("x_strategy_id", strategy.ID)
		query.Set("evaluation_run_id", run.ID)
		summary.EvidenceURL = "/api/v1/evidence-bundles?" + query.Encode()
	}
	return summary, nil
}
