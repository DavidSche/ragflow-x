package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV136CompilationDispatchStateContract(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/compilation-dispatch.db"})
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
	for _, column := range []string{"kind", "document_ids", "manifest_complete"} {
		if !gdb.Migrator().HasColumn(&model.CompilationRun{}, column) {
			t.Fatalf("compilation run column %q missing", column)
		}
	}
	if err := gdb.Exec(`INSERT INTO rgx_compilation_run
		(id, tenant_id, project_id, dataset_id, profile_id, idempotency_key, state, manifest_hash,
		 template_hash, pipeline_hash, requested_by, detail, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"run-old", "tenant-old", "project-old", "dataset-old", "",
		"legacy-key", model.CompilationRunAccepted, "manifest", "template", "pipeline",
		"user-old", "{}").Error; err != nil {
		t.Fatalf("seed legacy run: %v", err)
	}
	var run model.CompilationRun
	if err := gdb.Where("id = ?", "run-old").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Kind != model.CompilationKindWiki || run.DocumentIDs != "[]" || run.ManifestComplete {
		t.Fatalf("legacy run defaults are invalid: %+v", run)
	}
	var count int64
	if err := gdb.Model(&model.Permission{}).
		Where("resource = ? AND action = ?", "compilation", "execute").
		Where("role_id IN ?", []string{model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleTeamAdmin, model.RoleOperator}).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("expected explicit compilation execute grants for governance roles, got %d", count)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
}
