package service

import (
	"context"
	"encoding/json"
	"time"
)

// Metric Contract board (doc/107 §3.3.2, doc/125 §5). The board is read-only
// and builds on two existing sources without any new table: the latest
// RouteEvaluationRun (routing quality gate) and the 30-day KnowledgeOpsSummary
// (answer quality and operations efficiency). Targets are the frozen contract
// values from doc/107 §3.3.2 — they are code constants, not editable settings.

// MetricsContractItem is one contract row rendered by the board.
type MetricsContractItem struct {
	Key               string    `json:"key"`
	Label             string    `json:"label"`
	Group             string    `json:"group"` // routing | answer | operations
	Value             *float64  `json:"value"`
	Target            *float64  `json:"target"`
	Comparator        string    `json:"comparator"` // gte | lte | eq
	Format            string    `json:"format"`     // percent | ratio | ms | number | currency
	SampleSize        int       `json:"sample_size"`
	EvaluationVersion string    `json:"evaluation_version"`
	JudgeType         string    `json:"judge_type"` // rule | human_calibration_required
	GateState         string    `json:"gate_state"` // passed | failed | unknown
	UpdatedAt         time.Time `json:"updated_at"`
}

// MetricsContractBoard is the GET /knowledge-ops/metrics-contract payload.
type MetricsContractBoard struct {
	Items          []MetricsContractItem `json:"items"`
	RouteGateState string                `json:"route_gate_state"`
	RouteRunName   string                `json:"route_run_name,omitempty"`
	EvaluatedAt    *time.Time            `json:"evaluated_at,omitempty"`
	WindowDays     int                   `json:"window_days"`
}

// metricContractTarget is the frozen doc/107 §3.3.2 target for one metric.
type metricContractTarget struct {
	target     *float64
	comparator string
}

func contractTarget(value float64, comparator string) metricContractTarget {
	return metricContractTarget{target: &value, comparator: comparator}
}

func contractPointer(value float64) *float64 {
	return &value
}

// metricContractComparators maps each metric key to its frozen target from
// doc/107 §3.3.2. Missing keys render with an "unknown" gate (observed but
// not gated).
var metricContractComparators = map[string]metricContractTarget{
	// Routing quality: Top1 ≥ 90%, Top3 ≥ 98%, wrong execution ≤ 2%,
	// auto-execute wrong rate = 0, abstain ≤ 10%, ECE ≤ 0.05,
	// Wilson lower ≥ 0.85, route P95 ≤ 1200ms.
	"top1_accuracy":                   contractTarget(0.90, "gte"),
	"top3_recall":                     contractTarget(0.98, "gte"),
	"wrong_route_rate":                contractTarget(0.02, "lte"),
	"wrong_execution_rate":            contractTarget(0.02, "lte"),
	"auto_execute_wrong_rate":         contractTarget(0, "eq"),
	"abstain_rate":                    contractTarget(0.10, "lte"),
	"ece":                             contractTarget(0.05, "lte"),
	"high_confidence_wilson_lower_95": contractTarget(0.85, "gte"),
	"route_p95_ms":                    contractTarget(1200, "lte"),
	// Answer quality: citation coverage ≥ 95%, refusal (no-answer) rate ≤ 5%
	// with human calibration pending (doc/107 §3.3.2 judge note).
	"citation_rate":  contractTarget(0.95, "gte"),
	"no_answer_rate": contractTarget(0.05, "lte"),
	// Operations efficiency: average latency ≤ 3000ms, satisfaction ≥ 85%.
	"avg_latency_ms":    contractTarget(3000, "lte"),
	"satisfaction_rate": contractTarget(0.85, "gte"),
}

// metricsContractGate applies the comparator and returns the gate state.
func metricsContractGate(key string, value float64) string {
	target, ok := metricContractComparators[key]
	if !ok || target.target == nil {
		return "unknown"
	}
	switch target.comparator {
	case "gte":
		if value >= *target.target {
			return "passed"
		}
	case "lte":
		if value <= *target.target {
			return "passed"
		}
	case "eq":
		if value == *target.target {
			return "passed"
		}
	}
	return "failed"
}

func metricsContractItem(key, label, group, format string, value *float64, sampleSize int, judgeType string, updatedAt time.Time) MetricsContractItem {
	item := MetricsContractItem{
		Key: key, Label: label, Group: group, Value: value, Format: format,
		SampleSize: sampleSize, JudgeType: judgeType, GateState: "unknown",
		UpdatedAt: updatedAt,
	}
	if target, ok := metricContractComparators[key]; ok {
		item.Target = target.target
		item.Comparator = target.comparator
	}
	if value != nil {
		item.GateState = metricsContractGate(key, *value)
	}
	return item
}

// MetricsContractSummary aggregates the latest route gate and the 30-day
// knowledge-ops summary into the frozen metric contract board (doc/125 §5.1).
// Authorization stays in the handler, matching the KnowledgeOpsSummary slice.
func (s *Service) MetricsContractSummary(ctx context.Context, tenantID string) (*MetricsContractBoard, error) {
	board := &MetricsContractBoard{Items: []MetricsContractItem{}, WindowDays: 30}

	// 1. Latest route evaluation run (page 1, size 1 = newest by created_at).
	runs, _, err := s.Store.ListRouteEvaluationRuns(ctx, tenantID, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		run := runs[0]
		board.RouteGateState = run.GateState
		board.RouteRunName = run.Name
		board.EvaluatedAt = &run.CreatedAt
		var metrics RouteEvaluationMetrics
		if err := json.Unmarshal([]byte(run.MetricsJSON), &metrics); err == nil {
			evaluatedAt := run.CreatedAt
			sample := metrics.EvaluatedCount
			routing := func(key, label, format string, value *float64) {
				board.Items = append(board.Items, metricsContractItem(key, label, "routing", format, value, sample, "rule", evaluatedAt))
			}
			routing("top1_accuracy", "Top1 Accuracy", "percent", contractPointer(metrics.Top1Accuracy))
			routing("top3_recall", "Top3 Recall", "percent", contractPointer(metrics.Top3Recall))
			routing("wrong_route_rate", "Wrong Route Rate", "percent", contractPointer(metrics.WrongRouteRate))
			routing("wrong_execution_rate", "Wrong Execution Rate", "percent", contractPointer(metrics.WrongExecutionRate))
			routing("auto_execute_wrong_rate", "Auto-Execute Wrong Rate", "percent", contractPointer(metrics.AutoExecuteWrongRate))
			routing("abstain_rate", "Abstain Rate", "percent", contractPointer(metrics.AbstainRate))
			routing("ece", "ECE", "ratio", contractPointer(metrics.ECE))
			routing("high_confidence_wilson_lower_95", "High-Confidence Wilson Lower 95", "ratio", contractPointer(metrics.HighConfidenceWilsonLower))
			routing("route_p95_ms", "Route P95 Latency", "ms", contractPointer(metrics.RouteP95Ms))
		}
	}

	// 2. Answer quality and operations efficiency from the 30-day window.
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	summary, err := s.Store.KnowledgeOpsSummary(ctx, tenantID, false, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	if summary != nil {
		observedAt := to
		quality := func(key, label, format string, value *float64, sample int) {
			if sample <= 0 {
				// Without observations a gate verdict would be meaningless; the
				// board shows the row only once data exists (doc/125 §5.2 空数据占位由前端处理).
				return
			}
			board.Items = append(board.Items, metricsContractItem(key, label, "answer", format, value, sample, "rule", observedAt))
		}
		quality("citation_rate", "Citation Rate", "percent", contractPointer(summary.CitationRate), int(summary.Completed))
		// The refusal-correctness contract needs a human calibration set; the
		// observable no-answer rate is the v1 proxy (doc/125 §5.1).
		quality("no_answer_rate", "No-Answer Rate", "percent", contractPointer(summary.NoAnswerRate), int(summary.TotalTurns))
		operations := func(key, label, format string, value *float64, sample int) {
			if sample <= 0 {
				return
			}
			board.Items = append(board.Items, metricsContractItem(key, label, "operations", format, value, sample, "rule", observedAt))
		}
		operations("avg_latency_ms", "Average Latency", "ms", contractPointer(summary.AvgLatencyMs), int(summary.TotalTurns))
		operations("satisfaction_rate", "Satisfaction Rate", "percent", contractPointer(summary.SatisfactionRate), int(summary.Positive+summary.Negative))
	}
	return board, nil
}
