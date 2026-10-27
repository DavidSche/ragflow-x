package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

func openMigrationTestDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return gdb
}

func TestMigrationV126EvalCaseEvidenceHasOneCurrentSet(t *testing.T) {
	gdb := openMigrationTestDB(t, filepath.Join(t.TempDir(), "evidence-current.db"))
	if !gdb.Migrator().HasTable(&model.EvalCaseEvidence{}) {
		t.Fatal("eval case evidence table missing")
	}
	if !gdb.Migrator().HasIndex(&model.EvalCaseEvidence{}, "idx_rgx_eval_case_evidence_case_unique") {
		t.Fatal("one current evidence set unique index missing")
	}

	tenantID, caseID, snapshotID := id.New(), id.New(), id.New()
	now := time.Now().UTC()
	if err := gdb.Create(&model.EvidenceSnapshot{
		ID: snapshotID, TenantID: tenantID, SnapshotHash: "hash", DatasetIDs: "[]",
		DocumentVersions: "[]", RetrievalPolicyVersion: "v1", PromptVersion: "v1",
		ModelVersion: "v1", ParserPolicyVersion: "v1", ToolPolicyVersion: "v1",
		AuthorizationPolicyVersion: "v1", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.EvalCaseEvidence{
		ID: id.New(), TenantID: tenantID, EvalCaseID: caseID,
		EvidenceSnapshotID: snapshotID, DependencySet: "[]",
		StaleStatus: model.EvidenceStatusFresh, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.EvalCaseEvidence{
		ID: id.New(), TenantID: tenantID, EvalCaseID: caseID,
		EvidenceSnapshotID: snapshotID, DependencySet: "[]",
		StaleStatus: model.EvidenceStatusFresh, CreatedAt: now,
	}).Error; err == nil {
		t.Fatal("duplicate current evidence set must be rejected")
	}
}
