package db

// doc/319 R-01 / 方案 B: migration 194 turns evidence retention from a hard delete
// into a tombstone.
//
// What the migration has to survive is not the happy path but the two orders a
// rolling deploy produces: a new binary on an old schema (column missing → the purge
// fails, nothing is lost) and an old binary on a new schema (column present and
// unused → the old hard delete still works, and its rows simply have no tombstone).
// The second order is why the columns are nullable with no default and nothing is
// backfilled: an old binary must never be forced to invent a purge fact.

import (
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV194EvidenceTombstone(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: migratedTestDSN(t, "evidence-tombstone.db")})
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
	if LatestMigrationVersion() < 194 {
		t.Fatalf("latest migration = %d, want at least 194", LatestMigrationVersion())
	}
	for _, column := range []string{"purged_at", "purged_reason"} {
		if !gdb.Migrator().HasColumn(&model.ReportEvidenceSnapshot{}, column) {
			t.Fatalf("migration 194 must add rgx_report_evidence_snapshot.%s", column)
		}
	}
	if !gdb.Migrator().HasIndex(&model.ReportEvidenceSnapshot{}, "idx_report_evidence_purged") {
		t.Fatal("migration 194 must create idx_report_evidence_purged")
	}

	// A snapshot written by an older release: no purge fact, must read as live.
	live := model.ReportEvidenceSnapshot{
		ID: "snap-live", TenantID: "tenant-194", ChunkID: "chunk-194", Content: "正文",
		ContentHash: "hash-194", ContentSize: 6, SourceContentHash: "src-194",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := gdb.Create(&live).Error; err != nil {
		t.Fatalf("seed live snapshot: %v", err)
	}
	var read model.ReportEvidenceSnapshot
	if err := gdb.First(&read, "id = ?", "snap-live").Error; err != nil {
		t.Fatalf("read live snapshot: %v", err)
	}
	if read.PurgedAt != nil || read.PurgedReason != "" || read.Purged() {
		t.Fatalf("a snapshot that was never purged must read as live, got %+v", read)
	}

	// Re-running is a no-op, and it must not touch existing rows: an idempotent
	// migration that rewrites data on the second pass would make the tombstone's
	// own timestamps unreliable.
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate re-entry: %v", err)
	}
	if err := gdb.First(&read, "id = ?", "snap-live").Error; err != nil {
		t.Fatalf("read live snapshot after re-entry: %v", err)
	}
	if read.Purged() || read.Content != "正文" {
		t.Fatalf("migration 194 must not backfill or rewrite rows, got %+v", read)
	}
}

// TestMigrationV194TombstoneKeepsTheTwoFactsApart is the invariant the whole option
// rests on: `expires_at` is when the snapshot became due, `purged_at` is when the
// content was actually cleared. If only one of them were stored, editing
// `retention.report_evidence_days` would rewrite history (doc/319 §8 invariant 1).
func TestMigrationV194TombstoneKeepsTheTwoFactsApart(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: migratedTestDSN(t, "evidence-tombstone-facts.db")})
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
	due := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cleared := due.Add(72 * time.Hour) // the janitor ran three days late
	tomb := model.ReportEvidenceSnapshot{
		ID: "snap-tomb", TenantID: "tenant-194b", ChunkID: "chunk-194b", Content: "",
		ContentHash: "hash-194b", ContentSize: 0, SourceContentHash: "src-194b",
		CreatedAt: due.Add(-time.Hour), ExpiresAt: due,
		PurgedAt: &cleared, PurgedReason: model.ReportEvidencePurgeRetentionExpired,
	}
	if err := gdb.Create(&tomb).Error; err != nil {
		t.Fatalf("seed tombstone: %v", err)
	}
	var read model.ReportEvidenceSnapshot
	if err := gdb.First(&read, "id = ?", "snap-tomb").Error; err != nil {
		t.Fatalf("read tombstone: %v", err)
	}
	if !read.Purged() {
		t.Fatal("a tombstone must report Purged() = true")
	}
	if !read.ExpiresAt.Equal(due) {
		t.Fatalf("expires_at = %s, want the original due time %s", read.ExpiresAt, due)
	}
	if read.PurgedAt == nil || !read.PurgedAt.Equal(cleared) {
		t.Fatalf("purged_at = %v, want the actual clearing time %s", read.PurgedAt, cleared)
	}
	if read.Content != "" || read.ContentSize != 0 {
		t.Fatalf("a tombstone must not retain evidence text, got content=%q size=%d",
			read.Content, read.ContentSize)
	}
}
