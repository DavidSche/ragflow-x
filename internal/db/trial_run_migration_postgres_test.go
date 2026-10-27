package db

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestMigrationV157TrialRunPostgreSQLReentry proves the v0.4.0 trial-run
// migration is re-runnable on the production driver too (doc/228 T7.3′, doc/232
// §3). SQLite covers the DDL guards; PostgreSQL covers the real ALTER TABLE
// behaviour and the two unique keys that make runs replay-safe.
func TestMigrationV157TrialRunPostgreSQLReentry(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the trial run migration re-entry contract")
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
		for _, table := range []string{"rgx_trial_run", "rgx_trial_case_result"} {
			if !gdb.Migrator().HasTable(table) {
				t.Fatalf("attempt %d: migration 157 must create %s", attempt, table)
			}
		}
		for _, column := range []string{"scenario_template_id", "scenario_template_version"} {
			if !gdb.Migrator().HasColumn(&model.ChatShadow{}, column) {
				t.Fatalf("attempt %d: migration 157 must add rgx_chat.%s", attempt, column)
			}
		}
	}

	if !gdb.Migrator().HasIndex(&model.TrialRun{}, "uk_trial_run_idempotency") {
		t.Fatal("migration 157 must create uk_trial_run_idempotency")
	}
	if !gdb.Migrator().HasIndex(&model.TrialCaseResult{}, "uk_trial_case_result_case") {
		t.Fatal("migration 157 must create uk_trial_case_result_case")
	}
}
