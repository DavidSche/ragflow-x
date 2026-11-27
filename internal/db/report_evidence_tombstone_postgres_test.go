package db

// The tombstone columns and the two new quote states have to be proven on the driver
// production runs, not only on SQLite (doc/319 §9.3).
//
// Two facts are PostgreSQL-only:
//
//   - `varchar(16)` is enforced. SQLite stores `text` regardless of `size:`, so
//     `evidence_missing` fitting or not fitting is invisible to the unit suite.
//   - `excluded.<column>` (the re-bind upsert) type-checks against the real column.
//
// Skips without RGX_TEST_POSTGRES_DSN, like every other contract in this package.

import (
	"strconv"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMigrationV194EvidenceTombstonePostgreSQL(t *testing.T) {
	gdb := isolatedPostgresDB(t)
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, column := range []string{"purged_at", "purged_reason"} {
		if !gdb.Migrator().HasColumn(&model.ReportEvidenceSnapshot{}, column) {
			t.Fatalf("migration 194 must add rgx_report_evidence_snapshot.%s", column)
		}
	}
	if !gdb.Migrator().HasIndex(&model.ReportEvidenceSnapshot{}, "idx_report_evidence_purged") {
		t.Fatal("migration 194 must create idx_report_evidence_purged")
	}

	// The declared width of every enum column these new values land in. Reading it
	// from the catalogue (instead of hard-coding 16) means a later `size:` change is
	// caught here rather than by a failed run in production.
	declared := func(table, column string) int {
		t.Helper()
		var width *int
		row := gdb.Raw(`SELECT character_maximum_length FROM information_schema.columns
			WHERE table_name = ? AND column_name = ?`, table, column).Row()
		if err := row.Scan(&width); err != nil {
			t.Fatalf("read %s.%s width: %v", table, column, err)
		}
		if width == nil {
			t.Fatalf("%s.%s is unbounded: an enum column without a width accepts any typo", table, column)
		}
		return *width
	}
	quoteWidth := declared("rgx_report_claim_evaluation", "quote_status")
	if quoteWidth != 16 {
		t.Fatalf("quote_status is varchar(%d), want 16: the lifecycle answers are sized for 16", quoteWidth)
	}
	insufficientWidth := declared("rgx_report_claim_evaluation", "insufficient_reason")

	// Direction 1: every value in the closed set can be written. Direction 2: one byte
	// over is refused by this driver — which is what makes direction 1 meaningful.
	//
	// The write must be a real INSERT, not `SELECT (?::text)::varchar(16)`. PostgreSQL
	// *truncates* an explicit cast to varchar(n) and only raises when a value is
	// assigned to a typed column; a width check written as a cast therefore reports
	// that 17 bytes fit (observed here before this comment existed). Both casts are
	// silent, so the column — not a cast — is the only honest oracle.
	// The table has no foreign keys, so a synthetic evaluation row needs nothing but
	// its own required columns (checked against information_schema, above).
	// `uidx_rgx_report_claim_eval` is (run_id, claim_id), so the claim id has to vary
	// with the id or the second insert collides before it can prove anything.
	insertEvaluation := func(id, status string) error {
		return gdb.Exec(`INSERT INTO rgx_report_claim_evaluation
			(id, tenant_id, run_id, revision_id, section_ord, claim_id, method, support_verdict,
			 quote_status, tokens_in, tokens_out, cache_hit, attempt, created_at)
			VALUES (?, 'tenant-pg-194', 'run-pg-194', 'rev-pg-194', 1, ?,
			 'deterministic', 'supported', ?, 0, 0, false, 1, ?)`,
			id, "claim-"+id, status, time.Now().UTC()).Error
	}
	defer gdb.Exec(`DELETE FROM rgx_report_claim_evaluation WHERE tenant_id = 'tenant-pg-194'`)
	for index, status := range model.ReportQuoteStatuses() {
		if len(status) > quoteWidth {
			t.Fatalf("quote_status %q is %d bytes, column is %d", status, len(status), quoteWidth)
		}
		id := "eval-pg-194-" + strconv.Itoa(index)
		if err := insertEvaluation(id, status); err != nil {
			t.Fatalf("quote_status %q does not fit varchar(%d): %v", status, quoteWidth, err)
		}
		var stored string
		if err := gdb.Raw(`SELECT quote_status FROM rgx_report_claim_evaluation WHERE id = ?`, id).
			Row().Scan(&stored); err != nil {
			t.Fatalf("read back %q: %v", status, err)
		}
		if stored != status {
			t.Fatalf("quote_status %q was stored as %q: the driver truncated it silently", status, stored)
		}
	}
	for _, reason := range model.ReportInsufficientReasons() {
		if len(reason) > insufficientWidth {
			t.Fatalf("insufficient_reason %q is %d bytes, column is %d", reason, len(reason), insufficientWidth)
		}
	}
	oversize := "evidence_missing_" // 17 bytes: one past the limit
	if len(oversize) != quoteWidth+1 {
		t.Fatalf("fixture is %d bytes, want %d", len(oversize), quoteWidth+1)
	}
	if err := insertEvaluation("eval-pg-194-oversize", oversize); err == nil {
		t.Fatalf("PostgreSQL accepted %d bytes in varchar(%d): the width test proves nothing",
			len(oversize), quoteWidth)
	}

	// The tombstone write the store performs, on the real driver.
	due := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cleared := due.Add(72 * time.Hour)
	row := model.ReportEvidenceSnapshot{
		ID: "snap-pg-194", TenantID: "tenant-pg-194", ChunkID: "chunk-pg-194",
		Content: "证据原文", ContentHash: "hash-pg-194", ContentSize: 12,
		SourceContentHash: "src-pg-194", CreatedAt: due.Add(-time.Hour), ExpiresAt: due,
	}
	if err := gdb.Create(&row).Error; err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	if err := gdb.Model(&model.ReportEvidenceSnapshot{}).
		Where("tenant_id = ? AND id IN ? AND purged_at IS NULL", row.TenantID, []string{row.ID}).
		Updates(map[string]interface{}{
			"content": "", "content_size": int64(0),
			"purged_at": cleared, "purged_reason": model.ReportEvidencePurgeRetentionExpired,
		}).Error; err != nil {
		t.Fatalf("tombstone snapshot: %v", err)
	}
	var tomb model.ReportEvidenceSnapshot
	if err := gdb.First(&tomb, "id = ?", row.ID).Error; err != nil {
		t.Fatalf("tombstone row must survive the purge: %v", err)
	}
	if !tomb.Purged() || tomb.Content != "" || tomb.ContentSize != 0 {
		t.Fatalf("tombstone must clear content and set purged_at, got %+v", tomb)
	}
	if !tomb.ExpiresAt.Equal(due) {
		t.Fatalf("expires_at = %s, want the original due time %s (the two facts must not merge)",
			tomb.ExpiresAt, due)
	}

	// The re-bind upsert clears the tombstone by name, which only type-checks against
	// the real columns (`excluded.purged_at` is `timestamptz` here, not text).
	if err := gdb.Exec(`INSERT INTO rgx_report_evidence_snapshot
			(id, tenant_id, chunk_id, source_content_hash, content, content_hash, content_size,
			 truncation_strategy, created_at, expires_at, purged_at, purged_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, '')
		ON CONFLICT (tenant_id, chunk_id, source_content_hash) DO UPDATE SET
			content = excluded.content, content_size = excluded.content_size,
			expires_at = excluded.expires_at,
			purged_at = excluded.purged_at, purged_reason = excluded.purged_reason`,
		row.ID, row.TenantID, row.ChunkID, row.SourceContentHash, "证据原文", row.ContentHash,
		int64(12), "", due.Add(-time.Hour), due.Add(time.Hour)).Error; err != nil {
		t.Fatalf("re-bind upsert must revive a tombstone: %v", err)
	}
	// Read the fact from SQL rather than the struct: `purged_at IS NULL` is the
	// question the classification asks, and it is answerable without trusting an ORM
	// scan of a nullable timestamp.
	var revived struct {
		Content     string
		PurgedIsNil bool
	}
	if err := gdb.Raw(`SELECT content, purged_at IS NULL FROM rgx_report_evidence_snapshot
		WHERE id = ?`, row.ID).Row().Scan(&revived.Content, &revived.PurgedIsNil); err != nil {
		t.Fatalf("read revived snapshot: %v", err)
	}
	if !revived.PurgedIsNil || revived.Content != "证据原文" {
		t.Fatalf("re-binding live content must clear the tombstone, got content=%q purged_is_null=%v",
			revived.Content, revived.PurgedIsNil)
	}
}
