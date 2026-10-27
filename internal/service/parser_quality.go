package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	defaultHeuristicPassThreshold = 0.90
	defaultHeuristicWarnThreshold = 0.70
)

// QualityEvaluation separates the raw scores from the final status and action.
// The parser executor uses Action to decide publish, retry, or quarantine.
type QualityEvaluation struct {
	HeuristicScore float64            `json:"heuristic_score"`
	ReferenceScore *float64           `json:"reference_score,omitempty"`
	EffectiveScore float64            `json:"effective_score"`
	Status         string             `json:"status"`
	Action         string             `json:"action"`
	FailureReasons []string           `json:"failure_reasons,omitempty"`
	Metrics        map[string]float64 `json:"metrics"`
}

type ParseQualityRequest struct {
	ProjectID        string             `json:"project_id"`
	DatasetID        string             `json:"dataset_id"`
	DocumentType     string             `json:"document_type"`
	ParserPolicyID   string             `json:"parser_policy_id"`
	DocumentID       string             `json:"document_id" binding:"required"`
	UserID           string             `json:"user_id"`
	TraceID          string             `json:"trace_id"`
	StartedAt        time.Time          `json:"started_at"`
	FinishedAt       time.Time          `json:"finished_at"`
	HeuristicMetrics map[string]float64 `json:"heuristic_metrics"`
	ReferenceMetrics map[string]float64 `json:"reference_metrics"`
}

var heuristicMetricSources = map[string]string{
	"text_health":       "text_density",
	"structure_health":  "column_count_consistency",
	"encoding_health":   "garbled_char_ratio",
	"table_health":      "table_structure_consistency",
	"layout_health":     "reading_order_anomaly",
	"repetition_health": "repeated_header_ratio",
}

func (s *Service) ResolveParserPolicy(ctx context.Context, tenantID, projectID, datasetID, documentType string) (*model.ParserPolicy, error) {
	policies, err := s.Store.ListAllParserPolicies(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var best *model.ParserPolicy
	bestSpecificity := -1
	for index := range policies {
		policy := &policies[index]
		if !policy.Active {
			continue
		}
		specificity := parserPolicySpecificity(policy, projectID, datasetID, documentType)
		if specificity < 0 {
			continue
		}
		if specificity > bestSpecificity {
			best, bestSpecificity = policy, specificity
		}
	}
	return best, nil
}

func (s *Service) EvaluateParseQuality(ctx context.Context, tenantID, qualityProfileID string, heuristicMetrics, referenceMetrics map[string]float64) (*QualityEvaluation, error) {
	profile, err := s.getQualityProfile(ctx, tenantID, qualityProfileID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, notFoundQualityProfile()
	}
	profileMetrics, profileThresholds, err := decodeQualityProfilePayload(profile.Metrics, profile.Thresholds)
	if err != nil {
		return nil, invalidQualityProfile("stored quality profile payload is invalid")
	}
	if err := validateQualityMetrics(heuristicMetrics); err != nil {
		return nil, err
	}
	for metricName := range referenceMetrics {
		if err := validateMetricValue(metricName, referenceMetrics[metricName]); err != nil {
			return nil, err
		}
	}

	healthScores, missing := heuristicHealthScores(profileMetrics, heuristicMetrics)
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, httperr.New(409, 40982, "quality metric unavailable: "+strings.Join(missing, ","))
	}
	heuristicScore := 0.0
	for name, metric := range profileMetrics {
		if !metric.Enabled {
			continue
		}
		heuristicScore += metric.Weight * healthScores[name]
	}
	passThreshold := defaultHeuristicPassThreshold
	warnThreshold := defaultHeuristicWarnThreshold
	if value, ok := profileThresholds["heuristic_pass"]; ok {
		passThreshold = value
	}
	if value, ok := profileThresholds["heuristic_warn"]; ok {
		warnThreshold = value
	}
	if warnThreshold >= passThreshold {
		return nil, invalidQualityProfile("heuristic_warn threshold must be lower than heuristic_pass threshold")
	}

	evaluation := &QualityEvaluation{
		HeuristicScore: heuristicScore,
		Metrics:        healthScores,
	}
	switch profile.EvaluationMode {
	case model.QualityEvaluationHeuristicOnly:
		evaluation.EffectiveScore = heuristicScore
	case model.QualityEvaluationHeuristicPlusReference:
		if len(referenceMetrics) == 0 {
			return nil, httperr.New(409, 40982, "golden parse set metrics are unavailable")
		}
		referenceScore, failedReferences := evaluateReferenceMetrics(referenceMetrics, profileThresholds)
		evaluation.ReferenceScore = &referenceScore
		evaluation.EffectiveScore = heuristicScore*(1-profile.ReferenceWeight) + referenceScore*profile.ReferenceWeight
		evaluation.FailureReasons = append(evaluation.FailureReasons, failedReferences...)
	default:
		return nil, invalidQualityProfile("stored quality profile evaluation mode is invalid")
	}

	switch {
	case evaluation.EffectiveScore >= passThreshold:
		evaluation.Status = model.QualityStatusPass
	case evaluation.EffectiveScore >= warnThreshold:
		evaluation.Status = model.QualityStatusWarn
	default:
		evaluation.Status = model.QualityStatusFail
		evaluation.FailureReasons = append(evaluation.FailureReasons, fmt.Sprintf("effective score %.4f is below warn threshold %.4f", evaluation.EffectiveScore, warnThreshold))
	}
	if profile.EvaluationMode == model.QualityEvaluationHeuristicPlusReference && profile.ReferenceHardFail && len(failedReferencesFromEvaluation(evaluation)) > 0 {
		evaluation.Status = model.QualityStatusFail
	}
	return evaluation, nil
}

func (s *Service) RecordParseQuality(ctx context.Context, tenantID string, input ParseQualityRequest) (*model.ParseAttempt, *model.ParseQualityReport, *QualityEvaluation, error) {
	if strings.TrimSpace(input.DocumentID) == "" {
		return nil, nil, nil, invalidParseQuality("document_id is required")
	}
	if input.StartedAt.IsZero() || input.FinishedAt.IsZero() {
		return nil, nil, nil, invalidParseQuality("started_at and finished_at are required")
	}
	if input.FinishedAt.Before(input.StartedAt) {
		return nil, nil, nil, invalidParseQuality("finished_at must not be before started_at")
	}
	policy, err := s.resolveRecordPolicy(ctx, tenantID, input)
	if err != nil || policy == nil {
		if err == nil {
			return nil, nil, nil, notFoundParserPolicy()
		}
		return nil, nil, nil, err
	}
	evaluation, err := s.EvaluateParseQuality(ctx, tenantID, policy.QualityProfileID, input.HeuristicMetrics, input.ReferenceMetrics)
	if err != nil {
		return nil, nil, nil, err
	}

	now := time.Now().UTC()
	attempt := &model.ParseAttempt{
		ID: id.New(), TenantID: tenantID, DocumentID: input.DocumentID,
		ParserPolicyID: policy.ID, ParseMode: policy.ParseMode,
		StartedAt: input.StartedAt.UTC(), FinishedAt: input.FinishedAt.UTC(),
		QualityStatus: evaluation.Status, QualityScore: evaluation.EffectiveScore,
		FailureReason: strings.Join(evaluation.FailureReasons, "; "), CreatedAt: now,
	}
	report := &model.ParseQualityReport{
		ID: id.New(), TenantID: tenantID, DocumentID: input.DocumentID,
		ParserPolicyID: policy.ID, AttemptID: attempt.ID, ParseMode: policy.ParseMode,
		QualityStatus:  evaluation.Status,
		HeuristicScore: evaluation.HeuristicScore, ReferenceScore: evaluation.ReferenceScore,
		EffectiveScore: evaluation.EffectiveScore, CreatedAt: now,
	}
	metricsPayload, err := json.Marshal(evaluation.Metrics)
	if err != nil {
		return nil, nil, nil, err
	}
	report.Metrics = string(metricsPayload)
	if report.Metrics == "null" {
		report.Metrics = "{}"
	}

	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		attemptNo, err := tx.NextParseAttemptNo(ctx, tenantID, input.DocumentID)
		if err != nil {
			return err
		}
		attempt.AttemptNo = attemptNo
		switch evaluation.Status {
		case model.QualityStatusFail:
			evaluation.Action = s.parseFailAction(ctx, tenantID, tx, policy, attemptNo)
		case model.QualityStatusPass:
			evaluation.Action = model.GateActionPublish
		default:
			evaluation.Action = model.GateActionPublishWithRisk
		}
		report.GateAction = evaluation.Action
		if err := tx.CreateParseAttempt(ctx, attempt); err != nil {
			return err
		}
		if err := tx.CreateParseQualityReport(ctx, report); err != nil {
			return err
		}
		detail, err := json.Marshal(map[string]interface{}{
			"attempt_no":       attempt.AttemptNo,
			"parser_policy_id": policy.ID,
			"parse_mode":       policy.ParseMode,
			"quality_status":   evaluation.Status,
			"gate_action":      evaluation.Action,
			"effective_score":  evaluation.EffectiveScore,
		})
		if err != nil {
			return err
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
			UserID: input.UserID, Action: "parse_quality.record", Resource: "document",
			ResourceID: input.DocumentID, DetailJSON: string(detail), TraceID: input.TraceID,
			At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
			AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return attempt, report, evaluation, nil
}

func (s *Service) ListParseAttempts(ctx context.Context, tenantID, documentID string, page, pageSize int) ([]model.ParseAttempt, int64, error) {
	return s.Store.ListParseAttempts(ctx, tenantID, documentID, page, pageSize)
}

func (s *Service) LatestParseQualityReport(ctx context.Context, tenantID, documentID string) (*model.ParseQualityReport, error) {
	return s.Store.GetLatestParseQualityReport(ctx, tenantID, documentID)
}

func (s *Service) ListParseQualityReports(ctx context.Context, tenantID, documentID string, page, pageSize int) ([]model.ParseQualityReport, int64, error) {
	return s.Store.ListParseQualityReports(ctx, tenantID, documentID, page, pageSize)
}

func (s *Service) resolveRecordPolicy(ctx context.Context, tenantID string, input ParseQualityRequest) (*model.ParserPolicy, error) {
	if strings.TrimSpace(input.ParserPolicyID) != "" {
		return s.getParserPolicy(ctx, tenantID, input.ParserPolicyID)
	}
	return s.ResolveParserPolicy(ctx, tenantID, input.ProjectID, input.DatasetID, input.DocumentType)
}

func (s *Service) parseFailAction(ctx context.Context, tenantID string, store repository.Store, policy *model.ParserPolicy, attemptNo int64) string {
	if policy.FallbackPolicy == "" || policy.FallbackPolicy == "{}" {
		return model.GateActionQuarantine
	}
	var fallback ParserFallbackPolicy
	if err := json.Unmarshal([]byte(policy.FallbackPolicy), &fallback); err != nil || strings.TrimSpace(fallback.FallbackPolicyID) == "" {
		return model.GateActionQuarantine
	}
	maxAttempts := int64(2)
	if fallback.MaxAttempts != nil && *fallback.MaxAttempts > 0 {
		maxAttempts = int64(*fallback.MaxAttempts)
	}
	if attemptNo >= maxAttempts {
		return model.GateActionQuarantine
	}
	fallbackPolicy, err := store.GetParserPolicy(ctx, tenantID, fallback.FallbackPolicyID)
	if err != nil || fallbackPolicy == nil || !fallbackPolicy.Active {
		return model.GateActionQuarantine
	}
	return model.GateActionRetry
}

func parserPolicySpecificity(policy *model.ParserPolicy, projectID, datasetID, documentType string) int {
	switch {
	case policy.ProjectID == projectID && policy.DatasetID == datasetID && policy.DocumentType == documentType && projectID != "" && datasetID != "":
		return 5
	case policy.ProjectID == "" && policy.DatasetID == datasetID && policy.DocumentType == documentType && datasetID != "":
		return 4
	case policy.ProjectID == "" && policy.DatasetID == datasetID && policy.DocumentType == "" && datasetID != "":
		return 3
	case policy.ProjectID == "" && policy.DatasetID == "" && policy.DocumentType == documentType && documentType != "":
		return 2
	case policy.ProjectID == "" && policy.DatasetID == "" && policy.DocumentType == "":
		return 1
	default:
		return 0
	}
}

func heuristicHealthScores(profileMetrics map[string]QualityMetric, raw map[string]float64) (map[string]float64, []string) {
	scores := map[string]float64{}
	missing := []string{}
	for dimension, source := range heuristicMetricSources {
		metric, enabled := profileMetrics[dimension]
		if !enabled || !metric.Enabled {
			continue
		}
		value, ok := raw[source]
		if !ok {
			missing = append(missing, source)
			continue
		}
		switch dimension {
		case "encoding_health", "layout_health", "repetition_health":
			scores[dimension] = 1 - value
		case "text_health":
			emptyPageRatio := raw["empty_page_ratio"]
			if _, ok := raw["empty_page_ratio"]; !ok {
				missing = append(missing, "empty_page_ratio")
				continue
			}
			scores[dimension] = value * (1 - emptyPageRatio)
		default:
			scores[dimension] = value
		}
	}
	return scores, missing
}

func evaluateReferenceMetrics(metrics map[string]float64, thresholds map[string]float64) (float64, []string) {
	sum := 0.0
	failed := []string{}
	for _, name := range referenceMetricNames {
		value := metrics[name]
		sum += value
		threshold, ok := thresholds[name]
		if !ok {
			continue
		}
		if value < threshold {
			failed = append(failed, fmt.Sprintf("reference metric %s=%.4f is below threshold %.4f", name, value, threshold))
		}
	}
	return sum / float64(len(referenceMetricNames)), failed
}

func failedReferencesFromEvaluation(evaluation *QualityEvaluation) []string {
	failed := []string{}
	for _, reason := range evaluation.FailureReasons {
		if strings.HasPrefix(reason, "reference metric ") {
			failed = append(failed, reason)
		}
	}
	return failed
}

func validateQualityMetrics(metrics map[string]float64) error {
	for name, value := range metrics {
		if err := validateMetricValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateMetricValue(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return invalidParseQuality("quality metric " + name + " must be between 0 and 1")
	}
	return nil
}

func invalidParseQuality(message string) error { return httperr.New(400, 40083, message) }
