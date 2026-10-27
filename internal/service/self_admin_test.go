package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestLastTenantAdminProtected(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	admin1, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "admin1", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}

	// The only remaining tenant admin cannot be demoted, disabled, or deleted.
	if err := svc.AssignRole(ctx, ta.ID, model.RoleTenantAdmin, admin1.ID, model.RoleOperator); err == nil {
		t.Fatal("expected last tenant admin demotion to be blocked")
	}
	if err := svc.SetUserStatus(ctx, ta.ID, model.RoleTenantAdmin, admin1.ID, model.UserStatusDisabled); err == nil {
		t.Fatal("expected last tenant admin disable to be blocked")
	}
	if err := svc.DeleteUser(ctx, ta.ID, model.RoleTenantAdmin, admin1.ID); err == nil {
		t.Fatal("expected last tenant admin delete to be blocked")
	}

	// Adding a second tenant admin unblocks the operation.
	if _, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "admin2", Password: "secret123", Role: model.RoleTenantAdmin}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignRole(ctx, ta.ID, model.RoleTenantAdmin, admin1.ID, model.RoleOperator); err != nil {
		t.Fatalf("demotion should be allowed once a second admin exists: %v", err)
	}
}

func TestTenantAdminCannotManagePlatformRole(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "ta", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}

	// Role picker must not expose platform-scoped roles to a tenant admin.
	roles, err := svc.ListRoles(ctx, model.RoleTenantAdmin, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if r.Scope == model.RoleScopePlatform {
			t.Fatalf("tenant admin must not see platform role %q", r.ID)
		}
	}

	// Read/edit/delete of the platform_admin role must be forbidden.
	if _, err := svc.GetRolePermissions(ctx, admin.ID, model.RoleTenantAdmin, model.RolePlatformAdmin); err == nil {
		t.Fatal("tenant admin must not read platform_admin permissions")
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, model.RoleTenantAdmin, model.RolePlatformAdmin, nil); err == nil {
		t.Fatal("tenant admin must not edit platform_admin permissions")
	}
	if _, err := svc.UpdateRole(ctx, admin.ID, model.RoleTenantAdmin, model.RolePlatformAdmin, UpdateRoleRequest{Name: "x"}); err == nil {
		t.Fatal("tenant admin must not edit platform_admin role")
	}
	if err := svc.DeleteRole(ctx, admin.ID, model.RoleTenantAdmin, model.RolePlatformAdmin); err == nil {
		t.Fatal("tenant admin must not delete platform_admin role")
	}
}
