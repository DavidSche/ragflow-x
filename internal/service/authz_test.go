package service

import (
	"context"
	"path/filepath"
	"strconv"
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

// TestAuthorizeImpliesReadFromWriteGrants covers the custom-role half of the
// "write implies read" rule (doc/247). Builtin roles satisfy it through the
// matrix guard, but roles authored via PUT /roles/:id/permissions are not
// rewritten by any migration — the implication has to hold at authorization
// time or the UI renders a page the API then 403s.
func TestAuthorizeImpliesReadFromWriteGrants(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	workspace, err := svc.CreateTenant(ctx, "Implication Workspace")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		perms []PermissionSpec
	}{
		{
			name:  "manage only",
			perms: []PermissionSpec{{Action: "manage", Resource: "dataset", Effect: model.PermissionEffectAllow}},
		},
		{
			name:  "execute only",
			perms: []PermissionSpec{{Action: "execute", Resource: "chat", Effect: model.PermissionEffectAllow}},
		},
		{
			name:  "append only",
			perms: []PermissionSpec{{Action: "append", Resource: "document", Effect: model.PermissionEffectAllow}},
		},
		{
			name: "manage allowed but execute denied",
			perms: []PermissionSpec{
				{Action: "manage", Resource: "chat", Effect: model.PermissionEffectAllow},
				{Action: "execute", Resource: "chat", Effect: model.PermissionEffectDeny},
			},
		},
	}
	for i, tc := range cases {
		role, rerr := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{
			Name: "implication-" + strconv.Itoa(i), Scope: model.RoleScopeTenant,
		})
		if rerr != nil {
			t.Fatal(rerr)
		}
		if serr := svc.SetRolePermissions(ctx, admin.ID, admin.Role, role.ID, tc.perms); serr != nil {
			t.Fatal(serr)
		}
		user, uerr := svc.CreateUser(ctx, workspace.ID, "", CreateUserRequest{
			Username: "implication-user-" + strconv.Itoa(i), Password: "secret123", Role: role.ID,
		})
		if uerr != nil {
			t.Fatal(uerr)
		}
		resource := tc.perms[0].Resource
		if aerr := svc.Authorize(ctx, user.ID, "read", resource); aerr != nil {
			t.Fatalf("%s: read on %s should be implied: %v", tc.name, resource, aerr)
		}
	}

	// Deny rules stay literal: denying read still wins over an implied/allow read.
	role, err := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{Name: "implication-deny", Scope: model.RoleScopeTenant})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, admin.Role, role.ID, []PermissionSpec{
		{Action: "manage", Resource: "dataset", Effect: model.PermissionEffectAllow},
		{Action: "read", Resource: "dataset", Effect: model.PermissionEffectDeny},
	}); err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, workspace.ID, "", CreateUserRequest{Username: "implication-deny-user", Password: "secret123", Role: role.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, user.ID, "read", "dataset"); err == nil {
		t.Fatal("explicit deny read must not be overridden by an implied read")
	}
	if err := svc.Authorize(ctx, user.ID, "manage", "dataset"); err != nil {
		t.Fatalf("manage must stay allowed: %v", err)
	}
}

// TestSetRolePermissionsCannotExceedActorGrants pins the "you cannot grant what
// you cannot exercise" rule (doc/247 R-24). Before this guard, a tenant_admin
// (which holds role:manage) could create a tenant-scoped role carrying
// governance.read/tenant — a permission only platform_admin holds — assign it to
// itself, and then GET /api/v1/tenants would return every workspace, because
// ListTenants treats that permission as the full platform read.
func TestSetRolePermissionsCannotExceedActorGrants(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "Escalation Workspace")
	if err != nil {
		t.Fatal(err)
	}
	tenantAdmin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "escalation-admin", Password: "secret123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	role, err := svc.CreateRole(ctx, tenantAdmin.ID, tenantAdmin.Role, CreateRoleRequest{Name: "escalation-role", Scope: model.RoleScopeTenant})
	if err != nil {
		t.Fatal(err)
	}

	// 1) escalation attempt: grant a platform-only permission
	err = svc.SetRolePermissions(ctx, tenantAdmin.ID, tenantAdmin.Role, role.ID, []PermissionSpec{
		{Action: "governance.read", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "read", Resource: "chat", Effect: model.PermissionEffectAllow},
	})
	if err == nil {
		t.Fatal("tenant_admin must not be able to grant governance.read on tenant")
	}
	perms, perr := svc.Store.GetPermissions(ctx, role.ID)
	if perr != nil {
		t.Fatal(perr)
	}
	if len(perms) != 0 {
		t.Fatalf("rejected request must not persist partial grants: %+v", perms)
	}

	// 2) delegating permissions the actor does hold still works
	if err := svc.SetRolePermissions(ctx, tenantAdmin.ID, tenantAdmin.Role, role.ID, []PermissionSpec{
		{Action: "read", Resource: "chat", Effect: model.PermissionEffectAllow},
		{Action: "manage", Resource: "chat", Effect: model.PermissionEffectDeny},
	}); err != nil {
		t.Fatalf("tenant_admin should delegate a permission it holds: %v", err)
	}

	// 3) platform_admin holds the whole catalog, so the same grant is allowed for it
	platformRole, err := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{Name: "platform-delegation", Scope: model.RoleScopeTenant})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, admin.Role, platformRole.ID, []PermissionSpec{
		{Action: "governance.read", Resource: "tenant", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatalf("platform_admin must be able to delegate governance.read: %v", err)
	}
}
