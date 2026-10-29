package db

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestMigrationV158BusinessUserReadsPostgreSQL proves the corrective RBAC data
// migration lands on the production driver against an already-migrated
// database (the real upgrade path from 157 → 158, doc/242 §6).
//
// SQLite covers the logic; PostgreSQL covers the fact that the reconciler's
// delete-then-insert really applies inside the migration transaction on the
// database shape the workspace actually runs.
func TestMigrationV158BusinessUserReadsPostgreSQL(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the RBAC read-intent migration contract")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := Migrate(gdb); err != nil {
			t.Fatalf("migrate attempt %d: %v", attempt, err)
		}
		var applied int64
		if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 158).Count(&applied).Error; err != nil {
			t.Fatalf("attempt %d: read schema version: %v", attempt, err)
		}
		if applied != 1 {
			t.Fatalf("attempt %d: migration 158 must be recorded exactly once, got %d", attempt, applied)
		}
		var read int64
		if err := gdb.Model(&model.Permission{}).
			Where("role_id = ? AND action = ? AND resource = ?", model.RoleBusinessUser, "read", "scenario-template").
			Count(&read).Error; err != nil {
			t.Fatalf("attempt %d: count business_user read: %v", attempt, err)
		}
		if read != 1 {
			t.Fatalf("attempt %d: business_user must hold exactly one scenario-template read, got %d", attempt, read)
		}
	}

	// No duplicate triples may survive the reconciler, and every viewer-only
	// read must be a declared denial.
	var total, distinct int64
	if err := gdb.Model(&model.Permission{}).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Raw("SELECT COUNT(*) FROM (SELECT DISTINCT role_id, action, resource FROM rgx_permission) t").
		Scan(&distinct).Error; err != nil {
		t.Fatal(err)
	}
	if total != distinct {
		t.Fatalf("duplicate permission triples present: %d rows vs %d distinct", total, distinct)
	}

	viewerReads := map[string]bool{}
	var viewerRows []model.Permission
	if err := gdb.Where("role_id = ? AND action = ?", model.RoleViewer, "read").Find(&viewerRows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range viewerRows {
		viewerReads[row.Resource] = true
	}
	businessReads := map[string]bool{}
	var businessRows []model.Permission
	if err := gdb.Where("role_id = ? AND action = ?", model.RoleBusinessUser, "read").Find(&businessRows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range businessRows {
		businessReads[row.Resource] = true
	}
	rules := businessUserReadRuleMap()
	for resource := range viewerReads {
		if businessReads[resource] {
			continue
		}
		if rule, ok := rules[resource]; !ok || rule.Intent != BusinessUserReadDenied {
			t.Fatalf("resource %q: viewer can read, business_user cannot, and the denial is not declared", resource)
		}
	}
}

// TestMigrationV159RoleLatticePostgreSQL is the doc/243 companion: the lattice
// declared in role_lattice.go must hold on the rows of an already-migrated
// database after the corrective step runs, twice (re-entry), on PostgreSQL.
func TestMigrationV159RoleLatticePostgreSQL(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the role-lattice migration contract")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := Migrate(gdb); err != nil {
			t.Fatalf("migrate attempt %d: %v", attempt, err)
		}
		var applied int64
		if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 159).Count(&applied).Error; err != nil {
			t.Fatalf("attempt %d: read schema version: %v", attempt, err)
		}
		if applied != 1 {
			t.Fatalf("attempt %d: migration 159 must be recorded exactly once, got %d", attempt, applied)
		}

		persisted := map[string]map[string]map[string]struct{}{}
		var rows []model.Permission
		if err := gdb.Find(&rows).Error; err != nil {
			t.Fatalf("attempt %d: load permissions: %v", attempt, err)
		}
		for _, row := range rows {
			if persisted[row.Resource] == nil {
				persisted[row.Resource] = map[string]map[string]struct{}{}
			}
			if persisted[row.Resource][row.RoleID] == nil {
				persisted[row.Resource][row.RoleID] = map[string]struct{}{}
			}
			persisted[row.Resource][row.RoleID][row.Action] = struct{}{}
		}
		for _, rel := range roleSupersetRelations {
			for resource, byRole := range persisted {
				for action := range byRole[rel.Subset] {
					if _, ok := byRole[rel.Superset][action]; !ok {
						t.Fatalf("attempt %d: persisted grants violate %s ⊇ %s on %q/%q",
							attempt, rel.Superset, rel.Subset, resource, action)
					}
				}
			}
		}
		for _, resource := range []string{"chat", "search-app", "agent", "assistant", "memory"} {
			if _, ok := persisted[resource][model.RoleTeamAdmin]["read"]; !ok {
				t.Fatalf("attempt %d: team_admin still cannot read %q", attempt, resource)
			}
		}
		// doc/245: on a real deployment the read-only role must not out-read the
		// working role on any resource, and must hold nothing but read.
		for resource, byRole := range persisted {
			for action := range byRole[model.RoleViewer] {
				if action != "read" {
					t.Fatalf("attempt %d: viewer holds %q on %q, must be read-only (doc/245)", attempt, action, resource)
				}
				if _, ok := byRole[model.RoleBusinessUser][action]; !ok {
					t.Fatalf("attempt %d: viewer reads %q but business_user does not (doc/245 inversion)", attempt, resource)
				}
			}
		}
		if got := len(persisted["usage"]); got == 0 {
			t.Fatalf("attempt %d: expected usage grants to exist for other roles", attempt)
		}
		for _, resource := range viewerConvergenceRemovedReads {
			if _, ok := persisted[resource][model.RoleViewer]["read"]; ok {
				t.Fatalf("attempt %d: viewer must not read %q after doc/245", attempt, resource)
			}
		}
	}
}

// TestMigrationV161ViewerConvergencePostgreSQL proves doc/245 lands on an
// existing deployment: viewer keeps exactly the 14 business reads, loses the 19
// tenant-wide operational/governance/config reads, gains nothing writable, and
// the migration is a no-op when re-run.
func TestMigrationV161ViewerConvergencePostgreSQL(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the viewer convergence migration contract")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := Migrate(gdb); err != nil {
			t.Fatalf("migrate attempt %d: %v", attempt, err)
		}
		var applied int64
		if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 161).Count(&applied).Error; err != nil {
			t.Fatalf("attempt %d: read schema version: %v", attempt, err)
		}
		if applied != 1 {
			t.Fatalf("attempt %d: migration 161 must be recorded exactly once, got %d", attempt, applied)
		}

		var rows []model.Permission
		if err := gdb.Where("role_id = ?", model.RoleViewer).Find(&rows).Error; err != nil {
			t.Fatalf("attempt %d: load viewer grants: %v", attempt, err)
		}
		if len(rows) != 14 {
			t.Fatalf("attempt %d: viewer must hold exactly 14 grant rows, got %d", attempt, len(rows))
		}
		for _, row := range rows {
			if row.Action != "read" {
				t.Fatalf("attempt %d: viewer holds %q on %q, must be read-only", attempt, row.Action, row.Resource)
			}
		}
		for _, resource := range viewerConvergenceRemovedReads {
			var n int64
			if err := gdb.Model(&model.Permission{}).
				Where("role_id = ? AND resource = ?", model.RoleViewer, resource).
				Count(&n).Error; err != nil {
				t.Fatalf("attempt %d: count viewer %q: %v", attempt, resource, err)
			}
			if n != 0 {
				t.Fatalf("attempt %d: viewer must not read %q after doc/245, got %d rows", attempt, resource, n)
			}
		}

		// The working role must still be able to read everything the guest can.
		var business []model.Permission
		if err := gdb.Where("role_id = ? AND action = ?", model.RoleBusinessUser, "read").Find(&business).Error; err != nil {
			t.Fatalf("attempt %d: load business_user reads: %v", attempt, err)
		}
		businessReads := map[string]bool{}
		for _, row := range business {
			businessReads[row.Resource] = true
		}
		for _, row := range rows {
			if !businessReads[row.Resource] {
				t.Fatalf("attempt %d: viewer reads %q but business_user does not (doc/245)", attempt, row.Resource)
			}
		}
	}
}

// TestMigrationV160TrialRunPostgreSQL proves the dedicated trial-run resource
// lands on a database that was already at 159 (where trial runs still borrowed
// eval-set:execute), and that re-running the migration is a no-op. This is the
// concrete "existing deployment gets fixed" evidence for doc/244.
func TestMigrationV160TrialRunPostgreSQL(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the trial-run migration contract")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := Migrate(gdb); err != nil {
			t.Fatalf("migrate attempt %d: %v", attempt, err)
		}
		var applied int64
		if err := gdb.Model(&model.SchemaVersion{}).Where("version = ?", 160).Count(&applied).Error; err != nil {
			t.Fatalf("attempt %d: read schema version: %v", attempt, err)
		}
		if applied != 1 {
			t.Fatalf("attempt %d: migration 160 must be recorded exactly once, got %d", attempt, applied)
		}

		var rows []model.Permission
		if err := gdb.Where("resource = ?", "trial-run").Find(&rows).Error; err != nil {
			t.Fatalf("attempt %d: load trial-run grants: %v", attempt, err)
		}
		grants := map[string]map[string]bool{}
		for _, row := range rows {
			if grants[row.RoleID] == nil {
				grants[row.RoleID] = map[string]bool{}
			}
			grants[row.RoleID][row.Action] = true
		}
		if len(rows) != 10 {
			t.Fatalf("attempt %d: expected 10 trial-run grant rows, got %d: %v", attempt, len(rows), grants)
		}
		for _, role := range []string{model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator, model.RoleTeamAdmin} {
			if !grants[role]["read"] || !grants[role]["execute"] {
				t.Fatalf("attempt %d: %s must read+execute trial-run, got %v", attempt, role, grants[role])
			}
		}
		for _, role := range []string{model.RoleBusinessUser, model.RoleViewer} {
			if !grants[role]["read"] || grants[role]["execute"] {
				t.Fatalf("attempt %d: %s must stay read-only on trial-run, got %v", attempt, role, grants[role])
			}
		}

		// The whole point: team_admin gained practice runs *without* gaining the
		// evidence-recompute action that shares the old grant.
		var teamEvalExecute int64
		if err := gdb.Model(&model.Permission{}).
			Where("role_id = ? AND action = ? AND resource = ?", model.RoleTeamAdmin, "execute", "eval-set").
			Count(&teamEvalExecute).Error; err != nil {
			t.Fatalf("attempt %d: count eval-set execute: %v", attempt, err)
		}
		if teamEvalExecute != 0 {
			t.Fatalf("attempt %d: team_admin must not hold eval-set execute (doc/129 §1178), got %d", attempt, teamEvalExecute)
		}
	}
}
