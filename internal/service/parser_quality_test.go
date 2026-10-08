package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func balancedQualityProfileInput(name string) QualityProfileInput {
	return QualityProfileInput{
		Name: name,
		Metrics: map[string]QualityMetric{
			"text_health":       {Weight: .20, Enabled: true},
			"structure_health":  {Weight: .20, Enabled: true},
			"encoding_health":   {Weight: .20, Enabled: true},
			"table_health":      {Weight: .20, Enabled: true},
			"layout_health":     {Weight: .15, Enabled: true},
			"repetition_health": {Weight: .05, Enabled: true},
		},
		Thresholds:     map[string]float64{"heuristic_pass": .90, "heuristic_warn": .70},
		EvaluationMode: model.QualityEvaluationHeuristicOnly,
		Active:         &[]bool{true}[0],
	}
}

func balancedRawMetrics() map[string]float64 {
	return map[string]float64{
		"text_density":                1,
		"empty_page_ratio":            0,
		"column_count_consistency":    1,
		"garbled_char_ratio":          0,
		"table_structure_consistency": 1,
		"reading_order_anomaly":       0,
		"repeated_header_ratio":       0,
	}
}

func TestQualityGateEvaluatesAndPersistsParseAttempt(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", balancedQualityProfileInput("balanced-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attempt, report, evaluation, err := svc.RecordParseQuality(ctx, "tenant-1", ParseQualityRequest{
		DocumentID: "doc-1", ParserPolicyID: policy.ID,
		StartedAt: now.Add(-time.Second), FinishedAt: now,
		HeuristicMetrics: balancedRawMetrics(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.AttemptNo != 1 || evaluation.Status != model.QualityStatusPass || evaluation.Action != model.GateActionPublish {
		t.Fatalf("unexpected pass result: attempt=%+v evaluation=%+v", attempt, evaluation)
	}
	if report.QualityStatus != model.QualityStatusPass || report.GateAction != model.GateActionPublish || report.AttemptID != attempt.ID {
		t.Fatalf("unexpected report: %+v", report)
	}
	attempts, total, err := svc.ListParseAttempts(ctx, "tenant-1", "doc-1", 1, 10)
	if err != nil || total != 1 || len(attempts) != 1 || attempts[0].AttemptNo != 1 {
		t.Fatalf("unexpected attempts: total=%d items=%+v err=%v", total, attempts, err)
	}
	latest, err := svc.LatestParseQualityReport(ctx, "tenant-1", "doc-1")
	if err != nil || latest == nil || latest.ID != report.ID {
		t.Fatalf("latest report not found: %v %+v", err, latest)
	}
}

func TestQualityGateFailsWithoutRequiredMetrics(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", balancedQualityProfileInput("balanced-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, _, _, err = svc.RecordParseQuality(ctx, "tenant-1", ParseQualityRequest{
		DocumentID: "doc-missing", ParserPolicyID: policy.ID,
		StartedAt: now.Add(-time.Second), FinishedAt: now,
		HeuristicMetrics: map[string]float64{"text_density": 1},
	})
	assertHTTPError(t, err, 409, 40982)
}

func TestQualityGateQuarantinesWithoutFallback(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", balancedQualityProfileInput("balanced-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	raw := balancedRawMetrics()
	for _, name := range []string{"text_density", "column_count_consistency", "table_structure_consistency"} {
		raw[name] = 0
	}
	now := time.Now().UTC()
	_, _, evaluation, err := svc.RecordParseQuality(ctx, "tenant-1", ParseQualityRequest{
		DocumentID: "doc-fail", ParserPolicyID: policy.ID,
		StartedAt: now.Add(-time.Second), FinishedAt: now,
		HeuristicMetrics: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Status != model.QualityStatusFail || evaluation.Action != model.GateActionQuarantine {
		t.Fatalf("unexpected fail result: %+v", evaluation)
	}
}

func TestQualityGateReferenceHardFailHasVeto(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	profile := &model.QualityProfile{
		ID: "profile-reference", TenantID: "tenant-1", Name: "reference-quality-v1",
		Metrics:        `{"text_health":{"weight":1,"enabled":true}}`,
		Thresholds:     `{"table_recall":0.9,"reading_order_accuracy":0.9,"header_footer_precision":0.9,"ocr_character_accuracy":0.9}`,
		EvaluationMode: model.QualityEvaluationHeuristicPlusReference, ReferenceWeight: .2,
		ReferenceHardFail: true, Active: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	reference := map[string]float64{
		"table_recall": 1, "reading_order_accuracy": 1,
		"header_footer_precision": 1, "ocr_character_accuracy": .62,
	}
	_, _, evaluation, err := svc.RecordParseQuality(ctx, "tenant-1", ParseQualityRequest{
		DocumentID: "doc-reference", ParserPolicyID: policy.ID,
		StartedAt: now.Add(-time.Second), FinishedAt: now,
		HeuristicMetrics: map[string]float64{"text_density": 1, "empty_page_ratio": 0},
		ReferenceMetrics: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Status != model.QualityStatusFail || evaluation.Action != model.GateActionQuarantine {
		t.Fatalf("reference hard fail was overridden: %+v", evaluation)
	}
}

func TestQualityGateRetryExhaustsConfiguredAttempts(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", balancedQualityProfileInput("balanced-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	primary, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	fallbackInput := parserPolicyInput(profile.ID)
	fallbackInput.DocumentType = "pdf_text"
	fallbackInput.ChunkMethod = "general"
	fallback, err := svc.CreateParserPolicy(ctx, "tenant-1", fallbackInput)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"fallback_policy_id":"` + fallback.ID + `","max_attempts":2}`)
	if _, err := svc.UpdateParserPolicy(ctx, "tenant-1", primary.ID, ParserPolicyPatchInput{FallbackPolicy: &raw}); err != nil {
		t.Fatal(err)
	}

	failed := balancedRawMetrics()
	for _, name := range []string{"text_density", "column_count_consistency", "table_structure_consistency"} {
		failed[name] = 0
	}
	now := time.Now().UTC()
	_, _, first, err := svc.RecordParseQuality(ctx, "tenant-1", ParseQualityRequest{
		DocumentID: "doc-retry", ParserPolicyID: primary.ID,
		StartedAt: now.Add(-2 * time.Second), FinishedAt: now.Add(-time.Second),
		HeuristicMetrics: failed,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, second, err := svc.RecordParseQuality(ctx, "tenant-1", ParseQualityRequest{
		DocumentID: "doc-retry", ParserPolicyID: primary.ID,
		StartedAt: now.Add(-time.Second), FinishedAt: now,
		HeuristicMetrics: failed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != model.QualityStatusFail || first.Action != model.GateActionRetry {
		t.Fatalf("expected first retry, got %+v", first)
	}
	if second.Status != model.QualityStatusFail || second.Action != model.GateActionQuarantine {
		t.Fatalf("expected exhausted attempts to quarantine, got %+v", second)
	}
}

func TestParserPolicyResolverUsesMostSpecificActivePolicy(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", balancedQualityProfileInput("balanced-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dataset := &model.DatasetLink{ID: "dataset-1", TenantID: "tenant-1", RAGFlowDatasetID: "rf-dataset-1", Name: "Dataset One", CreatedAt: now, UpdatedAt: now}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	globalInput := parserPolicyInput(profile.ID)
	globalInput.DocumentType = ""
	if _, err := svc.CreateParserPolicy(ctx, "tenant-1", globalInput); err != nil {
		t.Fatal(err)
	}
	specificInput := parserPolicyInput(profile.ID)
	specificInput.DatasetID = "dataset-1"
	if _, err := svc.CreateParserPolicy(ctx, "tenant-1", specificInput); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.ResolveParserPolicy(ctx, "tenant-1", "", "dataset-1", "pdf_table")
	if err != nil || policy == nil || policy.DatasetID != "dataset-1" {
		t.Fatalf("expected dataset-specific policy, got %v %+v", err, policy)
	}
}
