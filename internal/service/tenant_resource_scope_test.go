package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestWorkspaceResourceGovernanceReadsAreTenantScoped(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	first, err := svc.CreateTenant(ctx, "Workspace First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTenant(ctx, "Workspace Second")
	if err != nil {
		t.Fatal(err)
	}

	for index, tenantID := range []string{first.ID, second.ID} {
		agent := &model.AgentShadow{ID: tenantID + "-agent", TenantID: tenantID, Title: "Agent " + string(rune('A'+index)), Status: "active"}
		chat := &model.ChatShadow{ID: tenantID + "-chat", TenantID: tenantID, Name: "Chat " + string(rune('A'+index)), Status: "active"}
		searchApp := &model.SearchAppShadow{ID: tenantID + "-app", TenantID: tenantID, Name: "App " + string(rune('A'+index)), Status: "active"}
		dataset := &model.DatasetLink{ID: tenantID + "-ds", TenantID: tenantID, RAGFlowDatasetID: "rag-" + tenantID, Name: "Dataset " + string(rune('A'+index))}
		if err := svc.Store.UpsertAgentShadow(ctx, agent); err != nil {
			t.Fatal(err)
		}
		if err := svc.Store.UpsertChatShadow(ctx, chat); err != nil {
			t.Fatal(err)
		}
		if err := svc.Store.UpsertSearchAppShadow(ctx, searchApp); err != nil {
			t.Fatal(err)
		}
		if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
			t.Fatal(err)
		}
	}

	tenantUser, err := svc.CreateUser(ctx, first.ID, "", CreateUserRequest{Username: "first-user", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	currentScope, err := svc.ResolveTenantScope(ctx, tenantUser.ID, first.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveTenantScope(ctx, tenantUser.ID, first.ID, "specific", second.ID); err == nil {
		t.Fatal("tenant users must not request specific tenant scope")
	}
	if _, err := svc.ResolveTenantScope(ctx, tenantUser.ID, first.ID, "all", ""); err == nil {
		t.Fatal("tenant users must not request all-authorized scope")
	}

	agents, _, err := svc.ListAgentsForScope(ctx, currentScope, repository.AgentFilter{}, 1, 20)
	if err != nil || len(agents) != 1 || agents[0].TenantID != first.ID {
		t.Fatalf("agent current scope: items=%+v err=%v", agents, err)
	}
	chats, _, err := svc.ListChatsForScope(ctx, currentScope, repository.ChatFilter{}, 1, 20)
	if err != nil || len(chats) != 1 || chats[0].TenantID != first.ID {
		t.Fatalf("chat current scope: items=%+v err=%v", chats, err)
	}
	apps, _, err := svc.ListSearchAppsForScope(ctx, currentScope, repository.SearchAppFilter{}, 1, 20)
	if err != nil || len(apps) != 1 || apps[0].TenantID != first.ID {
		t.Fatalf("search app current scope: items=%+v err=%v", apps, err)
	}
	datasets, err := svc.ListDatasetsForScope(ctx, currentScope, repository.DatasetFilter{})
	if err != nil || len(datasets) != 1 || datasets[0].TenantID != first.ID || datasets[0].TenantName == "" {
		t.Fatalf("dataset current scope: items=%+v err=%v", datasets, err)
	}
	for _, item := range agents {
		if item.TenantID == "" {
			t.Fatal("agent governance view lost tenant attribution")
		}
	}

	allScope, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "all", "")
	if err != nil {
		t.Fatal(err)
	}
	if agents, _, _ = svc.ListAgentsForScope(ctx, allScope, repository.AgentFilter{}, 1, 20); len(agents) != 2 {
		t.Fatalf("agent all scope got %d items", len(agents))
	}
	if chats, _, _ = svc.ListChatsForScope(ctx, allScope, repository.ChatFilter{}, 1, 20); len(chats) != 2 {
		t.Fatalf("chat all scope got %d items", len(chats))
	}
	if apps, _, _ = svc.ListSearchAppsForScope(ctx, allScope, repository.SearchAppFilter{}, 1, 20); len(apps) != 2 {
		t.Fatalf("search app all scope got %d items", len(apps))
	}
	if datasets, _ = svc.ListDatasetsForScope(ctx, allScope, repository.DatasetFilter{}); len(datasets) != 2 {
		t.Fatalf("dataset all scope got %d items", len(datasets))
	}

	specificScope, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "specific", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agents, _, _ = svc.ListAgentsForScope(ctx, specificScope, repository.AgentFilter{}, 1, 20); len(agents) != 1 || agents[0].TenantID != second.ID {
		t.Fatalf("agent specific scope: items=%+v", agents)
	}
	if chats, _, _ = svc.ListChatsForScope(ctx, specificScope, repository.ChatFilter{}, 1, 20); len(chats) != 1 || chats[0].TenantID != second.ID {
		t.Fatalf("chat specific scope: items=%+v", chats)
	}
	if apps, _, _ = svc.ListSearchAppsForScope(ctx, specificScope, repository.SearchAppFilter{}, 1, 20); len(apps) != 1 || apps[0].TenantID != second.ID {
		t.Fatalf("search app specific scope: items=%+v", apps)
	}
	if datasets, _ = svc.ListDatasetsForScope(ctx, specificScope, repository.DatasetFilter{}); len(datasets) != 1 || datasets[0].TenantID != second.ID {
		t.Fatalf("dataset specific scope: items=%+v", datasets)
	}

	if _, err := svc.GetAgentForScope(ctx, specificScope, first.ID+"-agent"); err == nil {
		t.Fatal("agent cross-tenant get must be denied")
	}
	if _, err := svc.GetChatForScope(ctx, specificScope, first.ID+"-chat"); err == nil {
		t.Fatal("chat cross-tenant get must be denied")
	}
	if _, err := svc.GetSearchAppForScope(ctx, specificScope, first.ID+"-app"); err == nil {
		t.Fatal("search app cross-tenant get must be denied")
	}
	if _, err := svc.GetDatasetForScope(ctx, specificScope, first.ID+"-ds"); err == nil {
		t.Fatal("dataset cross-tenant get must be denied")
	}
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_WorkspaceScopeRepositoriesFailClosedWithoutTenantIDs(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertAgentShadow(ctx, &model.AgentShadow{ID: "agent", TenantID: tenant.ID, Title: "Agent", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{ID: "chat", TenantID: tenant.ID, Name: "Chat", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertSearchAppShadow(ctx, &model.SearchAppShadow{ID: "app", TenantID: tenant.ID, Name: "App", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{ID: "dataset", TenantID: tenant.ID, RAGFlowDatasetID: "rag-dataset", Name: "Dataset"}); err != nil {
		t.Fatal(err)
	}

	if agents, _, err := svc.Store.ListAgentShadowsForScope(ctx, false, nil, repository.AgentFilter{}, 1, 20); err != nil || len(agents) != 0 {
		t.Fatalf("agent repository must fail closed: items=%+v err=%v", agents, err)
	}
	if chats, _, err := svc.Store.ListChatShadowsForScope(ctx, false, nil, repository.ChatFilter{}, 1, 20); err != nil || len(chats) != 0 {
		t.Fatalf("chat repository must fail closed: items=%+v err=%v", chats, err)
	}
	if apps, _, err := svc.Store.ListSearchAppShadowsForScope(ctx, false, nil, repository.SearchAppFilter{}, 1, 20); err != nil || len(apps) != 0 {
		t.Fatalf("search app repository must fail closed: items=%+v err=%v", apps, err)
	}
	if datasets, err := svc.Store.ListDatasetLinksForScope(ctx, false, nil, repository.DatasetFilter{}); err != nil || len(datasets) != 0 {
		t.Fatalf("dataset repository must fail closed: items=%+v err=%v", datasets, err)
	}
}

func TestDatasetWritesRemainCurrentWorkspaceOnly(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, "Workspace")
	if err != nil {
		t.Fatal(err)
	}
	dataset := &model.DatasetLink{ID: "workspace-dataset", TenantID: workspace.ID, RAGFlowDatasetID: "rag-workspace", Name: "Dataset"}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.UpdateDataset(ctx, admin.TenantID, dataset.ID, "Renamed"); err == nil {
		t.Fatal("platform actor must not update another workspace through current-workspace write path")
	}
	stillThere, err := svc.Store.GetDatasetLink(ctx, workspace.ID, dataset.ID)
	if err != nil || stillThere == nil {
		t.Fatalf("dataset was modified despite cross-tenant write denial: item=%+v err=%v", stillThere, err)
	}
}
