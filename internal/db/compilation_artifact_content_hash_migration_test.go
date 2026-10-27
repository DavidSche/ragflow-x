package db

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV141CompilationArtifactContentHash(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/artifact-content-hash.db"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := MigrateTo(gdb, 136); err != nil {
		t.Fatalf("migrate to projection baseline: %v", err)
	}
	rows := []struct {
		id      string
		state   string
		payload string
	}{
		{
			id: "ready-old", state: model.ArtifactProjectionReady,
			payload: `{"content_hash":"ready-content-hash","content_hash_binding":true}`,
		},
		{
			id: "unverified-old", state: model.ArtifactProjectionUnverified,
			payload: `{"content_hash":"unverified-content-hash","content_hash_binding":false}`,
		},
		{
			id: "corrupt-old", state: model.ArtifactProjectionReady,
			payload: "{not-json",
		},
	}
	for _, row := range rows {
		if err := gdb.Exec(`INSERT INTO rgx_artifact_projection
			(id, tenant_id, dataset_id, run_id, artifact_id, kind, state, artifact_hash, artifact_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
			row.id, "tenant-old", "dataset-old", "run-old", row.id, "page_index", row.state,
			"projection-hash", row.payload).Error; err != nil {
			t.Fatalf("seed legacy artifact %s: %v", row.id, err)
		}
	}
	if err := MigrateTo(gdb, 141); err != nil {
		t.Fatalf("migrate artifact content hash: %v", err)
	}
	var ready model.ArtifactProjection
	if err := gdb.Where("id = ?", "ready-old").First(&ready).Error; err != nil {
		t.Fatal(err)
	}
	if ready.ContentHash != "ready-content-hash" {
		t.Fatalf("verified legacy artifact did not recover content hash: %+v", ready)
	}
	var unverified model.ArtifactProjection
	if err := gdb.Where("id = ?", "unverified-old").First(&unverified).Error; err != nil {
		t.Fatal(err)
	}
	if unverified.ContentHash != "" {
		t.Fatalf("unverified artifact must not claim content hash: %+v", unverified)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
}
