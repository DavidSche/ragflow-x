package db

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// openMigratedRBACDB builds a fresh database and runs the full migration chain
// so the RBAC seeding behaviour is exercised exactly as a new deployment sees
// it (doc/242).
func openMigratedRBACDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "rbac-intent.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	return gdb
}

// TestMigrationV158AlignsBusinessUserReads guards the corrective data
// migration: after migrating, the persisted grants must equal the matrix
// (including the newly declared scenario-template read) and must contain no
// duplicate rows.
func TestMigrationV158AlignsBusinessUserReads(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	var scenarioRead int64
	if err := gdb.Model(&model.Permission{}).
		Where("role_id = ? AND action = ? AND resource = ?", model.RoleBusinessUser, "read", "scenario-template").
		Count(&scenarioRead).Error; err != nil {
		t.Fatal(err)
	}
	if scenarioRead != 1 {
		t.Fatalf("business_user must hold exactly one scenario-template read, got %d", scenarioRead)
	}

	// viewer must not dominate business_user on any resource after the fix:
	// every (viewer read) row the business user lacks has to be one of the
	// explicitly declared denials.
	viewerReads := map[string]bool{}
	var viewerRows []model.Permission
	if err := gdb.Where("role_id = ? AND action = ?", model.RoleViewer, "read").Find(&viewerRows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range viewerRows {
		viewerReads[row.Resource] = true
	}
	var businessRows []model.Permission
	if err := gdb.Where("role_id = ?", model.RoleBusinessUser).Find(&businessRows).Error; err != nil {
		t.Fatal(err)
	}
	businessReads := map[string]bool{}
	for _, row := range businessRows {
		if row.Action == "read" {
			businessReads[row.Resource] = true
		}
	}
	rules := businessUserReadRuleMap()
	for resource := range viewerReads {
		if businessReads[resource] {
			continue
		}
		rule, ok := rules[resource]
		if !ok || rule.Intent != BusinessUserReadDenied {
			t.Fatalf("resource %q: viewer can read, business_user cannot, and the denial is not declared in permission_intent.go", resource)
		}
	}

	// No duplicate (role, action, resource) triples may survive reconciliation.
	type triple struct{ Role, Action, Resource string }
	var all []model.Permission
	if err := gdb.Find(&all).Error; err != nil {
		t.Fatal(err)
	}
	seen := map[triple]int{}
	for _, row := range all {
		seen[triple{row.RoleID, row.Action, row.Resource}]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Fatalf("duplicate permission row after migration 158: %v x%d", key, count)
		}
	}
}

// TestMigrationV158IsReentrant proves the corrective step is safe to re-run and
// self-heals a drifted row set (an older binary that granted viewer-only reads
// and never granted the business user its template read).
func TestMigrationV158IsReentrant(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	// Simulate the pre-fix state: drop the business user's read and add a
	// stale duplicate-shaped row that is not in the matrix.
	if err := gdb.Where("role_id = ? AND action = ? AND resource = ?", model.RoleBusinessUser, "read", "scenario-template").
		Delete(&model.Permission{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.Permission{
		ID: "stale-grant", RoleID: model.RoleBusinessUser, Action: "manage", Resource: "tenant",
		Effect: model.PermissionEffectAllow,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatal(err)
	}

	var restored int64
	if err := gdb.Model(&model.Permission{}).
		Where("role_id = ? AND action = ? AND resource = ?", model.RoleBusinessUser, "read", "scenario-template").
		Count(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if restored != 1 {
		t.Fatalf("re-run must restore the scenario-template read, got %d rows", restored)
	}
	var stale int64
	if err := gdb.Model(&model.Permission{}).Where("id = ?", "stale-grant").Count(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatal("re-run must drop grants that are not in the built-in matrix")
	}

	// A second re-run must be a no-op (same row count, no duplicates).
	var before int64
	if err := gdb.Model(&model.Permission{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := gdb.Model(&model.Permission{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("second re-run changed the row count: %d -> %d", before, after)
	}
}
