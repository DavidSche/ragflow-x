package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// doc/262 §C: the engine kind mapping is version banded and page_index is a
// structure variant on 1.0+, so the mapping table is the single source of truth
// for drills, handlers and the trigger gate.
func TestResolveCompilationEngineKindBands(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		version    string
		wantKind   string
		wantStatus string
		wantReason string
		wantAlt    string
	}{
		{
			name: "legacy page_index keeps its own contract", kind: "page_index", version: "0.27.0",
			wantKind: model.CompilationKindPageIndex, wantStatus: CompilationEngineKindSupported,
			wantReason: CompilationEngineReasonLegacyKindContract,
		},
		{
			// The whole 1.0 line reclassified page_index (the drift was first seen on
			// the rc tag), so prereleases are banded with stable: never assume an rc
			// still owns the legacy artifact surface.
			name: "1.0 rc tag is banded with 1.0 stable", kind: "page_index", version: "1.0.0-rc3",
			wantKind: model.CompilationKindWiki, wantStatus: CompilationEngineKindUnsupported,
			wantReason: CompilationEngineReasonArtifactAPIWikiOnly, wantAlt: model.CompilationKindWiki,
		},
		{
			name: "v1 page_index is rejected with a declared alternative", kind: "page_index", version: "1.0.0",
			wantKind: model.CompilationKindWiki, wantStatus: CompilationEngineKindUnsupported,
			wantReason: CompilationEngineReasonArtifactAPIWikiOnly, wantAlt: model.CompilationKindWiki,
		},
		{
			name: "v1 pageindex alias resolves the same", kind: "pageindex", version: "v1.0.0",
			wantKind: model.CompilationKindWiki, wantStatus: CompilationEngineKindUnsupported,
			wantReason: CompilationEngineReasonArtifactAPIWikiOnly, wantAlt: model.CompilationKindWiki,
		},
		{
			name: "v1 wiki is the probe-verified artifact surface", kind: "wiki", version: "1.0.0",
			wantKind: model.CompilationKindWiki, wantStatus: CompilationEngineKindSupported,
			wantReason: CompilationEngineReasonV1WikiArtifactProbe,
		},
		{
			name: "v1 tree is allowed but explicitly unverified", kind: "tree", version: "1.0.0",
			wantKind: model.CompilationKindTree, wantStatus: CompilationEngineKindUnverified,
			wantReason: CompilationEngineReasonArtifactReadUnverified,
		},
		{
			name: "unobservable version keeps the legacy contract", kind: "page_index", version: "",
			wantKind: model.CompilationKindPageIndex, wantStatus: CompilationEngineKindUnverified,
			wantReason: CompilationEngineReasonVersionUnobservable,
		},
		{
			name: "unknown kind is rejected", kind: "not_a_kind", version: "1.0.0",
			wantStatus: CompilationEngineKindUnsupported, wantReason: CompilationEngineReasonUnknownKind,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resolution := resolveCompilationEngineKind(testCase.kind, testCase.version)
			if resolution.EngineKind != testCase.wantKind {
				t.Fatalf("engine kind: got %q want %q (%+v)", resolution.EngineKind, testCase.wantKind, resolution)
			}
			if resolution.Status != testCase.wantStatus {
				t.Fatalf("status: got %q want %q (%+v)", resolution.Status, testCase.wantStatus, resolution)
			}
			if resolution.Reason != testCase.wantReason {
				t.Fatalf("reason: got %q want %q (%+v)", resolution.Reason, testCase.wantReason, resolution)
			}
			if resolution.Alternative != testCase.wantAlt {
				t.Fatalf("alternative: got %q want %q (%+v)", resolution.Alternative, testCase.wantAlt, resolution)
			}
			if resolution.Status == CompilationEngineKindUnsupported && resolution.Bindable() {
				t.Fatalf("unsupported resolutions must not be bindable: %+v", resolution)
			}
		})
	}
}

// The gate must fail closed before a run row exists, so callers see an
// actionable 422 instead of a run that can only ever end in unknown.
func TestTriggerCompilationRunRejectsUnsupportedEngineKind(t *testing.T) {
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	mock.SetEngineVersion("1.0.0")
	svc.SetupWorker(fastWorkerConfig())
	ctx := context.Background()

	_, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "engine-kind-gate-1", Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{documentID},
	})
	if err == nil {
		t.Fatal("page_index on the 1.0+ band must be rejected")
	}
	if !expectHTTPError(err, 422, 42261) {
		t.Fatalf("expected 422/42261, got %v", err)
	}
	if !strings.Contains(err.Error(), "use kind wiki instead") {
		t.Fatalf("rejection must name the declarative alternative: %v", err)
	}
	if runs, total, listErr := svc.Store.ListCompilationRuns(ctx, tenant, repository.CompilationRunFilter{DatasetID: datasetID}, 1, 10); listErr != nil || total != 0 || len(runs) != 0 {
		t.Fatalf("rejected trigger must not leave a run row: total=%d err=%v", total, listErr)
	}

	// The declared alternative is accepted on the same band and records the
	// resolution so evidence never claims something the engine did not do.
	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "engine-kind-gate-2", Kind: model.CompilationKindWiki,
		DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("wiki on the 1.0+ band must be accepted: %v", err)
	}
	if run.Kind != model.CompilationKindWiki {
		t.Fatalf("accepted run kind: got %q want wiki", run.Kind)
	}
	if !strings.Contains(run.Detail, `"engine_kind":"wiki"`) ||
		!strings.Contains(run.Detail, `"engine_band":"1.0+"`) ||
		!strings.Contains(run.Detail, `"status":"supported"`) {
		t.Fatalf("accepted run must record the engine kind resolution: %s", run.Detail)
	}
}

// The legacy band must stay dispatchable: the mapping change must not break
// engines that still own the page_index artifact surface.
func TestTriggerCompilationRunLegacyBandKeepsPageIndex(t *testing.T) {
	svc, mock, tenant, datasetID, documentID := createCompilationFixture(t)
	mock.SetEngineVersion("0.27.0")
	svc.SetupWorker(fastWorkerConfig())
	ctx := context.Background()

	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "engine-kind-legacy-1", Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("legacy band must accept page_index: %v", err)
	}
	if run.Kind != model.CompilationKindPageIndex {
		t.Fatalf("legacy run kind: got %q want page_index", run.Kind)
	}
	if !strings.Contains(run.Detail, `"engine_band":"legacy"`) ||
		!strings.Contains(run.Detail, `"reason":"legacy_kind_contract"`) {
		t.Fatalf("legacy run must record the legacy kind contract: %s", run.Detail)
	}
}
