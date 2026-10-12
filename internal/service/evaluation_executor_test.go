package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type scopedEvaluationStub struct {
	ragflow.Mock
	requests         []ragflow.RetrieveDatasetsRequest
	results          map[bool]ragflow.RetrieveDatasetsResult
	searchRequests   []ragflow.SearchDatasetRequest
	searchResults    map[bool]ragflow.SearchDatasetResult
	artifactRequests int
	detailRequests   int
	artifactItems    []ragflow.DatasetArtifact
	artifactDetails  map[string]*ragflow.DatasetArtifactDetail
}

func (stub *scopedEvaluationStub) RetrieveDatasets(
	_ context.Context, request ragflow.RetrieveDatasetsRequest,
) (*ragflow.RetrieveDatasetsResult, error) {
	stub.requests = append(stub.requests, request)
	result := stub.results[request.IncludeKnowledgeCompilation]
	return &result, nil
}

func (stub *scopedEvaluationStub) SearchDataset(
	_ context.Context, _ string, request ragflow.SearchDatasetRequest,
) (*ragflow.SearchDatasetResult, error) {
	stub.searchRequests = append(stub.searchRequests, request)
	result := stub.searchResults[request.IncludeKnowledgeCompilation]
	return &result, nil
}

func (stub *scopedEvaluationStub) ListDatasetArtifacts(
	_ context.Context, _ string, _ ragflow.DatasetArtifactFilter,
) ([]ragflow.DatasetArtifact, int64, error) {
	stub.artifactRequests++
	return append([]ragflow.DatasetArtifact{}, stub.artifactItems...), int64(len(stub.artifactItems)), nil
}

func (stub *scopedEvaluationStub) GetDatasetArtifact(
	_ context.Context, _, pageType, slug string,
) (*ragflow.DatasetArtifactDetail, error) {
	stub.detailRequests++
	return stub.artifactDetails[pageType+"/"+slug], nil
}

func newScopedEvaluationFixture(t *testing.T) (*Service, context.Context, model.Tenant, *model.User, *model.KnowledgeStrategy) {
	t.Helper()
	svc, ctx, tenant, user := newGovernanceFixture(t)
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Scoped Evaluation "+id.New()[:8])
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	return svc, ctx, tenant, user, strategy
}

func createScopedPairEvaluation(t *testing.T, svc *Service, ctx context.Context, tenantID, userID, strategyID string) *model.EvaluationRun {
	t.Helper()
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenantID, userID, EvaluationSetVersionInput{
		EvalSetID: strategyID, Version: 1,
		Cases: json.RawMessage(`[{"case_id":"pair-1","paired_sample_id":"sample-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseVersion, err := svc.CreateEvaluationCaseVersion(ctx, tenantID, userID, EvaluationCaseVersionInput{
		EvalSetID: strategyID, EvalSetVersion: 1, CaseID: "pair-1", CaseVersion: 1,
		Question: "What is X?", ExpectedAnswer: "X is 1", ExpectedCitationDocIDs: "doc-1",
		Metadata: json.RawMessage(`{"capability":"page_index"}`), PairedSampleID: "sample-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenantID, userID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategyID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v1","top_k":10,"min_samples":1,
			"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
			"max_no_answer_lift_pp":0,"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if caseVersion.PairedSampleID != "sample-1" {
		t.Fatalf("case pairing was not persisted: %+v", caseVersion)
	}
	return run
}

func TestScopedEvaluationExecutesPairedABAndPasses(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{{"id": "base", "document_id": "doc-other", "content": "irrelevant"}}},
		true:  {Chunks: []map[string]interface{}{{"id": "hit", "document_id": "doc-1", "content": "X is 1"}}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluation(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.EvaluationRunCompleted || completed.GateStatus != model.EvaluationGatePass ||
		completed.Pass == nil || !*completed.Pass {
		t.Fatalf("unexpected run: %+v", completed)
	}
	if len(stub.searchRequests) != 2 || stub.searchRequests[0].IncludeKnowledgeCompilation || !stub.searchRequests[1].IncludeKnowledgeCompilation {
		t.Fatalf("unexpected paired requests: %+v", stub.searchRequests)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, run.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("unexpected case results: %+v err=%v", results, err)
	}
	for _, result := range results {
		if result.PairedSampleID != "sample-1" || (result.ExperimentArm != model.EvaluationArmBaseline &&
			result.ExperimentArm != model.EvaluationArmExperiment) {
			t.Fatalf("invalid pair result: %+v", result)
		}
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["pair_count"] != float64(1) || metrics["hit_lift_pp"] != float64(100) {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	if metrics["hit_lift_ci95_pp"].(map[string]interface{})["lower"].(float64) < 0 {
		t.Fatalf("improvement CI lower must be non-negative: %+v", metrics["hit_lift_ci95_pp"])
	}
}

func TestScopedEvaluationFailsOnCitationDegradation(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{{"id": "base", "document_id": "doc-1", "content": "X is 1"}}},
		true: {Chunks: []map[string]interface{}{
			{"id": "hit", "document_id": "doc-1", "content": "X is 1"},
			{"id": "noise", "document_id": "doc-noise", "content": "noise"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluation(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.EvaluationRunCompleted || completed.GateStatus != model.EvaluationGateFail {
		t.Fatalf("expected citation failure, got %+v", completed)
	}
	if completed.Pass == nil || *completed.Pass {
		t.Fatalf("failed gate must not pass: %+v", completed)
	}
}

func TestScopedEvaluationV2RecordsArtifactDiagnostics(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{{"id": "base", "document_id": "doc-1", "content": "X is 1"}}},
		true: {Chunks: []map[string]interface{}{
			{"id": "artifact", "document_id": "doc-1", "content": "X is 1", "compile_kwd": "wiki_page"},
			{"id": "raw", "document_id": "doc-1", "content": "X is 1"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV2(t, svc, ctx, tenant.ID, user.ID, strategy.ID, 1)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.EvaluationRunCompleted || completed.GateStatus != model.EvaluationGateWait {
		t.Fatalf("unexpected v2 run: %+v", completed)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["contract_version"] != "scoped-paired-ab-v2" ||
		metrics["compiled_artifact_pair_count"] != float64(1) {
		t.Fatalf("missing artifact diagnostics: %+v", metrics)
	}
	diagnostics := metrics["pair_diagnostics"].([]interface{})
	if len(diagnostics) != 1 {
		t.Fatalf("expected one pair diagnostic: %+v", diagnostics)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("expected paired results: count=%d err=%v", len(results), err)
	}
	var experiment model.EvaluationCaseResult
	for _, result := range results {
		if result.ExperimentArm == model.EvaluationArmExperiment {
			experiment = result
		}
	}
	var resultMetrics map[string]interface{}
	if err := json.Unmarshal([]byte(experiment.Metrics), &resultMetrics); err != nil {
		t.Fatal(err)
	}
	if resultMetrics["compiled_chunk_count"] != float64(1) || resultMetrics["raw_chunk_count"] != float64(1) {
		t.Fatalf("unexpected per-arm diagnostics: %+v", resultMetrics)
	}
	var references []map[string]interface{}
	if err := json.Unmarshal([]byte(experiment.References), &references); err != nil {
		t.Fatal(err)
	}
	if references[0]["source"] != "compiled" || references[1]["source"] != "raw" {
		t.Fatalf("references did not distinguish compiled artifacts: %+v", references)
	}
}

func TestScopedEvaluationV2WaitsWithoutCompiledArtifact(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"pair-1": scopedArtifactResult("pair-1", false, 1, 0),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"pair-1": scopedArtifactResult("pair-1", true, 0, 1),
	}
	_, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: "scoped-paired-ab-v2", TopK: 10, MinSamples: 1, MinHitLiftPP: 2,
		MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0, MinCompiledArtifactPairs: 1,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
	})
	if status != model.EvaluationGateWait || reason != "compiled artifact participation is below the pre-registered minimum" {
		t.Fatalf("expected artifact participation WAIT, got %s/%s", status, reason)
	}
}

func TestScopedEvaluationV3AnnotatesCompiledProvenance(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{
		artifactItems: []ragflow.DatasetArtifact{{Slug: "operations-page", Title: "Operations", PageType: "page_index"}},
		artifactDetails: map[string]*ragflow.DatasetArtifactDetail{
			"page_index/operations-page": {
				Slug: "operations-page", Title: "Operations", PageType: "page_index",
				ContentMDRendered: "# Operations page", SourceChunkIDs: []string{"source-1"},
			},
		},
		searchResults: map[bool]ragflow.SearchDatasetResult{
			false: {Chunks: []map[string]interface{}{
				{"id": "base-hit", "document_id": "doc-1", "content": "X is 1"},
				{"id": "base-noise-1", "document_id": "doc-noise", "content": "noise"},
				{"id": "base-noise-2", "document_id": "doc-noise", "content": "noise"},
				{"id": "base-noise-3", "document_id": "doc-noise", "content": "noise"},
				{"id": "base-noise-4", "document_id": "doc-noise", "content": "noise"},
			}},
			true: {Chunks: []map[string]interface{}{
				{"id": "direct", "document_id": "doc-1", "content": "# Operations page"},
				{"id": "source-1", "document_id": "doc-1", "content": "X is 1"},
				{"id": "exp-hit", "document_id": "doc-1", "content": "X is 1"},
				{"id": "exp-hit-2", "document_id": "doc-1", "content": "X is 1"},
			}},
		},
	}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV3(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.EvaluationRunCompleted || completed.GateStatus != model.EvaluationGatePass {
		t.Fatalf("unexpected v3 run: %+v", completed)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["compiled_artifact_effect_pair_count"] != float64(1) ||
		metrics["compiled_derived_artifact_pair_count"] != float64(1) ||
		metrics["citation_precision_lift_pp"] != float64(80) {
		t.Fatalf("missing v3 diagnostics: %+v", metrics)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("expected paired results: count=%d err=%v", len(results), err)
	}
	var experiment model.EvaluationCaseResult
	for _, result := range results {
		if result.ExperimentArm == model.EvaluationArmExperiment {
			experiment = result
		}
	}
	var references []map[string]interface{}
	if err := json.Unmarshal([]byte(experiment.References), &references); err != nil {
		t.Fatal(err)
	}
	if references[0]["source"] != "compiled" || references[0]["compiled_artifact_id"] != "operations-page" ||
		references[1]["source"] != "compiled_derived" || references[1]["compiled_source_chunk_id"] != "source-1" ||
		references[2]["source"] != "raw" {
		t.Fatalf("unexpected provenance references: %+v", references)
	}
	directProvenance := references[0]["compiled_provenance"].(map[string]interface{})
	derivedProvenance := references[1]["compiled_provenance"].(map[string]interface{})
	if directProvenance["match_type"] != "content_sha256" || directProvenance["artifact_id"] != "operations-page" ||
		derivedProvenance["match_type"] != "source_chunk_id" || derivedProvenance["source_chunk_id"] != "source-1" {
		t.Fatalf("unexpected compiled provenance fields: %+v", references)
	}
	if stub.artifactRequests != 2 || stub.detailRequests != 2 {
		t.Fatalf("unexpected provenance requests: artifacts=%d details=%d", stub.artifactRequests, stub.detailRequests)
	}
}

func TestScopedEvaluationV3WaitsWithoutArtifactEffect(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{{"id": "base", "document_id": "doc-1", "content": "X is 1"}}},
		true:  {Chunks: []map[string]interface{}{{"id": "exp", "document_id": "doc-1", "content": "X is 1"}}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV3(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.EvaluationRunCompleted ||
		completed.GateStatus != model.EvaluationGateWait ||
		completed.GateReason != "compiled artifact effect is below the pre-registered minimum" {
		t.Fatalf("expected artifact effect WAIT, got %+v", completed)
	}
}

func TestScopedEvaluationV3UsesSearchDatasetForWiki(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	strategy.Config = `{"compilation_template":"wiki"}`
	if err := svc.Store.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	stub := &scopedEvaluationStub{
		artifactItems: []ragflow.DatasetArtifact{{Slug: "wiki-page", Title: "Wiki Page", PageType: "wiki"}},
		artifactDetails: map[string]*ragflow.DatasetArtifactDetail{
			"wiki/wiki-page": {
				Slug: "wiki-page", Title: "Wiki Page", PageType: "wiki",
				ContentMDRendered: "# Wiki page", SourceChunkIDs: []string{"source-1"},
			},
		},
		searchResults: map[bool]ragflow.SearchDatasetResult{
			false: {Chunks: []map[string]interface{}{
				{"id": "base", "document_id": "doc-1", "content": "X is 1"},
				{"id": "base-noise", "document_id": "doc-noise", "content": "noise"},
			}},
			true: {Chunks: []map[string]interface{}{
				{"id": "wiki", "document_id": "doc-1", "content": "X is 1", "compile_kwd": "wiki_page"},
				{"id": "wiki-noise", "document_id": "doc-noise", "content": "noise", "compile_kwd": "other_page"},
			}},
		},
	}
	svc.RAGFlow = stub

	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1,
		Cases: json.RawMessage(`[{"case_id":"pair-1","paired_sample_id":"sample-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "pair-1", CaseVersion: 1,
		Question: "What is X?", ExpectedAnswer: "X is 1", ExpectedCitationDocIDs: "doc-1",
		Metadata: json.RawMessage(`{"capability":"wiki"}`), PairedSampleID: "sample-1",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "wiki-live-v3", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "wiki-live-v3", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityWiki, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v3","top_k":8,"min_samples":1,
			"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
			"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
			"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,
			"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.EvaluationRunCompleted || completed.GateStatus != model.EvaluationGateWait {
		t.Fatalf("unexpected wiki v3 run: %+v", completed)
	}
	if len(stub.requests) != 0 || len(stub.searchRequests) != 2 {
		t.Fatalf("wiki must use SearchDataset: retrieve=%d search=%d", len(stub.requests), len(stub.searchRequests))
	}
	if stub.artifactRequests != 2 || stub.detailRequests != 2 {
		t.Fatalf("wiki v3 must build artifact provenance: artifacts=%d details=%d", stub.artifactRequests, stub.detailRequests)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["compiled_artifact_effect_pair_count"] != float64(1) {
		t.Fatalf("missing wiki compiled effect: %+v", metrics)
	}
}

func createScopedPairEvaluationV3(
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
		EvaluationPolicyVersion: "pageindex-live-v3", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-live-v3", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(`{
			"contract_version":"scoped-paired-ab-v3","top_k":8,"min_samples":1,
			"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
			"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
			"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,
			"answer_correctness_method":"expected_evidence_match",
			"require_citation_precision":true
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func createScopedPairEvaluationV2(
	t *testing.T, svc *Service, ctx context.Context, tenantID, userID, strategyID string, minArtifactPairs int,
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
		EvaluationPolicyVersion: "pageindex-live-v2", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-live-v2", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(fmt.Sprintf(`{
			"contract_version":"scoped-paired-ab-v2","top_k":10,"min_samples":1,
			"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
			"max_no_answer_lift_pp":0,"min_compiled_artifact_pairs":%d,
			"answer_correctness_method":"expected_evidence_match","require_citation_precision":true
		}`, minArtifactPairs)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func scopedArtifactResult(caseID string, hit bool, compiledCount, rawCount int) model.EvaluationCaseResult {
	metrics, _ := json.Marshal(map[string]interface{}{
		"task_type": "unclassified", "hit_at_k": hit, "no_answer": !hit,
		"compiled_chunk_count": compiledCount, "raw_chunk_count": rawCount,
	})
	pass := hit
	return model.EvaluationCaseResult{
		ID: id.New(), CaseID: caseID, PairedSampleID: caseID, Metrics: string(metrics),
		Pass: &pass, HitAtK: &hit, NoAnswer: &hit,
	}
}

func TestScopedEvaluationPairedDifferenceIntervalHandlesDegradation(t *testing.T) {
	values := make([]float64, 0, 20)
	for range 17 {
		values = append(values, -1)
	}
	for range 3 {
		values = append(values, 0)
	}
	lower, upper := pairedDifferenceInterval(values)
	if math.IsNaN(lower) || math.IsNaN(upper) || lower >= 0 || upper <= lower {
		t.Fatalf("degradation interval must be finite and negative: lower=%v upper=%v", lower, upper)
	}
}

func TestScopedEvaluationFailsClosedAndRejectsInvalidProtocol(t *testing.T) {
	svc, ctx, tenant, user, _ := newScopedEvaluationFixture(t)
	if _, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, "missing"); err == nil {
		t.Fatal("expected missing run failure")
	}
	if _, err := parseScopedEvaluationProtocol(`{"contract_version":"wrong"}`); err == nil {
		t.Fatal("expected invalid protocol failure")
	}
	if _, err := parseScopedEvaluationProtocol(`{
		"contract_version":"scoped-paired-ab-v1","top_k":10,"min_samples":1,
		"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,"max_no_answer_lift_pp":0,
		"min_compiled_artifact_pairs":1,
		"answer_correctness_method":"expected_evidence_match"
	}`); err == nil {
		t.Fatal("expected v1 artifact field rejection")
	}
	if _, err := parseScopedEvaluationProtocol(`{
		"contract_version":"scoped-paired-ab-v2","top_k":10,"min_samples":1,
		"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,"max_no_answer_lift_pp":0,
		"min_compiled_artifact_pairs":2,
		"answer_correctness_method":"expected_evidence_match"
	}`); err == nil {
		t.Fatal("expected v2 artifact minimum rejection")
	}
	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: id.New(), StrategyType: model.KnowledgeStrategyCompiled,
		RAGFlowCapability: model.RAGFlowCapabilityKnowledgeCompilation, Config: `{}`,
		FallbackStrategy: model.KnowledgeStrategySemantic, AuthorizationScope: `{"projects":[]}`,
		Active: true, CreatedBy: user.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	run := createScopedPairEvaluation(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	if _, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID); err == nil {
		t.Fatal("expected unavailable dataset failure")
	}
	failed, err := svc.Store.GetEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil || failed == nil || failed.Status != model.EvaluationRunFailed ||
		failed.GateStatus != model.EvaluationGateFail {
		t.Fatalf("expected failed run, got %+v err=%v", failed, err)
	}
}

func scopedGateResult(caseID, taskType string, hit, noAnswer bool, latency int64) model.EvaluationCaseResult {
	metrics, _ := json.Marshal(map[string]interface{}{
		"task_type": taskType, "hit_at_k": hit, "no_answer": noAnswer,
	})
	pass := hit
	return model.EvaluationCaseResult{
		ID: id.New(), CaseID: caseID, PairedSampleID: caseID, Metrics: string(metrics),
		Pass: &pass, HitAtK: &hit, NoAnswer: &noAnswer, LatencyMs: latency,
	}
}

func TestScopedEvaluationGateRequiresTaskTypeCoverage(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"preference-1": scopedGateResult("preference-1", "preference", false, true, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"preference-1": scopedGateResult("preference-1", "preference", true, false, 20),
	}
	metrics, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersion, MinSamples: 1, MinHitLiftPP: 0,
		MaxNoAnswerLiftPP: 0, MaxP95LatencyDeltaMS: 300,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
		RequireTaskTypeCoverage: true, RequiredTaskTypes: []string{"preference", "boundary"}, MinTaskTypeSamples: 1,
	})
	if status != model.EvaluationGateWait || reason != "task type coverage is below the pre-registered minimum" {
		t.Fatalf("expected coverage WAIT, got %s/%s %s", status, reason, metrics)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(metrics), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["task_types"].(map[string]interface{})["preference"] == nil {
		t.Fatalf("task type metrics missing: %+v", decoded)
	}
}

func TestScopedEvaluationGateFailsOnTaskTypeDegradation(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"preference-1": scopedGateResult("preference-1", "preference", false, true, 10),
		"project-1":    scopedGateResult("project-1", "project_state", true, false, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"preference-1": scopedGateResult("preference-1", "preference", true, false, 20),
		"project-1":    scopedGateResult("project-1", "project_state", false, true, 20),
	}
	_, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersion, MinSamples: 2, MinHitLiftPP: 2,
		MaxNoAnswerLiftPP: 0, MaxP95LatencyDeltaMS: 300,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
		RequireTaskTypeCoverage: true, RequiredTaskTypes: []string{"preference", "project_state"}, MinTaskTypeSamples: 1,
	})
	if status != model.EvaluationGateFail || reason != "task type quality, citation, no-answer or latency budget degraded" {
		t.Fatalf("expected task type FAIL, got %s/%s", status, reason)
	}
}

func TestScopedEvaluationGateFailsOnBoundaryFalseHit(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, true, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", true, false, 20),
	}
	_, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersion, MinSamples: 1, MinHitLiftPP: 2,
		MaxNoAnswerLiftPP: 0, MaxP95LatencyDeltaMS: 300,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
		RequireTaskTypeCoverage: true, RequiredTaskTypes: []string{"boundary"}, MinTaskTypeSamples: 1,
	})
	if status != model.EvaluationGateFail || reason != "boundary or no-answer task produced a false hit" {
		t.Fatalf("expected boundary false-hit FAIL, got %s/%s", status, reason)
	}
}

func TestScopedEvaluationGateAllowsIrrelevantBoundaryRetrieval(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, true, 10),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"boundary-1": scopedGateResult("boundary-1", "boundary", false, false, 20),
	}
	_, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersion, MinSamples: 1, MinHitLiftPP: 0,
		MaxNoAnswerLiftPP: 0, MaxP95LatencyDeltaMS: 300,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
		RequireTaskTypeCoverage: true, RequiredTaskTypes: []string{"boundary"}, MinTaskTypeSamples: 1,
	})
	if status != model.EvaluationGatePass || reason != "pre-registered paired quality gate passed" {
		t.Fatalf("expected boundary pass, got %s/%s", status, reason)
	}
}
