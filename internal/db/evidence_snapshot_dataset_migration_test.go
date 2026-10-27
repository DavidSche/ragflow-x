package db

import (
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV127EvidenceSnapshotDatasetProjection(t *testing.T) {
	gdb := openMigrationTestDB(t, filepath.Join(t.TempDir(), "evidence-dataset-projection.db"))
	if !gdb.Migrator().HasTable(&model.EvidenceSnapshotDataset{}) {
		t.Fatal("evidence snapshot dataset projection table missing")
	}
	if !gdb.Migrator().HasIndex(&model.EvidenceSnapshotDataset{}, "idx_evidence_snapshot_dataset_unique") {
		t.Fatal("evidence snapshot dataset unique index missing")
	}
	if !gdb.Migrator().HasIndex(&model.EvidenceSnapshotDataset{}, "idx_evidence_snapshot_dataset_lookup") {
		t.Fatal("evidence snapshot dataset lookup index missing")
	}
}
