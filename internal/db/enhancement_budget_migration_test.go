package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestMigrationV142EnhancementCostBudgets(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "enhancement-budget.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"project_id", "x_dataset_id", "x_profile_id", "x_assistant_id", "x_run_id"} {
		if !gdb.Migrator().HasColumn("rgx_cost_metric", column) {
			t.Fatalf("migration 142 missing cost metric attribution column %s", column)
		}
	}
	for _, column := range []string{"budget_snapshot", "quota_reservation_id"} {
		if !gdb.Migrator().HasColumn("rgx_compilation_run", column) {
			t.Fatalf("migration 142 missing compilation run budget column %s", column)
		}
	}
	for _, column := range []string{"project_id", "x_dataset_id", "x_profile_id", "x_assistant_id", "x_run_id"} {
		if !gdb.Migrator().HasColumn("rgx_quota_reservation", column) {
			t.Fatalf("migration 142 missing reservation attribution column %s", column)
		}
	}
	if !gdb.Migrator().HasColumn("rgx_tenant", "compilation_token_budget") {
		t.Fatal("migration 142 missing tenant compilation budget")
	}
	if !gdb.Migrator().HasColumn("rgx_dataset_link", "compilation_token_budget") {
		t.Fatal("migration 142 missing dataset compilation budget")
	}
	for _, index := range []string{
		"idx_rgx_cost_metric_enhancement_scope",
		"idx_rgx_quota_reservation_enhancement_scope",
	} {
		if !gdb.Migrator().HasIndex("rgx_cost_metric", index) &&
			!gdb.Migrator().HasIndex("rgx_quota_reservation", index) {
			t.Fatalf("migration 142 missing index %s", index)
		}
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migration 142 must be idempotent: %v", err)
	}
}
