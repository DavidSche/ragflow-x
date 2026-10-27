package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// repeat-run test-retest reliability (223 T4 初版): a v4 protocol may
// pre-register a minimum per-case pass consistency between the experiment
// pass and a second experiment-arm pass; values below the threshold report
// WAIT instead of silently trusting a single noisy pass.

// scopedPreregistrationV4RepeatedProtocol is the canonical repeat-enabled v4
// protocol: identical quality thresholds to the v4 protocol plus the
// test-retest reliability guard (min_repeat_consistency_pp=95).
const scopedPreregistrationV4RepeatedProtocol = `{
	"contract_version":"scoped-paired-ab-v4","top_k":8,"min_samples":1,
	"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
	"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
	"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,
	"retrieval_mode":"compiled_first","exclude_internal_rows":true,
	"compiled_first_max_chunks":2,
	"answer_correctness_method":"expected_evidence_match",
	"require_citation_precision":true,
	"min_repeat_consistency_pp":95
}`

func TestParseScopedEvaluationProtocolV4RepeatThreshold(t *testing.T) {
	valid, err := parseScopedEvaluationProtocol(scopedPreregistrationV4RepeatedProtocol)
	if err != nil {
		t.Fatalf("repeat-enabled v4 protocol must parse: %v", err)
	}
	if valid.MinRepeatConsistencyPP != 95 {
		t.Fatalf("repeat consistency threshold was not parsed: %+v", valid.MinRepeatConsistencyPP)
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"threshold above 100", `{"contract_version":"scoped-paired-ab-v4","retrieval_mode":"compiled_first","exclude_internal_rows":true,"compiled_first_max_chunks":2,"min_repeat_consistency_pp":120}`, true},
		{"threshold below zero", `{"contract_version":"scoped-paired-ab-v4","retrieval_mode":"compiled_first","exclude_internal_rows":true,"compiled_first_max_chunks":2,"min_repeat_consistency_pp":-5}`, true},
		{"repeat on v3", `{"contract_version":"scoped-paired-ab-v3","primary_metric":"citation_precision","min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,"min_repeat_consistency_pp":95}`, true},
		{"repeat on v1", `{"contract_version":"scoped-paired-ab-v1","min_repeat_consistency_pp":95}`, true},
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

// repeatGateProtocol is a gate-only protocol (no parse validation involved)
// with minimal thresholds so the main gate passes on a consistent repeat.
func repeatGateProtocol() scopedEvaluationProtocol {
	return scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersionV4, MinSamples: 1, MinHitLiftPP: 0,
		MaxNoAnswerLiftPP: 0, MaxP95LatencyDeltaMS: 300,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
		RequireTaskTypeCoverage: true, RequiredTaskTypes: []string{"boundary"}, MinTaskTypeSamples: 1,
		RetrievalMode: scopedRetrievalModeCompiledFirst, ExcludeInternalRows: true,
		CompiledFirstMaxChunks: 2, MinRepeatConsistencyPP: 95,
	}
}

func TestScopedEvaluationRepeatGateBlocksLowConsistency(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, true, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", true, false, 20),
	}
	// Repeat disagrees with the experiment pass: same case, opposite verdict.
	repeat := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, false, 20),
	}
	metrics, status, reason := scopedEvaluationGateRepeated(baseline, experiment, repeat, repeatGateProtocol())
	if status != model.EvaluationGateWait || reason != "repeat-run answer consistency is below the pre-registered minimum" {
		t.Fatalf("expected repeat consistency WAIT, got %s/%s metrics=%s", status, reason, metrics)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(metrics), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["repeat_consistency_pp"].(float64) != 0 || decoded["repeat_pair_count"].(float64) != 1 ||
		decoded["repeat_consistent_pair_count"].(float64) != 0 {
		t.Fatalf("repeat diagnostics missing or wrong: %+v", decoded)
	}
}

func TestScopedEvaluationRepeatGatePassesWithHighConsistency(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, true, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, false, 20),
	}
	// Repeat agrees with the experiment pass: test-retest is reliable.
	repeat := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, false, 20),
	}
	metrics, status, reason := scopedEvaluationGateRepeated(baseline, experiment, repeat, repeatGateProtocol())
	if status != model.EvaluationGatePass {
		t.Fatalf("expected repeat gate pass, got %s/%s metrics=%s", status, reason, metrics)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(metrics), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["repeat_consistency_pp"].(float64) != 100 || decoded["repeat_consistent_pair_count"].(float64) != 1 {
		t.Fatalf("repeat diagnostics must be folded into the pass report: %+v", decoded)
	}
	// The folded report must keep the abstention-calibration section intact so
	// repeat + abstention gates compose without clobbering one another.
	if _, ok := decoded["abstention_calibration"].(map[string]interface{}); !ok {
		t.Fatalf("abstention_calibration must survive the repeat fold: %+v", decoded)
	}
}

func TestScopedEvaluationRepeatConsistencyCountsPartialRepeat(t *testing.T) {
	experiment := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, false, 20),
		"boundary-2": scopedGateResult("boundary-2", "boundary", false, false, 20),
	}
	// The second case is missing from the repeat pass: it counts as
	// disagreement, so consistency is 1/2 = 0.5 below the guard.
	repeat := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, false, 20),
	}
	pp, consistent, total := scopedRepeatConsistency(experiment, repeat)
	if pp != 0.5 || consistent != 1 || total != 2 {
		t.Fatalf("partial repeat must count as disagreement: pp=%v consistent=%d total=%d", pp, consistent, total)
	}
}

func TestScopedEvaluationV4RepeatArmExecutesAndReports(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		true: {Chunks: []map[string]interface{}{
			{"id": "compiled-entity", "document_id": "kb-1", "content": "entity page X is 1",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-1", "doc-2"}},
			{"id": "compiled-concept", "document_id": "kb-1", "content": "concept page Y is 2",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-2"}},
			{"id": "raw-in-compiled-leg", "document_id": "doc-9", "content": "raw noise"},
		}},
		false: {Chunks: []map[string]interface{}{
			{"id": "raw-1", "document_id": "doc-1", "content": "X is 1"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV4Repeated(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute repeat-enabled v4 A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("repeat-enabled v4 run must complete: %+v", completed)
	}
	// baseline issues 1 compilation-free call; experiment and the repeat arm
	// each issue the 2-leg compiled-first pair -> 1 + 2 + 2 = 5 retrieval calls.
	if len(stub.searchRequests) != 5 {
		t.Fatalf("baseline(1) + experiment(2) + repeat(2) must issue five retrieval calls: %d", len(stub.searchRequests))
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 3 {
		t.Fatalf("expected 3 case results (baseline/experiment/repeat): count=%d err=%v", len(results), err)
	}
	armSet := map[string]bool{}
	for _, result := range results {
		armSet[result.ExperimentArm] = true
	}
	for _, arm := range []string{model.EvaluationArmBaseline, model.EvaluationArmExperiment, scopedRepeatArm} {
		if !armSet[arm] {
			t.Fatalf("repeat arm did not persist with distinct arm value: %+v", armSet)
		}
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["repeat_consistency_pp"].(float64) != 100 || metrics["repeat_pair_count"].(float64) != 1 ||
		metrics["repeat_consistent_pair_count"].(float64) != 1 {
		t.Fatalf("repeat report missing from gate metrics: %+v", metrics)
	}
}

func createScopedPairEvaluationV4Repeated(
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
		EvaluationPolicyVersion: "pageindex-repeat-v4", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-repeat-v4", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(scopedPreregistrationV4RepeatedProtocol),
	})
	if err != nil {
		t.Fatal(err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, tenantID, userID, model.EvaluationCapabilityPageIndex,
		json.RawMessage(scopedPreregistrationV4RepeatedProtocol))
	return run
}
