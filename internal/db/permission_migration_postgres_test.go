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
