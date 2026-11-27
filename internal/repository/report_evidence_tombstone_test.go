package repository

// doc/319 R-01: retention must leave a fact behind, not a hole.
//
// These are the three behaviours that turn the tombstone from a schema change into
// a classification: the purge clears content and keeps the row, a second pass counts
// nothing (or `retention.purge` inflates on every tick), and a re-bind of the same
// chunk revives the row instead of publishing a citation that reads as expired the
// moment it is written.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/model"

	"gorm.io/gorm"
)

func newEvidenceTombstoneStore(t *testing.T) *store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "evidence-tombstone.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return NewStore(gdb).(*store)
}

func tombstoneSnapshot(id, tenantID string, expiresAt time.Time) model.ReportEvidenceSnapshot {
	return model.ReportEvidenceSnapshot{
		ID: id, TenantID: tenantID, ChunkID: "chunk-" + id, ChunkOrd: 1,
		SourceContentHash: "src-" + id, SourceContentSize: 8,
		Content: "证据正文", ContentHash: "hash-" + id, ContentSize: 12,
		CreatedAt: expiresAt.Add(-24 * time.Hour), ExpiresAt: expiresAt,
	}
}

func TestPurgeExpiredReportEvidenceLeavesATombstone(t *testing.T) {
	store := newEvidenceTombstoneStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	due := now.Add(-72 * time.Hour)
	dueRow := tombstoneSnapshot("due", "tenant-tomb", due)
	liveRow := tombstoneSnapshot("live", "tenant-tomb", now.Add(time.Hour))
	for _, row := range []model.ReportEvidenceSnapshot{dueRow, liveRow} {
		if err := store.Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}

	counts, err := store.PurgeExpiredReportEvidence(ctx, now, 50)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if counts["tenant-tomb"] != 1 {
		t.Fatalf("purge counted %d purged snapshots, want 1", counts["tenant-tomb"])
	}

	var tomb model.ReportEvidenceSnapshot
	if err := store.First(&tomb, "id = ?", "due").Error; err != nil {
		t.Fatalf("the purged row must still exist: %v", err)
	}
	if !tomb.Purged() || tomb.PurgedReason != model.ReportEvidencePurgeRetentionExpired {
		t.Fatalf("purge must write a tombstone, got %+v", tomb)
	}
	if tomb.Content != "" || tomb.ContentSize != 0 {
		t.Fatalf("tombstone must not keep the evidence text, got content=%q size=%d", tomb.Content, tomb.ContentSize)
	}
	if !tomb.PurgedAt.Equal(now) {
		t.Fatalf("purged_at = %s, want the actual pass time %s", tomb.PurgedAt, now)
	}
	if !tomb.ExpiresAt.Equal(due) {
		t.Fatalf("expires_at = %s, want the original due time %s: the two facts must not merge",
			tomb.ExpiresAt, due)
	}
	// The identity columns survive, which is what lets `reportAssuranceQuoteOf` say
	// "this snapshot expired" rather than "this citation is unverifiable".
	if tomb.ChunkID != "chunk-due" || tomb.SourceContentHash != "src-due" {
		t.Fatalf("tombstone lost its identity columns: %+v", tomb)
	}

	var stillLive model.ReportEvidenceSnapshot
	if err := store.First(&stillLive, "id = ?", "live").Error; err != nil {
		t.Fatalf("read live snapshot: %v", err)
	}
	if stillLive.Purged() || stillLive.Content != "证据正文" {
		t.Fatalf("a snapshot inside its window must not be touched, got %+v", stillLive)
	}

	// Idempotent: a second tick must not re-count and must not move `purged_at`,
	// otherwise `retention.purge` reports work that did not happen and the tombstone
	// stops being a fixed record of when it did.
	again, err := store.PurgeExpiredReportEvidence(ctx, now, 50)
	if err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if again["tenant-tomb"] != 0 {
		t.Fatalf("second purge counted %d, want 0: the tombstone was counted twice", again["tenant-tomb"])
	}
	if err := store.First(&tomb, "id = ?", "due").Error; err != nil {
		t.Fatalf("re-read tombstone: %v", err)
	}
	if !tomb.PurgedAt.Equal(now) {
		t.Fatalf("purged_at moved on the second pass: %s, want %s", tomb.PurgedAt, now)
	}

	// A later tick still purges what became due in between — and only that: the
	// tombstone stays out of the count, so `retention.purge` measures new work.
	later, err := store.PurgeExpiredReportEvidence(ctx, now.Add(2*time.Hour), 50)
	if err != nil {
		t.Fatalf("later purge: %v", err)
	}
	if later["tenant-tomb"] != 1 {
		t.Fatalf("later purge counted %d, want 1 (the row that became due)", later["tenant-tomb"])
	}
	if err := store.First(&tomb, "id = ?", "due").Error; err != nil {
		t.Fatalf("re-read tombstone: %v", err)
	}
	if !tomb.PurgedAt.Equal(now) {
		t.Fatalf("an older tombstone must keep its own purged_at: %s, want %s", tomb.PurgedAt, now)
	}
}

// TestBindEvidenceRevivesATombstone is the control for the purge above: without the
// revival, the very first citation written after a purge would carry a tombstoned
// snapshot id and be classified `evidence_expired` while its content is present.
func TestBindEvidenceRevivesATombstone(t *testing.T) {
	store := newEvidenceTombstoneStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	row := tombstoneSnapshot("revive", "tenant-revive", now.Add(-time.Hour))
	if err := store.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.PurgeExpiredReportEvidence(ctx, now, 10); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Same (tenant, chunk, source content hash): the dedupe key, i.e. the same frozen
	// content re-observed. `bindReportEvidenceInTx` runs inside the section commit
	// transaction; a bare transaction reproduces exactly what it sees.
	renewed := row
	renewed.Content, renewed.ContentSize = "证据正文", 12
	renewed.ExpiresAt = now.Add(24 * time.Hour)
	var ids map[string]string
	if err := store.Transaction(func(tx *gorm.DB) error {
		var err error
		ids, err = bindReportEvidenceInTx(tx, row.TenantID, []model.ReportEvidenceSnapshot{renewed})
		return err
	}); err != nil {
		t.Fatalf("re-bind: %v", err)
	}
	if ids[reportEvidenceKey(renewed.ChunkID, renewed.SourceContentHash)] != row.ID {
		t.Fatalf("re-bind must return the existing snapshot id %s, got %v", row.ID, ids)
	}

	var revived model.ReportEvidenceSnapshot
	if err := store.First(&revived, "id = ?", row.ID).Error; err != nil {
		t.Fatalf("read revived snapshot: %v", err)
	}
	if revived.Purged() {
		t.Fatalf("re-bound evidence must not stay tombstoned: purged_at=%v reason=%q",
			revived.PurgedAt, revived.PurgedReason)
	}
	if revived.Content != "证据正文" || revived.ContentSize != 12 {
		t.Fatalf("re-bind must restore the content, got %q/%d", revived.Content, revived.ContentSize)
	}
	if !revived.ExpiresAt.Equal(renewed.ExpiresAt) {
		t.Fatalf("re-bind must extend the clock: expires_at=%s, want %s", revived.ExpiresAt, renewed.ExpiresAt)
	}
}

// TestReuseRefusesTombstonedEvidence covers the third consumer: a section reusing an
// earlier revision's evidence must not inherit a snapshot whose content is gone.
//
// It calls the predicate the reuse path calls instead of re-writing its query — a
// copy of the SQL asserts that the SQL was copied, not that the path uses it.
func TestReuseRefusesTombstonedEvidence(t *testing.T) {
	store := newEvidenceTombstoneStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	tombstoned := tombstoneSnapshot("reuse-tomb", "tenant-reuse", now.Add(-time.Hour))
	live := tombstoneSnapshot("reuse-live", "tenant-reuse", now.Add(time.Hour))
	for _, row := range []model.ReportEvidenceSnapshot{tombstoned, live} {
		if err := store.Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	if _, err := store.PurgeExpiredReportEvidence(ctx, now, 10); err != nil {
		t.Fatalf("purge: %v", err)
	}

	citing := func(ids ...string) []model.ReportCitation {
		citations := make([]model.ReportCitation, 0, len(ids))
		for index, id := range ids {
			citations = append(citations, model.ReportCitation{
				ID: "cite-" + id + "-" + string(rune('a'+index)), TenantID: "tenant-reuse",
				EvidenceSnapshotID: id,
			})
		}
		return citations
	}
	if err := store.Transaction(func(tx *gorm.DB) error {
		return assertReportSnapshotsLiveInTx(tx, "tenant-reuse", citing(live.ID))
	}); err != nil {
		t.Fatalf("live evidence must stay inheritable: %v", err)
	}
	if err := store.Transaction(func(tx *gorm.DB) error {
		return assertReportSnapshotsLiveInTx(tx, "tenant-reuse", citing(tombstoned.ID))
	}); !errors.Is(err, errReportReuseEvidenceMissing) {
		t.Fatalf("reuse of a tombstoned snapshot returned %v, want errReportReuseEvidenceMissing: "+
			"the copy would produce a report whose appendix cannot be exported", err)
	}
}
