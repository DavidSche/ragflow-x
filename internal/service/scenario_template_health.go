package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioTemplateMetric is one row of the template health board (doc/276 §8.6).
// `Available=false` is a first-class answer: a metric with no evidence source
// must say so instead of reporting 0, which would read as "bad" when it means
// "unknown".
type ScenarioTemplateMetric struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Value       float64 `json:"value"`
	Numerator   int     `json:"numerator"`
	Denominator int     `json:"denominator"`
	Available   bool    `json:"available"`
	Reason      string  `json:"reason,omitempty"`
	// Definition is the exact formula, echoed to the UI so the number can be
	// recomputed by hand (doc/276 §8.6 口径).
	Definition string `json:"definition"`
}

// ScenarioTemplateHealth is the health panel payload.
type ScenarioTemplateHealth struct {
	TemplateID   string                   `json:"template_id"`
	TemplateName string                   `json:"template_name"`
	WindowDays   int                      `json:"window_days"`
	Instances    int                      `json:"instances"`
	Truncated    bool                     `json:"truncated"`
	Metrics      []ScenarioTemplateMetric `json:"metrics"`
	ComputedAt   time.Time                `json:"computed_at"`
}

// healthInstanceLimit keeps the panel bounded; the truncated flag tells the
// reviewer that the denominators cover the newest N instances only.
const healthInstanceLimit = 2000

// ScenarioTemplateHealth computes the five §8.6 metrics from real evidence.
// Two of them (eval rates) come from evaluation runs, three from instantiation
// and answer activity. Anything without an evidence source is reported as
// unavailable — never as zero.
func (s *Service) ScenarioTemplateHealth(ctx context.Context, tenantID, templateID string) (*ScenarioTemplateHealth, error) {
	asset, _, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, 0, false)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, httperr.NotFound("scenario template not found")
	}
	health := &ScenarioTemplateHealth{
		TemplateID: asset.ID, TemplateName: asset.Name, WindowDays: 7,
		ComputedAt: time.Now().UTC(),
	}
	instances, err := s.Store.ListTemplateInstancesForHealth(ctx, tenantID, asset.ID, healthInstanceLimit)
	if err != nil {
		return nil, err
	}
	health.Instances = len(instances)
	health.Truncated = len(instances) >= healthInstanceLimit

	assistantIDs := make([]string, 0, len(instances))
	observedAt := make(map[string]time.Time, len(instances))
	for _, instance := range instances {
		if instance.AssistantID == "" {
			continue
		}
		assistantIDs = append(assistantIDs, instance.AssistantID)
		observedAt[instance.AssistantID] = instance.CreatedAt
	}
	windowStart := health.ComputedAt.AddDate(0, 0, -health.WindowDays)
	activity, err := s.Store.AnswerActivityByAssistant(ctx, tenantID, assistantIDs, windowStart)
	if err != nil {
		return nil, err
	}
	// Capability-check observations (doc/276 §7.4-7, migration 167) are the
	// repair metric's evidence source — persisted by the check endpoint, read
	// here, never approximated from unrelated tables.
	capabilityChecks, err := s.Store.ListScenarioCapabilityChecksForHealth(ctx, tenantID, asset.ID, windowStart)
	if err != nil {
		return nil, err
	}

	firstRunNumerator := 0
	for _, instance := range instances {
		entry, ok := activity[instance.AssistantID]
		if !ok {
			continue
		}
		// "First run completed" is a 24h window from instantiation: the user
		// actually asked something and got an answer, not merely clicked create.
		if !instance.CreatedAt.IsZero() && entry.LastAt.Before(instance.CreatedAt.Add(24*time.Hour)) {
			firstRunNumerator++
		}
	}
	health.Metrics = append(health.Metrics, ScenarioTemplateMetric{
		Key: "first_run_completion_rate", Label: "首次运行完成率",
		Numerator: firstRunNumerator, Denominator: len(instances),
		Value:      ratio(firstRunNumerator, len(instances)),
		Available:  len(instances) > 0,
		Reason:     reasonIf(len(instances) == 0, "no instantiation yet"),
		Definition: "首次实例化后 24h 内产生 ≥1 次回答的实例 / 实例总数（不等于通过率）",
	})

	activeNumerator := 0
	for _, instance := range instances {
		if _, ok := activity[instance.AssistantID]; ok && instance.AssistantID != "" {
			activeNumerator++
		}
	}
	health.Metrics = append(health.Metrics, ScenarioTemplateMetric{
		Key: "app_active_7d", Label: "7 日活跃实例",
		Numerator: activeNumerator, Denominator: len(instances),
		Value:      ratio(activeNumerator, len(instances)),
		Available:  len(instances) > 0,
		Reason:     reasonIf(len(instances) == 0, "no instantiation yet"),
		Definition: "7 日内有 ≥1 次回答（rgx_answer_run）的实例占比",
	})

	evalMetrics, evalErr := s.scenarioEvalMetrics(ctx, tenantID, asset)
	if evalErr != nil {
		return nil, evalErr
	}
	health.Metrics = append(health.Metrics, evalMetrics...)

	// Repair completion computes from persisted verdicts: a dataset that had a
	// repairable verdict and later a satisfied one counts as repaired. With no
	// recorded repairable gap the metric reports unavailable (unknown, not zero).
	health.Metrics = append(health.Metrics, repairCompletionMetric(capabilityChecks))
	return health, nil
}

// repairCompletionMetric derives the §8.6 repair rate from the persisted
// capability-check observations. Denominator: distinct datasets with a
// `repairable` verdict inside the rolling window. Numerator: of those, ones
// with a `satisfied` verdict at-or-after their most recent repairable verdict
// (repaired at least once since the last gap). No recorded gap ⇒ unknown, which
// must read as unavailable, never as 0.
func repairCompletionMetric(checks []model.ScenarioCapabilityCheck) ScenarioTemplateMetric {
	metric := ScenarioTemplateMetric{
		Key: "repair_completion_rate", Label: "修复完成率",
		Available:  false,
		Reason:     "窗口内无 repairable 判定记录",
		Definition: "窗口内曾判定 repairable 且后续出现 satisfied 判定的数据集 / 窗口内曾判定 repairable 的数据集",
	}
	latestGapAt := make(map[string]time.Time, len(checks))
	for _, c := range checks {
		if c.Verdict != CapabilityVerdictRepairable {
			continue
		}
		if existing, ok := latestGapAt[c.DatasetID]; !ok || c.CreatedAt.After(existing) {
			latestGapAt[c.DatasetID] = c.CreatedAt
		}
	}
	if len(latestGapAt) == 0 {
		return metric
	}
	repaired := make(map[string]bool, len(latestGapAt))
	for _, c := range checks {
		if c.Verdict != CapabilityVerdictSatisfied {
			continue
		}
		gapAt, ok := latestGapAt[c.DatasetID]
		if ok && !gapAt.IsZero() && !c.CreatedAt.Before(gapAt) {
			repaired[c.DatasetID] = true
		}
	}
	metric.Numerator = len(repaired)
	metric.Denominator = len(latestGapAt)
	metric.Value = ratio(len(repaired), len(latestGapAt))
	metric.Available = true
	metric.Reason = ""
	return metric
}

// scenarioEvalMetrics reads the two retrieval-scoped rates from the newest
// evaluation run for this template. Values come from the run's metrics JSON so
// the board and the eval report cannot disagree.
func (s *Service) scenarioEvalMetrics(
	ctx context.Context, tenantID string, asset *model.ScenarioTemplateAsset,
) ([]ScenarioTemplateMetric, error) {
	returnRate := ScenarioTemplateMetric{
		Key: "eval_return_rate", Label: "评测返回率", Available: false,
		Reason:     "尚无评测运行",
		Definition: "有返回题数 / 总题数（retrieval_only 口径，不使用答案准确率）",
	}
	locatableRate := ScenarioTemplateMetric{
		Key: "eval_evidence_locatable_rate", Label: "引用可定位率", Available: false,
		Reason:     "尚无评测运行",
		Definition: "有引用且引用可定位的题数 / 有返回题数（retrieval_only 口径）",
	}
	runs, _, err := s.Store.ListEvaluationRuns(ctx, tenantID, repository.EvaluationScopeFilter{
		CandidateID: asset.ID, CandidateVer: asset.LatestVersion,
	}, 1, 1)
	if err != nil || len(runs) == 0 {
		return []ScenarioTemplateMetric{returnRate, locatableRate}, nil
	}
	metrics := map[string]interface{}{}
	if runs[0].Metrics != "" {
		_ = json.Unmarshal([]byte(runs[0].Metrics), &metrics)
	}
	apply := func(metric *ScenarioTemplateMetric, keys ...string) {
		for _, key := range keys {
			raw, ok := metrics[key]
			if !ok {
				continue
			}
			value, ok := numericValue(raw)
			if !ok {
				continue
			}
			metric.Value = value
			metric.Available = true
			metric.Reason = ""
			return
		}
		metric.Reason = "评测运行未产出该指标"
	}
	apply(&returnRate, "return_rate", "eval_return_rate", "returnRate")
	apply(&locatableRate, "evidence_locatable_rate", "eval_evidence_locatable_rate", "citation_locatable_rate")
	return []ScenarioTemplateMetric{returnRate, locatableRate}, nil
}

func numericValue(raw interface{}) (float64, bool) {
	switch typed := raw.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func reasonIf(condition bool, reason string) string {
	if condition {
		return reason
	}
	return ""
}
