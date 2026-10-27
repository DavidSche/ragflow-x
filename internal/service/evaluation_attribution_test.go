package service

import (
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestScopedEvaluationClassifiesInternalPlumbingRows pins the M205 attribution
// hardening: wiki draft / planner / reduction rows carry compile_kwd but are
// machine plumbing, never citable artifact pages. They must be counted under
// the dedicated compiled_internal source (not compiled, not raw) so retrieval
// pollution stays observable without inflating compiled artifact pairs.
func TestScopedEvaluationClassifiesInternalPlumbingRows(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{
			{"id": "base", "document_id": "doc-1", "content": "X is 1"},
		}},
		true: {Chunks: []map[string]interface{}{
			{"id": "draft", "document_id": strategy.DatasetID, "content": "{\"slug\":\"operations\"}", "compile_kwd": "wiki_page_draft"},
			{"id": "plan", "document_id": strategy.DatasetID, "content": "{\"plan\":[]}", "compile_kwd": "wiki_compilation_plan"},
			{"id": "page", "document_id": "doc-1", "content": "X is 1", "compile_kwd": "wiki_page"},
			{"id": "raw", "document_id": "doc-1", "content": "X is 1"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluation(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("unexpected case results: %+v err=%v", results, err)
	}
	for _, result := range results {
		if result.ExperimentArm != model.EvaluationArmExperiment {
			continue
		}
		var metrics map[string]interface{}
		if err := json.Unmarshal([]byte(result.Metrics), &metrics); err != nil {
			t.Fatal(err)
		}
		if metrics["compiled_internal_chunk_count"] != float64(2) {
			t.Fatalf("internal plumbing rows must be counted separately: %+v", metrics)
		}
		if metrics["compiled_chunk_count"] != float64(1) {
			t.Fatalf("only recognized artifact pages count as compiled: %+v", metrics)
		}
		if metrics["raw_chunk_count"] != float64(1) {
			t.Fatalf("internal plumbing rows must not count as raw: %+v", metrics)
		}
		var references []map[string]interface{}
		if err := json.Unmarshal([]byte(result.References), &references); err != nil {
			t.Fatal(err)
		}
		sources := map[string]string{}
		for _, reference := range references {
			if id, ok := reference["chunk_id"].(string); ok {
				sources[id] = reference["source"].(string)
			}
		}
		if sources["draft"] != compiledArtifactSourceInternal || sources["plan"] != compiledArtifactSourceInternal {
			t.Fatalf("internal plumbing rows must use the compiled_internal source: %+v", sources)
		}
		if sources["page"] != compiledArtifactSourceCompiled || sources["raw"] != compiledArtifactSourceRaw {
			t.Fatalf("unexpected artifact classification: %+v", sources)
		}
	}
}

// TestCompiledArtifactForChunkExcludesInternalKeywords pins the provenance
// index behavior: internal plumbing keywords never classify as direct
// artifacts even when a provenance index is present.
func TestCompiledArtifactForChunkExcludesInternalKeywords(t *testing.T) {
	index := &compiledArtifactProvenanceIndex{
		byContent: map[string]compiledArtifactProvenance{},
		bySourceChunk: map[string]compiledArtifactProvenance{
			"source-1": {ArtifactID: "operations-page", PageType: "page_index", Direct: true, Derived: true},
		},
	}
	chunk := map[string]interface{}{
		"id": "some-draft", "compile_kwd": "wiki_page_draft", "content": "draft json",
	}
	provenance := compiledArtifactForChunk(chunk, index)
	if provenance.Direct {
		t.Fatalf("internal plumbing row must not classify as direct artifact: %+v", provenance)
	}
	sourceChunk := map[string]interface{}{
		"id": "source-1", "compile_kwd": "wiki_page_draft", "content": "raw chunk content",
	}
	provenance = compiledArtifactForChunk(sourceChunk, index)
	if !provenance.Direct || provenance.ArtifactID != "operations-page" {
		t.Fatalf("source chunk identity match must still attribute the artifact: %+v", provenance)
	}
	artifactPage := map[string]interface{}{
		"id": "page-row", "compile_kwd": "wiki_page", "content": "rendered page",
	}
	provenance = compiledArtifactForChunk(artifactPage, index)
	if !provenance.Direct || provenance.MatchType != "compile_kwd" {
		t.Fatalf("recognized artifact page must classify directly: %+v", provenance)
	}
}
