package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

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
			scanned, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
			results <- result{run: scanned, err: err}
		}()
	}
	wg.Wait()
	close(results)

	created := 0
	conflicted := 0
	for outcome := range results {
		if outcome.err == nil {
			created++
			continue
		}
		var businessErr *httperr.Error
		if errors.As(outcome.err, &businessErr) && businessErr.Status == 409 {
			conflicted++
			continue
		}
		t.Fatalf("unexpected scan error: %v", outcome.err)
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("expected one scan success and one 409 conflict, got success=%d conflict=%d", created, conflicted)
	}
	runs, total, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(runs) != 1 || runs[0].Status != model.SyncRunPlanned {
		t.Fatalf("expected one planned scan run, got total=%d runs=%+v", total, runs)
	}
}
