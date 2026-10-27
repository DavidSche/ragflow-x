package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func dispatchCompilationForStateTest(t *testing.T) (*Service, *ragflow.Mock, string, string, *model.CompilationRun) {
	t.Helper()
	ctx := context.Background()
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())
	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "state-" + documentID, Kind: "page_index", DocumentIDs: []string{documentID},
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
	return svc, mock, tenant, datasetID, run
}

func compilationRunDocumentIDs(t *testing.T, run *model.CompilationRun) []string {
	t.Helper()
	documentIDs := []string{}
	if err := json.Unmarshal([]byte(run.DocumentIDs), &documentIDs); err != nil {
		t.Fatalf("decode compilation run document ids: %v", err)
	}
	return documentIDs
}

func TestCompilationRunningErrorIsDiagnosticNotFailure(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, run := dispatchCompilationForStateTest(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{
		State: "running", Error: "previous batch retry diagnostic", Progress: 0.12,
		CurrentPhase: "deleting", Inflight: 3, UpdatedAt: time.Now().UTC().Add(-time.Minute),
	})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile running diagnostic: %v", err)
	}
	reconciled, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || reconciled == nil || reconciled.State != model.CompilationRunDispatched {
		t.Fatalf("running error must not fail the run: %+v err=%v", reconciled, err)
	}
	if !strings.Contains(reconciled.Detail, `"reason":"upstream_running"`) ||
		!strings.Contains(reconciled.Detail, `"upstream_error":"previous batch retry diagnostic"`) ||
		!strings.Contains(reconciled.Detail, `"inflight":3`) {
		t.Fatalf("diagnostics were not persisted: %s", reconciled.Detail)
	}
}

func TestCompilationUnknownStateFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, run := dispatchCompilationForStateTest(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "deleting"})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile unknown state: %v", err)
	}
	reconciled, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || reconciled == nil || reconciled.State != model.CompilationRunUnknown {
		t.Fatalf("unknown state must become unknown: %+v err=%v", reconciled, err)
	}
	if !strings.Contains(reconciled.Detail, `"reason":"upstream_state_unrecognized"`) {
		t.Fatalf("unknown state reason missing: %s", reconciled.Detail)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) == 0 {
		t.Fatal("expected the initial dispatch to have occurred")
	}
}

func TestCompilationPostDispatchIdleWithLastCompletedAtConfirms(t *testing.T) {
	dispatchedAt := time.Now().UTC().Add(-time.Minute)
	completedAt := dispatchedAt.Add(time.Second)
	status := &ragflow.CompilationStatus{State: "idle", LastCompletedAt: &completedAt}
	run := &model.CompilationRun{DispatchedAt: &dispatchedAt}
	if !compilationStatusCompletedAfterDispatch(run, status) {
		t.Fatal("post-dispatch completed idle status must be treated as completed")
	}

	beforeDispatch := dispatchedAt.Add(-time.Second)
	run = &model.CompilationRun{DispatchedAt: &dispatchedAt}
	status = &ragflow.CompilationStatus{State: "idle", LastCompletedAt: &beforeDispatch}
	if compilationStatusCompletedAfterDispatch(run, status) {
		t.Fatal("pre-dispatch completed idle status must remain unknown")
	}

	status = &ragflow.CompilationStatus{State: "idle", UpdatedAt: dispatchedAt.Add(time.Second)}
	run = &model.CompilationRun{DispatchedAt: &dispatchedAt}
	if compilationStatusCompletedAfterDispatch(run, status) {
		t.Fatal("idle status without last completed timestamp must remain unknown")
	}
}

func TestCompilationRunningSourceDriftFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, run := dispatchCompilationForStateTest(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	if err := mock.UpdateDocumentParseConfig(ctx, upstream.RAGFlowDatasetID, compilationRunDocumentIDs(t, run)[0], ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: "pipeline-2",
	}); err != nil {
		t.Fatalf("drift compilation source: %v", err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{
		State: "running", Progress: 0.25,
	})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile source drift: %v", err)
	}
	reconciled, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || reconciled == nil || reconciled.State != model.CompilationRunFailed {
		t.Fatalf("source drift must fail closed: %+v err=%v", reconciled, err)
	}
	if reconciled.SourceConsistency != model.CompilationSourceConsistencyChanged ||
		!strings.Contains(reconciled.Detail, `"reason":"source_manifest_changed_during_execution"`) ||
		!strings.Contains(reconciled.Detail, "post_run_source_manifest_hash") {
		t.Fatalf("source drift evidence missing: %+v %s", reconciled, reconciled.Detail)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 1 {
		t.Fatalf("source drift reconciliation must not resend: %+v", got)
	}
}

func TestCompilationExecutionFieldsDoNotChangeSourceIdentity(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, run := dispatchCompilationForStateTest(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	documentID := compilationRunDocumentIDs(t, run)[0]
	if err := mock.SetDocumentExecutionState(ctx, upstream.RAGFlowDatasetID, documentID, "running", 7, time.Now().UTC().Add(time.Minute).Unix()); err != nil {
		t.Fatalf("simulate execution fields: %v", err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{
		State: "running", Progress: 0.75, CurrentPhase: "compiling",
	})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile running execution fields: %v", err)
	}
	running, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || running == nil || running.State != model.CompilationRunDispatched {
		t.Fatalf("mutable execution fields must not fail running run: %+v err=%v", running, err)
	}
	if !strings.Contains(running.Detail, `"reason":"upstream_running"`) {
		t.Fatalf("running evidence missing: %s", running.Detail)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 1 {
		t.Fatalf("mutable execution reconciliation must not resend: %+v", got)
	}

	if err := mock.SetDocumentExecutionState(ctx, upstream.RAGFlowDatasetID, documentID, "parsed", 7, time.Now().UTC().Add(2*time.Minute).Unix()); err != nil {
		t.Fatalf("simulate completed execution fields: %v", err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "completed"})
	mock.SetDatasetArtifacts(upstream.RAGFlowDatasetID, []ragflow.DatasetArtifact{
		{Slug: "page-index/manual", Title: "Manual", PageType: model.CompilationKindPageIndex},
	})
	setCompilationArtifactDetail(t, mock, upstream.RAGFlowDatasetID, "page-index/manual", compilationRunDocumentIDs(t, run)[0])
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile completed execution fields: %v", err)
	}
	confirmed, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || confirmed == nil || confirmed.State != model.CompilationRunConfirmed {
		t.Fatalf("mutable execution fields must not prevent confirmation: %+v err=%v", confirmed, err)
	}
	if confirmed.SourceConsistency != model.CompilationSourceConsistencyConsistent ||
		confirmed.PostRunSourceManifestHash != confirmed.ManifestHash {
		t.Fatalf("stable source identity was not preserved: %+v", confirmed)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 1 {
		t.Fatalf("completed reconciliation must not resend: %+v", got)
	}
}

func TestCompilationRunningStallBecomesUnknownWithoutResend(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, run := dispatchCompilationForStateTest(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{
		State: "running", Progress: 0.12, CurrentPhase: "deleting",
		Inflight: 3, Backlog: 0, UpdatedAt: time.Now().UTC().Add(-25 * time.Hour),
	})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile stalled running state: %v", err)
	}
	reconciled, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || reconciled == nil || reconciled.State != model.CompilationRunUnknown {
		t.Fatalf("stalled running must become unknown: %+v err=%v", reconciled, err)
	}
	if !strings.Contains(reconciled.Detail, `"reason":"upstream_stalled"`) ||
		!strings.Contains(reconciled.Detail, `"current_phase":"deleting"`) ||
		!strings.Contains(reconciled.Detail, `"inflight":3`) ||
		!strings.Contains(reconciled.Detail, `"stall_threshold_seconds":86400`) {
		t.Fatalf("stall evidence missing: %s", reconciled.Detail)
	}
	if got := mock.LastIngestDocumentIDs(); len(got) != 1 {
		t.Fatalf("stalled reconciliation must not resend: %+v", got)
	}
}

func TestCompilationCompletedSourceDriftFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenant, datasetID, run := dispatchCompilationForStateTest(t)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenant, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load upstream dataset: %+v err=%v", upstream, err)
	}
	if err := mock.UpdateDocumentParseConfig(ctx, upstream.RAGFlowDatasetID, compilationRunDocumentIDs(t, run)[0], ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: "pipeline-2",
	}); err != nil {
		t.Fatalf("drift compilation source: %v", err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "completed"})
	if err := svc.ProcessCompilationRuns(ctx, tenant, datasetID, ""); err != nil {
		t.Fatalf("reconcile completed source drift: %v", err)
	}
	reconciled, err := svc.Store.GetCompilationRun(ctx, tenant, run.ID)
	if err != nil || reconciled == nil || reconciled.State != model.CompilationRunFailed {
		t.Fatalf("completed source drift must fail closed: %+v err=%v", reconciled, err)
	}
	if reconciled.SourceConsistency != model.CompilationSourceConsistencyChanged ||
		!strings.Contains(reconciled.Detail, `"reason":"post_run_source_manifest_changed"`) {
		t.Fatalf("completed drift evidence missing: %+v %s", reconciled, reconciled.Detail)
	}
	projections, _, err := svc.Store.ListArtifactProjections(ctx, tenant, datasetID, run.ID, 1, 10)
	if err != nil || len(projections) != 0 {
		t.Fatalf("drifted source must not create artifacts: %+v err=%v", projections, err)
	}
}
