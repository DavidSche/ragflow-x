package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func updateResourceSyncSwitchesForTest(t *testing.T, svc *Service, tenantID string, enabled, scheduled bool) error {
	t.Helper()
	_, err := svc.UpdateResourceSyncSetting(context.Background(), tenantID, ResourceSyncSettingUpdate{
		Enabled:                   &enabled,
		ScheduledReconcileEnabled: &scheduled,
	})
	return err
}

func TestResourceSyncSchedulerEnqueuesOneNextRun(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Scheduler")
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)

	jobs, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Status != model.JobStatusQueued {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
	queuedAgain, err := svc.ScheduleResourceSyncReconcile(ctx, "")
	if err != nil || queuedAgain {
		t.Fatalf("duplicate schedule: queued=%v err=%v", queuedAgain, err)
	}
	if time.Until(jobs[0].RunAfter) < time.Minute {
		t.Fatalf("next run must honor interval: %+v", jobs[0].RunAfter)
	}
	var payload struct {
		ResourceTypes []string `json:"resource_types"`
		Scheduled     bool     `json:"scheduled"`
	}
	if err := json.Unmarshal([]byte(jobs[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Scheduled || len(payload.ResourceTypes) != 1 || payload.ResourceTypes[0] != model.SyncItemTypeDataset {
		t.Fatalf("unexpected schedule payload: %+v", payload)
	}
}

func TestResourceSyncSchedulerRequiresBothEnabledSwitches(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Switches")
	if err != nil {
		t.Fatal(err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, false, false); err != nil {
		t.Fatal(err)
	}
	scheduled, err := svc.ScheduleResourceSyncReconcile(ctx, "")
	if err != nil || scheduled {
		t.Fatalf("default switches must not schedule: scheduled=%v err=%v", scheduled, err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, true, false); err != nil {
		t.Fatal(err)
	}
	scheduled, err = svc.ScheduleResourceSyncReconcile(ctx, "")
	if err != nil || scheduled {
		t.Fatalf("manual-only mode must not schedule: scheduled=%v err=%v", scheduled, err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, true, true); err != nil {
		t.Fatal(err)
	}
	setting, settingErr := svc.GetResourceSyncSetting(ctx, "")
	if settingErr != nil || setting == nil || !setting.Enabled || !setting.ScheduledReconcileEnabled {
		t.Fatalf("both switches must persist: %+v err=%v", setting, settingErr)
	}
	jobs, _, jobsErr := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if jobsErr != nil || len(jobs) != 1 || jobs[0].Status != model.JobStatusQueued {
		t.Fatalf("both switches must schedule: jobs=%+v err=%v", jobs, jobsErr)
	}
}

func TestScheduledResourceSyncWorkerArmsNextRunBeforeExecution(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Chain")
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	if _, err := svc.ScheduleResourceSyncReconcile(ctx, ""); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("initial job: %+v err=%v", jobs, err)
	}
	current := jobs[0]
	worker := ragflowReconcileWorker{svc: svc}
	if err := worker.Run(ctx, &current); err != nil {
		t.Fatal(err)
	}
	next, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(next) != 2 {
		t.Fatalf("jobs after scheduled run: %+v err=%v", next, err)
	}
	found := false
	for _, job := range next {
		if job.ID != current.ID && job.Status == model.JobStatusQueued {
			found = true
		}
	}
	if !found {
		t.Fatal("scheduled worker did not arm the next cycle")
	}
	runs, _, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil || len(runs) != 1 || runs[0].TriggerType != model.SyncTriggerScheduledReconcile {
		t.Fatalf("scheduled worker must record scheduled trigger: %+v err=%v", runs, err)
	}
}

func TestDisabledScheduledResourceSyncDoesNotRunOrReschedule(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Disabled")
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	if _, err := svc.ScheduleResourceSyncReconcile(ctx, ""); err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err := svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("queued job: %+v err=%v", jobs, err)
	}
	current := jobs[0]
	worker := ragflowReconcileWorker{svc: svc}
	if err := worker.Run(ctx, &current); err != nil {
		t.Fatal(err)
	}
	next, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(next) != 1 {
		t.Fatalf("disabled worker must not reschedule: %+v err=%v", next, err)
	}
	runs, _, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil || len(runs) != 0 {
		t.Fatalf("disabled job must not reconcile: %+v err=%v", runs, err)
	}
}

func TestScheduledWorkerRecoversStaleRunBeforeActiveCheck(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Scheduled Stale Recovery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "scheduled-recovery"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	staleAt := time.Now().UTC().Add(-staleSyncRunTimeout - time.Minute)
	stale := &model.SyncRun{
		ID: "stale-run", SourceID: ResourceSyncSourceID, TriggerType: model.SyncTriggerManualImport,
		Status: model.SyncRunRunning, ResourceTypesJSON: `["dataset"]`,
		ScanStartedAt: &staleAt, CreatedAt: staleAt, UpdatedAt: staleAt,
	}
	if err := svc.Store.CreateSyncRun(ctx, stale); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("scheduled job: %+v err=%v", jobs, err)
	}
	worker := ragflowReconcileWorker{svc: svc}
	if err := worker.Run(ctx, &jobs[0]); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.Store.GetSyncRun(ctx, stale.ID)
	if err != nil || recovered == nil || recovered.Status != model.SyncRunFailed {
		t.Fatalf("stale run recovery: %+v err=%v", recovered, err)
	}
	runs, _, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs after recovery: %+v err=%v", runs, err)
	}
	found := false
	for _, run := range runs {
		if run.ID != stale.ID && run.Status == model.SyncRunSucceeded && run.TriggerType == model.SyncTriggerScheduledReconcile {
			found = true
		}
	}
	if !found {
		t.Fatalf("scheduled worker did not run after recovery: %+v", runs)
	}
}

func TestDisabledScheduledReconcileCancelsQueuedJobOnly(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Cancel")
	if err != nil {
		t.Fatal(err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ScheduleResourceSyncReconcile(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, true, false); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != model.JobStatusCanceled {
		t.Fatalf("queued scheduled job: %+v err=%v", jobs, err)
	}
	setting, err := svc.GetResourceSyncSetting(ctx, "")
	if err != nil || setting == nil || !setting.Enabled || setting.ScheduledReconcileEnabled {
		t.Fatalf("manual reconcile setting must remain enabled: %+v err=%v", setting, err)
	}
}

func TestScheduledWorkerSkipsWhenEitherSwitchIsDisabled(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Scheduled Skip")
	if err != nil {
		t.Fatal(err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, false, true); err != nil {
		t.Fatal(err)
	}
	job := &model.Job{
		ID:       "scheduled-skip",
		Kind:     model.JobKindRAGFlowReconcile,
		Key:      "scheduled-skip",
		TenantID: ResourceSyncSourceID,
		Payload:  `{"scheduled":true,"resource_types":["dataset"]}`,
		Status:   model.JobStatusRunning,
	}
	worker := ragflowReconcileWorker{svc: svc}
	if err := worker.Run(ctx, job); err != nil {
		t.Fatal(err)
	}
	runs, _, err := svc.Store.ListSyncRuns(ctx, 1, 10, ResourceSyncSourceID, "")
	if err != nil || len(runs) != 0 {
		t.Fatalf("scheduled job must not reconcile: %+v err=%v", runs, err)
	}
	next, _, err := svc.Store.ListJobs(ctx, ResourceSyncSourceID, model.JobKindRAGFlowReconcile, "", 1, 10)
	if err != nil || len(next) != 0 {
		t.Fatalf("scheduled job must not reschedule: %+v err=%v", next, err)
	}
}
