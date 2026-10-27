package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Milestone 212: plan-137 §4.3 compilation lifecycle invariants — the
// single-active-run guard on trigger, the §4.3 "cancelled records
// cancel_scope" contract, and the refusal matrix for non-cancellable states.

func triggerCompilationRunForTest(
	t *testing.T, svc *Service, ctx context.Context, tenant, datasetID, documentID, key string,
) *model.CompilationRun {
	t.Helper()
	svc.SetupWorker(fastWorkerConfig())
	run, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: key, Kind: model.CompilationKindPageIndex, DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	return run
}

func TestCompilationTriggerConflictsWithActiveRun(t *testing.T) {
	ctx := context.Background()
	svc, _, tenant, datasetID, documentID := createCompilationFixture(t)
	first := triggerCompilationRunForTest(t, svc, ctx, tenant, datasetID, documentID, "compile-active-1")

	_, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-active-2", Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{documentID},
	})
	var conflict *httperr.Error
	if err == nil || !(func() bool { conflict, _ = err.(*httperr.Error); return conflict != nil }()) {
		t.Fatalf("expected 409 conflict for second active run, got %v", err)
	}
	if conflict.Code != 40978 || conflict.Status != 409 {
		t.Fatalf("unexpected conflict error: %+v", conflict)
	}

	// Same idempotency key still returns the original run (idempotency
	// semantics take precedence over the conflict guard).
	replayed, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-active-1", Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("idempotent replay must succeed: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("idempotent replay returned different run %s != %s", replayed.ID, first.ID)
	}

	// A different artifact kind is a separate invariant scope: triggering it
	// must succeed.
	other, err := svc.TriggerCompilationRun(ctx, tenant, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "compile-active-wiki", Kind: model.CompilationKindWiki,
		DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger second kind must succeed: %v", err)
	}
	if other.ID == first.ID || other.Kind != model.CompilationKindWiki {
		t.Fatalf("unexpected second-kind run: %+v", other)
	}
}

func TestCompilationCancelAcceptedRunRecordsCancelScope(t *testing.T) {
	ctx := context.Background()
	svc, _, tenant, datasetID, documentID := createCompilationFixture(t)
	run := triggerCompilationRunForTest(t, svc, ctx, tenant, datasetID, documentID, "compile-cancel-1")

	cancelled, err := svc.CancelCompilationRun(ctx, tenant, "requester", datasetID, run.ID, "operator stop")
	if err != nil {
		t.Fatalf("cancel accepted run: %v", err)
	}
	if cancelled.State != model.CompilationRunCancelled {
		t.Fatalf("expected cancelled run, got %+v", cancelled)
	}
	if cancelled.CancelScope != model.CompilationCancelScopeXSchedulingOnly {
		t.Fatalf("expected cancel_scope x_scheduling_only, got %q", cancelled.CancelScope)
	}
	if !strings.Contains(cancelled.Detail, "cancelled_by_operator") {
		t.Fatalf("expected cancel reason appended to detail, got %q", cancelled.Detail)
	}
	// The detail column must stay a single valid JSON object after the cancel
	// merge; string-concatenation would produce `{...} {...}` and silently
	// break decodeCompilationRunDetail consumers.
	var detailCheck map[string]interface{}
	if err := json.Unmarshal([]byte(cancelled.Detail), &detailCheck); err != nil {
		t.Fatalf("cancelled detail is not valid JSON: %v (%q)", err, cancelled.Detail)
	}
	if detailCheck["reason"] != "cancelled_by_operator" || detailCheck["dispatch_policy"] == nil {
		t.Fatalf("cancel merge lost original detail keys: %v", detailCheck)
	}

	// Re-triggering the same (tenant, dataset, kind) must now succeed.
	again := triggerCompilationRunForTest(t, svc, ctx, tenant, datasetID, documentID, "compile-cancel-2")
	if again.ID == run.ID {
		t.Fatal("expected a new run after cancellation")
	}

	// Cancelling a terminal run is refused.
	if _, err := svc.CancelCompilationRun(ctx, tenant, "requester", datasetID, run.ID, ""); err == nil {
		t.Fatal("cancelling a cancelled run must fail")
	} else if conflict, ok := err.(*httperr.Error); !ok || conflict.Code != 40979 {
		t.Fatalf("unexpected terminal-cancel error: %v", err)
	}
}

func TestCompilationCancelRefusesDispatchedUnknownAndTerminal(t *testing.T) {
	ctx := context.Background()
	// Each state scenario needs its own dataset: the single-active-run
	// invariant (migration 147) keeps the first run in the active set.
	for _, scenario := range []struct {
		nextState string
		key       string
	}{{
		nextState: model.CompilationRunDispatched, key: "compile-ref-1",
	}, {
		nextState: model.CompilationRunUnknown, key: "compile-ref-2",
	}} {
		svc, _, tenant, datasetID, documentID := createCompilationFixture(t)
		run := triggerCompilationRunForTest(t, svc, ctx, tenant, datasetID, documentID, scenario.key)
		if _, err := svc.Store.TransitionCompilationRun(ctx, tenant, run.ID,
			[]string{model.CompilationRunAccepted},
			map[string]interface{}{"state": scenario.nextState},
		); err != nil {
			t.Fatalf("transition to %s: %v", scenario.nextState, err)
		}
		if _, err := svc.CancelCompilationRun(ctx, tenant, "requester", datasetID, run.ID, ""); err == nil {
			t.Fatalf("cancelling a %s run must fail", scenario.nextState)
		} else if conflict, ok := err.(*httperr.Error); !ok || conflict.Code != 40979 {
			t.Fatalf("unexpected %s-cancel error: %v", scenario.nextState, err)
		}
	}
}

func TestCompilationCancelReleasesBudgetReservation(t *testing.T) {
	ctx := context.Background()
	svc, _, tenant, datasetID, documentID := createCompilationFixture(t)
	run := triggerCompilationRunForTest(t, svc, ctx, tenant, datasetID, documentID, "compile-budget-1")
	if run.QuotaReservationID == "" {
		t.Fatal("expected trigger-time budget reservation on the run")
	}
	reservation, err := svc.Store.GetQuotaReservation(ctx, run.QuotaReservationID)
	if err != nil || reservation == nil {
		t.Fatalf("load reservation: %+v err=%v", reservation, err)
	}
	if reservation.Status != model.QuotaReservationPending {
		t.Fatalf("expected pending reservation before cancel, got %q", reservation.Status)
	}
	if _, err := svc.CancelCompilationRun(ctx, tenant, "requester", datasetID, run.ID, ""); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	reservation, err = svc.Store.GetQuotaReservation(ctx, run.QuotaReservationID)
	if err != nil || reservation == nil {
		t.Fatalf("reload reservation: %+v err=%v", reservation, err)
	}
	if reservation.Status != model.QuotaReservationReleased {
		t.Fatalf("expected released reservation after cancel, got %q", reservation.Status)
	}
}

func TestCompilationCancelRejectsForeignDatasetAndRun(t *testing.T) {
	ctx := context.Background()
	svc, _, tenant, datasetID, documentID := createCompilationFixture(t)
	run := triggerCompilationRunForTest(t, svc, ctx, tenant, datasetID, documentID, "compile-foreign-1")

	// Run not bound to the given dataset.
	if _, err := svc.CancelCompilationRun(ctx, tenant, "requester", datasetID+"-other", run.ID, ""); err == nil {
		t.Fatal("cancel against foreign dataset must fail")
	} else if _, ok := err.(*httperr.Error); ok && err.(*httperr.Error).Status != 404 {
		t.Fatalf("expected 404 for foreign dataset, got %v", err)
	}
	// Nonexistent run.
	if _, err := svc.CancelCompilationRun(ctx, tenant, "requester", datasetID, "missing-run", ""); err == nil {
		t.Fatal("cancel for missing run must fail")
	}
}
