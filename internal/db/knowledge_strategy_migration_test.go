package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV123KnowledgeStrategy(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "knowledge-strategy.db")})
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
	if LatestMigrationVersion() < 123 {
		t.Fatalf("latest migration = %d, want at least 123", LatestMigrationVersion())
	}
	if !gdb.Migrator().HasTable(&model.KnowledgeStrategy{}) {
		t.Fatal("knowledge strategy table missing")
	}
	if !gdb.Migrator().HasIndex(&model.KnowledgeStrategy{}, "idx_rgx_knowledge_strategy_lookup") {
		t.Fatal("knowledge strategy lookup index missing")
	}
}

func TestMigrationV124KnowledgeCapabilityProbe(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "knowledge-probe.db")})
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
	if LatestMigrationVersion() < 124 {
		t.Fatalf("latest migration = %d, want at least 124", LatestMigrationVersion())
	}
	if !gdb.Migrator().HasTable(&model.KnowledgeCapabilityProbe{}) {
		t.Fatal("knowledge capability probe table missing")
	}
	if !gdb.Migrator().HasIndex(&model.KnowledgeCapabilityProbe{}, "idx_rgx_knowledge_capability_probe_lookup") {
		t.Fatal("knowledge capability probe lookup index missing")
	}
}
