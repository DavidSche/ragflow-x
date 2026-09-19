package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newAuthzSvc(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	jm := jwt.NewManager("secret", 24)
	svc := New(store, ragflow.NewMock(), jm, "key")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func TestAuthorize_RBACMultiRoleAndABAC(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	ua, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "adminA", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	ub, err := svc.CreateUser(ctx, tb.ID, "", CreateUserRequest{Username: "adminB", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}

	// tenant A admin can manage its own tenant resource
	if err := svc.AuthorizeObject(ctx, ua.ID, "manage", "dataset", ta.ID, ""); err != nil {
		t.Fatalf("own-tenant should be allowed: %v", err)
	}
	// cross-tenant is denied by ABAC (RBAC would allow, attribute check rejects)
	if err := svc.AuthorizeObject(ctx, ua.ID, "manage", "dataset", tb.ID, ""); err == nil {
		t.Fatal("cross-tenant access should be forbidden")
	}
	// owner attribute grants access even across tenant
	if err := svc.AuthorizeObject(ctx, ua.ID, "manage", "dataset", tb.ID, ua.ID); err != nil {
		t.Fatalf("owner should be allowed: %v", err)
	}
	// different tenant admin cannot manage across tenant
	if err := svc.AuthorizeObject(ctx, ub.ID, "manage", "dataset", ta.ID, ""); err == nil {
		t.Fatal("adminB should not manage tenant A resource")
	}
	// an empty object tenant is out of scope (fail closed) for non-platform users
	if err := svc.AuthorizeObject(ctx, ua.ID, "manage", "dataset", "", ""); err == nil {
		t.Fatal("empty object tenant should be forbidden for tenant admin")
	}

	// multi-role: operator has read/execute, not manage
	op, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "op", Password: "secret123", Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, op.ID, "read", "dataset"); err != nil {
		t.Fatalf("operator read should be allowed: %v", err)
	}
	if err := svc.Authorize(ctx, op.ID, "manage", "dataset"); err == nil {
		t.Fatal("operator manage should be forbidden")
	}

	// gateway RBAC: execute chat requires permission; viewer denied, operator allowed
	if err := svc.Authorize(ctx, op.ID, "execute", "chat"); err != nil {
		t.Fatalf("operator execute chat should be allowed: %v", err)
	}
	vw, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "vw", Password: "secret123", Role: "viewer"})
	if err := svc.Authorize(ctx, vw.ID, "execute", "chat"); err == nil {
		t.Fatal("viewer execute chat should be forbidden")
	}
}

func TestTenantRoleWithGovernancePermissionIsNotPlatform(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	workspace, err := svc.CreateTenant(ctx, "Explicit Workspace")
	if err != nil {
		t.Fatal(err)
	}
	role, err := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{Name: "workspace-governance", Scope: model.RoleScopeTenant})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, admin.Role, role.ID, []PermissionSpec{
		{Action: "governance.manage", Resource: "tenant", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, workspace.ID, "", CreateUserRequest{
		Username: "explicit-governance", Password: "secret123", Role: role.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	me, err := svc.MePermissions(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if me.Platform {
		t.Fatal("tenant role must not inherit implicit platform identity")
	}
	if err := svc.Authorize(ctx, user.ID, "governance.manage", "tenant"); err != nil {
		t.Fatalf("explicit permission should be granted: %v", err)
	}
}

// TestBootstrapAdminReconcile simulates the mailbox deadlock (admin demoted to
// operator) and verifies BootstrapAdmin restores it to platform_admin.
func TestBootstrapAdminReconcile(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin not found: %v", err)
	}
	// demote admin to operator and drop its roles (deadlock state)
	admin.Role = "operator"
	if err := svc.Store.UpdateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	_ = svc.Store.RemoveUserRoles(ctx, admin.ID)

	if err := svc.BootstrapAdmin(ctx, "admin", "anything"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	re, _ := svc.Store.GetUser(ctx, admin.ID)
	if re == nil || re.Role != "platform_admin" {
		t.Fatalf("admin not restored: %+v", re)
	}
	roles, _ := svc.Store.ListRolesByUser(ctx, admin.ID)
	found := false
	for _, r := range roles {
		if r.ID == "platform_admin" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("platform_admin user_role missing: %+v", roles)
	}
}
