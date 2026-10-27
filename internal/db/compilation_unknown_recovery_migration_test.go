package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV140CompilationUnknownRecoveryGovernance(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/compilation-recovery.db"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, column := range []string{
		"downstream_task_id", "triggered_by_x_run_id", "cancel_scope", "failure_code",
		"recovery_state", "recovery_evidence", "approval_id", "unknown_at",
		"recovery_first_no_match_at", "recovery_last_no_match_at", "recovery_no_match_count",
		"recovery_decision_at", "recovery_observation_seconds", "recovery_approved_by", "recovery_operated_by",
	} {
		if !gdb.Migrator().HasColumn(&model.CompilationRun{}, column) {
			t.Fatalf("compilation run column %q missing", column)
		}
	}
	if !gdb.Migrator().HasIndex(&model.CompilationRun{}, "idx_rgx_compilation_recovery_state") {
		t.Fatal("compilation recovery state index missing")
	}
	if err := gdb.Exec(`INSERT INTO rgx_compilation_run
		(id, tenant_id, project_id, dataset_id, profile_id, idempotency_key, state, manifest_hash,
		 template_hash, pipeline_hash, requested_by, detail, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"recovery-run-old", "tenant-old", "project-old", "dataset-old", "",
		"recovery-legacy-key", model.CompilationRunUnknown, "manifest", "template", "pipeline",
		"user-old", "{}").Error; err != nil {
		t.Fatalf("seed legacy run: %v", err)
	}
	var run model.CompilationRun
	if err := gdb.Where("id = ?", "recovery-run-old").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.CancelScope != model.CompilationCancelSchedulingOnly || run.RecoveryEvidence != "{}" ||
		run.RecoveryNoMatchCount != 0 || run.DownstreamTaskID != "" || run.TriggeredByXRunID != "" {
		t.Fatalf("legacy recovery defaults are not fail-closed: %+v", run)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
}
