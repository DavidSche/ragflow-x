package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestMigrationV138PairedEvaluationColumnsAndIndexes(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "evaluation-execution.db")})
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
	for _, column := range []string{"capability", "execution_mode", "execution_config", "gate_status", "gate_reason"} {
		if !gdb.Migrator().HasColumn("rgx_evaluation_run", column) {
			t.Fatalf("migration 138 missing evaluation run column %s", column)
		}
	}
	for _, column := range []string{"paired_sample_id", "experiment_arm", "hit_at_k", "answer_correct", "no_answer", "citation_precision"} {
		if !gdb.Migrator().HasColumn("rgx_evaluation_case_result", column) {
			t.Fatalf("migration 138 missing case result column %s", column)
		}
	}
	if !gdb.Migrator().HasColumn("rgx_evaluation_case_version", "paired_sample_id") {
		t.Fatal("migration 138 missing case version paired_sample_id")
	}
	if !gdb.Migrator().HasIndex("rgx_evaluation_case_result", "idx_evaluation_case_result_run_case") {
		t.Fatal("migration 138 missing paired case result unique index")
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migration 138 must be idempotent: %v", err)
	}
}
