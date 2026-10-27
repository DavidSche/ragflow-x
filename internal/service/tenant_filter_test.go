package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestListTenantsNonPlatformAppliesFilter verifies that a non-platform caller
// stays scoped to their own tenant but the requested criteria still decide
// whether that tenant is returned.
func TestListTenantsNonPlatformAppliesFilter(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "ta-admin", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}

	match := func(filter repository.TenantFilter) int {
		items, total, err := svc.ListTenants(ctx, user.ID, ta.ID, 1, 20, filter)
		if err != nil {
			t.Fatalf("list tenants: %v", err)
		}
		if len(items) == 0 {
			return 0
		}
		if len(items) != 1 || items[0].ID != ta.ID {
			t.Fatalf("tenant user leaked other tenants: %v", items)
		}
		return int(total)
	}

	if got := match(repository.TenantFilter{Name: "Nope"}); got != 0 {
		t.Fatalf("non-matching name should filter the tenant out, got %d", got)
	}
	if got := match(repository.TenantFilter{Status: model.TenantStatusDisabled}); got != 0 {
		t.Fatalf("non-matching status should filter the tenant out, got %d", got)
	}
	if got := match(repository.TenantFilter{}); got != 1 {
		t.Fatalf("empty filter should return the resident tenant, got %d", got)
	}
	if got := match(repository.TenantFilter{Status: model.TenantStatusActive}); got != 1 {
		t.Fatalf("matching status should return the resident tenant, got %d", got)
	}
}
