package service

import (
	"context"
	"testing"
)

func TestTenantDeleteRules(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin: %v", err)
	}

	// platform tenant is protected
	if err := svc.DeleteTenant(ctx, admin.ID, SystemTenantID, SystemTenantID, false); err == nil {
		t.Fatal("platform tenant should not be deletable")
	}

	// tenant with children cannot be deleted without force
	t1, _ := svc.CreateTenant(ctx, "T1")
	_, _ = svc.CreateTeam(ctx, t1.ID, "dev", "")
	if err := svc.DeleteTenant(ctx, admin.ID, SystemTenantID, t1.ID, false); err == nil {
		t.Fatal("tenant with teams should require force")
	}
	if err := svc.DeleteTenant(ctx, admin.ID, SystemTenantID, t1.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	ten, _ := svc.Store.GetTenant(ctx, t1.ID)
	if ten != nil {
		t.Fatal("tenant not removed")
	}

	// empty tenant deletes without force
	t2, _ := svc.CreateTenant(ctx, "T2")
	if err := svc.DeleteTenant(ctx, admin.ID, SystemTenantID, t2.ID, false); err != nil {
		t.Fatalf("empty tenant delete: %v", err)
	}
}
