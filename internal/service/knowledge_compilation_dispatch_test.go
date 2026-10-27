package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func createCompilationFixture(t *testing.T) (*Service, *ragflow.Mock, string, string, string) {
	t.Helper()
	svc := newAuthzSvc(t)
	mock := svc.RAGFlow.(*ragflow.Mock)
	ctx := context.Background()
	tenant, _, project := id.New(), id.New(), id.New()
	datasetID := id.New()
	ragflowDataset, err := mock.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "Compilation Docs"})
	if err != nil {
		t.Fatalf("create ragflow dataset: %v", err)
	}
	dataset := &model.DatasetLink{
		ID: datasetID, TenantID: tenant, RAGFlowDatasetID: ragflowDataset.ID,
		Name: "Compilation Docs", ProjectID: project,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create dataset link: %v", err)
	}
	document, err := mock.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{
		Name: "manual.pdf", Content: []byte("manual"),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "pipeline-1"}, compilationPipelineDSL())
	if err := mock.UpdateDocumentParseConfig(ctx, dataset.RAGFlowDatasetID, document.ID, ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: "pipeline-1",
	}); err != nil {
		t.Fatalf("configure pipeline: %v", err)
	}
	return svc, mock, tenant, datasetID, document.ID
}

func compilationPipelineDSL() map[string]interface{} {
	return map[string]interface{}{
		"components": map[string]interface{}{
			"file": map[string]interface{}{
				"downstream": []interface{}{"parser"},
				"obj": map[string]interface{}{
					"component_name": "File",
					"params":         map[string]interface{}{},
				},
			},
			"parser": map[string]interface{}{
				"downstream": []interface{}{"chunker"},
				"obj":        map[string]interface{}{"component_name": "Parser", "params": map[string]interface{}{}},
			},
			"chunker": map[string]interface{}{
				"downstream": []interface{}{"compiler"},
				"obj":        map[string]interface{}{"component_name": "Chunker", "params": map[string]interface{}{}},
			},
			"compiler": map[string]interface{}{
				"downstream": []interface{}{"indexer"},
				"obj": map[string]interface{}{
					"component_name": "Compiler",
					"params": map[string]interface{}{
						"compilation_template_group_id": "group-1",
					},
				},
			},
			"indexer": map[string]interface{}{
				"downstream": []interface{}{},
				"obj":        map[string]interface{}{"component_name": "Tokenizer", "params": map[string]interface{}{}},
			},
		},
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{"id": "file", "type": "fileNode"},
				map[string]interface{}{"id": "parser", "type": "parserNode"},
				map[string]interface{}{"id": "chunker", "type": "chunkerNode"},
				map[string]interface{}{"id": "compiler", "type": "compilationNode"},
				map[string]interface{}{"id": "indexer", "type": "tokenizerNode"},
			},
			"edges": []interface{}{
				map[string]interface{}{"source": "file", "target": "parser"},
				map[string]interface{}{"source": "parser", "target": "chunker"},
				map[string]interface{}{"source": "chunker", "target": "compiler"},
				map[string]interface{}{"source": "compiler", "target": "indexer"},
			},
		},
	}
}

func compilationPipelineContractDetails(t *testing.T, run *model.CompilationRun) map[string]interface{} {
	t.Helper()
	detail := map[string]interface{}{}
	if err := json.Unmarshal([]byte(run.Detail), &detail); err != nil {
		t.Fatalf("decode run detail: %v", err)
	}
	contracts, ok := detail["pipeline_contracts"].(map[string]interface{})
	if !ok || len(contracts) != 1 {
		t.Fatalf("expected one pipeline contract in detail: %+v", detail)
	}
	return contracts
}

func setCompilationArtifactDetail(
	t *testing.T, mock *ragflow.Mock, datasetID, artifactID, documentID string,
) {
	t.Helper()
	mock.SetDatasetArtifactDetail(datasetID, ragflow.DatasetArtifactDetail{
		Slug: artifactID, Title: "Manual", PageType: model.CompilationKindPageIndex,
		ContentMDRendered: "# Manual", SourceChunkIDs: []string{"chunk-1"},
		SourceDocIDs: []string{documentID},
	})
}

func TestCompilationRunDispatchesConfiguredDocumentsAndProjectsUnverifiedArtifacts(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())

	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-1", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if run.State != model.CompilationRunAccepted || run.Kind != model.CompilationKindPageIndex {
		t.Fatalf("unexpected accepted run: %+v", run)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("dispatch compilation run: %v", err)
	}
	dispatched, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || dispatched == nil || dispatched.State != model.CompilationRunDispatched {
		t.Fatalf("expected dispatched run, got %+v err=%v", dispatched, err)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 1 || got[0] != documentID {
		t.Fatalf("unexpected ingest ids: %+v", got)
	}
	if dispatched.ManifestHash == "" || dispatched.TemplateHash == "" || dispatched.PipelineHash == "" ||
		!dispatched.ManifestComplete || dispatched.DocumentIDs == "" {
		t.Fatalf("dispatch manifest is incomplete: %+v", dispatched)
	}
	contracts := compilationPipelineContractDetails(t, dispatched)
	contract, ok := contracts["pipeline-1"].(map[string]interface{})
	if !ok || contract["node_count"] != float64(5) || contract["has_parser"] != true ||
		contract["has_chunker"] != true || contract["has_compiler"] != true ||
		contract["has_indexer"] != true || contract["path_valid"] != true {
		t.Fatalf("invalid persisted pipeline contract: %+v", contract)
	}

	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "unknown-lifecycle"})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile unknown state: %v", err)
	}
	unknown, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || unknown == nil || unknown.State != model.CompilationRunUnknown {
		t.Fatalf("unrecognized upstream state must become unknown: %+v err=%v", unknown, err)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 1 {
		t.Fatalf("unknown reconciliation must not resend: %+v", got)
	}

	mock.SetDatasetArtifacts(upstream.RAGFlowDatasetID, []ragflow.DatasetArtifact{{
		Slug: "page_index/manual", Title: "Manual", PageType: "page_index",
	}})
	setCompilationArtifactDetail(t, mock, upstream.RAGFlowDatasetID, "page_index/manual", documentID)
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "completed"})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("complete compilation run: %v", err)
	}
	confirmed, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || confirmed == nil || confirmed.State != model.CompilationRunConfirmed {
		t.Fatalf("expected confirmed run: %+v err=%v", confirmed, err)
	}
	projections, _, err := svc.Store.ListArtifactProjections(ctx, tenant, datasetID, run.ID, 1, 10)
	if err != nil || len(projections) != 1 {
		t.Fatalf("expected one projection: %+v err=%v", projections, err)
	}
	if projections[0].State != model.ArtifactProjectionReady ||
		projections[0].ArtifactID != "page_index/manual" || projections[0].ArtifactHash == "" {
		t.Fatalf("artifact projection must be ready payload evidence: %+v", projections[0])
	}
	snapshot := map[string]interface{}{}
	if err := json.Unmarshal([]byte(projections[0].ArtifactJSON), &snapshot); err != nil {
		t.Fatalf("decode artifact snapshot: %v", err)
	}
	if snapshot["run_binding"] != true || snapshot["content_hash_binding"] != true ||
		snapshot["content_hash_semantics"] != "artifact_payload_sha256_v1" ||
		!strings.Contains(projections[0].ArtifactJSON, documentID) {
		t.Fatalf("artifact run/content binding missing: %+v", snapshot)
	}
	if projections[0].ContentHash == "" || projections[0].ContentHash != snapshot["content_hash"] {
		t.Fatalf("artifact content hash must use 137 schema semantics: %+v", projections[0])
	}
	if confirmed.SourceConsistency != model.CompilationSourceConsistencyConsistent ||
		confirmed.PostRunSourceManifestHash != confirmed.ManifestHash {
		t.Fatalf("post-run source consistency was not proven: %+v", confirmed)
	}
	if !strings.Contains(confirmed.Detail, `"artifact_run_binding":true`) ||
		!strings.Contains(confirmed.Detail, `"artifact_hash_binding":true`) {
		t.Fatalf("confirmed run lacks binding evidence: %s", confirmed.Detail)
	}
}

func TestCompilationCompletedArtifactsRequireRunAndContentBinding(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())
	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-unbound", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("dispatch compilation run: %v", err)
	}
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	mock.SetDatasetArtifacts(upstream.RAGFlowDatasetID, []ragflow.DatasetArtifact{{
		Slug: "page_index/manual", Title: "Manual", PageType: "page_index",
	}})
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "completed"})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile unbound artifact: %v", err)
	}
	current, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || current == nil || current.State != model.CompilationRunUnknown {
		t.Fatalf("missing artifact detail must fail closed: %+v err=%v", current, err)
	}
	projections, _, err := svc.Store.ListArtifactProjections(ctx, tenant, datasetID, run.ID, 1, 10)
	if err != nil || len(projections) != 1 || projections[0].State != model.ArtifactProjectionUnverified {
		t.Fatalf("unbound artifact must stay unverified: %+v err=%v", projections, err)
	}
	if !strings.Contains(current.Detail, `"binding_incomplete_count":1`) {
		t.Fatalf("binding evidence missing: %s", current.Detail)
	}
	if projections[0].ContentHash != "" {
		t.Fatalf("unverified artifact must not claim a content hash: %+v", projections[0])
	}
}

func TestCompilationCompletedIgnoresArtifactsFromOtherRuns(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())
	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-isolated", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("dispatch compilation run: %v", err)
	}
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	otherDocumentID := id.New()
	artifactID := "page_index/manual"
	otherArtifactID := "page_index/stale"
	mock.SetDatasetArtifacts(upstream.RAGFlowDatasetID, []ragflow.DatasetArtifact{
		{Slug: artifactID, Title: "Manual", PageType: "page_index"},
		{Slug: otherArtifactID, Title: "Stale", PageType: "page_index"},
	})
	setCompilationArtifactDetail(t, mock, upstream.RAGFlowDatasetID, artifactID, documentID)
	mock.SetDatasetArtifactDetail(upstream.RAGFlowDatasetID, ragflow.DatasetArtifactDetail{
		Slug: otherArtifactID, Title: "Stale", PageType: "page_index",
		ContentMDRendered: "# Stale", SourceChunkIDs: []string{"stale-chunk"},
		SourceDocIDs: []string{otherDocumentID},
	})
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "completed"})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("complete compilation run with stale artifact: %v", err)
	}
	confirmed, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || confirmed == nil || confirmed.State != model.CompilationRunConfirmed {
		t.Fatalf("stale artifact must not block run confirmation: %+v err=%v", confirmed, err)
	}
	projections, _, err := svc.Store.ListArtifactProjections(ctx, tenant, datasetID, run.ID, 1, 10)
	if err != nil || len(projections) != 1 || projections[0].ArtifactID != artifactID ||
		projections[0].State != model.ArtifactProjectionReady {
		t.Fatalf("expected only source-bound ready projection: %+v err=%v", projections, err)
	}
	if strings.Contains(projections[0].ArtifactJSON, otherDocumentID) {
		t.Fatalf("projection leaked another run source: %+v", projections[0])
	}
	filter := mock.LastDatasetArtifactFilter()
	if filter == nil || filter.PageType != model.CompilationKindPageIndex {
		t.Fatalf("PageIndex completion must query only PageIndex artifacts: %+v", filter)
	}
}

func TestCompilationArtifactFilterMatchesUpstreamCapability(t *testing.T) {
	pageIndex := compilationArtifactFilterForKind(model.CompilationKindPageIndex, 2)
	if pageIndex.Page != 2 || pageIndex.PageSize != 100 || pageIndex.PageType != model.CompilationKindPageIndex {
		t.Fatalf("PageIndex artifact filter lost kind binding: %+v", pageIndex)
	}
	wiki := compilationArtifactFilterForKind(model.CompilationKindWiki, 3)
	if wiki.Page != 3 || wiki.PageSize != 100 || wiki.PageType != "" {
		t.Fatalf("Wiki artifact listing must include all Wiki page categories: %+v", wiki)
	}
}

func TestCompilationTriggerRejectsDocumentWithoutPipeline(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	if err := mock.UpdateDocumentParseConfig(ctx, upstream.RAGFlowDatasetID, documentID, ragflow.DocumentParseConfigUpdate{
		BuiltinParserID: "naive", Pipeline: false,
	}); err != nil {
		t.Fatalf("remove pipeline binding: %v", err)
	}
	if _, err := svc.TriggerCompilationRun(ctx, tenant, "user", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-invalid", DocumentIDs: []string{documentID},
	}); err == nil {
		t.Fatal("builtin-only document must fail compilation dispatch")
	}
}

func TestCompilationDispatchFailsClosedWithoutCompiler(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	dsl := compilationPipelineDSL()
	components := dsl["components"].(map[string]interface{})
	delete(components, "compiler")
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "pipeline-1"}, dsl)
	svc.SetupWorker(fastWorkerConfig())

	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-no-compiler", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("process invalid compilation run: %v", err)
	}
	failed, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || failed == nil || failed.State != model.CompilationRunFailed {
		t.Fatalf("missing compiler must fail the run: %+v err=%v", failed, err)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 0 {
		t.Fatalf("invalid DSL must not dispatch parse: %+v", got)
	}
	if !strings.Contains(failed.Detail, "pipeline_contract_invalid") {
		t.Fatalf("expected pipeline contract failure reason: %s", failed.Detail)
	}
}

func TestCompilationDispatchFailsClosedWithoutCompilerToIndexerPath(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	dsl := compilationPipelineDSL()
	graph := dsl["graph"].(map[string]interface{})
	edges := graph["edges"].([]interface{})
	graph["edges"] = append(edges[:3:3], map[string]interface{}{"source": "chunker", "target": "file"})
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "pipeline-1"}, dsl)
	svc.SetupWorker(fastWorkerConfig())

	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-broken-path", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("process broken-path compilation run: %v", err)
	}
	failed, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || failed == nil || failed.State != model.CompilationRunFailed {
		t.Fatalf("compiler without indexer path must fail the run: %+v err=%v", failed, err)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 0 {
		t.Fatalf("unreachable indexer must not dispatch parse: %+v", got)
	}
	if !strings.Contains(failed.Detail, "pipeline_contract_invalid") {
		t.Fatalf("expected pipeline contract failure reason: %s", failed.Detail)
	}
}

type dispatchRaceStore struct {
	repository.Store
	entered chan struct{}
	release chan struct{}
}

func (s *dispatchRaceStore) TransitionCompilationRun(
	ctx context.Context, tenantID, runID string, fromStates []string, updates map[string]interface{},
) (bool, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.Store.TransitionCompilationRun(ctx, tenantID, runID, fromStates, updates)
}

func TestCompilationDispatchExternalCallIsLinearized(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())

	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-race", Kind: "page_index", DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	svc.Store = &dispatchRaceStore{Store: svc.Store, entered: entered, release: release}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			results <- svc.ProcessCompilationRuns(ctx, tenant, datasetID, "")
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("timed out waiting for concurrent dispatch admission")
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent compilation dispatch: %v", err)
		}
	}

	if got := mock.LastIngestDocumentIDs(); len(got) != 1 || got[0] != documentID {
		t.Fatalf("external ingestion must be called once, got %+v", got)
	}
	dispatched, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || dispatched == nil || dispatched.State != model.CompilationRunDispatched {
		t.Fatalf("expected one dispatched run: %+v err=%v", dispatched, err)
	}
}
