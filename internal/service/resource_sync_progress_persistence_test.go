package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// ScenarioID: SC-SYNC-001
func TestP0_SYNC_003_FailedSyncRunPreservesPersistedProgress(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	run := &model.SyncRun{
		ID:                id.New(),
		SourceID:          ResourceSyncSourceID,
		TriggerType:       model.SyncTriggerManualReconcile,
		Status:            model.SyncRunRunning,
		ResourceTypesJSON: `["dataset"]`,
		ScopeJSON:         `{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}`,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := svc.Store.CreateSyncRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.TouchSyncRunProgress(ctx, run.ID, "worker", 5, 3, 1, now); err != nil {
		t.Fatal(err)
	}

	svc.failSyncRun(ctx, run, errors.New("sync worker stopped"))
	stored, err := svc.Store.GetSyncRun(ctx, run.ID)
	if err != nil || stored == nil {
		t.Fatalf("stored failed run missing: %+v err=%v", stored, err)
	}
	if stored.Status != model.SyncRunFailed || stored.Error != "sync worker stopped" {
		t.Fatalf("unexpected failed run: %+v", stored)
	}
	if stored.ProgressTotal != 5 || stored.ProgressDone != 3 || stored.ProgressFailed != 1 {
		t.Fatalf("failed run progress was overwritten: total=%d done=%d failed=%d", stored.ProgressTotal, stored.ProgressDone, stored.ProgressFailed)
	}
}
