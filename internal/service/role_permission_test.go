package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRolePermissionInheritanceAndBuiltinLock(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin not found: %v", err)
	}

	parent, err := svc.CreateRole(ctx, admin.ID, model.RolePlatformAdmin, CreateRoleRequest{Name: "parent", Scope: "tenant"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, parent.ID, []PermissionSpec{{Action: "read", Resource: "audit", Effect: "allow"}}); err != nil {
		t.Fatalf("parent perms: %v", err)
	}
	child, err := svc.CreateRole(ctx, admin.ID, model.RolePlatformAdmin, CreateRoleRequest{Name: "child", Scope: "tenant", ParentID: parent.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	ta, _ := svc.CreateTenant(ctx, "TenantA")
	u, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "cmember", Password: "secret123", Role: model.RoleOperator})
	if err := svc.Store.AssignUserRole(ctx, u.ID, child.ID); err != nil {
		t.Fatalf("assign child role: %v", err)
	}

	// The child role inherits the parent's read-audit rule.
	if err := svc.Authorize(ctx, u.ID, "read", "audit"); err != nil {
		t.Fatalf("child role should inherit parent read audit: %v", err)
	}
	if err := svc.Authorize(ctx, u.ID, "manage", "audit"); err == nil {
		t.Fatal("child role must not inherit an unset action")
	}

	// Built-in role permissions are locked.
	if err := svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, model.RoleTenantAdmin, []PermissionSpec{{Action: "read", Resource: "chat", Effect: "allow"}}); err == nil {
		t.Fatal("built-in role permissions should be immutable")
	}
	_ = svc.Store.RemoveUserRoles(ctx, u.ID)
}
