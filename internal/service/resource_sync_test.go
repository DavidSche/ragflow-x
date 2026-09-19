package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func enableResourceSyncForTest(t *testing.T, svc *Service, tenantID string) {
	t.Helper()
	enabled := true
	scheduledEnabled := true
	_, err := svc.UpdateResourceSyncSetting(context.Background(), tenantID, ResourceSyncSettingUpdate{
		Enabled:                   &enabled,
		ScheduledReconcileEnabled: &scheduledEnabled,
		ResourceTypes:             []string{model.SyncItemTypeDataset},
		DefaultTargetTenantID:     &tenantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpsertTenantMapping(context.Background(), "admin", ResourceSyncMappingRequest{
		ExternalTenantID: ResourceSyncGlobalTenant,
		TargetTenantID:   tenantID,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResourceSyncImportCreatesBindingVersionAndPendingReviewShadow(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "RAGFlow Sync")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "imported"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)

	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.SyncRunPlanned || run.DeletionSafe {
		t.Fatalf("planned scan: status=%s deletion_safe=%v", run.Status, run.DeletionSafe)
	}
	items, total, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 20)
	if err != nil || total != 1 {
		t.Fatalf("scan items: total=%d err=%v", total, err)
	}
	if items[0].Action != model.SyncActionCreate {
		t.Fatalf("expected create action, got %s", items[0].Action)
	}
	if run.PlanSummaryJSON != `{"dataset":{"create":1}}` {
		t.Fatalf("unexpected plan report: %s", run.PlanSummaryJSON)
	}

	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.SyncRunSucceeded {
		t.Fatalf("import status=%s error=%s", run.Status, run.Error)
	}
	if run.ResultSummaryJSON != `{"dataset":{"create_succeeded":1}}` {
		t.Fatalf("unexpected result report: %s", run.ResultSummaryJSON)
	}
	dataset, err := svc.Store.GetRAGFlowDatasetLinkForScope(ctx, true, nil, items[0].ExternalID)
	if err != nil || dataset == nil {
		t.Fatalf("imported dataset shadow missing: %+v err=%v", dataset, err)
	}
	if dataset.TenantID != tenant.ID || dataset.OwnerID != "" {
		t.Fatalf("unexpected dataset tenant/owner: %s/%s", dataset.TenantID, dataset.OwnerID)
	}
	binding, err := svc.Store.GetResourceBindingByLocal(ctx, model.SyncItemTypeDataset, dataset.ID)
	if err != nil || binding == nil {
		t.Fatalf("binding missing: %+v err=%v", binding, err)
	}
	if binding.GovernanceState != model.ResourceGovernancePendingReview || binding.BindingLifecycle != model.ResourceBindingLifecycleActive {
		t.Fatalf("unexpected governance/lifecycle: %s/%s", binding.GovernanceState, binding.BindingLifecycle)
	}
	version, err := svc.Store.GetCurrentBindingVersion(ctx, binding.ID)
	if err != nil || version == nil {
		t.Fatalf("binding version missing: %+v err=%v", version, err)
	}
	if version.Version != 1 || version.UpstreamHash != binding.LastSyncedUpstreamHash {
		t.Fatalf("version/baseline mismatch: %+v %+v", version, binding)
	}
}

func TestResourceSyncSettingsPreserveResourceTypesAndValidateOwner(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Settings")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateTenant(ctx, "Other Sync Settings")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateUser(ctx, other.ID, "", CreateUserRequest{
		Username: "other-owner", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	setting, err := svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{
		Enabled:               boolPtr(true),
		ResourceTypes:         []string{model.SyncItemTypeChat},
		DefaultTargetTenantID: &tenant.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if setting.ResourceTypesJSON != `["chat"]` {
		t.Fatalf("explicit resource types: %s", setting.ResourceTypesJSON)
	}
	interval := 120
	setting, err = svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{IntervalSeconds: &interval})
	if err != nil {
		t.Fatal(err)
	}
	if setting.ResourceTypesJSON != `["chat"]` {
		t.Fatalf("omitted resource types must be preserved: %s", setting.ResourceTypesJSON)
	}
	view, err := NewResourceSyncSettingView(setting)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ResourceTypes) != 1 || view.ResourceTypes[0] != model.SyncItemTypeChat {
		t.Fatalf("setting API must expose resource types as an array: %+v", view.ResourceTypes)
	}
	if _, err := svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{DefaultOwnerID: &owner.ID}); err == nil {
		t.Fatal("owner from another tenant must be rejected")
	}
}

func TestResourceSyncRecoversStaleRuns(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-staleSyncRunTimeout - time.Minute)
	run := &model.SyncRun{
		ID: id.New(), SourceID: ResourceSyncSourceID, TriggerType: model.SyncTriggerManualImport,
		Status: model.SyncRunRunning, ResourceTypesJSON: `["dataset"]`,
		ScanStartedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateSyncRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := svc.recoverStaleSyncRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.Store.GetSyncRun(ctx, run.ID)
	if err != nil || recovered == nil {
		t.Fatalf("load recovered run: %+v err=%v", recovered, err)
	}
	if recovered.Status != model.SyncRunFailed || recovered.Error == "" || recovered.FinishedAt == nil {
		t.Fatalf("stale run was not recovered: %+v", recovered)
	}
}

func TestResourceSyncConflictRequiresActorTenantScope(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Conflict Scope")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "scope-conflict"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	binding.LastSyncedUpstreamHash = "changed-upstream"
	binding.LastSyncedLocalHash = "changed-local"
	binding.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateResourceBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	run, err = svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("conflict items: %+v err=%v", items, err)
	}
	outsider, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "conflict-outsider", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateTenant(ctx, "Conflict Outsider")
	if err != nil {
		t.Fatal(err)
	}
	outsider.TenantID = other.ID
	if err := svc.Store.UpdateUser(ctx, outsider); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveResourceSyncConflict(ctx, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncConflictRequest{
		ItemID: items[0].ID, Resolution: "use_local",
	}, outsider.ID, outsider.TenantID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant conflict resolve must be forbidden: %v", err)
	}
}

func TestResourceSyncClassifiesMissingLocalShadow(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Missing Shadow")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "missing-shadow"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	binding.LocalID = "deleted-local-shadow"
	binding.LastSyncedLocalHash = ""
	if err := svc.Store.UpdateResourceBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	run, err = svc.ReconcileResourceSync(ctx, "admin", model.SyncTriggerManualReconcile, []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != model.SyncActionConflict || items[0].ConflictType != model.SyncConflictLocalMissing ||
		items[0].SyncState != model.ResourceSyncStateConflict {
		t.Fatalf("missing local shadow must be local_missing conflict: %+v", items)
	}
}

// ScenarioID: SC-COMP-001
func TestP0_COMP_001_ResourceSyncImportTransactionRollsBackPartialResource(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Atomic Import")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = svc.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.UpsertDatasetLink(ctx, &model.DatasetLink{
			ID: "atomic-partial", TenantID: tenant.ID, RAGFlowDatasetID: "atomic-partial",
			Name: "partial", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("expected forced transaction error")
	}
	resource, err := svc.Store.GetDatasetLink(ctx, tenant.ID, "atomic-partial")
	if err != nil || resource != nil {
		t.Fatalf("partial resource write was not rolled back: %+v err=%v", resource, err)
	}
}

func TestResourceSyncChatUpdateSyncsAssistantCatalog(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Chat Catalog Sync")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.RAGFlow.CreateChat(ctx, ragflow.CreateChatRequest{
		Name: "original", DatasetIDs: []string{"dataset-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeChat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	catalog, err := svc.Store.GetAssistantCatalog(ctx, tenant.ID, model.AssistantKindChat, chat.ID)
	if err != nil || catalog == nil {
		t.Fatalf("imported catalog missing: %+v err=%v", catalog, err)
	}
	catalog.GovernanceStatus = model.AssistantGovernanceEnabled
	catalog.EffectiveStatus = model.AssistantEffectiveActive
	catalog.EffectiveStatusReason = model.AssistantStatusReasonActive
	catalog.Discoverable = true
	if err := svc.Store.UpdateAssistantGovernanceFields(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SyncAssistantCatalogBasicFields(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.UpdateChat(ctx, chat.ID, ragflow.UpdateChatRequest{
		Name: "renamed", DatasetIDs: []string{"dataset-b"},
	}); err != nil {
		t.Fatal(err)
	}
	run, err = svc.ReconcileResourceSync(ctx, "admin", model.SyncTriggerManualReconcile, []string{model.SyncItemTypeChat})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("chat reconcile: %+v err=%v", run, err)
	}
	updated, err := svc.Store.GetChatShadow(ctx, tenant.ID, chat.ID, true)
	if err != nil || updated == nil {
		t.Fatalf("updated chat missing: %+v err=%v", updated, err)
	}
	if updated.Name != "renamed" || updated.DatasetIDs != "dataset-b" {
		t.Fatalf("unexpected chat update: %+v", updated)
	}
	syncedCatalog, err := svc.Store.GetAssistantCatalog(ctx, tenant.ID, model.AssistantKindChat, chat.ID)
	if err != nil || syncedCatalog == nil {
		t.Fatal(err)
	}
	if syncedCatalog.Name != "renamed" || syncedCatalog.EffectiveStatus != model.AssistantEffectiveActive ||
		syncedCatalog.EffectiveStatusReason != model.AssistantStatusReasonActive || !syncedCatalog.Discoverable {
		t.Fatalf("catalog update changed governance state: %+v", syncedCatalog)
	}
}

func TestResourceSyncDetectsConflictFromCrossRunBaseline(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "RAGFlow Conflict")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "original"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("first import: status=%s err=%v", run.Status, err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	// Simulate two cross-run facts: upstream changed and the local governance
	// baseline differs. A single SyncItem must not decide this conflict.
	binding.LastSyncedUpstreamHash = "changed-upstream"
	binding.LastSyncedLocalHash = "changed-local"
	binding.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateResourceBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}

	run, err = svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != model.SyncActionConflict || items[0].ConflictType != model.SyncConflictContent {
		t.Fatalf("expected content conflict, got %+v", items)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("conflicting import: status=%s err=%v", run.Status, err)
	}
	unchanged, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameBindingVersionPointer(unchanged.CurrentBindingVersionID, binding.CurrentBindingVersionID) {
		t.Fatal("conflicting item must not move immutable version pointer")
	}
}

func TestResourceSyncPageScanDoesNotInferDeletion(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "RAGFlow Deletion Safe")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "will-delete"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RAGFlow.DeleteDataset(ctx, dataset.ID); err != nil {
		t.Fatal(err)
	}

	run, err = svc.ReconcileResourceSync(ctx, "admin", model.SyncTriggerManualReconcile, []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if !run.DeletionSafe || run.ScanConsistency != model.SyncConsistencyPageScan {
		t.Fatalf("complete page scan must be deletion-safe: %s/%v", run.ScanConsistency, run.DeletionSafe)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	if binding.BindingLifecycle != model.ResourceBindingLifecycleActive || binding.MissingConfirmations != 1 {
		t.Fatalf("approximate scan must count but not infer deletion: %+v", binding)
	}
	if !strings.Contains(run.ResultSummaryJSON, "missing_pending") {
		t.Fatalf("missing run report: %s", run.ResultSummaryJSON)
	}
}

func TestResourceSyncApproximateScanMarksMissingAfterConfirmations(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Missing Confirm")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "missing-confirm"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RAGFlow.DeleteDataset(ctx, dataset.ID); err != nil {
		t.Fatal(err)
	}
	for confirmations := 1; confirmations <= 3; confirmations++ {
		run, err = svc.ReconcileResourceSync(ctx, "admin", model.SyncTriggerManualReconcile, []string{model.SyncItemTypeDataset})
		if err != nil {
			t.Fatal(err)
		}
		binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
		if err != nil || binding == nil {
			t.Fatal(err)
		}
		if confirmations < 3 {
			if binding.BindingLifecycle != model.ResourceBindingLifecycleActive || binding.MissingConfirmations != confirmations {
				t.Fatalf("confirmations %d: %+v", confirmations, binding)
			}
			continue
		}
		if binding.BindingLifecycle != model.ResourceBindingLifecycleMissing || binding.ConflictType != model.SyncConflictIdentity ||
			binding.GovernanceState != model.ResourceGovernancePendingReview {
			t.Fatalf("binding must be missing after confirmations: %+v", binding)
		}
		if !strings.Contains(run.ResultSummaryJSON, "mark_missing") {
			t.Fatalf("mark missing report: %s", run.ResultSummaryJSON)
		}
	}
}

// ScenarioID: SC-SYNC-001
func TestP0_SYNC_001_ResourceSyncRejectsConcurrentRuns(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "RAGFlow Mutex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "mutex"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	if _, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	var businessErr *httperr.Error
	if !errors.As(err, &businessErr) || businessErr.Status != 409 {
		t.Fatalf("expected 409 active run conflict, got %v", err)
	}
}

func TestManualReconcileWorksWhenScheduledReconcileDisabled(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Manual Only Reconcile")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "manual-only"})
	if err != nil {
		t.Fatal(err)
	}
	if err := updateResourceSyncSwitchesForTest(t, svc, tenant.ID, true, false); err != nil {
		t.Fatal(err)
	}
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	_ = svc.RAGFlow.DeleteDataset(ctx, dataset.ID)
	run, err = svc.ReconcileResourceSync(ctx, "admin", model.SyncTriggerManualReconcile, []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatalf("manual reconcile must ignore scheduled switch: err=%v", err)
	}
	if run.TriggerType != model.SyncTriggerManualReconcile {
		t.Fatalf("unexpected reconcile trigger: %s", run.TriggerType)
	}
}

func TestReconcileFlagsMissingLocalShadowAsIdentityConflict(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Missing Local")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "missing-local"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("initial import: %+v err=%v", run, err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	if err := svc.Store.DeleteDatasetLinkByTenant(ctx, tenant.ID, binding.LocalID); err != nil {
		t.Fatal(err)
	}
	run, err = svc.ReconcileResourceSync(ctx, "admin", model.SyncTriggerManualReconcile, []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run.PlanSummaryJSON, `"local_missing":1`) || !strings.Contains(run.ResultSummaryJSON, "conflict_skipped") {
		t.Fatalf("missing local report: plan=%s result=%s", run.PlanSummaryJSON, run.ResultSummaryJSON)
	}
	unchanged, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || unchanged == nil {
		t.Fatal(err)
	}
	if unchanged.ConflictType != model.SyncConflictLocalMissing || !sameBindingVersionPointer(unchanged.CurrentBindingVersionID, binding.CurrentBindingVersionID) {
		t.Fatalf("missing local must not auto overwrite: %+v", unchanged)
	}
}

func TestResourceSyncRelinkOptionsAreScopeFiltered(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	first, err := svc.CreateTenant(ctx, "Relink A")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTenant(ctx, "Relink B")
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		id, tenantID, name string
	}{
		{id: "dataset-a", tenantID: first.ID, name: "Alpha dataset"},
		{id: "dataset-b", tenantID: second.ID, name: "Beta dataset"},
	} {
		if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{ID: seed.id, TenantID: seed.tenantID, RAGFlowDatasetID: seed.id, Name: seed.name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{ID: "chat-b", TenantID: second.ID, Name: "Beta chat", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertAgentShadow(ctx, &model.AgentShadow{ID: "agent-b", TenantID: second.ID, Title: "Beta agent", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertSearchAppShadow(ctx, &model.SearchAppShadow{ID: "app-b", TenantID: second.ID, Name: "Beta app", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertMemoryShadow(ctx, &model.MemoryShadow{ID: "memory-b", TenantID: second.ID, Name: "Beta memory", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, first.ID, "", CreateUserRequest{Username: "relink-user", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}

	tenants, total, err := svc.ListResourceSyncRelinkTenantOptions(ctx, user.ID, first.ID, "current", "", 1, 20)
	if err != nil || total != 1 || len(tenants) != 1 || tenants[0].ID != first.ID {
		t.Fatalf("current tenant options: total=%d items=%+v err=%v", total, tenants, err)
	}
	options, total, err := svc.ListResourceSyncRelinkResourceOptions(ctx, user.ID, first.ID, model.SyncItemTypeDataset, "current", first.ID, "", 1, 20)
	if err != nil || total != 1 || len(options) != 1 || options[0].LocalID != "dataset-a" {
		t.Fatalf("current resource options: total=%d items=%+v err=%v", total, options, err)
	}
	if _, _, err := svc.ListResourceSyncRelinkResourceOptions(ctx, user.ID, first.ID, model.SyncItemTypeDataset, "all", "", "", 1, 20); err == nil {
		t.Fatal("tenant user scope=all options must be denied")
	}
	if _, _, err := svc.ListResourceSyncRelinkResourceOptions(ctx, user.ID, first.ID, model.SyncItemTypeDataset, "specific", second.ID, "", 1, 20); err == nil {
		t.Fatal("tenant user specific cross-tenant options must be denied")
	}

	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %+v err=%v", admin, err)
	}
	tenants, tenantTotal, err := svc.ListResourceSyncRelinkTenantOptions(ctx, admin.ID, admin.TenantID, "all", "Relink", 1, 20)
	if err != nil || tenantTotal != 2 || len(tenants) != 2 {
		t.Fatalf("platform tenant options: total=%d items=%+v err=%v", total, tenants, err)
	}
	options, _, err = svc.ListResourceSyncRelinkResourceOptions(ctx, admin.ID, admin.TenantID, model.SyncItemTypeDataset, "all", "", "Alpha", 1, 20)
	if err != nil || len(options) != 1 || options[0].TenantID != first.ID {
		t.Fatalf("platform resource options: items=%+v err=%v", options, err)
	}
	options, _, err = svc.ListResourceSyncRelinkResourceOptions(ctx, admin.ID, admin.TenantID, model.SyncItemTypeDataset, "all", second.ID, "", 1, 20)
	if err != nil || len(options) != 1 || options[0].TenantID != second.ID {
		t.Fatalf("platform tenant-filtered options: items=%+v err=%v", options, err)
	}
	if _, _, err := svc.ListResourceSyncRelinkResourceOptions(ctx, admin.ID, admin.TenantID, model.SyncItemTypeDataset, "all", "not-authorized", "", 1, 20); err == nil {
		t.Fatal("unknown tenant in all scope must be denied")
	}
	for _, resourceType := range []string{model.SyncItemTypeChat, model.SyncItemTypeAgent, model.SyncItemTypeSearchApp} {
		options, total, err := svc.ListResourceSyncRelinkResourceOptions(ctx, admin.ID, admin.TenantID, resourceType, "all", second.ID, "Beta", 1, 20)
		if err != nil || total != 1 || len(options) != 1 || options[0].TenantID != second.ID {
			t.Fatalf("%s options: total=%d items=%+v err=%v", resourceType, total, options, err)
		}
	}
	options, total, err = svc.ListResourceSyncRelinkResourceOptions(ctx, admin.ID, admin.TenantID, model.SyncItemTypeMemory, "all", second.ID, "Beta", 1, 20)
	if err != nil || total != 1 || len(options) != 1 || options[0].LocalID != "memory-b" {
		t.Fatalf("memory relink options: total=%d items=%+v err=%v", total, options, err)
	}
}

func TestResourceSyncRelinkValidatesTargetAndWritesAudit(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Relink Target")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := svc.CreateTenant(ctx, "Relink Workspace")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "external"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{}); err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateDataset(ctx, workspace.ID, "relink-target")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %+v err=%v", admin, err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatalf("load binding: %+v err=%v", binding, err)
	}

	if _, err := svc.RelinkResourceSync(ctx, model.SyncItemTypeChat, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncRelinkRequest{
		LocalID: target.ID, TenantID: workspace.ID,
	}, admin.ID, admin.TenantID); err == nil {
		t.Fatal("resource type mismatch must be rejected")
	}
	relinked, err := svc.RelinkResourceSync(ctx, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncRelinkRequest{
		LocalID: target.ID, TenantID: workspace.ID,
	}, admin.ID, admin.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if relinked.TenantID != workspace.ID || relinked.LocalID != target.ID {
		t.Fatalf("unexpected relink target: %+v", relinked)
	}

	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "cross-relink", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.RelinkResourceSync(ctx, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncRelinkRequest{
		LocalID: target.ID, TenantID: workspace.ID,
	}, user.ID, user.TenantID)
	if err == nil {
		t.Fatal("tenant user cross-tenant relink must be denied")
	}
	audits, _, err := svc.Store.ListAudits(ctx, workspace.ID, 1, 20, repository.AuditFilter{Action: "ragflow_sync.resource.relinked"})
	if err != nil || len(audits) != 1 || audits[0].ResourceID != relinked.ID {
		t.Fatalf("relink audit: rows=%+v err=%v", audits, err)
	}
}

func sameBindingVersionPointer(first, second *string) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}
