package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// These tests pin the three defects the second review of doc/304 §4 opened
// (doc/315): a tenant admin could not *read* the permissions of the shared roles
// it is allowed to hand out, a legacy role with no single holder stayed
// assignable by every workspace, and a scope change did not re-check the parent
// it left behind. They are separate permissions on purpose: reading a shared
// role is not editing it, and marking a role as unowned is not revoking grants.

func roleIDs(roles []model.Role) map[string]model.Role {
	out := make(map[string]model.Role, len(roles))
	for _, r := range roles {
		out[r.ID] = r
	}
	return out
}

// TestTenantAdminReadsSharedRolePermissions is finding 1. The console renders a
// permission button on every row it lists, and a tenant admin is allowed to list
// the shared lattice; opening the dialog must show the matrix instead of a 403.
func TestTenantAdminReadsSharedRolePermissions(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)

	// A built-in every workspace can hand out.
	perms, err := f.svc.GetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, model.RoleTenantAdmin)
	if err != nil {
		t.Fatalf("tenant admin cannot read tenant_admin's permissions: %v", err)
	}
	if len(perms) == 0 {
		t.Fatal("tenant_admin returned no permissions; the read succeeded at nothing")
	}

	// A shared custom role authored by the platform, for the same reason.
	shared, err := f.svc.CreateRole(ctx, f.platform, model.RolePlatformAdmin, CreateRoleRequest{
		Name: "shared-audit", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetRolePermissions(ctx, f.platform, model.RolePlatformAdmin, shared.ID, []PermissionSpec{
		{Action: "read", Resource: "audit", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.GetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, shared.ID)
	if err != nil {
		t.Fatalf("tenant admin cannot read a shared role's permissions: %v", err)
	}
	if len(got) != 1 || got[0].Resource != "audit" {
		t.Fatalf("shared role permissions read back wrong: %+v", got)
	}

	// Reads get wider, writes do not: the same row must still refuse every write
	// path, or "readable" would have been implemented as "owned".
	if err := f.svc.SetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, shared.ID, []PermissionSpec{
		{Action: "manage", Resource: "role", Effect: model.PermissionEffectAllow},
	}); codeOf(err) != 403 {
		t.Fatalf("tenant rewrote a shared role: err = %v (want 403)", err)
	}
	if _, err := f.svc.UpdateRole(ctx, f.adminA, model.RoleTenantAdmin, shared.ID, UpdateRoleRequest{Description: "mine"}); codeOf(err) != 403 {
		t.Fatalf("tenant edited a shared role: err = %v (want 403)", err)
	}
	if err := f.svc.DeleteRole(ctx, f.adminA, model.RoleTenantAdmin, shared.ID); codeOf(err) != 403 {
		t.Fatalf("tenant deleted a shared role: err = %v (want 403)", err)
	}

	// Neither of the two boundaries moved: another tenant's row is still absent,
	// and a platform-scoped row is still refused rather than hidden.
	bRole, err := f.svc.CreateRole(ctx, f.adminB, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "b-only-read", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, bRole.ID); codeOf(err) != 404 {
		t.Fatalf("read across tenants: err = %v (want 404)", err)
	}
	if _, err := f.svc.GetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, model.RolePlatformAdmin); codeOf(err) != 403 {
		t.Fatalf("read of a platform-scoped role: err = %v (want 403)", err)
	}
}

// TestRoleListReportsWhoMayEditEachRow keeps the console honest: the derived
// flags must agree with the write path, because they are what decides whether
// the edit and delete buttons are rendered at all.
func TestRoleListReportsWhoMayEditEachRow(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	own, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "a-owned", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := f.svc.CreateRole(ctx, f.platform, model.RolePlatformAdmin, CreateRoleRequest{
		Name: "shared-flags", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}

	seen, err := f.svc.ListRoles(ctx, f.adminA, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	byID := roleIDs(seen)
	if r := byID[own.ID]; !r.Owned || !r.Editable {
		t.Fatalf("own role is not editable for its tenant: %+v", r)
	}
	if r := byID[model.RoleTenantAdmin]; r.Owned || r.Editable {
		t.Fatalf("shared built-in claims to be editable by a tenant: %+v", r)
	}
	if r := byID[shared.ID]; r.Owned || r.Editable {
		t.Fatalf("platform-authored role claims to be editable by a tenant: %+v", r)
	}

	platformSeen, err := f.svc.ListRoles(ctx, f.platform, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if r := roleIDs(platformSeen)[shared.ID]; !r.Editable {
		t.Fatalf("platform cannot edit the role it authored: %+v", r)
	}
	// Even the platform must not be told a built-in is editable: the write path
	// refuses it with 40082.
	if r := roleIDs(platformSeen)[model.RoleTenantAdmin]; r.Editable {
		t.Fatalf("a built-in advertises itself as editable: %+v", r)
	}
}

// TestUpdateRoleRevalidatesParentWhenScopeChanges is finding 3, found while
// reproducing the other two: the create path validates (scope, parent) as a
// pair, the update path validated the parent only when a new one was supplied.
func TestUpdateRoleRevalidatesParentWhenScopeChanges(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	role, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "scope-swap", Scope: model.RoleScopeTenant, ParentID: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The leftover parent is tenant-scoped: widening the child would make the
	// pair exactly what the create path calls 40088.
	if _, err := f.svc.UpdateRole(ctx, f.platform, model.RolePlatformAdmin, role.ID, UpdateRoleRequest{
		Scope: model.RoleScopePlatform,
	}); codeOf(err) != httperr.CodeRoleParentScopeMismatch {
		t.Fatalf("scope change kept a cross-scope parent: err = %v (want 40088)", err)
	}
	// Nothing was half-written: the row is still tenant-scoped with its parent.
	stored, err := f.svc.Store.GetRole(ctx, role.ID)
	if err != nil || stored == nil {
		t.Fatalf("role vanished: %v", err)
	}
	if stored.Scope != model.RoleScopeTenant || stored.ParentID != model.RoleTenantAdmin {
		t.Fatalf("rejected update persisted a partial change: %+v", stored)
	}
	// The same move is allowed when the parent is replaced by one of the new
	// scope in the same request: the rule is the pair, not the scope change.
	if _, err := f.svc.UpdateRole(ctx, f.platform, model.RolePlatformAdmin, role.ID, UpdateRoleRequest{
		Scope: model.RoleScopePlatform, ParentID: model.RolePlatformAdmin,
	}); err != nil {
		t.Fatalf("legal scope+parent change refused: %v", err)
	}
	// The owner has to be cleared in the database, not only in the returned
	// struct: a platform-scoped row that still names a tenant is an edge
	// `expandRoles` has to refuse, and the console would offer it to that tenant.
	moved, err := f.svc.Store.GetRole(ctx, role.ID)
	if err != nil || moved == nil {
		t.Fatalf("role vanished: %v", err)
	}
	if moved.TenantID != "" || moved.Scope != model.RoleScopePlatform {
		t.Fatalf("scope change kept the old owner: %+v", moved)
	}
}

// TestGovernancePermissionAloneIsNotAPlatformIdentity is finding 2. A
// tenant-scope role may be granted governance.manage on purpose (doc/304 §4
// E176 lets a platform admin delegate it), but the grant must not become the
// platform identity the role endpoints authorise against — otherwise the holder
// can author a platform-scoped role.
func TestGovernancePermissionAloneIsNotAPlatformIdentity(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	delegated, err := f.svc.CreateRole(ctx, f.platform, model.RolePlatformAdmin, CreateRoleRequest{
		Name: "tenant-governor", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetRolePermissions(ctx, f.platform, model.RolePlatformAdmin, delegated.ID, []PermissionSpec{
		{Action: "governance.manage", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "manage", Resource: "role", Effect: model.PermissionEffectAllow},
		{Action: "read", Resource: "role", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatal(err)
	}
	governor, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "governor-a", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.AssignUserRole(ctx, governor.ID, delegated.ID); err != nil {
		t.Fatal(err)
	}
	me, err := f.svc.MePermissions(ctx, governor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if me.Platform {
		t.Fatal("a tenant-scope grant produced a platform identity")
	}
	// The grant itself still works — that is the delegation E176 established.
	if err := f.svc.Authorize(ctx, governor.ID, "governance.manage", "tenant"); err != nil {
		t.Fatalf("delegated governance.manage was lost: %v", err)
	}

	owned, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "a-climb", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateRole(ctx, governor.ID, model.RoleTenantAdmin, owned.ID, UpdateRoleRequest{
		Scope: model.RoleScopePlatform,
	}); codeOf(err) != 403 {
		t.Fatalf("governance grant widened a role to platform scope: err = %v (want 403)", err)
	}
	if _, err := f.svc.CreateRole(ctx, governor.ID, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "a-platform", Scope: model.RoleScopePlatform,
	}); codeOf(err) != 403 {
		t.Fatalf("governance grant authored a platform role: err = %v (want 403)", err)
	}
}

// TestQuarantinedLegacyRoleIsParkedNotShared is finding 2 of the migration
// review. Rows the migration cannot attribute to one workspace are marked, not
// guessed at: no tenant may list, read, inherit or assign them, and only a
// platform admin can put one back into service.
func TestQuarantinedLegacyRoleIsParkedNotShared(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	legacy := &model.Role{
		ID: "role_legacy_orphan_0001", Name: "legacy-orphan", Scope: model.RoleScopeTenant,
		Status: model.RoleStatusQuarantined,
	}
	if err := f.svc.Store.CreateRole(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.SetPermissions(ctx, legacy.ID, []model.Permission{{
		ID: "perm_legacy_orphan", RoleID: legacy.ID, Action: "read", Resource: "chat",
		Effect: model.PermissionEffectAllow,
	}}); err != nil {
		t.Fatal(err)
	}

	seen, err := f.svc.ListRoles(ctx, f.adminA, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roleIDs(seen)[legacy.ID]; ok {
		t.Fatal("a quarantined role is still listed to a tenant admin")
	}
	if _, err := f.svc.GetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, legacy.ID); codeOf(err) != 404 {
		t.Fatalf("a quarantined role is readable by a tenant: err = %v (want 404)", err)
	}
	if _, err := f.svc.UpdateRole(ctx, f.adminA, model.RoleTenantAdmin, legacy.ID, UpdateRoleRequest{Description: "mine"}); codeOf(err) != 404 {
		t.Fatalf("a quarantined role is editable by a tenant: err = %v (want 404)", err)
	}
	member, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "quarantine-member", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AssignRole(ctx, f.tenantA, model.RoleTenantAdmin, member.ID, legacy.ID); codeOf(err) != 40021 {
		t.Fatalf("a quarantined role is assignable: err = %v (want 40021)", err)
	}
	if _, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "parked-parent", Scope: model.RoleScopeTenant, ParentID: legacy.ID,
	}); codeOf(err) != 40086 {
		t.Fatalf("a quarantined role is usable as a parent: err = %v (want 40086)", err)
	}

	// The platform still sees it, with the status and the write ability the
	// review flow needs.
	platformSeen, err := f.svc.ListRoles(ctx, f.platform, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	row, ok := roleIDs(platformSeen)[legacy.ID]
	if !ok {
		t.Fatal("the platform admin cannot see a role it has to review")
	}
	if row.Status != model.RoleStatusQuarantined || !row.Editable {
		t.Fatalf("platform view of a quarantined role is wrong: %+v", row)
	}

	// A tenant cannot lift the quarantine; the platform can.
	if _, err := f.svc.UpdateRole(ctx, f.adminA, model.RoleTenantAdmin, model.RoleTenantAdmin, UpdateRoleRequest{
		Status: ptr(model.RoleStatusActive),
	}); codeOf(err) != 403 {
		t.Fatalf("tenant changed a role's status: err = %v (want 403)", err)
	}
	if _, err := f.svc.UpdateRole(ctx, f.platform, model.RolePlatformAdmin, legacy.ID, UpdateRoleRequest{
		Status: ptr("bogus"),
	}); codeOf(err) != 40000 {
		t.Fatalf("invalid status accepted: err = %v (want 40000)", err)
	}
	if _, err := f.svc.UpdateRole(ctx, f.platform, model.RolePlatformAdmin, legacy.ID, UpdateRoleRequest{
		Status: ptr(model.RoleStatusActive),
	}); err != nil {
		t.Fatalf("platform could not re-authorise the role: %v", err)
	}
	// Re-authorised means shared again: listed, readable, assignable.
	seen, err = f.svc.ListRoles(ctx, f.adminA, repository.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roleIDs(seen)[legacy.ID]; !ok {
		t.Fatal("re-authorised role is still hidden from tenants")
	}
	if _, err := f.svc.GetRolePermissions(ctx, f.adminA, model.RoleTenantAdmin, legacy.ID); err != nil {
		t.Fatalf("re-authorised role is still unreadable: %v", err)
	}
}

// TestQuarantineDoesNotRevokeWhatAlreadyWorks pins the boundary chosen above:
// parking a role stops new grants and new inheritance, not the resolution of
// grants that already exist. Recorded here because the opposite reading
// ("quarantined means inert") is plausible and would change who can do what on
// upgrade day.
func TestQuarantineDoesNotRevokeWhatAlreadyWorks(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	parent := &model.Role{
		ID: "role_legacy_parent_0001", Name: "legacy-parent", Scope: model.RoleScopeTenant,
		Status: model.RoleStatusQuarantined,
	}
	if err := f.svc.Store.CreateRole(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.SetPermissions(ctx, parent.ID, []model.Permission{{
		ID: "perm_legacy_parent", RoleID: parent.ID, Action: "read", Resource: "audit",
		Effect: model.PermissionEffectAllow,
	}}); err != nil {
		t.Fatal(err)
	}
	// A child written before the quarantine existed.
	child := &model.Role{
		ID: "role_legacy_child_0001", TenantID: f.tenantA, Name: "legacy-child",
		Scope: model.RoleScopeTenant, ParentID: parent.ID, Status: model.RoleStatusActive,
	}
	if err := f.svc.Store.CreateRole(ctx, child); err != nil {
		t.Fatal(err)
	}
	member, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "legacy-member", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.AssignUserRole(ctx, member.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Authorize(ctx, member.ID, "read", "audit"); err != nil {
		t.Fatalf("quarantine revoked an inherited grant: %v", err)
	}
	// New edges are still refused, so the hole cannot widen.
	if _, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "new-child", Scope: model.RoleScopeTenant, ParentID: parent.ID,
	}); codeOf(err) != 40086 {
		t.Fatalf("new inheritance from a quarantined role: err = %v (want 40086)", err)
	}
	// And a quarantined role cannot be re-activated while its own parent is
	// quarantined either.
	orphanChild := &model.Role{
		ID: "role_legacy_child_0002", TenantID: f.tenantA, Name: "parked-child",
		Scope: model.RoleScopeTenant, ParentID: parent.ID, Status: model.RoleStatusQuarantined,
	}
	if err := f.svc.Store.CreateRole(ctx, orphanChild); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateRole(ctx, f.platform, model.RolePlatformAdmin, orphanChild.ID, UpdateRoleRequest{
		Status: ptr(model.RoleStatusActive),
	}); codeOf(err) != 40086 {
		t.Fatalf("re-activated a child of a quarantined role: err = %v (want 40086)", err)
	}
}

// TestRoleIsAssignableOnlyInsideItsOwnTenant covers a hole the doc/315 review
// found one step past finding 2: `validateRole` skipped the ownership check for a
// platform admin and was handed the *actor's* tenant, so a platform identity
// could give tenant A's custom role to a member of tenant B. The write succeeded
// and the account then failed at `authz` with 40305 on every request — a user
// bricked by a dropdown, and the kind of cross-tenant grant E175 exists to stop.
func TestRoleIsAssignableOnlyInsideItsOwnTenant(t *testing.T) {
	ctx := context.Background()
	f := newRoleTenantFixture(t)
	aRole, err := f.svc.CreateRole(ctx, f.adminA, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "a-private", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.svc.CreateUser(ctx, f.tenantB, "", CreateUserRequest{
		Username: "cross-tenant-member", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The platform admin may touch the account (it is platform-wide), but it may
	// not hand it a role from another workspace.
	if err := f.svc.AssignRole(ctx, f.tenantB, model.RolePlatformAdmin, member.ID, aRole.ID); codeOf(err) != 40021 {
		t.Fatalf("platform admin handed A's role to B's member: err = %v (want 40021)", err)
	}
	if err := f.svc.UpdateUserProfile(ctx, f.tenantB, model.RolePlatformAdmin, member.ID, "", "", aRole.ID, ""); codeOf(err) != 40021 {
		t.Fatalf("platform admin set A's role on B's member: err = %v (want 40021)", err)
	}
	// The same role is assignable inside its own workspace — the refusal above is
	// about the pair, not about the role.
	if err := f.svc.AssignRole(ctx, f.tenantA, model.RolePlatformAdmin, member.ID, model.RoleViewer); err != nil {
		t.Fatalf("shared role is not assignable: %v", err)
	}
	other, err := f.svc.CreateUser(ctx, f.tenantA, "", CreateUserRequest{
		Username: "same-tenant-member", Password: "secret123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AssignRole(ctx, f.tenantA, model.RolePlatformAdmin, other.ID, aRole.ID); err != nil {
		t.Fatalf("own-tenant role is not assignable by the platform: %v", err)
	}
	// And the member is still able to authenticate: the refusal left the existing
	// grant alone instead of half-writing a foreign role.
	if _, err := f.svc.authz(ctx, member.ID); err != nil {
		t.Fatalf("a refused cross-tenant grant broke the account: %v", err)
	}
}
