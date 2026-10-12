package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV139CompilationSourceConsistency(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/compilation-source-consistency.db"})
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
	for _, column := range []string{"downstream_source_snapshot_id", "post_run_source_manifest_hash", "source_consistency"} {
		if !gdb.Migrator().HasColumn(&model.CompilationRun{}, column) {
			t.Fatalf("compilation run column %q missing", column)
		}
	}
	if err := gdb.Exec(`INSERT INTO rgx_compilation_run
		(id, tenant_id, project_id, dataset_id, profile_id, idempotency_key, state, manifest_hash,
		 template_hash, pipeline_hash, requested_by, detail, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"source-run-old", "tenant-old", "project-old", "dataset-old", "",
		"source-legacy-key", model.CompilationRunConfirmed, "manifest", "template", "pipeline",
		"user-old", "{}").Error; err != nil {
		t.Fatalf("seed legacy run: %v", err)
	}
	var run model.CompilationRun
	if err := gdb.Where("id = ?", "source-run-old").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.SourceConsistency != model.CompilationSourceConsistencyUnverified ||
		run.DownstreamSourceSnapshotID != "" || run.PostRunSourceManifestHash != "" {
		t.Fatalf("legacy source consistency must remain unverified: %+v", run)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
}
