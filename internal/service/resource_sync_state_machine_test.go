package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type plannedRunGateStore struct {
	repository.Store
	runID       string
	arrived     atomic.Int32
	start       chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func (s *plannedRunGateStore) GetSyncRun(ctx context.Context, id string) (*model.SyncRun, error) {
	run, err := s.Store.GetSyncRun(ctx, id)
	if err != nil || run == nil || run.ID != s.runID || run.Status != model.SyncRunPlanned {
		return run, err
	}
	if s.arrived.Add(1) == 1 {
		close(s.start)
	}
	<-s.release
	return run, nil
}

// ScenarioID: SC-SYNC-001
func TestP0_SYNC_001_ConcurrentImportClaimsSyncRunExactlyOnce(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Import Claim")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "claim-race"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.SyncRunPlanned {
		t.Fatalf("unexpected planned run: %+v", run)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("load sync item: total=%d err=%v", len(items), err)
	}
	externalID := items[0].ExternalID
	gated := &plannedRunGateStore{
		Store:   svc.Store,
		runID:   run.ID,
		start:   make(chan struct{}),
		release: make(chan struct{}),
	}
	originalStore := svc.Store
	svc.Store = gated
	t.Cleanup(func() {
		svc.Store = originalStore
	})

	type result struct {
		run *model.SyncRun
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			imported, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
			results <- result{run: imported, err: err}
		}()
	}
	<-gated.start
	gated.releaseOnce.Do(func() { close(gated.release) })
	wg.Wait()
	close(results)

	succeeded := 0
	conflicted := 0
	for outcome := range results {
		if outcome.err == nil {
			succeeded++
			continue
		}
		var businessErr *httperr.Error
		if errors.As(outcome.err, &businessErr) && businessErr.Status == 409 {
			conflicted++
			continue
		}
		t.Fatalf("unexpected import error: %v", outcome.err)
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("expected one import success and one 409 conflict, got success=%d conflict=%d", succeeded, conflicted)
	}

	stored, err := svc.Store.GetSyncRun(ctx, run.ID)
	if err != nil || stored == nil {
		t.Fatalf("load claimed run: %v", err)
	}
	if stored.Status != model.SyncRunSucceeded {
		t.Fatalf("claimed run status = %s, error = %s", stored.Status, stored.Error)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, externalID)
	if err != nil || binding == nil {
		t.Fatalf("load binding: %+v err=%v", binding, err)
	}
	versions, total, err := svc.Store.ListBindingVersions(ctx, binding.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(versions) != 1 {
		t.Fatalf("one successful import must create one binding version, got total=%d rows=%d", total, len(versions))
	}
}

// ScenarioID: SC-SYNC-001
// A run that is visible in scanning/running must carry a lease. Stale recovery
// treats rows without one as leftovers from before leases existed, so an
// unleased scan claim gets reaped by the very next scan and two scans run
// against the same source at once.
func TestP0_SYNC_001_ScanRunClaimsLeaseThatStaleRecoveryRespects(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Scan Lease")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "scan-lease"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)

	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected the mock provider, got %T", svc.RAGFlow)
	}
	gate := &ragflow.ListDatasetsGate{Entered: make(chan struct{}, 1), Release: make(chan struct{})}
	previousGate := mock.ConfigureListDatasetsGate(gate)
	t.Cleanup(func() { mock.ConfigureListDatasetsGate(previousGate) })

	scanDone := make(chan error, 1)
	go func() {
		_, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
		scanDone <- err
	}()
	select {
	case <-gate.Entered:
	case <-time.After(30 * time.Second):
		t.Fatal("scan never reached the upstream listing gate")
	}

	runs, total, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("expected one scan run while scanning, got total=%d", total)
	}
	if runs[0].Status != model.SyncRunScanning {
		t.Fatalf("expected the scan run to be scanning, got %s", runs[0].Status)
	}
	if runs[0].LeaseExpiresAt == nil {
		t.Fatal("a scanning run must own a lease, otherwise stale recovery fails it while it is still running")
	}
	if err := svc.recoverStaleSyncRuns(ctx); err != nil {
		t.Fatalf("stale recovery must not error on a leased run: %v", err)
	}
	after, err := svc.Store.GetSyncRun(ctx, runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != model.SyncRunScanning {
		t.Fatalf("stale recovery reaped a live scan claim: status=%s error=%s", after.Status, after.Error)
	}

	close(gate.Release)
	if err := <-scanDone; err != nil {
		t.Fatalf("first scan must succeed once the gate is released: %v", err)
	}
}

// ScenarioID: SC-SYNC-001
func TestP0_SYNC_001_ConcurrentScansCreateOnlyOneSourceRun(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Scan Claim")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "scan-race"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)

	// Park the running scan inside the upstream listing so the competing scan is
	// guaranteed to arrive while the first run is still active. Without the gate
	// this test only asserts a scheduling outcome: when the first scan finishes
	// before the second one starts, the second scan is legitimately accepted and
	// the expected 409 never materialises.
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected the mock provider, got %T", svc.RAGFlow)
	}
	gate := &ragflow.ListDatasetsGate{Entered: make(chan struct{}, 1), Release: make(chan struct{})}
	previousGate := mock.ConfigureListDatasetsGate(gate)
	t.Cleanup(func() { mock.ConfigureListDatasetsGate(previousGate) })

	type result struct {
		run *model.SyncRun
		err error
	}
	firstDone := make(chan result, 1)
	go func() {
		scanned, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
		firstDone <- result{run: scanned, err: err}
	}()

	select {
	case <-gate.Entered:
	case <-time.After(30 * time.Second):
		t.Fatal("first scan never reached the upstream listing gate")
	}

	// The first run is active, so the competing scan must be rejected.
	_, competingErr := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	close(gate.Release)

	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first scan must succeed once the gate is released: %v", first.err)
	}
	var businessErr *httperr.Error
	if !errors.As(competingErr, &businessErr) || businessErr.Status != 409 {
		t.Fatalf("expected the competing scan to be rejected with 409 while a run is active, got %v", competingErr)
	}
	runs, total, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(runs) != 1 || runs[0].Status != model.SyncRunPlanned {
		t.Fatalf("expected one planned scan run, got total=%d runs=%+v", total, runs)
	}
}
