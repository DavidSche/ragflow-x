package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestListProviderGovernanceIsScopedAndReadOnly(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	first, err := svc.CreateTenant(ctx, "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTenant(ctx, "Second")
	if err != nil {
		t.Fatal(err)
	}
	for index, tenant := range []model.Tenant{first, second} {
		if err := svc.Store.CreateModelProvider(ctx, &model.ModelProvider{
			ID: tenant.ID + "-p", TenantID: tenant.ID, ProviderType: "openai-api-compatible",
			Name: "Provider " + string(rune('A'+index)), Enabled: true, Status: model.ProviderStatusActive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	tenantUser, err := svc.CreateUser(ctx, first.ID, "", CreateUserRequest{Username: "first-admin", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}

	currentScope, err := svc.ResolveTenantScope(ctx, tenantUser.ID, first.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListProviderGovernance(ctx, currentScope, "")
	if err != nil || len(items) != 1 || items[0].TenantID != first.ID {
		t.Fatalf("current governance scope leaked data: items=%+v err=%v", items, err)
	}

	allScope, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "all", "")
	if err != nil {
		t.Fatal(err)
	}
	items, err = svc.ListProviderGovernance(ctx, allScope, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("platform all scope: items=%+v err=%v", items, err)
	}
	for _, item := range items {
		if item.TenantID == "" || item.TenantName == "" {
			t.Fatalf("governance view missing tenant attribution: %+v", item)
		}
	}

	specificScope, err := svc.ResolveTenantScope(ctx, admin.ID, admin.TenantID, "specific", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err = svc.ListProviderGovernance(ctx, specificScope, "")
	if err != nil || len(items) != 1 || items[0].TenantID != second.ID {
		t.Fatalf("platform specific scope: items=%+v err=%v", items, err)
	}
}
