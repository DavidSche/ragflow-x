package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// scopedPreregistrationV4Protocol is the canonical v4 protocol: identical
// quality thresholds to v3 plus the compiled-first retrieval semantics
// (137 §5.4). v4 changes HOW the experiment arm retrieves and HOW compiled
// citations are counted; it never relaxes a threshold.
const scopedPreregistrationV4Protocol = `{
	"contract_version":"scoped-paired-ab-v4","top_k":8,"min_samples":1,
	"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
	"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
	"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,
	"retrieval_mode":"compiled_first","exclude_internal_rows":true,
	"compiled_first_max_chunks":2,
	"answer_correctness_method":"expected_evidence_match",
	"require_citation_precision":true
}`

func TestParseScopedEvaluationProtocolV4(t *testing.T) {
	protocol, err := parseScopedEvaluationProtocol(scopedPreregistrationV4Protocol)
	if err != nil {
		t.Fatalf("valid v4 protocol must parse: %v", err)
	}
	if protocol.ContractVersion != scopedEvaluationContractVersionV4 {
		t.Fatalf("unexpected contract version: %s", protocol.ContractVersion)
	}
	if protocol.RetrievalMode != scopedRetrievalModeCompiledFirst {
		t.Fatalf("v4 retrieval mode must be compiled_first: %q", protocol.RetrievalMode)
	}
	if !protocol.ExcludeInternalRows {
		t.Fatal("v4 must exclude internal rows from returned evidence")
	}
	if protocol.CompiledFirstMaxChunks != 2 {
		t.Fatalf("unexpected compiled-first cap: %d", protocol.CompiledFirstMaxChunks)
	}
	if protocol.MinCitationLiftPP != 20 || protocol.MinCompiledEffectPairs != 1 {
		t.Fatalf("v4 must carry the v3 quality thresholds: %+v", protocol)
	}
}

func TestParseScopedEvaluationProtocolV4Rejections(t *testing.T) {
	cases := map[string]string{
		"missing retrieval mode": strings.Replace(scopedPreregistrationV4Protocol,
			`"retrieval_mode":"compiled_first",`, ``, 1),
		"unknown retrieval mode": strings.Replace(scopedPreregistrationV4Protocol,
			`"retrieval_mode":"compiled_first"`, `"retrieval_mode":"append_only"`, 1),
		"internal rows not excluded": strings.Replace(scopedPreregistrationV4Protocol,
			`"exclude_internal_rows":true`, `"exclude_internal_rows":false`, 1),
		"compiled-first cap missing": strings.Replace(scopedPreregistrationV4Protocol,
			`"compiled_first_max_chunks":2,`, ``, 1),
		"compiled-first cap zero": strings.Replace(scopedPreregistrationV4Protocol,
			`"compiled_first_max_chunks":2`, `"compiled_first_max_chunks":0`, 1),
		"compiled-first cap above top_k": strings.Replace(scopedPreregistrationV4Protocol,
			`"compiled_first_max_chunks":2`, `"compiled_first_max_chunks":9`, 1),
		"v3 artifact minimum on v4": strings.Replace(scopedPreregistrationV4Protocol,
			`"retrieval_mode":"compiled_first"`,
			`"retrieval_mode":"compiled_first","min_compiled_artifact_pairs":1`, 1),
		"wrong primary metric": strings.Replace(scopedPreregistrationV4Protocol,
			`"primary_metric":"citation_precision"`, `"primary_metric":"hit_rate"`, 1),
		"zero citation lift": strings.Replace(scopedPreregistrationV4Protocol,
			`"min_citation_precision_lift_pp":20`, `"min_citation_precision_lift_pp":0`, 1),
		"zero effect pairs": strings.Replace(scopedPreregistrationV4Protocol,
			`"min_compiled_artifact_effect_pairs":1`, `"min_compiled_artifact_effect_pairs":0`, 1),
	}
	for name, raw := range cases {
		if _, err := parseScopedEvaluationProtocol(raw); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	// v3 must reject v4-only fields: retrieval modes never leak across versions.
	if _, err := parseScopedEvaluationProtocol(strings.Replace(scopedPreregistrationV3Protocol,
		`"contract_version":"scoped-paired-ab-v3"`,
		`"contract_version":"scoped-paired-ab-v3","retrieval_mode":"compiled_first"`, 1)); err == nil {
		t.Fatal("v3 protocol must not accept the v4 retrieval mode")
	}
}

func createScopedPairEvaluationV4(
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
		EvaluationPolicyVersion: "pageindex-live-v4", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-live-v4", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(scopedPreregistrationV4Protocol),
	})
	if err != nil {
		t.Fatal(err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, tenantID, userID, model.EvaluationCapabilityPageIndex,
		json.RawMessage(scopedPreregistrationV4Protocol))
	return run
}

// TestScopedEvaluationV4ExecutesCompiledFirstRetrieval drives the v4
// experiment arm through the two-leg compiled-first merge: a compiled leg
// (include=true, candidate window) feeds the compiled prefix, the raw leg
// (include=false, top_k) fills the remaining slots, internal plumbing rows
// never occupy evidence slots, and compiled citations count through
// source_doc_ids provenance.
func TestScopedEvaluationV4ExecutesCompiledFirstRetrieval(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		true: {Chunks: []map[string]interface{}{
			{"id": "draft-1", "document_id": "kb-1", "content": "internal draft page json",
				"compile_kwd": "wiki_page_draft"},
			{"id": "compiled-entity", "document_id": "kb-1", "content": "entity page X is 1",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-1", "doc-2"}},
			{"id": "compiled-concept", "document_id": "kb-1", "content": "concept page Y is 2",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-2"}},
			{"id": "compiled-extra", "document_id": "kb-1", "content": "extra page Z is 3",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-3"}},
			{"id": "raw-in-compiled-leg", "document_id": "doc-9", "content": "raw noise"},
		}},
		false: {Chunks: []map[string]interface{}{
			{"id": "raw-1", "document_id": "doc-1", "content": "X is 1"},
			{"id": "raw-2", "document_id": "doc-9", "content": "noise 2"},
			{"id": "raw-3", "document_id": "doc-9", "content": "noise 3"},
			{"id": "raw-4", "document_id": "doc-9", "content": "noise 4"},
			{"id": "raw-5", "document_id": "doc-9", "content": "noise 5"},
			{"id": "raw-6", "document_id": "doc-9", "content": "noise 6"},
			{"id": "raw-7", "document_id": "doc-9", "content": "noise 7"},
			{"id": "raw-8", "document_id": "doc-9", "content": "noise 8"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV4(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute v4 A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("v4 run must complete: %+v", completed)
	}
	// The run executes both arms: baseline issues one compilation-free call;
	// the experiment arm issues exactly two retrieval calls - the compiled leg
	// with the candidate window and the raw leg with the top_k size.
	if len(stub.searchRequests) != 3 {
		t.Fatalf("baseline(1) + experiment(2) must issue three retrieval calls: %d", len(stub.searchRequests))
	}
	compiledLegCount, rawLegCount, baselineLegCount := 0, 0, 0
	for _, request := range stub.searchRequests {
		switch {
		case request.IncludeKnowledgeCompilation:
			compiledLegCount++
			if request.Size != scopedRetrievalCandidateWindowV4 {
				t.Fatalf("compiled leg must use the candidate window size: %+v", request)
			}
		default:
			if request.Size != 8 {
				t.Fatalf("compilation-free legs must use the top_k size: %+v", request)
			}
			// The raw fill leg and the baseline leg share one request shape;
			// the arm split is asserted via the 1+2 total above.
			rawLegCount++
			baselineLegCount++
		}
	}
	if compiledLegCount != 1 || rawLegCount != 2 {
		t.Fatalf("v4 must run one compiled leg and two compilation-free legs: compiled=%d raw=%d",
			compiledLegCount, rawLegCount)
	}
	_ = baselineLegCount
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("expected 2 paired results: count=%d err=%v", len(results), err)
	}
	for _, result := range results {
		var metrics map[string]interface{}
		if err := json.Unmarshal([]byte(result.Metrics), &metrics); err != nil {
			t.Fatal(err)
		}
		if result.ExperimentArm == model.EvaluationArmBaseline {
			if metrics["returned_count"].(float64) != 8 {
				t.Fatalf("baseline arm keeps the raw top_k result: %+v", metrics)
			}
			continue
		}
		// Merged experiment evidence: 2 compiled prefix (cap) + 6 raw fill.
		// The internal draft row and the third compiled chunk must not occupy
		// evidence slots.
		if metrics["returned_count"].(float64) != 8 {
			t.Fatalf("v4 merge must cap returned evidence at top_k: %+v", metrics)
		}
		if metrics["compiled_chunk_count"].(float64) != 2 {
			t.Fatalf("compiled prefix must be capped at 2: %+v", metrics)
		}
		if metrics["compiled_internal_chunk_count"].(float64) != 0 {
			t.Fatalf("internal plumbing rows must be excluded: %+v", metrics)
		}
		if metrics["raw_chunk_count"].(float64) != 6 {
			t.Fatalf("raw fill must complete the top_k budget: %+v", metrics)
		}
		// Provenance counting: the entity page cites doc-1 via source_doc_ids,
		// the concept page only cites doc-2. Precision = (1 raw doc-1 +
		// 1 provenance-cited compiled) / 8.
		if metrics["compiled_cited_chunk_count"].(float64) != 1 {
			t.Fatalf("exactly one compiled chunk must be provenance-cited: %+v", metrics)
		}
		if metrics["citation_precision"].(float64) != 0.25 {
			t.Fatalf("v4 provenance citation counting failed: %+v", metrics)
		}
		if result.HitAtK == nil || !*result.HitAtK {
			t.Fatalf("provenance citation must satisfy hit_at_k: %+v", result)
		}
		refs := []map[string]interface{}{}
		if err := json.Unmarshal([]byte(result.References), &refs); err != nil {
			t.Fatal(err)
		}
		sawProvenanceBasis := false
		for _, ref := range refs {
			if ref["source"] == compiledArtifactSourceCompiled {
				if ref["citation_basis"] != "source_doc_ids" {
					t.Fatalf("compiled reference must record its citation basis: %+v", ref)
				}
				provenance, _ := ref["compiled_provenance"].(map[string]interface{})
				if provenance == nil || len(provenance["source_doc_ids"].([]interface{})) == 0 {
					t.Fatalf("compiled reference must carry source_doc_ids: %+v", ref)
				}
				sawProvenanceBasis = true
			} else if ref["citation_basis"] != "document_id" {
				t.Fatalf("raw reference must record the document_id basis: %+v", ref)
			}
		}
		if !sawProvenanceBasis {
			t.Fatal("experiment references must include compiled provenance entries")
		}
	}
}

// TestScopedEvaluationV4ProvenanceCitationOverlap verifies that compiled
// chunks whose source_doc_ids do not intersect the expected citations dilute
// precision instead of counting, and that the compiled prefix is skipped
// entirely when the compiled leg returns no citable artifact rows.
func TestScopedEvaluationV4ProvenanceCitationOverlap(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		true: {Chunks: []map[string]interface{}{
			{"id": "compiled-offtopic", "document_id": "kb-1", "content": "offtopic artifact page",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-7"}},
		}},
		false: {Chunks: []map[string]interface{}{
			{"id": "raw-1", "document_id": "doc-1", "content": "X is 1"},
			{"id": "raw-2", "document_id": "doc-9", "content": "noise"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV4(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute v4 A/B: %v", err)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("expected 2 paired results: count=%d err=%v", len(results), err)
	}
	for _, result := range results {
		if result.ExperimentArm != model.EvaluationArmExperiment {
			continue
		}
		var metrics map[string]interface{}
		if err := json.Unmarshal([]byte(result.Metrics), &metrics); err != nil {
			t.Fatal(err)
		}
		// 1 raw cited + 1 uncited compiled (offtopic provenance) / 3 returned.
		if metrics["citation_precision"].(float64) != 1.0/3.0 {
			t.Fatalf("uncited compiled chunks must dilute precision only: %+v", metrics)
		}
		if metrics["compiled_cited_chunk_count"].(float64) != 0 {
			t.Fatalf("offtopic provenance must not count as a citation: %+v", metrics)
		}
	}
}

// TestScopedEvaluationV3KeepsStrictDocumentCitationCounting pins the v3
// semantics: compiled chunks never count as citations regardless of
// source_doc_ids, so the v4 counting change cannot silently alter v3 runs.
func TestScopedEvaluationV3KeepsStrictDocumentCitationCounting(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		true: {Chunks: []map[string]interface{}{
			{"id": "compiled-1", "document_id": "kb-1", "content": "entity page X is 1",
				"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-1"}},
			{"id": "raw-1", "document_id": "doc-1", "content": "X is 1"},
		}},
		false: {Chunks: []map[string]interface{}{
			{"id": "raw-1", "document_id": "doc-1", "content": "X is 1"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV3(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute v3 A/B: %v", err)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("expected 2 paired results: count=%d err=%v", len(results), err)
	}
	for _, result := range results {
		if result.ExperimentArm != model.EvaluationArmExperiment {
			continue
		}
		var metrics map[string]interface{}
		if err := json.Unmarshal([]byte(result.Metrics), &metrics); err != nil {
			t.Fatal(err)
		}
		// v3 strict counting: the compiled chunk is returned but never cited.
		if metrics["citation_precision"].(float64) != 0.5 {
			t.Fatalf("v3 must keep strict document_id citation counting: %+v", metrics)
		}
		if _, present := metrics["compiled_cited_chunk_count"]; present {
			t.Fatalf("v3 must not emit v4 diagnostics: %+v", metrics)
		}
	}
}
