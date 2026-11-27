package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// adminIDOf returns the bootstrap platform admin's user id. The role endpoints
// now resolve the caller's tenant and platform authority from the store, so a
// test that passes a role name where a user id belongs is asking a different
// question — and would read as "unauthorized" rather than "no such role".
func adminIDOf(t *testing.T, svc *Service) string {
	t.Helper()
	u, err := svc.Store.GetUserByUsername(context.Background(), "admin")
	if err != nil || u == nil {
		t.Fatalf("bootstrap platform admin missing: %v", err)
	}
	return u.ID
}

func TestRoleCRUDAndScopes(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin not found: %v", err)
	}
	projectRole, err := svc.CreateRole(ctx, admin.ID, model.RolePlatformAdmin, CreateRoleRequest{Name: "auditor", Scope: "tenant", Description: "audit"})
	if err != nil {
		t.Fatalf("create tenant role: %v", err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, projectRole.ID, []PermissionSpec{{Action: "read", Resource: "audit", Effect: "allow"}}); err != nil {
		t.Fatalf("set permissions: %v", err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, projectRole.ID, []PermissionSpec{{Action: "execute", Resource: "unknown-resource", Effect: "allow"}}); err == nil {
		t.Fatal("permissions outside catalog should be rejected")
	}
	perms, err := svc.GetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, projectRole.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(perms) != 1 || perms[0].Action != "read" || perms[0].Resource != "audit" {
		t.Fatalf("unexpected permissions: %+v", perms)
	}
	roles, err := svc.ListRoles(ctx, adminIDOf(t, svc), repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range roles {
		if r.ID == projectRole.ID && r.Scope == "tenant" {
			found = true
		}
	}
	if !found {
		t.Fatal("created role not listed")
	}
	if err := svc.DeleteRole(ctx, admin.ID, model.RolePlatformAdmin, model.RolePlatformAdmin); err == nil {
		t.Fatal("built-in role should not be deletable")
	}
	if err := svc.DeleteRole(ctx, admin.ID, model.RolePlatformAdmin, projectRole.ID); err != nil {
		t.Fatalf("delete custom role: %v", err)
	}
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	op, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "opX", Password: "secret123", Role: "operator"})
	if _, err := svc.CreateRole(ctx, op.ID, model.RoleOperator, CreateRoleRequest{Name: "r", Scope: "platform"}); err == nil {
		t.Fatal("operator should not create platform-scoped role")
	}
}

func TestBuiltinRoleCatalogMatchesDoc30(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	roles, err := svc.ListRoles(ctx, adminIDOf(t, svc), repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		name        string
		scope       string
		description string
	}{
		model.RolePlatformAdmin: {name: "平台管理员", scope: model.RoleScopePlatform, description: "平台级全量管理"},
		model.RoleTenantAdmin:   {name: "工作区管理员", scope: model.RoleScopeTenant, description: "工作区内管理"},
		model.RoleOperator:      {name: "内容运营员", scope: model.RoleScopeTenant, description: "工作区内内容与业务运营"},
		model.RoleViewer:        {name: "只读", scope: model.RoleScopeTenant, description: "工作区内只读"},
		model.RoleTeamAdmin:     {name: "团队管理员", scope: model.RoleScopeTenant, description: "负责团队的项目/数据集管理"},
		model.RoleBusinessUser:  {name: "业务使用者", scope: model.RoleScopeTenant, description: "使用助手并维护数据集文档"},
	}
	if len(roles) != len(expected) {
		t.Fatalf("expected %d builtin roles, got %d: %+v", len(expected), len(roles), roles)
	}
	for _, role := range roles {
		want, ok := expected[role.ID]
		if !ok || !role.BuiltIn {
			t.Fatalf("unexpected non-builtin role in fresh catalog: %+v", role)
		}
		if role.Name != want.name || role.Scope != want.scope || role.Description != want.description {
			t.Fatalf("builtin role mismatch: got %+v, want %+v", role, want)
		}
	}
}

func TestRoleScopesListed(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	roles, err := svc.ListRoles(ctx, adminIDOf(t, svc), repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]bool{}
	for _, r := range roles {
		scopes[r.Scope] = true
	}
	if !scopes["platform"] {
		t.Fatalf("platform scope role missing, scopes=%v", scopes)
	}
	if !scopes["tenant"] {
		t.Fatalf("tenant scope role missing, scopes=%v", scopes)
	}
}

func TestRoleDeleteUsageInUse(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin: %v", err)
	}
	ta, _ := svc.CreateTenant(ctx, "T")
	r, err := svc.CreateRole(ctx, admin.ID, "platform_admin", CreateRoleRequest{Name: "custom", Scope: "tenant"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "u1", Password: "secret123", Role: "operator"})
	_ = svc.Store.AssignUserRole(ctx, u.ID, r.ID)
	if err := svc.DeleteRole(ctx, admin.ID, model.RolePlatformAdmin, r.ID); err == nil {
		t.Fatal("in-use role should not be deletable")
	}
	_ = svc.Store.RemoveUserRoles(ctx, u.ID)
	if err := svc.DeleteRole(ctx, admin.ID, model.RolePlatformAdmin, r.ID); err != nil {
		t.Fatalf("delete after unassign: %v", err)
	}
}

// E165 (doc/304 §4): deleting a user must release the roles it held.
//
// `40083 role is assigned to users` is answered from `rgx_user_role`, so a user that
// was deleted while its assignment rows survived leaves a role that no one holds and
// no one can delete — the operator is told "assigned to users" with zero users left.
// The test deletes through the *service*, not the store, because the service is what
// the endpoint calls; a store-level fix that the service bypassed would be invisible.
func TestDeletingAUserReleasesItsRoles(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin: %v", err)
	}
	ta, _ := svc.CreateTenant(ctx, "T165")
	r, err := svc.CreateRole(ctx, admin.ID, model.RolePlatformAdmin, CreateRoleRequest{Name: "released", Scope: "tenant"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	u, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{
		Username: "e165", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := svc.Store.AssignUserRole(ctx, u.ID, r.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if n, err := svc.Store.RoleUserCount(ctx, r.ID); err != nil || n != 1 {
		t.Fatalf("role user count before delete = %d, %v; want 1", n, err)
	}

	if err := svc.DeleteUser(ctx, ta.ID, model.RoleTenantAdmin, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if n, err := svc.Store.RoleUserCount(ctx, r.ID); err != nil || n != 0 {
		t.Fatalf("role user count after delete = %d, %v; a deleted user's grants survived", n, err)
	}
	// The observable consequence: the role is deletable without a manual unassign.
	if err := svc.DeleteRole(ctx, admin.ID, model.RolePlatformAdmin, r.ID); err != nil {
		t.Fatalf("role must be deletable after its only user is gone: %v", err)
	}
	// `RoleUserCount` above already proves the rows are gone (it counts by role_id),
	// so no row survives to make the role undeletable later — the rating is on the
	// row that decides `40083`, not on a convention.
}
