package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// These tests pin doc/304 §4 E175: role inheritance and role ownership are
// tenant boundaries. Both halves matter, and neither is visible from a passing
// happy path — a tenant admin who can point `parent_id` at `platform_admin`, or
// who can edit another workspace's role, is an ordinary 200 response.

// newRoleTenantFixture returns the platform admin's id plus two workspaces and
// one admin in each, so every test below can ask "can A touch B's row?" without
// re-deriving ids.
type roleTenantFixture struct {
	svc       *Service
	platform  string
	tenantA   string
	tenantB   string
	adminA    string
	adminB    string
	platformU string
}

func newRoleTenantFixture(t *testing.T) roleTenantFixture {
	t.Helper()
	ctx := context.Background()
	svc := newAuthzSvc(t)
	pa, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || pa == nil {
		t.Fatalf("bootstrap platform admin missing: %v", err)
	}
	a, err := svc.CreateTenant(ctx, "WorkspaceA")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateTenant(ctx, "WorkspaceB")
	if err != nil {
		t.Fatal(err)
	}
	adminA, err := svc.CreateUser(ctx, a.ID, "", CreateUserRequest{
		Username: "admin-a", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	adminB, err := svc.CreateUser(ctx, b.ID, "", CreateUserRequest{
		Username: "admin-b", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return roleTenantFixture{
		svc: svc, platform: pa.ID, tenantA: a.ID, tenantB: b.ID,
		adminA: adminA.ID, adminB: adminB.ID, platformU: pa.ID,
	}
}

// codeOf reports the business code carried by an error, or 0 when the error is
// not a coded HTTP error. Asserting on the code (not the message) keeps the test
// about the rule rather than about the wording of the rule.
func codeOf(err error) int {
	var e *httperr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// TestTenantRoleCannotInheritPlatformRole is the exploit from the review: a
// workspace admin creators a workspace-scoped role with `parent_id =
// platform_admin`, hands it to a member, and the member's permission closure
// contains every platform grant — with `ac.platform` true on top.
func TestTenantRoleCannotInheritPlatformRole(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	if _, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "escalate", Scope: model.RoleScopeTenant, ParentID: model.RolePlatformAdmin,
	}); codeOf(err) != httperr.CodeRoleParentScopeMismatch {
		t.Fatalf("tenant admin inherited platform_admin: err = %v (want code 41161)", err)
	}
	// UpdateRole reaches the same validator, so an existing role cannot be
	// re-pointed afterwards either.
	role, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "later", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateRole(ctx, f.adminA, model.RoleTenantAdmin, role.ID, UpdateRoleRequest{
		ParentID: model.RolePlatformAdmin,
	}); codeOf(err) != httperr.CodeRoleParentScopeMismatch {
		t.Fatalf("UpdateRole inherited platform_admin: err = %v (want code 41161)", err)
	}
}

// TestRoleParentMustShareTheChildsOwner covers the other direction of the same
// boundary: A's role cannot parent itself onto B's role, because that would
// inherit B's grants and make B's edit change what A's members may do.
func TestRoleParentMustShareTheChildsOwner(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	foreign, err := f.svc.CreateRole(ctx, f.adminB, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "b-only", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "a-child", Scope: model.RoleScopeTenant, ParentID: foreign.ID,
	}); codeOf(err) != httperr.CodeRoleParentForeignTenant {
		t.Fatalf("parent from another tenant accepted: err = %v (want code 41162)", err)
	}
	// A shared, platform-owned parent is legitimate: that is how custom roles
	// build on the built-in lattice.
	if _, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "a-ok", Scope: model.RoleScopeTenant, ParentID: model.RoleTenantAdmin,
	}); err != nil {
		t.Fatalf("shared parent rejected: %v", err)
	}
}

// TestRoleListsAndEditsAreTenantScoped is the "同租户可见、跨租户不可见/不可改"
// half of E175. Listing, editing, permission writes, deletion and assignment all
// have to answer the same question about ownership.
func TestRoleListsAndEditsAreTenantScoped(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	bRole, err := f.svc.CreateRole(ctx, f.adminB, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "b-secret", Scope: model.RoleScopeTenant, Description: "workspace B",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bRole.TenantID != f.tenantB {
		t.Fatalf("created role is not owned by its creator's tenant: %q", bRole.TenantID)
	}

	// A sees its own rows plus the shared ones, never B's.
	seen, err := f.svc.ListRoles(ctx, f.adminA, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range seen {
		if r.ID == bRole.ID {
			t.Fatalf("workspace A can list workspace B's role %q", bRole.Name)
		}
	}
	if len(seen) == 0 {
		t.Fatal("workspace A sees no roles at all; the shared lattice must stay visible")
	}

	// Editing someone else's row is not "forbidden", it is "not there": the id
	// itself is what isolation withholds (doc/304 §4 E139).
	if _, err := f.svc.UpdateRole(ctx, f.adminA, model.RoleTenantAdmin, bRole.ID, UpdateRoleRequest{Description: "hijacked"}); codeOf(err) != 404 {
		t.Fatalf("UpdateRole across tenants: err = %v (want 404)", err)
	}
	if err := f.svc.DeleteRole(ctx, f.adminA, model.RoleTenantAdmin, bRole.ID); codeOf(err) != 404 {
		t.Fatalf("DeleteRole across tenants: err = %v (want 404)", err)
	}
	if err := f.svc.SetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, bRole.ID, []PermissionSpec{
		{Action: "manage", Resource: "role", Effect: "allow"},
	}); codeOf(err) != 404 {
		t.Fatalf("SetRolePermissions across tenants: err = %v (want 404)", err)
	}
	user, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "member-a", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AssignRole(ctx, f.tenantA, model.RoleTenantAdmin, user.ID, bRole.ID); codeOf(err) != 40021 {
		t.Fatalf("AssignRole across tenants: err = %v (want code 40021)", err)
	}

	// B still owns its row, unchanged.
	got, err := f.svc.Store.GetRole(ctx, bRole.ID)
	if err != nil || got == nil {
		t.Fatalf("role vanished: %v", err)
	}
	if got.Description != "workspace B" {
		t.Fatalf("role was edited across tenants: %q", got.Description)
	}
}

// TestPlatformAuthoredRoleIsSharedButNotEditable keeps the other side honest: a
// role the platform writes carries no owner, so every tenant can hand it out,
// and none of them can rewrite it.
func TestPlatformAuthoredRoleIsSharedButNotEditable(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	shared, err := f.svc.CreateRole(ctx, f.platform, model.RolePlatformAdmin, CreateRoleRequest{
		Name: "shared-policy", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if shared.TenantID != "" {
		t.Fatalf("platform-authored role was stamped with a tenant: %q", shared.TenantID)
	}
	seen, err := f.svc.ListRoles(ctx, f.adminA, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range seen {
		if r.ID == shared.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("tenant cannot see a platform-authored role")
	}
	if err := f.svc.SetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, shared.ID, []PermissionSpec{
		{Action: "read", Resource: "audit", Effect: "allow"},
	}); err == nil {
		t.Fatal("tenant rewrote a platform-authored role")
	}
}

// TestLegacyCrossScopeParentDoesNotEscalate is the read-path defence. The write
// path can be closed, but a row poisoned before the upgrade is already in the
// database, so `authz` must refuse the edge instead of walking into it.
func TestLegacyCrossScopeParentDoesNotEscalate(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	// Bypass the service on purpose: this is the pre-E175 row shape.
	poisoned := &model.Role{
		ID: "role_poisoned_escalation_0001", TenantID: f.tenantA, Name: "poisoned",
		Scope: model.RoleScopeTenant, ParentID: model.RolePlatformAdmin,
	}
	if err := f.svc.Store.CreateRole(ctx, poisoned); err != nil {
		t.Fatal(err)
	}
	user, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "poisoned-member", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.AssignUserRole(ctx, user.ID, poisoned.ID); err != nil {
		t.Fatal(err)
	}
	// The user keeps holding `viewer` as well, so a denial here is about the
	// poisoned edge and not about "no roles at all".
	if _, err := f.svc.authz(ctx, user.ID); codeOf(err) != 40304 {
		t.Fatalf("authz walked a cross-scope parent: err = %v (want code 40304)", err)
	}
	// The direct question a caller would ask must also stay denied.
	if err := f.svc.Authorize(ctx, user.ID, "governance", "manage"); err == nil {
		t.Fatal("poisoned role conferred a platform grant")
	}
}

// TestInheritedPlatformRoleDoesNotMakeAPlatformIdentity is the second half of
// the read-path defence and the precise line the review drew: `ac.platform` is a
// property of the roles a user *holds*, not of the roles it can reach. A tenant
// role inheriting the built-in tenant_admin must inherit its grants without
// becoming a platform identity.
func TestInheritedPlatformRoleDoesNotMakeAPlatformIdentity(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	child, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "inherits-admin", Scope: model.RoleScopeTenant, ParentID: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "heir", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.AssignUserRole(ctx, user.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	ac, err := f.svc.authz(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ac.platform {
		t.Fatal("inheriting a shared role produced a platform identity")
	}
	// The grants do flow: inheritance is not disabled, only the identity shortcut.
	if err := ac.evaluate("read", "role"); err != nil {
		t.Fatalf("inherited grant missing: %v", err)
	}
}
