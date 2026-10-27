package service

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestVersionPublishReconcilerWorkerRegistersAndSchedules(t *testing.T) {
	svc, ctx, _, _ := createIncrementalFixture(t)
	_ = svc.SetupWorker(DefaultWorkerConfig())
	active, err := svc.Store.CountActiveJobsExcept(ctx, model.JobKindVersionPublishReconcile, SystemTenantID, "")
	if err != nil {
		t.Fatalf("count reconciler jobs: %v", err)
	}
	if active != 1 {
		t.Fatalf("reconciler scheduled jobs = %d, want 1", active)
	}
}
