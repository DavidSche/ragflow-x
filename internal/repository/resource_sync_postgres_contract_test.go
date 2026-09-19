package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// ScenarioID: SC-SYNC-001
func TestP0_SYNC_001_PostgresResourceSyncStateClaimsAreAtomic(t *testing.T) {
	contractStore := newPostgresContractStore(t)
	ctx := context.Background()
	tenant := mustCreatePostgresTenant(t, contractStore, "PG Resource Sync State Tenant")
	sourceID := "pg-sync-state-" + id.New()
	now := time.Now().UTC().Truncate(time.Second)
	if err := contractStore.SaveResourceSyncSetting(ctx, &model.ResourceSyncSetting{
		ID: id.New(), SourceID: sourceID, TenantID: tenant.ID,
		ResourceTypesJSON: `["dataset"]`, ScopeJSON: `{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}`,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sync setting: %v", err)
	}

	newRun := func(runID string) *model.SyncRun {
		return &model.SyncRun{
			ID: runID, SourceID: sourceID, SourceCredentialVersion: "v1",
			TriggerType: model.SyncTriggerManualImport, Status: model.SyncRunScanning,
			ResourceTypesJSON: `["dataset"]`, ScopeJSON: `{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}`,
			SourceSnapshotJSON: `{"datasets":[]}`, PlanSummaryJSON: `{}`, ResultSummaryJSON: `{}`,
			CreatedBy: "pg-sync-actor", CreatedAt: now, UpdatedAt: now,
		}
	}

	createdResults := make(chan bool, 3)
	var createdWG sync.WaitGroup
	for index := range 3 {
		createdWG.Add(1)
		go func(index int) {
			defer createdWG.Done()
			created, err := contractStore.CreateSyncRunIfSourceIdle(ctx, newRun(id.New()))
			if err != nil {
				t.Errorf("atomic create sync run: %v", err)
				return
			}
			createdResults <- created
		}(index)
	}
	createdWG.Wait()
	close(createdResults)
	createdRuns := 0
	for created := range createdResults {
		if created {
			createdRuns++
		}
	}
	if createdRuns != 1 {
		t.Fatalf("atomic source claims = %d, want 1", createdRuns)
	}

	var plannedRun *model.SyncRun
	runs, total, err := contractStore.ListSyncRuns(ctx, 1, 10, sourceID, "")
	if err != nil || total != 1 || len(runs) != 1 {
		t.Fatalf("load claimed run: total=%d rows=%+v err=%v", total, runs, err)
	}
	plannedRun = &runs[0]
	if err := contractStore.(*store).DB.WithContext(ctx).Model(&model.SyncRun{}).
		Where("id = ?", plannedRun.ID).
		Update("status", model.SyncRunPlanned).Error; err != nil {
		t.Fatalf("reset run for claim: %v", err)
	}

	beginResults := make(chan bool, 3)
	var beginWG sync.WaitGroup
	for range 3 {
		beginWG.Add(1)
		go func() {
			defer beginWG.Done()
			claimed, err := contractStore.BeginSyncRun(ctx, plannedRun.ID, "pg-sync-worker", time.Now().UTC())
			if err != nil {
				t.Errorf("atomic begin sync run: %v", err)
				return
			}
			beginResults <- claimed
		}()
	}
	beginWG.Wait()
	close(beginResults)
	claimedRuns := 0
	for claimed := range beginResults {
		if claimed {
			claimedRuns++
		}
	}
	if claimedRuns != 1 {
		t.Fatalf("atomic status claims = %d, want 1", claimedRuns)
	}
	stored, err := contractStore.GetSyncRun(ctx, plannedRun.ID)
	if err != nil || stored == nil || stored.Status != model.SyncRunRunning {
		t.Fatalf("claimed run: %+v err=%v", stored, err)
	}
}
