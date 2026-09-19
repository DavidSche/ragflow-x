package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_ResolveTenantScopeDefaultsToCurrentAndGuardsGovernanceScopes(t *testing.T) {
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

	current, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "", "")
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if current.Kind != TenantScopeCurrent || current.CurrentTenantID() != model.PlatformTenantID {
		t.Fatalf("unexpected default scope: %+v", current)
	}

	all, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "all", "")
	if err != nil || all.Kind != TenantScopeAllAuthorized || all.ScopeAll() == false {
		t.Fatalf("platform all scope: %+v err=%v", all, err)
	}
	expected := map[string]bool{model.PlatformTenantID: true, workspace.ID: true}
	if len(all.AllowedTenantIDs) != len(expected) {
		t.Fatalf("all scope must expand authorized workspaces: %+v", all.AllowedTenantIDs)
	}
	for _, tenantID := range all.AllowedTenantIDs {
		if !expected[tenantID] {
			t.Fatalf("unexpected authorized workspace %s", tenantID)
		}
	}
	if _, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "all", workspace.ID); err == nil {
		t.Fatal("scope=all with tenant_id is conflicting and must be rejected")
	}
	specific, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "specific", workspace.ID)
	if err != nil || specific.Kind != TenantScopeSpecific || specific.TargetTenantID != workspace.ID {
		t.Fatalf("platform specific scope: %+v err=%v", specific, err)
	}
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_ResolveTenantScopeRejectsTenantGovernanceRequests(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Workspace")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "tenant-admin", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveTenantScope(ctx, user.ID, tenant.ID, "all", ""); err == nil {
		t.Fatal("tenant admin scope=all must be denied")
	}
	if _, err := svc.ResolveTenantScope(ctx, user.ID, tenant.ID, "specific", tenant.ID); err == nil {
		t.Fatal("tenant admin scope=specific must be denied")
	}
	current, err := svc.ResolveTenantScope(ctx, user.ID, tenant.ID, "current", "")
	if err != nil || current.CurrentTenantID() != tenant.ID {
		t.Fatalf("tenant current scope: %+v err=%v", current, err)
	}
}
