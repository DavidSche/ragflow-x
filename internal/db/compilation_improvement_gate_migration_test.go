package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestMigrationV143CompilationImprovementGateReadiness(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "compilation-improvement-gate.db")})
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
	for _, column := range []string{"strategy_id", "readiness_snapshot"} {
		if !gdb.Migrator().HasColumn("rgx_compilation_run", column) {
			t.Fatalf("migration 143 missing compilation run %s column", column)
		}
	}
	if !gdb.Migrator().HasIndex("rgx_compilation_run", "idx_rgx_compilation_run_strategy") {
		t.Fatal("migration 143 missing strategy scoping index")
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migration 143 must be idempotent: %v", err)
	}
}
