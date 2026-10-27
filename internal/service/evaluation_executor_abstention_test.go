package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Abstention calibration (223 T3 拒答校准初版): the v4 protocol may declare a
// no-answer stance (abstention_policy) plus two pre-registered budgets - an
// abstention precision floor and a confident-wrong cap. The paired gate folds
// an abstention_calibration report into the metrics and reports WAIT when a
// non-zero budget is exceeded. Recalibration therefore requires a fresh
// protocol registration (new hash), keeping 校准 from silently drifting.

// scopedPreregistrationV4AbstentionProtocol is the canonical abstention-enabled
// v4 protocol: the low-confidence empty-answer stance with the KnowEval-derived
// budgets (拒答准确率 ≥80% floor, confident_wrong ≤20% cap).
const scopedPreregistrationV4AbstentionProtocol = `{
	"contract_version":"scoped-paired-ab-v4","top_k":8,"min_samples":1,
	"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
	"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
	"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,
	"retrieval_mode":"compiled_first","exclude_internal_rows":true,
	"compiled_first_max_chunks":2,
	"answer_correctness_method":"expected_evidence_match",
	"require_citation_precision":true,
	"abstention_policy":"abstention_low_confidence",
	"min_abstain_precision_pp":80,
	"max_confident_wrong_pp":20
}`

func TestParseScopedEvaluationProtocolV4Abstention(t *testing.T) {
	parsed, err := parseScopedEvaluationProtocol(scopedPreregistrationV4AbstentionProtocol)
	if err != nil {
		t.Fatalf("abstention-enabled v4 protocol must parse: %v", err)
	}
	if parsed.AbstentionPolicy != scopedAbstentionPolicyLowConfidence ||
		parsed.MinAbstainPrecisionPP != 80 || parsed.MaxConfidentWrongPP != 20 {
		t.Fatalf("abstention fields were not parsed: %+v", parsed)
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"unknown policy", `{"contract_version":"scoped-paired-ab-v4","retrieval_mode":"compiled_first","exclude_internal_rows":true,"compiled_first_max_chunks":2,"abstention_policy":"abstention_always_answer"}`, true},
		{"precision above 100", `{"contract_version":"scoped-paired-ab-v4","retrieval_mode":"compiled_first","exclude_internal_rows":true,"compiled_first_max_chunks":2,"min_abstain_precision_pp":120}`, true},
		{"confident wrong negative", `{"contract_version":"scoped-paired-ab-v4","retrieval_mode":"compiled_first","exclude_internal_rows":true,"compiled_first_max_chunks":2,"max_confident_wrong_pp":-1}`, true},
		{"abstention on v3", `{"contract_version":"scoped-paired-ab-v3","primary_metric":"citation_precision","min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,"abstention_policy":"abstention_hint_gap"}`, true},
		{"abstention on v1", `{"contract_version":"scoped-paired-ab-v1","max_confident_wrong_pp":20}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseScopedEvaluationProtocol(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parse result mismatch: err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// abstainGateProtocol returns a gate-only protocol (parse validation not
// involved) whose non-calibration thresholds permit a single paired case to
// pass, isolating the abstention-calibration verdicts.
func abstainGateProtocol() scopedEvaluationProtocol {
	return scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersionV4, MinSamples: 1, MinHitLiftPP: 0,
		MaxNoAnswerLiftPP: 0, MaxP95LatencyDeltaMS: 300,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
		RequireTaskTypeCoverage: true, RequiredTaskTypes: []string{"project_state"}, MinTaskTypeSamples: 1,
		RetrievalMode: scopedRetrievalModeCompiledFirst, ExcludeInternalRows: true,
		CompiledFirstMaxChunks: 2, AbstentionPolicy: scopedAbstentionPolicyLowConfidence,
		MinAbstainPrecisionPP: 80, MaxConfidentWrongPP: 20,
	}
}

// abstainResult builds an experiment/baseline case with explicit abstention
// observables (no_answer, answer_correct, expected_evidence_present).
func abstainResult(caseID, taskType string, hit, answer, noAnswer, expectedPresent bool, latency int64) model.EvaluationCaseResult {
	metrics, _ := json.Marshal(map[string]interface{}{
		"task_type": taskType, "hit_at_k": hit, "answer_correct": answer, "no_answer": noAnswer,
		"expected_evidence_present": expectedPresent,
	})
	return model.EvaluationCaseResult{
		ID: caseID, CaseID: caseID, PairedSampleID: caseID, Metrics: string(metrics),
		Pass: &hit, HitAtK: &hit, AnswerCorrect: &answer, NoAnswer: &noAnswer, LatencyMs: latency,
	}
}

func TestScopedEvaluationGateWaitsOnLowAbstainPrecision(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"state-1": abstainResult("state-1", "project_state", false, false, true, false, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"state-1": abstainResult("state-1", "project_state", false, false, true, true, 20),
	}
	metrics, status, reason := scopedEvaluationGate(baseline, experiment, abstainGateProtocol())
	if status != model.EvaluationGateWait || reason != "abstention precision is below the pre-registered calibration floor" {
		t.Fatalf("expected abstention precision WAIT, got %s/%s", status, reason)
	}
	if metrics == "" {
		t.Fatal("gate metrics must be populated on calibration WAIT")
	}
}

func TestScopedEvaluationGateWaitsOnExcessConfidentWrong(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"state-1": abstainResult("state-1", "project_state", false, false, true, false, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"state-1": abstainResult("state-1", "project_state", true, false, false, true, 20),
	}
	metrics, status, reason := scopedEvaluationGate(baseline, experiment, abstainGateProtocol())
	if status != model.EvaluationGateWait || reason != "confident wrong rate exceeds the pre-registered calibration budget" {
		t.Fatalf("expected confident-wrong WAIT, got %s/%s", status, reason)
	}
	if metrics == "" {
		t.Fatal("gate metrics must be populated on calibration WAIT")
	}
}

func TestScopedEvaluationGatePassesAbstentionCalibration(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"state-1": abstainResult("state-1", "project_state", false, false, true, false, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"state-1": abstainResult("state-1", "project_state", true, true, false, true, 20),
	}
	metrics, status, reason := scopedEvaluationGate(baseline, experiment, abstainGateProtocol())
	if status != model.EvaluationGatePass {
		t.Fatalf("calibrated gate must pass, got %s/%s", status, reason)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(metrics), &decoded); err != nil {
		t.Fatal(err)
	}
	calibration, ok := decoded["abstention_calibration"].(map[string]interface{})
	if !ok {
		t.Fatalf("abstention_calibration report missing: %+v", decoded)
	}
	if calibration["policy"] != scopedAbstentionPolicyLowConfidence ||
		calibration["abstain_rate_pp"].(float64) != 0 ||
		calibration["over_abstain_pp"].(float64) != 0 ||
		calibration["confident_wrong_pp"].(float64) != 0 {
		t.Fatalf("unexpected calibration report: %+v", calibration)
	}
}

func TestScopedEvaluationAbstentionNeutralForUnmarkedFixtures(t *testing.T) {
	// Legacy fixtures without the expected-evidence flag must not skew the
	// calibration: abstains are counted but never classified (Classified=0),
	// so the precision floor gate is skipped.
	experiment := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, true, 20),
		"boundary-2": scopedGateResult("boundary-2", "boundary", true, false, 20),
	}
	calibration := collectScopedAbstentionCalibration(experiment, "")
	if calibration.Policy != scopedAbstentionPolicyConservative {
		t.Fatalf("empty policy must default to conservative: %+v", calibration)
	}
	if calibration.AbstainCount != 1 || calibration.Classified != 0 {
		t.Fatalf("unmarked abstains must stay unclassified: %+v", calibration)
	}
	if scopedAbstainPrecisionPP(calibration) != 0 || calibration.Classified != 0 {
		t.Fatalf("unmarked abstains must stay unclassified: %+v", calibration)
	}
	// boundary-2 carries no answer_correct marker, so it is never counted as a
	// confident wrong either - unmarked fixtures stay fully neutral.
	if calibration.AnswerGiven != 1 || calibration.ConfidentWrong != 0 {
		t.Fatalf("unmarked answers must stay neutral: %+v", calibration)
	}
}

func TestScopedEvaluationV4AbstentionCalibrationReports(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		true: {Chunks: []map[string]interface{}{
			{"id": "compiled-entity", "document_id": "kb-1", "content": "entity page X is 1",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-1", "doc-2"}},
			{"id": "raw-in-compiled-leg", "document_id": "doc-9", "content": "raw noise"},
		}},
		false: {Chunks: []map[string]interface{}{
			{"id": "raw-1", "document_id": "doc-1", "content": "X is 1"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV4Abstention(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute abstention-enabled v4 A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("abstention-enabled v4 run must complete: %+v", completed)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatal(err)
	}
	calibration, ok := metrics["abstention_calibration"].(map[string]interface{})
	if !ok {
		t.Fatalf("abstention_calibration missing from completed run: %+v", metrics)
	}
	if calibration["policy"] != scopedAbstentionPolicyLowConfidence {
		t.Fatalf("abstention policy not persisted in report: %+v", calibration)
	}
	if calibration["confident_wrong_count"].(float64) != 0 || calibration["abstain_count"].(float64) != 0 {
		t.Fatalf("confident hit run must report zero abstention counts: %+v", calibration)
	}
}

func createScopedPairEvaluationV4Abstention(
	t *testing.T, svc *Service, ctx context.Context, tenantID, userID, strategyID string,
) *model.EvaluationRun {
	t.Helper()
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenantID, userID, EvaluationSetVersionInput{
		EvalSetID: strategyID, Version: 1,
		Cases: json.RawMessage(`[{"case_id":"pair-1","paired_sample_id":"sample-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEvaluationCaseVersion(ctx, tenantID, userID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "pair-1", CaseVersion: 1,
		Question: "What is X?", ExpectedAnswer: "X is 1", ExpectedCitationDocIDs: "doc-1",
		Metadata: json.RawMessage(`{"capability":"page_index"}`), PairedSampleID: "sample-1",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenantID, userID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategyID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "pageindex-abstain-v4", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-abstain-v4", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(scopedPreregistrationV4AbstentionProtocol),
	})
	if err != nil {
		t.Fatal(err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, tenantID, userID, model.EvaluationCapabilityPageIndex,
		json.RawMessage(scopedPreregistrationV4AbstentionProtocol))
	return run
}

var _ = fmt.Sprintf // keep fmt import when assertions change
