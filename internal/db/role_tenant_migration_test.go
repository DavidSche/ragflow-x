package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// These tests pin migration 192 (doc/304 §4 E175). The migration is the only
// thing that repairs rows written before role ownership existed, so it has to be
// both narrowing and idempotent: running it twice may not widen anything, and a
// role whose holders disagree about the workspace has no safe owner.

func newRoleOwnershipDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/role-ownership.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return gdb
}

func mustCreateRole(t *testing.T, gdb *gorm.DB, r *model.Role) {
	t.Helper()
	if err := gdb.Create(r).Error; err != nil {
		t.Fatal(err)
	}
}

func mustCreateUser(t *testing.T, gdb *gorm.DB, id, tenantID string) {
	t.Helper()
	u := &model.User{ID: id, TenantID: tenantID, Username: id, Status: model.UserStatusActive, Role: model.RoleViewer}
	if err := gdb.Create(u).Error; err != nil {
		t.Fatal(err)
	}
}

func mustLink(t *testing.T, gdb *gorm.DB, userID, roleID string) {
	t.Helper()
	if err := gdb.Create(&model.UserRole{UserID: userID, RoleID: roleID}).Error; err != nil {
		t.Fatal(err)
	}
}

func roleTenant(t *testing.T, gdb *gorm.DB, id string) (string, string) {
	t.Helper()
	var r model.Role
	if err := gdb.Where("id = ?", id).First(&r).Error; err != nil {
		t.Fatal(err)
	}
	return r.TenantID, r.ParentID
}

func TestRoleTenantOwnershipBackfill(t *testing.T) {
	gdb := newRoleOwnershipDB(t)
	mustCreateUser(t, gdb, "user-a1", "tenant-a")
	mustCreateUser(t, gdb, "user-a2", "tenant-a")
	mustCreateUser(t, gdb, "user-b1", "tenant-b")

	// Held by two users of one workspace: unambiguous, adopt.
	mustCreateRole(t, gdb, &model.Role{ID: "role_one_workspace", Name: "one", Scope: model.RoleScopeTenant})
	mustLink(t, gdb, "user-a1", "role_one_workspace")
	mustLink(t, gdb, "user-a2", "role_one_workspace")

	// Held across workspaces: no honest single owner, stays shared.
	mustCreateRole(t, gdb, &model.Role{ID: "role_two_workspaces", Name: "two", Scope: model.RoleScopeTenant})
	mustLink(t, gdb, "user-a1", "role_two_workspaces")
	mustLink(t, gdb, "user-b1", "role_two_workspaces")

	// Nobody holds it: adoption would be a guess about who may edit it.
	mustCreateRole(t, gdb, &model.Role{ID: "role_nobody", Name: "nobody", Scope: model.RoleScopeTenant})

	// Built-in roles are the platform's, whatever their holder count says.
	mustLink(t, gdb, "user-a1", model.RoleViewer)

	if err := backfillRoleTenantOwnership(gdb); err != nil {
		t.Fatal(err)
	}

	if tenant, _ := roleTenant(t, gdb, "role_one_workspace"); tenant != "tenant-a" {
		t.Fatalf("single-holder role not adopted: %q", tenant)
	}
	if tenant, _ := roleTenant(t, gdb, "role_two_workspaces"); tenant != "" {
		t.Fatalf("multi-tenant role was adopted by guessing: %q", tenant)
	}
	if tenant, _ := roleTenant(t, gdb, "role_nobody"); tenant != "" {
		t.Fatalf("unheld role was adopted by guessing: %q", tenant)
	}
	if tenant, _ := roleTenant(t, gdb, model.RoleViewer); tenant != "" {
		t.Fatalf("built-in role was adopted by a workspace: %q", tenant)
	}

	// Idempotent: a second pass changes nothing.
	if err := backfillRoleTenantOwnership(gdb); err != nil {
		t.Fatal(err)
	}
	if tenant, _ := roleTenant(t, gdb, "role_one_workspace"); tenant != "tenant-a" {
		t.Fatalf("second pass changed the adoption: %q", tenant)
	}
}

func TestRoleParentPruneDropsCrossScopeAndCrossTenantEdges(t *testing.T) {
	gdb := newRoleOwnershipDB(t)
	// platform_admin is seeded by Migrate with scope=platform.
	mustCreateRole(t, gdb, &model.Role{ID: "role_tenant_child", Name: "child", Scope: model.RoleScopeTenant, ParentID: model.RolePlatformAdmin})
	// A legitimate edge: tenant-scoped child under the shared tenant_admin.
	mustCreateRole(t, gdb, &model.Role{ID: "role_ok_child", Name: "ok", Scope: model.RoleScopeTenant, ParentID: model.RoleTenantAdmin})
	// A cross-tenant edge between two custom roles.
	mustCreateRole(t, gdb, &model.Role{ID: "role_owner_b", Name: "owner-b", Scope: model.RoleScopeTenant, TenantID: "tenant-b"})
	mustCreateRole(t, gdb, &model.Role{ID: "role_owner_a", Name: "owner-a", Scope: model.RoleScopeTenant, TenantID: "tenant-a", ParentID: "role_owner_b"})

	if err := pruneInvalidRoleParents(gdb); err != nil {
		t.Fatal(err)
	}

	if _, parent := roleTenant(t, gdb, "role_tenant_child"); parent != "" {
		t.Fatalf("cross-scope parent survived the repair: %q", parent)
	}
	if _, parent := roleTenant(t, gdb, "role_ok_child"); parent != model.RoleTenantAdmin {
		t.Fatalf("legitimate inherited parent was dropped: %q", parent)
	}
	if _, parent := roleTenant(t, gdb, "role_owner_a"); parent != "" {
		t.Fatalf("cross-tenant parent survived the repair: %q", parent)
	}
}

// roleStatus reads the columns migration 193 owns.
func roleStatus(t *testing.T, gdb *gorm.DB, id string) (string, string) {
	t.Helper()
	var r model.Role
	if err := gdb.Where("id = ?", id).First(&r).Error; err != nil {
		t.Fatal(err)
	}
	return r.Status, r.TenantID
}

// TestLegacyRoleQuarantineClosesTheSharedGap pins migration 193 (doc/315
// finding 2). Migration 192 could not adopt a role with no holders or with
// holders in two workspaces, so those rows kept `tenant_id = ”` — the value
// that means "shared with every workspace". Leaving them assignable was the
// gap; the repair parks them instead of guessing an owner.
func TestLegacyRoleQuarantineClosesTheSharedGap(t *testing.T) {
	gdb := newRoleOwnershipDB(t)
	mustCreateUser(t, gdb, "user-a1", "tenant-a")
	mustCreateUser(t, gdb, "user-b1", "tenant-b")
	// A pre-RBAC row: held only through the rgx_user.role marker, with no
	// rgx_user_role association. 192's query could not see this holder.
	legacy := &model.User{
		ID: "user-c1", TenantID: "tenant-c", Username: "user-c1",
		Status: model.UserStatusActive, Role: "role_marker_only",
	}
	if err := gdb.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}

	mustCreateRole(t, gdb, &model.Role{ID: "role_unheld", Name: "unheld", Scope: model.RoleScopeTenant})
	mustCreateRole(t, gdb, &model.Role{ID: "role_cross", Name: "cross", Scope: model.RoleScopeTenant})
	mustLink(t, gdb, "user-a1", "role_cross")
	mustLink(t, gdb, "user-b1", "role_cross")
	mustCreateRole(t, gdb, &model.Role{ID: "role_marker_only", Name: "marker", Scope: model.RoleScopeTenant})
	// A pre-upgrade row with no owner and no holders is indistinguishable from a
	// platform-authored shared role. It is parked too: the platform admin can
	// re-authorise the ones it meant to share, which is cheaper than leaving a
	// tenant-authored row assignable by everyone.
	mustCreateRole(t, gdb, &model.Role{ID: "role_platform_shared", Name: "shared", Scope: model.RoleScopeTenant})

	if err := quarantineUnownedLegacyRoles(gdb); err != nil {
		t.Fatal(err)
	}

	if status, owner := roleStatus(t, gdb, "role_unheld"); status != model.RoleStatusQuarantined || owner != "" {
		t.Fatalf("unheld legacy role stayed assignable: status=%q owner=%q", status, owner)
	}
	if status, owner := roleStatus(t, gdb, "role_cross"); status != model.RoleStatusQuarantined || owner != "" {
		t.Fatalf("cross-tenant legacy role stayed assignable: status=%q owner=%q", status, owner)
	}
	// The marker-only holder is a real holder: adopt it rather than park it,
	// otherwise a workspace that never used the association table loses a role
	// it authored (the third item of the review).
	if status, owner := roleStatus(t, gdb, "role_marker_only"); status != model.RoleStatusActive || owner != "tenant-c" {
		t.Fatalf("marker-only holder was not counted: status=%q owner=%q", status, owner)
	}
	// Only an explicit platform decision (status back to active) brings one back.
	if status, owner := roleStatus(t, gdb, "role_platform_shared"); status != model.RoleStatusQuarantined || owner != "" {
		t.Fatalf("unowned pre-upgrade role was assumed platform-authored: status=%q owner=%q", status, owner)
	}
	// Built-ins stay the platform's, active.
	if status, owner := roleStatus(t, gdb, model.RoleViewer); status != model.RoleStatusActive || owner != "" {
		t.Fatalf("built-in role was quarantined or adopted: status=%q owner=%q", status, owner)
	}
	// A row the journal has not seen yet cannot be told apart from a legacy one
	// (that is why the migration runs once, guarded by the schema_version row);
	// the assertion here is that a *second* pass changes nothing it already
	// decided, which is what makes the step safe to re-run in a test.
	if err := quarantineUnownedLegacyRoles(gdb); err != nil {
		t.Fatal(err)
	}
	if status, owner := roleStatus(t, gdb, "role_marker_only"); status != model.RoleStatusActive || owner != "tenant-c" {
		t.Fatalf("second pass changed the adopted role: status=%q owner=%q", status, owner)
	}
	if status, owner := roleStatus(t, gdb, "role_unheld"); status != model.RoleStatusQuarantined || owner != "" {
		t.Fatalf("second pass un-quarantined a role: status=%q owner=%q", status, owner)
	}
}
