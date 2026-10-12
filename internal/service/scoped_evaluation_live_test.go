package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type scopedEvaluationLiveSample struct {
	CaseID                 string `json:"case_id"`
	PairedSampleID         string `json:"paired_sample_id"`
	Question               string `json:"question"`
	ExpectedCitationDocIDs string `json:"expected_citation_doc_ids"`
}

type scopedEvaluationLiveReport struct {
	RunID        string          `json:"run_id"`
	DatasetID    string          `json:"dataset_id"`
	SampleCount  int             `json:"sample_count"`
	TopK         int             `json:"top_k"`
	Status       string          `json:"status"`
	GateStatus   string          `json:"gate_status"`
	GateReason   string          `json:"gate_reason"`
	CompletedAt  time.Time       `json:"completed_at"`
	GateMetrics  json.RawMessage `json:"gate_metrics"`
	ArmSummaries json.RawMessage `json:"arm_summaries"`
}

func decodeScopedEvaluationLiveSamples(t *testing.T, path string) []scopedEvaluationLiveSample {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scoped evaluation live samples: %v", err)
	}
	var samples []scopedEvaluationLiveSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatalf("decode scoped evaluation live samples: %v", err)
	}
	if err := validateScopedEvaluationLiveSamples(samples); err != nil {
		t.Fatalf("invalid scoped evaluation live samples: %v", err)
	}
	return samples
}

func validateScopedEvaluationLiveSamples(samples []scopedEvaluationLiveSample) error {
	if len(samples) < 20 || len(samples) > 50 {
		return fmt.Errorf("must contain 20-50 pairs, got %d", len(samples))
	}
	seenCase := map[string]bool{}
	seenPair := map[string]bool{}
	for index, sample := range samples {
		if strings.TrimSpace(sample.CaseID) == "" || strings.TrimSpace(sample.PairedSampleID) == "" ||
			strings.TrimSpace(sample.Question) == "" || strings.TrimSpace(sample.ExpectedCitationDocIDs) == "" {
			return fmt.Errorf("sample %d requires case_id, paired_sample_id, question and expected_citation_doc_ids", index)
		}
		if seenCase[sample.CaseID] {
			return fmt.Errorf("duplicate case_id %q", sample.CaseID)
		}
		if seenPair[sample.PairedSampleID] {
			return fmt.Errorf("duplicate paired_sample_id %q", sample.PairedSampleID)
		}
		seenCase[sample.CaseID] = true
		seenPair[sample.PairedSampleID] = true
	}
	return nil
}

func TestParseScopedEvaluationLiveSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.json")
	samples := make([]scopedEvaluationLiveSample, 0, 20)
	for index := range 20 {
		samples = append(samples, scopedEvaluationLiveSample{
			CaseID: fmt.Sprintf("case-%02d", index+1), PairedSampleID: fmt.Sprintf("pair-%02d", index+1),
			Question: "What is X?", ExpectedCitationDocIDs: "doc-1",
		})
	}
	raw, _ := json.Marshal(samples)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got := decodeScopedEvaluationLiveSamples(t, path)
	if len(got) != 20 || got[0].CaseID != "case-01" {
		t.Fatalf("unexpected samples: %+v", got)
	}

	invalid := append(samples, scopedEvaluationLiveSample{
		CaseID: "case-duplicate-pair", PairedSampleID: "pair-01", Question: "Y",
		ExpectedCitationDocIDs: "doc-2",
	})
	if err := validateScopedEvaluationLiveSamples(invalid); err == nil ||
		!strings.Contains(err.Error(), `duplicate paired_sample_id "pair-01"`) {
		t.Fatalf("expected duplicate pair error, got %v", err)
	}
}

func TestLiveScopedEvaluationMinimalPairedAB(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	datasetID := os.Getenv("RGX_SCOPED_EVALUATION_LIVE_DATASET_ID")
	samplePath := os.Getenv("RGX_SCOPED_EVALUATION_LIVE_SAMPLE_FILE")
	if baseURL == "" || datasetID == "" || samplePath == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL, RGX_SCOPED_EVALUATION_LIVE_DATASET_ID and RGX_SCOPED_EVALUATION_LIVE_SAMPLE_FILE are required")
	}
	if os.Getenv("RGX_ALLOW_SCOPED_EVALUATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_SCOPED_EVALUATION_LIVE=1 is required for the real RAGFlow A/B drill")
	}

	samples := decodeScopedEvaluationLiveSamples(t, samplePath)
	svc, ctx, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = ragflow.NewHTTPClientWithMiddleware(
		baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4,
	)
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: datasetID,
		Name: "Scoped Evaluation Live " + id.New()[:8],
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create live dataset link: %v", err)
	}
	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: dataset.ID,
		StrategyType:       model.KnowledgeStrategyCompiled,
		RAGFlowCapability:  model.RAGFlowCapabilityKnowledgeCompilation,
		Config:             `{"compilation_template":"page_index"}`,
		FallbackStrategy:   model.KnowledgeStrategyHybrid,
		AuthorizationScope: `{"projects":[]}`, Active: true, CreatedBy: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatalf("create live strategy: %v", err)
	}
	caseManifest, err := json.Marshal(samples)
	if err != nil {
		t.Fatalf("encode live case manifest: %v", err)
	}

	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1,
		ChangeSummary: "real RAGFlow scoped evaluation minimal A/B", Cases: caseManifest,
	})
	if err != nil {
		t.Fatalf("create live evaluation set: %v", err)
	}
	for index, sample := range samples {
		if _, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
			EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version,
			CaseID: sample.CaseID, CaseVersion: int64(index + 1),
			Question: sample.Question, ExpectedCitationDocIDs: sample.ExpectedCitationDocIDs,
			Metadata:       json.RawMessage(`{"capability":"page_index"}`),
			PairedSampleID: sample.PairedSampleID,
		}); err != nil {
			t.Fatalf("create live case %s: %v", sample.CaseID, err)
		}
	}

	run, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "live-minimal-v1", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "live-minimal-v1", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v1","top_k":10,"min_samples":20,
			"min_hit_lift_pp":2,"max_p95_latency_delta_ms":2000,
			"max_no_answer_lift_pp":0,"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`),
	})
	if err != nil {
		t.Fatalf("create live evaluation run: %v", err)
	}
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute live scoped evaluation: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("live scoped evaluation did not complete: %+v", completed)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatalf("decode live gate metrics: %v", err)
	}
	if int(metrics["pair_count"].(float64)) != len(samples) {
		t.Fatalf("expected %d paired samples, got %+v", len(samples), metrics)
	}
	t.Logf("live scoped evaluation run=%s gate=%s reason=%s pair_count=%d hit_lift_pp=%v p95_delta_ms=%v",
		completed.ID, completed.GateStatus, completed.GateReason,
		int(metrics["pair_count"].(float64)), metrics["hit_lift_pp"], metrics["p95_latency_delta_ms"])

	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil {
		t.Fatalf("list live case results: %v", err)
	}
	if len(results) != len(samples)*2 {
		t.Fatalf("expected baseline and experiment result per sample, got %d", len(results))
	}
	if reportPath := os.Getenv("RGX_SCOPED_EVALUATION_LIVE_REPORT"); reportPath != "" {
		armSummaries := summarizeScopedEvaluationArms(results)
		encodedArms, _ := json.Marshal(armSummaries)
		report := scopedEvaluationLiveReport{
			RunID: completed.ID, DatasetID: datasetID, SampleCount: len(samples), TopK: 10,
			Status: completed.Status, GateStatus: completed.GateStatus, GateReason: completed.GateReason,
			CompletedAt: time.Now().UTC(), GateMetrics: json.RawMessage(completed.Metrics),
			ArmSummaries: encodedArms,
		}
		encoded, encodeErr := json.MarshalIndent(report, "", "  ")
		if encodeErr != nil {
			t.Fatalf("encode live report: %v", encodeErr)
		}
		if dir := filepath.Dir(reportPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("create live report directory: %v", err)
			}
		}
		if err := os.WriteFile(reportPath, encoded, 0o600); err != nil {
			t.Fatalf("write live report: %v", err)
		}
	}
}

func summarizeScopedEvaluationArms(results []model.EvaluationCaseResult) map[string]map[string]interface{} {
	summaries := map[string]map[string]interface{}{}
	for _, arm := range []string{model.EvaluationArmBaseline, model.EvaluationArmExperiment} {
		count, hits, answers, noAnswers := 0, 0, 0, 0
		var citationTotal float64
		citationCount := 0
		var latencyTotal int64
		for _, result := range results {
			if result.ExperimentArm != arm {
				continue
			}
			count++
			if result.HitAtK != nil && *result.HitAtK {
				hits++
			}
			if result.AnswerCorrect != nil && *result.AnswerCorrect {
				answers++
			}
			if result.NoAnswer != nil && *result.NoAnswer {
				noAnswers++
			}
			if result.CitationPrecision != nil {
				citationTotal += *result.CitationPrecision
				citationCount++
			}
			latencyTotal += result.LatencyMs
		}
		summary := map[string]interface{}{"count": count, "hit_count": hits, "answer_correct_count": answers, "no_answer_count": noAnswers}
		if count > 0 {
			summary["mean_latency_ms"] = latencyTotal / int64(count)
		}
		if citationCount > 0 {
			summary["mean_citation_precision"] = citationTotal / float64(citationCount)
		}
		summaries[arm] = summary
	}
	return summaries
}
