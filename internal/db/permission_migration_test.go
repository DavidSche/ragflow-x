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
// TestMigrationV160AddsTrialRunResource proves the dedicated trial-run
// resource reaches databases that were seeded while trial runs still borrowed
// eval-set:execute (doc/244). It also pins the split that motivated the
// resource: team_admin may execute, business_user and viewer may only read, and
// team_admin still must not gain eval-set execute (that would re-open
// POST /eval-cases/:id/revalidate).
func TestMigrationV160AddsTrialRunResource(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	var applied int64
	if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 160).Count(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("expected migration 160 to be recorded once, got %d", applied)
	}

	grants := map[string]map[string]bool{}
	var rows []model.Permission
	if err := gdb.Where("resource = ?", "trial-run").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if grants[row.RoleID] == nil {
			grants[row.RoleID] = map[string]bool{}
		}
		grants[row.RoleID][row.Action] = true
	}
	want := map[string][]string{
		model.RolePlatformAdmin: {"read", "execute"},
		model.RoleTenantAdmin:   {"read", "execute"},
		model.RoleOperator:      {"read", "execute"},
		model.RoleTeamAdmin:     {"read", "execute"},
		model.RoleBusinessUser:  {"read"},
		model.RoleViewer:        {"read"},
	}
	if len(grants) != len(want) {
		t.Fatalf("trial-run granted to %d roles, want %d: %v", len(grants), len(want), grants)
	}
	for role, actions := range want {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s trial-run grants = %v, want %v", role, grants[role], actions)
		}
		for _, action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing trial-run %s", role, action)
			}
		}
	}

	// The whole point of doc/244: execute@trial-run must not be implemented by
	// handing out execute@eval-set, whose other consumer is revalidate.
	var teamEvalExecute int64
	if err := gdb.Model(&model.Permission{}).
		Where("role_id = ? AND action = ? AND resource = ?", model.RoleTeamAdmin, "execute", "eval-set").
		Count(&teamEvalExecute).Error; err != nil {
		t.Fatal(err)
	}
	if teamEvalExecute != 0 {
		t.Fatalf("team_admin must not hold eval-set execute (doc/129 §1178), got %d", teamEvalExecute)
	}
}

// TestMigrationV160IsReentrant deletes the grants the migration adds, plants a
// stale row the migration must remove, and re-runs the reconciler twice.
func TestMigrationV160IsReentrant(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	if err := gdb.Where("resource = ?", "trial-run").Delete(&model.Permission{}).Error; err != nil {
		t.Fatal(err)
	}
	stale := model.Permission{
		ID: "perm-stale-160", RoleID: model.RoleTeamAdmin, Action: "manage", Resource: "trial-run",
	}
	if err := gdb.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}

	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var restored, staleRows int64
	gdb.Model(&model.Permission{}).Where("resource = ?", "trial-run").Count(&restored)
	gdb.Model(&model.Permission{}).Where("id = ?", "perm-stale-160").Count(&staleRows)
	if restored != 10 {
		t.Fatalf("expected 10 trial-run grant rows after reconcile, got %d", restored)
	}
	if staleRows != 0 {
		t.Fatal("reconcile must drop grants that are not in the builtin matrix")
	}

	var before, after int64
	gdb.Model(&model.Permission{}).Count(&before)
	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	gdb.Model(&model.Permission{}).Count(&after)
	if before != after {
		t.Fatalf("reconcile is not idempotent: %d rows then %d rows", before, after)
	}
}

// TestMigrationV161ConvergesViewerReads proves the v0.5.0 convergence reaches
// existing databases: `viewer` keeps exactly the 14 business reads it shares
// with business_user, and the 19 tenant-wide operational/governance/config rows
// are gone (doc/245).
func TestMigrationV161ConvergesViewerReads(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	var applied int64
	if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 161).Count(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("expected migration 161 to be recorded once, got %d", applied)
	}

	viewerActions := map[string]map[string]bool{}
	var rows []model.Permission
	if err := gdb.Where("role_id = ?", model.RoleViewer).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if viewerActions[row.Resource] == nil {
			viewerActions[row.Resource] = map[string]bool{}
		}
		viewerActions[row.Resource][row.Action] = true
	}
	if len(viewerActions) != 14 {
		t.Fatalf("viewer must read exactly 14 resources, got %d: %v", len(viewerActions), sortedKeys(viewerActions))
	}
	for resource, actions := range viewerActions {
		if len(actions) != 1 || !actions["read"] {
			t.Fatalf("viewer must be read-only on %q, got %v", resource, actions)
		}
	}
	for _, resource := range viewerConvergenceRemovedReads {
		if _, ok := viewerActions[resource]; ok {
			t.Fatalf("migration 161 must remove viewer read on %q", resource)
		}
	}

	// business_user must still hold every read viewer holds (viewer ⊆
	// business_user), and must not have lost ground in the process.
	businessReads := map[string]bool{}
	var businessRows []model.Permission
	if err := gdb.Where("role_id = ? AND action = ?", model.RoleBusinessUser, "read").Find(&businessRows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range businessRows {
		businessReads[row.Resource] = true
	}
	for resource := range viewerActions {
		if !businessReads[resource] {
			t.Fatalf("viewer reads %q but business_user does not: the inversion doc/245 removed came back", resource)
		}
	}
	if len(businessReads) != 15 {
		t.Fatalf("business_user read set must stay at 15 resources, got %d", len(businessReads))
	}
}

// TestMigrationV161IsReentrant plants the removed rows back, re-runs the
// reconciler twice, and expects them to disappear again with a stable count.
func TestMigrationV161IsReentrant(t *testing.T) {
	gdb := openMigratedRBACDB(t)

	planted := []model.Permission{
		{ID: "perm-v161-usage", RoleID: model.RoleViewer, Action: "read", Resource: "usage"},
		{ID: "perm-v161-task", RoleID: model.RoleViewer, Action: "read", Resource: "task"},
		{ID: "perm-v161-manage", RoleID: model.RoleViewer, Action: "manage", Resource: "dataset"},
	}
	for i := range planted {
		if err := gdb.Create(&planted[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, row := range planted {
		var n int64
		gdb.Model(&model.Permission{}).Where("id = ?", row.ID).Count(&n)
		if n != 0 {
			t.Fatalf("reconcile must drop %s@%s for viewer", row.Action, row.Resource)
		}
	}
	var viewerRows int64
	gdb.Model(&model.Permission{}).Where("role_id = ?", model.RoleViewer).Count(&viewerRows)
	if viewerRows != 14 {
		t.Fatalf("expected 14 viewer rows after reconcile, got %d", viewerRows)
	}

	var before, after int64
	gdb.Model(&model.Permission{}).Count(&before)
	if err := reconcileBuiltinPermissions(gdb); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	gdb.Model(&model.Permission{}).Count(&after)
	if before != after {
		t.Fatalf("reconcile is not idempotent: %d rows then %d rows", before, after)
	}
}

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
