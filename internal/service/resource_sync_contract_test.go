package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestResourceHashCanonicalizationAndSemanticRedaction(t *testing.T) {
	first := map[string]interface{}{
		"update_time": int64(100), "scan_run_id": "run-a",
		"prompt_config": map[string]interface{}{"prompt": "same semantic prompt"},
	}
	second := map[string]interface{}{
		"update_time": int64(200), "request_id": "request-b",
		"prompt_config": map[string]interface{}{"prompt": "same semantic prompt"},
	}
	firstHash := resourceUpstreamHash(model.SyncItemTypeChat, first)
	secondHash := resourceUpstreamHash(model.SyncItemTypeChat, second)
	if firstHash != secondHash {
		t.Fatal("non-semantic sync fields must not change upstream hash")
	}
	timeFirst := map[string]interface{}{"updated_at": time.Date(2026, 9, 8, 1, 2, 3, 500000000, time.FixedZone("X", 3600))}
	timeSecond := map[string]interface{}{"updated_at": time.Date(2026, 9, 8, 0, 2, 3, 500000000, time.UTC)}
	if canonicalJSONHash(timeFirst) != canonicalJSONHash(timeSecond) {
		t.Fatal("equivalent timestamps must canonicalize to the same hash")
	}
	first["prompt_config"] = map[string]interface{}{"prompt": "first secret prompt"}
	second["prompt_config"] = map[string]interface{}{"prompt": "second secret prompt"}
	if resourceUpstreamHash(model.SyncItemTypeChat, first) == resourceUpstreamHash(model.SyncItemTypeChat, second) {
		t.Fatal("semantic prompt changes must remain detectable")
	}
	firstPersisted := resourcePersistedPayload(model.SyncItemTypeChat, first)
	secondPersisted := resourcePersistedPayload(model.SyncItemTypeChat, second)
	if firstPersisted["prompt_config"] == secondPersisted["prompt_config"] {
		t.Fatal("persisted redaction must retain a distinct semantic digest")
	}
	promptMarker, ok := firstPersisted["prompt_config"].(string)
	if !ok || !containsRedacted(promptMarker) {
		t.Fatalf("prompt must be redacted in persisted payload: %#v", firstPersisted["prompt_config"])
	}
}

func containsRedacted(value interface{}) bool {
	text, ok := value.(string)
	return ok && len(text) > 10 && text[:10] == "[REDACTED:"
}

func TestGovernanceChangeDoesNotCreateContentConflict(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Governance Hash")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "governance-hash"})
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
	link, err := svc.Store.GetRAGFlowDatasetLinkForScope(ctx, true, nil, dataset.ID)
	if err != nil || link == nil {
		t.Fatal(err)
	}
	baselineHash := svc.currentLocalHash(ctx, model.SyncItemTypeDataset, tenant.ID, link.ID)
	link.OwnerID = "changed-owner"
	if err := svc.Store.UpsertDatasetLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	if svc.currentLocalHash(ctx, model.SyncItemTypeDataset, tenant.ID, link.ID) != baselineHash {
		t.Fatal("governance fields must not participate in local content hash")
	}
	run, err = svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != model.SyncActionSkip || items[0].ConflictType != model.SyncConflictNone {
		t.Fatalf("governance change must not create conflict: %+v", items)
	}
}

func TestRelinkKeepsOneBindingIdentity(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Relink Identity")
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
	target, err := svc.CreateDataset(ctx, tenant.ID, "relink-target")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load relink actor: %+v err=%v", admin, err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	originalID := binding.ID
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("sync items: %+v err=%v", items, err)
	}
	binding.ConflictType = model.SyncConflictIdentity
	binding.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateResourceBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	items[0].ConflictType = model.SyncConflictIdentity
	items[0].UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateSyncItem(ctx, &items[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveResourceSyncConflict(ctx, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncConflictRequest{
		ItemID: items[0].ID, Resolution: "use_local",
	}, "admin", admin.TenantID); err == nil {
		t.Fatal("identity conflict must not accept a content-only resolution")
	}
	if _, err := svc.ResolveResourceSyncConflict(ctx, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncConflictRequest{
		ItemID: items[0].ID, Resolution: "manual_merge",
	}, "admin", admin.TenantID); err == nil {
		t.Fatal("identity conflict must not accept manual merge")
	}
	relinked, err := svc.RelinkResourceSync(ctx, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID, ResourceSyncRelinkRequest{
		ExternalTenantID: ResourceSyncGlobalTenant, ExternalID: dataset.ID,
		LocalID: target.ID, TenantID: tenant.ID,
	}, admin.ID, admin.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if relinked.ID != originalID {
		t.Fatal("relink must not create a new binding identity")
	}
	if relinked.LastSyncedLocalHash != svc.currentLocalHash(ctx, model.SyncItemTypeDataset, tenant.ID, target.ID) {
		t.Fatal("relink must refresh the local baseline hash")
	}
	bindings, err := svc.Store.ListAllResourceBindings(ctx, ResourceSyncSourceID, []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range bindings {
		if item.ResourceType == model.SyncItemTypeDataset && item.ExternalScopeKey == ResourceSyncGlobalTenant && item.ExternalID == dataset.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("external identity binding count=%d", count)
	}
}
