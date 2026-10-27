package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV125EvidenceSnapshot(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "evidence-snapshot.db")})
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
	if LatestMigrationVersion() < 125 {
		t.Fatalf("latest migration = %d, want at least 125", LatestMigrationVersion())
	}
	for _, target := range []interface{}{
		&model.EvidenceSnapshot{}, &model.EvalCaseEvidence{}, &model.EvalCaseDependency{},
	} {
		if !gdb.Migrator().HasTable(target) {
			t.Fatalf("evidence migration table missing for %T", target)
		}
	}
	if !gdb.Migrator().HasIndex(&model.EvidenceSnapshot{}, "idx_rgx_evidence_snapshot_lookup") {
		t.Fatal("evidence snapshot lookup index missing")
	}
	if !gdb.Migrator().HasIndex(&model.EvalCaseEvidence{}, "idx_rgx_eval_case_evidence_lookup") {
		t.Fatal("eval case evidence lookup index missing")
	}
	if !gdb.Migrator().HasIndex(&model.EvalCaseDependency{}, "idx_rgx_eval_case_dependency_lookup") {
		t.Fatal("eval case dependency lookup index missing")
	}
}
