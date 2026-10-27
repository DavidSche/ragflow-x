package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestResourceSyncMemoryImportCreatesShadowBindingAndVersion(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Sync")
	if err != nil {
		t.Fatal(err)
	}
	memoryID, err := svc.RAGFlow.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: "imported-memory", MemoryType: []string{"raw"}, EmbdID: "embedding", LLMID: "llm",
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{
		Enabled:               &enabled,
		ResourceTypes:         []string{model.SyncItemTypeMemory},
		DefaultTargetTenantID: &tenant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertTenantMapping(ctx, "admin", ResourceSyncMappingRequest{
		ExternalTenantID: ResourceSyncGlobalTenant, TargetTenantID: tenant.ID,
	}); err != nil {
		t.Fatal(err)
	}

	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeMemory})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.SyncRunPlanned {
		t.Fatalf("memory scan status=%s", run.Status)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 10)
	if err != nil || len(items) != 1 || items[0].ResourceType != model.SyncItemTypeMemory {
		t.Fatalf("memory scan items=%+v err=%v", items, err)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("memory import: status=%s err=%v", run.Status, err)
	}
	shadow, err := svc.Store.GetMemoryShadow(ctx, tenant.ID, memoryID, false)
	if err != nil || shadow == nil {
		t.Fatalf("memory shadow=%+v err=%v", shadow, err)
	}
	binding, err := svc.Store.GetResourceBindingByLocal(ctx, model.SyncItemTypeMemory, shadow.ID)
	if err != nil || binding == nil {
		t.Fatalf("memory binding=%+v err=%v", binding, err)
	}
	version, err := svc.Store.GetCurrentBindingVersion(ctx, binding.ID)
	if err != nil || version == nil {
		t.Fatalf("memory version=%+v err=%v", version, err)
	}
	if binding.LastSyncedUpstreamHash != version.UpstreamHash || binding.LastSyncedLocalHash != svc.currentLocalHash(ctx, model.SyncItemTypeMemory, tenant.ID, shadow.ID) {
		t.Fatalf("memory hashes binding=%+v version=%+v", binding, version)
	}
}

func TestResourceSyncAssistantCatalogIsPendingReviewAfterImport(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Catalog Sync")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateChat(ctx, ragflow.CreateChatRequest{Name: "imported-chat"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{
		Enabled:               &enabled,
		ResourceTypes:         []string{model.SyncItemTypeChat},
		DefaultTargetTenantID: &tenant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertTenantMapping(ctx, "admin", ResourceSyncMappingRequest{
		ExternalTenantID: ResourceSyncGlobalTenant, TargetTenantID: tenant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeChat})
	if err != nil {
		t.Fatal(err)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("chat import: status=%s err=%v", run.Status, err)
	}
	shadows, _, err := svc.Store.ListChatShadows(ctx, tenant.ID, false, repository.ChatFilter{}, 1, 10)
	if err != nil || len(shadows) != 1 {
		t.Fatalf("chat shadows=%+v err=%v", shadows, err)
	}
	chat := &shadows[0]
	if err != nil || chat == nil {
		t.Fatalf("chat shadow=%+v err=%v", chat, err)
	}
	catalog, err := svc.Store.GetAssistantCatalog(ctx, tenant.ID, model.AssistantKindChat, chat.ID)
	if err != nil || catalog == nil {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if catalog.GovernanceStatus != model.AssistantGovernanceDisabled || catalog.Discoverable || catalog.AutoSelectEnabled || catalog.EffectiveStatus != model.AssistantEffectiveInactive {
		t.Fatalf("catalog must remain pending governance: %+v", catalog)
	}
}

func TestResourceSyncAssignItemsValidatesScopeOwnerAndWritesAudit(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Assign Sync")
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTenant(ctx, "Assign Target")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateUser(ctx, target.ID, "", CreateUserRequest{Username: "assign-owner", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	crossOwner, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "assign-other", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateChat(ctx, ragflow.CreateChatRequest{Name: "assign-chat"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := svc.UpdateResourceSyncSetting(ctx, tenant.ID, ResourceSyncSettingUpdate{
		Enabled:               &enabled,
		ResourceTypes:         []string{model.SyncItemTypeChat},
		DefaultTargetTenantID: &tenant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertTenantMapping(ctx, "admin", ResourceSyncMappingRequest{
		ExternalTenantID: ResourceSyncGlobalTenant, TargetTenantID: tenant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeChat})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListResourceSyncItems(ctx, run.ID, 1, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin=%+v err=%v", admin, err)
	}
	tenantUser, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "assign-user", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AssignResourceSyncItems(ctx, run.ID, ResourceSyncItemAssignmentRequest{
		ItemIDs: []string{items[0].ID}, TenantID: target.ID,
	}, tenantUser.ID, tenant.ID); err == nil {
		t.Fatal("cross-tenant assignment must be denied")
	}
	if _, err := svc.AssignResourceSyncItems(ctx, run.ID, ResourceSyncItemAssignmentRequest{
		ItemIDs: []string{items[0].ID}, TenantID: target.ID, OwnerID: crossOwner.ID,
	}, admin.ID, admin.TenantID); err == nil {
		t.Fatal("owner outside target tenant must be denied")
	}
	result, err := svc.AssignResourceSyncItems(ctx, run.ID, ResourceSyncItemAssignmentRequest{
		ItemIDs: []string{items[0].ID}, TenantID: target.ID, OwnerID: owner.ID,
	}, admin.ID, admin.TenantID)
	if err != nil || result.Updated != 1 {
		t.Fatalf("assignment result=%+v err=%v", result, err)
	}
	item, err := svc.Store.GetSyncItem(ctx, items[0].ID)
	if err != nil || item.TenantID != target.ID || item.WorkspaceID != target.ID || item.OwnerID != owner.ID {
		t.Fatalf("assigned item=%+v err=%v", item, err)
	}
	audits, _, err := svc.Store.ListAudits(ctx, target.ID, 1, 20, repository.AuditFilter{Action: "ragflow_sync.items.assigned"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("assignment audits=%+v err=%v", audits, err)
	}
}
