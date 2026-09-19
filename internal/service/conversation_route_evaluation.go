package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

const routeEvaluationSourceDoc41 = "doc41-real-scenarios"

const (
	RouteGateEvidencePilot      = "pilot"
	RouteGateEvidenceProduction = "production"
)

type RouteEvalCaseInput struct {
	Question     string `json:"question"`
	ExpectedKind string `json:"expected_kind"`
	ExpectedID   string `json:"expected_id"`
	ExpectedName string `json:"expected_name"`
	Split        string `json:"split"`
	CaseType     string `json:"case_type"`
	Routable     *bool  `json:"routable"`
	MustDeny     bool   `json:"must_deny"`
}

type RouteEvaluationRequest struct {
	Name          string               `json:"name"`
	Source        string               `json:"source"`
	EvidenceLevel string               `json:"evidence_level"`
	Cases         []RouteEvalCaseInput `json:"cases"`
}

type RouteEvaluationMetrics struct {
	CaseCount                  int                   `json:"case_count"`
	EvaluatedCount             int                   `json:"evaluated_count"`
	TrainCount                 int                   `json:"train_count"`
	ValidationCount            int                   `json:"validation_count"`
	Top1Accuracy               float64               `json:"top1_accuracy"`
	Top3Recall                 float64               `json:"top3_recall"`
	HighConfidenceCount        int                   `json:"high_confidence_count"`
	HighConfidencePrecision    float64               `json:"high_confidence_precision"`
	HighConfidenceCoverage     float64               `json:"high_confidence_coverage"`
	HighConfidenceWilsonLower  float64               `json:"high_confidence_wilson_lower_95"`
	HighConfidenceMinimum      int                   `json:"high_confidence_minimum"`
	AgentFlowReadinessMinimum  float64               `json:"agent_flow_readiness_minimum"`
	AgentFlowCheckedCount      int                   `json:"agent_flow_checked_count"`
	AgentFlowReadyCount        int                   `json:"agent_flow_ready_count"`
	AgentFlowBlockedCount      int                   `json:"agent_flow_blocked_count"`
	ManualOverrideRate         float64               `json:"manual_override_rate"`
	ClarificationAccuracy      float64               `json:"clarification_accuracy"`
	ClarificationSampleCount   int                   `json:"clarification_sample_count"`
	PermissionLeakage          int                   `json:"permission_leakage"`
	OfflineErrorAutoExecutions int                   `json:"offline_error_auto_executions"`
	ECE                        float64               `json:"ece"`
	BrierScore                 float64               `json:"brier_score"`
	ReliabilityCurve           []RouteReliabilityBin `json:"reliability_curve"`
	AllowAutoLowRisk           bool                  `json:"allow_auto_low_risk"`
	EvidenceLevel              string                `json:"evidence_level"`
	GateState                  string                `json:"gate_state"`
	GateFailures               []string              `json:"gate_failures"`
}

type RouteReliabilityBin struct {
	Lower         float64 `json:"lower"`
	Upper         float64 `json:"upper"`
	PredictedMean float64 `json:"predicted_mean"`
	ActualMean    float64 `json:"actual_mean"`
	Count         int     `json:"count"`
}

type RouteBoundaryMatrix struct {
	Rows []RouteBoundaryRow `json:"rows"`
}

type RouteBoundaryRow struct {
	Expected     string  `json:"expected"`
	Predicted    string  `json:"predicted"`
	SampleCount  int     `json:"sample_count"`
	Top1Accuracy float64 `json:"top1_accuracy"`
	MeanMargin   float64 `json:"mean_margin"`
}

type RouteCalibration struct {
	Version         string    `json:"version"`
	Method          string    `json:"method"`
	Intercept       float64   `json:"intercept"`
	Weights         []float64 `json:"weights"`
	FeatureNames    []string  `json:"feature_names"`
	TrainCount      int       `json:"train_count"`
	ValidationCount int       `json:"validation_count"`
	Validated       bool      `json:"validated"`
}

type RouteEvaluationReport struct {
	Run         *model.RouteEvaluationRun `json:"run"`
	Metrics     RouteEvaluationMetrics    `json:"metrics"`
	Boundary    RouteBoundaryMatrix       `json:"boundary"`
	Calibration RouteCalibration          `json:"calibration"`
	Suggestions []string                  `json:"suggestions"`
}

type routeEvalFeatureRow struct {
	normalizedScore float64
	margin          float64
	candidates      int
	competitor      bool
	readiness       float64
	length          float64
	chinese         bool
	flowReadiness   float64
}

type routeEvalResult struct {
	caseInput        RouteEvalCaseInput
	features         routeEvalFeatureRow
	expectedKey      string
	predictedKey     string
	secondKey        string
	top1Correct      bool
	top3Correct      bool
	label            float64
	confidence       float64
	normalizedMargin float64
}

type routeEvidenceThresholds struct {
	Level             string
	MinHighConfidence int
	MinWilsonLower    float64
	MaxECE            float64
}

// RunRouteEvaluation performs the M2.5 offline discovery gate. It never turns
// on auto routing itself; a passing report is only a prerequisite.
func (s *Service) RunRouteEvaluation(ctx context.Context, actorID, tenantID string, request RouteEvaluationRequest) (*RouteEvaluationReport, error) {
	if err := s.Authorize(ctx, actorID, "manage", "assistant"); err != nil {
		return nil, err
	}
	cases := request.Cases
	source := strings.TrimSpace(request.Source)
	evidenceLevel := normalizeRouteEvidenceLevel(request.EvidenceLevel)
	if len(cases) == 0 {
		cases = Doc41RouteEvaluationCases()
		source = routeEvaluationSourceDoc41
	}
	if source == "" {
		source = "manual"
	}
	if len(cases) == 0 {
		return nil, httperr.BadRequest(40100, "route evaluation cases are required")
	}
	for index, evalCase := range cases {
		if strings.TrimSpace(evalCase.Question) == "" {
			return nil, httperr.BadRequest(40101, "route evaluation case question is required")
		}
		if evalCase.ExpectedKind != "" && evalCase.ExpectedKind != model.AssistantKindChat && evalCase.ExpectedKind != model.AssistantKindAgent {
			return nil, httperr.BadRequest(40102, "route evaluation expected_kind is invalid")
		}
		cases[index].Question = strings.TrimSpace(evalCase.Question)
	}
	catalog, _, err := s.ListConversationAssistants(ctx, actorID, tenantID, "", nil, 1, 100)
	if err != nil {
		return nil, err
	}
	report := evaluateRouteCases(catalog, cases, s.routeEvidenceThresholdsFor(evidenceLevel), normalizeRoutePolicy(s.RoutePolicy).CandidateLimit)
	report.Metrics.AllowAutoLowRisk = report.Metrics.GateState == model.RouteEvalGatePassed
	now := time.Now().UTC()
	metricsJSON, err := json.Marshal(report.Metrics)
	if err != nil {
		return nil, err
	}
	boundaryJSON, err := json.Marshal(report.Boundary)
	if err != nil {
		return nil, err
	}
	calibrationJSON, err := json.Marshal(report.Calibration)
	if err != nil {
		return nil, err
	}
	run := &model.RouteEvaluationRun{
		ID: id.New(), TenantID: tenantID, Name: evaluationName(request.Name),
		Source: source, ActorID: actorID, RouterVersion: conversationRouterVersion,
		CalibrationVersion: report.Calibration.Version, NormalizationMethod: "sigmoid",
		CaseCount: report.Metrics.CaseCount, MetricsJSON: string(metricsJSON),
		BoundaryJSON: string(boundaryJSON), CalibrationJSON: string(calibrationJSON),
		GateState:        report.Metrics.GateState,
		AllowAutoLowRisk: report.Metrics.AllowAutoLowRisk, CreatedAt: now,
	}
	if err := s.Store.CreateRouteEvaluationRun(ctx, run); err != nil {
		return nil, err
	}
	report.Run = run
	return report, nil
}

func normalizeRouteEvidenceLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RouteGateEvidenceProduction:
		return RouteGateEvidenceProduction
	default:
		return RouteGateEvidencePilot
	}
}

func (s *Service) routeEvidenceThresholdsFor(level string) routeEvidenceThresholds {
	if level == RouteGateEvidenceProduction {
		return routeEvidenceThresholdsForPolicy(level, s.RoutePolicy.Gate.Production, 24, 0.99, 0.05)
	}
	return routeEvidenceThresholdsForPolicy(level, s.RoutePolicy.Gate.Pilot, 24, 0.85, 0.07)
}

func routeEvidenceThresholdsForPolicy(
	level string,
	threshold config.RouteEvidenceThreshold,
	defaultSamples int,
	defaultWilson float64,
	defaultECE float64,
) routeEvidenceThresholds {
	if threshold.MinHighConfidence <= 0 {
		threshold.MinHighConfidence = defaultSamples
	}
	if threshold.MinWilsonLower <= 0 || threshold.MinWilsonLower >= 1 {
		threshold.MinWilsonLower = defaultWilson
	}
	if threshold.MaxECE <= 0 || threshold.MaxECE >= 1 {
		threshold.MaxECE = defaultECE
	}
	return routeEvidenceThresholds{
		Level: level, MinHighConfidence: threshold.MinHighConfidence,
		MinWilsonLower: threshold.MinWilsonLower, MaxECE: threshold.MaxECE,
	}
}

func (s *Service) GetRouteEvaluationRun(ctx context.Context, tenantID, runID string) (*model.RouteEvaluationRun, error) {
	run, err := s.Store.GetRouteEvaluationRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("route evaluation run not found")
	}
	return run, nil
}

func (s *Service) ListRouteEvaluationRuns(ctx context.Context, tenantID string, page, pageSize int) ([]model.RouteEvaluationRun, int64, error) {
	return s.Store.ListRouteEvaluationRuns(ctx, tenantID, page, pageSize)
}

func evaluateRouteCases(catalog []ConversationAssistant, inputs []RouteEvalCaseInput, thresholds routeEvidenceThresholds, candidateLimit int) *RouteEvaluationReport {
	results := make([]routeEvalResult, 0, len(inputs))
	for _, input := range inputs {
		candidates := routeCandidatesFromCatalog(catalog, input.Question, candidateLimit)
		applyRouteCompetitors(candidates)
		expected := resolveRouteEvalTarget(catalog, input)
		expectedKey := routeTargetKey(expected.Kind, expected.ID)
		predictedKey, secondKey := "", ""
		if len(candidates) > 0 {
			predictedKey = routeTargetKey(candidates[0].Kind, candidates[0].TargetID)
		}
		if len(candidates) > 1 {
			secondKey = routeTargetKey(candidates[1].Kind, candidates[1].TargetID)
		}
		correct := expectedKey != "" && predictedKey == expectedKey
		top3 := expectedKey != "" && (predictedKey == expectedKey || secondKey == expectedKey)
		margin := 0.0
		if len(candidates) > 1 && candidates[0].NormalizedMargin != nil {
			margin = *candidates[0].NormalizedMargin
		}
		normalizedScore := 0.0
		if len(candidates) > 0 {
			normalizedScore = candidates[0].NormalizedScore
		}
		readiness := 0.0
		flowReadiness := 1.0
		if len(candidates) > 0 {
			readiness = candidates[0].RoutingReadiness
			flowReadiness = candidates[0].AgentFlowReadiness
		} else if input.ExpectedKind == model.AssistantKindAgent {
			flowReadiness = 0
		}
		results = append(results, routeEvalResult{
			caseInput: input, expectedKey: expectedKey, predictedKey: predictedKey,
			secondKey: secondKey, top1Correct: correct, top3Correct: top3,
			label: boolFloat(correct), normalizedMargin: margin,
			features: routeEvalFeatureRow{
				normalizedScore: normalizedScore, margin: margin,
				candidates: len(candidates), competitor: len(candidates) > 1,
				readiness: readiness, length: math.Min(float64(len([]rune(input.Question)))/100, 1),
				chinese: containsHan(input.Question), flowReadiness: flowReadiness,
			},
		})
	}
	assignRouteEvalSplits(results)
	calibration := fitRouteCalibration(results)
	for index := range results {
		if results[index].features.candidates == 0 {
			// The calibration model is trained only on candidate-backed rows.
			// Extrapolating it to an empty recall set produces false certainty.
			results[index].confidence = 0
		} else {
			results[index].confidence = routeCalibrationConfidence(calibration, results[index].features)
		}
	}
	metrics, boundary := summarizeRouteEvaluation(results, calibration, thresholds)
	return &RouteEvaluationReport{
		Metrics: metrics, Boundary: boundary, Calibration: calibration,
		Suggestions: routeMetadataSuggestions(results),
	}
}

func resolveRouteEvalTarget(catalog []ConversationAssistant, input RouteEvalCaseInput) ConversationAssistant {
	expectedID, expectedName := strings.TrimSpace(input.ExpectedID), strings.TrimSpace(input.ExpectedName)
	for _, item := range catalog {
		if input.ExpectedKind != "" && item.Kind != input.ExpectedKind {
			continue
		}
		if expectedID != "" && item.ID == expectedID {
			return item
		}
		if expectedName != "" && strings.EqualFold(item.Name, expectedName) {
			return item
		}
	}
	return ConversationAssistant{}
}

func assignRouteEvalSplits(results []routeEvalResult) {
	validationSeen := false
	for index := range results {
		split := strings.ToLower(strings.TrimSpace(results[index].caseInput.Split))
		switch split {
		case model.RouteEvalSplitTrain, model.RouteEvalSplitValidation, model.RouteEvalSplitHoldout:
			results[index].caseInput.Split = split
		default:
			if results[index].caseInput.CaseType == model.RouteEvalCaseAmbiguous {
				// Ambiguous cases are evaluation-only; putting them into the
				// calibration training set would teach the model to avoid
				// clarification and hide low-confidence behavior.
				results[index].caseInput.Split = model.RouteEvalSplitValidation
				validationSeen = true
				continue
			}
			// The initial doc41 set uses a deterministic time-like split so
			// calibration is not scored on its own training rows.
			if index%10 < 7 {
				results[index].caseInput.Split = model.RouteEvalSplitTrain
			} else {
				results[index].caseInput.Split = model.RouteEvalSplitValidation
				validationSeen = true
			}
		}
	}
	if !validationSeen {
		for index := len(results) - 1; index >= 0; index-- {
			if strings.EqualFold(results[index].caseInput.Split, model.RouteEvalSplitTrain) {
				results[index].caseInput.Split = model.RouteEvalSplitValidation
				return
			}
		}
	}
}

func fitRouteCalibration(results []routeEvalResult) RouteCalibration {
	featureNames := []string{"normalized_score", "normalized_margin", "candidate_count", "has_competitor", "routing_readiness", "query_length"}
	weights := make([]float64, len(featureNames))
	intercept := -0.5
	learningRate := 0.5
	for epoch := 0; epoch < 20000; epoch++ {
		gradient := make([]float64, len(weights))
		interceptGradient := 0.0
		count := 0
		for _, result := range results {
			if result.caseInput.Split != model.RouteEvalSplitTrain {
				continue
			}
			features := routeCalibrationFeatures(result.features)
			predicted := routeLogistic(dotProduct(weights, features) + intercept)
			errorValue := predicted - result.label
			for index, value := range features {
				gradient[index] += errorValue * value
			}
			interceptGradient += errorValue
			count++
		}
		if count == 0 {
			break
		}
		for index := range weights {
			weights[index] -= learningRate * (gradient[index]/float64(count) + 0.001*weights[index])
		}
		intercept -= learningRate * interceptGradient / float64(count)
	}
	trainCount, validationCount := 0, 0
	for _, result := range results {
		switch result.caseInput.Split {
		case model.RouteEvalSplitTrain:
			trainCount++
		case model.RouteEvalSplitValidation:
			validationCount++
		}
	}
	return RouteCalibration{
		Version: "m25-doc41-v1", Method: "l2_logistic_regression", Intercept: intercept,
		Weights: weights, FeatureNames: featureNames, TrainCount: trainCount,
		ValidationCount: validationCount, Validated: trainCount > 0 && validationCount > 0,
	}
}

func routeCalibrationConfidence(calibration RouteCalibration, row routeEvalFeatureRow) float64 {
	return routeLogistic(dotProduct(calibration.Weights, routeCalibrationFeatures(row)) + calibration.Intercept)
}

func routeCalibrationFeatures(row routeEvalFeatureRow) []float64 {
	margin := row.margin
	if row.candidates < 2 {
		margin = 0
	}
	candidateCount := math.Min(float64(row.candidates)/5, 1)
	competitor := boolFloat(row.competitor)
	return []float64{row.normalizedScore, margin, candidateCount, competitor, row.readiness, row.length}
}

func summarizeRouteEvaluation(results []routeEvalResult, calibration RouteCalibration, thresholds routeEvidenceThresholds) (RouteEvaluationMetrics, RouteBoundaryMatrix) {
	evalResults := routeSplitResults(results, model.RouteEvalSplitValidation)
	if len(evalResults) == 0 {
		evalResults = results
	}
	positiveCount, top1Correct, top3Correct := 0, 0, 0
	highConfidenceCount, highConfidenceCorrect := 0, 0
	ambiguousCount, clarified := 0, 0
	permissionLeakage := 0
	brierSum := 0.0
	reliability := make([]RouteReliabilityBin, 10)
	for index := range reliability {
		reliability[index].Lower = float64(index) / 10
		reliability[index].Upper = float64(index+1) / 10
		if index == 9 {
			reliability[index].Upper = 1
		}
	}
	for _, result := range evalResults {
		isExpected := result.expectedKey != ""
		if isExpected {
			positiveCount++
			if result.top1Correct {
				top1Correct++
			}
			if result.top3Correct {
				top3Correct++
			}
		} else {
			ambiguousCount++
		}
		// A single candidate can be high-confidence evidence, but M3 policy
		// still requires at least two candidates before auto routing.
		hasMargin := result.features.candidates < 2 || result.normalizedMargin >= 0.12
		highConfidence := result.confidence >= 0.86 && hasMargin && result.features.candidates >= 1
		if highConfidence {
			highConfidenceCount++
			if result.top1Correct {
				highConfidenceCorrect++
			}
		}
		if result.caseInput.MustDeny && result.features.candidates > 0 {
			permissionLeakage++
		}
		if !isExpected && (result.features.candidates == 0 || result.confidence < 0.86) {
			clarified++
		}
		brierSum += math.Pow(result.confidence-result.label, 2)
		binIndex := int(math.Floor(math.Min(math.Max(result.confidence, 0), 0.999) * 10))
		bin := &reliability[binIndex]
		bin.Count++
		bin.PredictedMean += result.confidence
		bin.ActualMean += result.label
	}
	metrics := RouteEvaluationMetrics{
		CaseCount: len(results), EvaluatedCount: len(evalResults),
		TrainCount: calibration.TrainCount, ValidationCount: calibration.ValidationCount,
		HighConfidenceCount:        highConfidenceCount,
		ClarificationSampleCount:   ambiguousCount,
		HighConfidenceMinimum:      thresholds.MinHighConfidence,
		OfflineErrorAutoExecutions: 0,
		EvidenceLevel:              thresholds.Level,
	}
	if positiveCount > 0 {
		metrics.Top1Accuracy = float64(top1Correct) / float64(positiveCount)
		metrics.Top3Recall = float64(top3Correct) / float64(positiveCount)
	}
	for _, result := range evalResults {
		if result.caseInput.ExpectedKind != model.AssistantKindAgent {
			continue
		}
		metrics.AgentFlowCheckedCount++
		if result.features.flowReadiness >= model.AgentFlowReadinessThreshold {
			metrics.AgentFlowReadyCount++
		} else {
			metrics.AgentFlowBlockedCount++
		}
	}
	metrics.AgentFlowReadinessMinimum = model.AgentFlowReadinessThreshold
	if highConfidenceCount > 0 {
		metrics.HighConfidencePrecision = float64(highConfidenceCorrect) / float64(highConfidenceCount)
		metrics.HighConfidenceWilsonLower = wilsonLower95(highConfidenceCorrect, highConfidenceCount)
	}
	eligible := 0
	for _, result := range evalResults {
		if routeIsRoutable(result.caseInput) {
			eligible++
		}
	}
	if eligible > 0 {
		metrics.HighConfidenceCoverage = float64(highConfidenceCount) / float64(eligible)
	}
	if highConfidenceCount > 0 {
		metrics.ManualOverrideRate = float64(highConfidenceCount-highConfidenceCorrect) / float64(highConfidenceCount)
	}
	if ambiguousCount > 0 {
		metrics.ClarificationAccuracy = float64(clarified) / float64(ambiguousCount)
	}
	metrics.PermissionLeakage = permissionLeakage
	if len(evalResults) > 0 {
		metrics.BrierScore = brierSum / float64(len(evalResults))
	}
	eceSum := 0.0
	for index := range reliability {
		if reliability[index].Count > 0 {
			reliability[index].PredictedMean /= float64(reliability[index].Count)
			reliability[index].ActualMean /= float64(reliability[index].Count)
			eceSum += float64(reliability[index].Count) / float64(len(evalResults)) *
				math.Abs(reliability[index].PredictedMean-reliability[index].ActualMean)
		}
	}
	metrics.ECE = eceSum
	metrics.ReliabilityCurve = reliability
	metrics.GateFailures = routeGateFailures(metrics, thresholds)
	if len(metrics.GateFailures) == 0 {
		metrics.GateState = model.RouteEvalGatePassed
	} else if metrics.HighConfidenceCount < metrics.HighConfidenceMinimum {
		metrics.GateState = model.RouteEvalGateInsuffi
	} else {
		metrics.GateState = model.RouteEvalGateFailed
	}
	return metrics, routeBoundaryMatrix(evalResults)
}

func routeGateFailures(metrics RouteEvaluationMetrics, thresholds routeEvidenceThresholds) []string {
	failures := []string{}
	if metrics.Top1Accuracy < 0.80 {
		failures = append(failures, "top1_accuracy_below_0.80")
	}
	if metrics.Top3Recall < 0.92 {
		failures = append(failures, "top3_recall_below_0.92")
	}
	if metrics.HighConfidenceCount < thresholds.MinHighConfidence {
		failures = append(failures, fmt.Sprintf("high_confidence_samples_below_%d", thresholds.MinHighConfidence))
	}
	if metrics.HighConfidenceWilsonLower < thresholds.MinWilsonLower {
		failures = append(failures, fmt.Sprintf("high_confidence_wilson_lower_95_below_%.2f", thresholds.MinWilsonLower))
	}
	if metrics.PermissionLeakage != 0 {
		failures = append(failures, "permission_leakage_not_zero")
	}
	if metrics.OfflineErrorAutoExecutions != 0 {
		failures = append(failures, "offline_error_auto_execution_not_zero")
	}
	if metrics.ECE > thresholds.MaxECE {
		failures = append(failures, fmt.Sprintf("ece_above_%.2f", thresholds.MaxECE))
	}
	if metrics.BrierScore > 0.10 {
		failures = append(failures, "brier_score_above_0.10")
	}
	if metrics.ManualOverrideRate > 0.10 {
		failures = append(failures, "manual_override_rate_above_0.10")
	}
	if metrics.ClarificationSampleCount > 0 && metrics.ClarificationAccuracy < 0.90 {
		failures = append(failures, "clarification_accuracy_below_0.90")
	}
	if metrics.AgentFlowBlockedCount > 0 {
		failures = append(failures, fmt.Sprintf("agent_flow_readiness_below_%.2f", metrics.AgentFlowReadinessMinimum))
	}
	return failures
}

func routeBoundaryMatrix(results []routeEvalResult) RouteBoundaryMatrix {
	type key struct{ expected, predicted string }
	groups := map[key]*RouteBoundaryRow{}
	order := []key{}
	for _, result := range results {
		item := key{expected: result.expectedKey, predicted: result.predictedKey}
		row, ok := groups[item]
		if !ok {
			row = &RouteBoundaryRow{Expected: result.expectedKey, Predicted: result.predictedKey}
			groups[item] = row
			order = append(order, item)
		}
		row.SampleCount++
		row.MeanMargin += result.normalizedMargin
		if result.top1Correct {
			row.Top1Accuracy++
		}
	}
	rows := make([]RouteBoundaryRow, 0, len(groups))
	for _, item := range order {
		row := groups[item]
		row.MeanMargin /= float64(row.SampleCount)
		row.Top1Accuracy /= float64(row.SampleCount)
		rows = append(rows, *row)
	}
	return RouteBoundaryMatrix{Rows: rows}
}

func routeMetadataSuggestions(results []routeEvalResult) []string {
	suggestions := []string{}
	seen := map[string]bool{}
	for _, result := range results {
		if result.top1Correct || result.expectedKey == "" || result.predictedKey == "" {
			continue
		}
		suggestion := result.expectedKey + " needs keywords/examples to separate from " + result.predictedKey
		if !seen[suggestion] {
			seen[suggestion] = true
			suggestions = append(suggestions, suggestion)
		}
	}
	return suggestions
}

func routeSplitResults(results []routeEvalResult, split string) []routeEvalResult {
	out := make([]routeEvalResult, 0, len(results))
	for _, result := range results {
		if result.caseInput.Split == split {
			out = append(out, result)
		}
	}
	return out
}

func routeIsRoutable(input RouteEvalCaseInput) bool {
	if input.Routable != nil {
		return *input.Routable
	}
	caseType := strings.ToLower(strings.TrimSpace(input.CaseType))
	return caseType == model.RouteEvalCasePositive || caseType == model.RouteEvalCaseHardNegative
}

func routeTargetKey(kind, targetID string) string {
	if kind == "" || targetID == "" {
		return ""
	}
	return kind + ":" + targetID
}

func wilsonLower95(successes, trials int) float64 {
	if trials == 0 {
		return 0
	}
	p := float64(successes) / float64(trials)
	z := 1.959963984540054
	denominator := 1 + z*z/float64(trials)
	center := p + z*z/(2*float64(trials))
	margin := z * math.Sqrt((p*(1-p)+z*z/(4*float64(trials)))/float64(trials))
	return math.Max(0, (center-margin)/denominator)
}

func routeLogistic(value float64) float64 { return 1 / (1 + math.Exp(-value)) }

func dotProduct(weights, features []float64) float64 {
	sum := 0.0
	for index, value := range features {
		if index < len(weights) {
			sum += weights[index] * value
		}
	}
	return sum
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func evaluationName(name string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return "统一对话路由 M2.5 评估"
}
