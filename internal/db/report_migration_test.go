package db

import (
	"testing"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestMigrationV168ReportTablesExist pins the eleven new tables, the additive
// export columns and the two properties other tests rely on: the frozen-TTL
// columns are NOT NULL (a row without a clock would be immortal), and the
// partial unique index on the idempotency key really rejects a replay.
func TestMigrationV168ReportTablesExist(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/report-v168.db"})
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

	tables := []string{
		"rgx_report_template", "rgx_report", "rgx_report_revision",
		"rgx_report_job", "rgx_report_job_attempt", "rgx_report_section",
		"rgx_report_claim", "rgx_report_citation", "rgx_report_charge",
		"rgx_report_evidence_snapshot", "rgx_report_job_event",
	}
	for _, table := range tables {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("table %s missing after v168", table)
		}
	}

	// Export extension: existing answer exports must keep working, so every new
	// column is defaulted rather than required at insert time.
	exportColumns := map[string][]string{
		"rgx_export_snapshot": {"subject_type", "report_revision_id"},
		"rgx_export_job":      {"subject_type", "report_revision_id"},
		"rgx_export_artifact": {
			"subject_type", "report_revision_id", "artifact_version",
			"artifact_status", "created_by", "deleted_by", "delete_reason", "deleted_at",
		},
		"rgx_export_download_audit": {"report_revision_id"},
	}
	for table, columns := range exportColumns {
		for _, column := range columns {
			if !gdb.Migrator().HasColumn(table, column) {
				t.Fatalf("%s.%s missing after v168", table, column)
			}
		}
	}

	// v175: the outline-time evidence preview must exist on the section row, or the
	// confirmation screen silently loses the evidence盘面 on every upgraded
	// database (the same defect migration 174 repaired for the download audit).
	if !gdb.Migrator().HasColumn("rgx_report_section", "evidence_preview_json") {
		t.Fatal("rgx_report_section.evidence_preview_json missing after v175")
	}

	// The frozen clocks: a row that could be inserted without expires_at would
	// never be purged and would silently violate the TTL contract.
	for _, target := range []struct{ table, column string }{
		{"rgx_report", "expires_at"},
		{"rgx_report_evidence_snapshot", "expires_at"},
		{"rgx_report_job_event", "expires_at"},
	} {
		if nullable, ok := columnNullable(t, gdb, target.table, target.column); ok && nullable {
			t.Fatalf("%s.%s must be NOT NULL", target.table, target.column)
		}
	}

	// Partial unique idempotency index: two jobs, same tenant, same client key.
	seed := `INSERT INTO rgx_report_job (id, tenant_id, report_id, revision_id, job_kind, status,
		attempt_seq, slice_seq, event_seq, progress_done, progress_total,
		reserved_tokens, actual_tokens, charged_tokens, released_tokens, overage_tokens,
		pending_reconcile_tokens, idempotency_key, updated_at)
		VALUES (?, 't1', 'r1', 'rev1', 'generate', 'queued', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, ?, datetime('now'))`
	if err := gdb.Exec(seed, "job-a", "client-key-1").Error; err != nil {
		t.Fatalf("seed first job: %v", err)
	}
	if err := gdb.Exec(seed, "job-b", "client-key-1").Error; err == nil {
		t.Fatal("duplicate (tenant_id, idempotency_key) was accepted, want unique violation")
	}
	// Rows without a key stay legal (internal retriggers never send one).
	if err := gdb.Exec(seed, "job-c", "").Error; err != nil {
		t.Fatalf("keyless job rejected: %v", err)
	}
	if err := gdb.Exec(seed, "job-d", "").Error; err != nil {
		t.Fatalf("second keyless job rejected (partial index must exclude ''): %v", err)
	}

	// Charge ledger: the six-segment key is the anti-double-charge guarantee.
	charge := `INSERT INTO rgx_report_charge (id, tenant_id, report_job_id, attempt_seq, slice_seq,
		section_id, section_attempt, call_seq, ledger_key, state, reserved_tokens,
		actual_tokens, charged_tokens, released_tokens, created_at)
		VALUES (?, 't1', 'job-a', 1, 1, 'sec-1', 1, 1, ?, 'reserved', 100, 0, 0, 0, datetime('now'))`
	key := "report:job-a:1:1:sec-1:1:1"
	if err := gdb.Exec(charge, "ch-1", key).Error; err != nil {
		t.Fatalf("seed charge: %v", err)
	}
	if err := gdb.Exec(charge, "ch-2", key).Error; err == nil {
		t.Fatal("duplicate ledger_key was accepted, want unique violation")
	}

	// Closed-set defaults: an answer export row written by the old code path
	// must materialize as subject_type=answer / artifact_status=ready.
	if err := gdb.Exec(`INSERT INTO rgx_export_artifact
		(id, tenant_id, project_id, export_job_id, format, renderer_version, file_ref, filename,
		 mime_type, byte_size, sha256, created_at, expires_at)
		VALUES ('art-1', 't1', 'p1', 'ej-1', 'md', 'export.renderer.v1', 'ref', 'a.md',
		 'text/markdown', 10, 'sha', datetime('now'), datetime('now', '+1 day'))`).Error; err != nil {
		t.Fatalf("legacy answer artifact insert: %v", err)
	}
	var artifact model.ExportArtifact
	if err := gdb.Where("id = ?", "art-1").First(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	if artifact.SubjectType != model.ExportSubjectAnswer || artifact.ArtifactStatus != model.ExportArtifactReady {
		t.Fatalf("legacy artifact defaults = %s/%s, want answer/ready",
			artifact.SubjectType, artifact.ArtifactStatus)
	}
	if artifact.ArtifactVersion != 1 {
		t.Fatalf("legacy artifact_version = %d, want 1", artifact.ArtifactVersion)
	}

	// The doc-named unique indexes must exist as created (doc/288 §7.8), because
	// a later migration reading index names would otherwise miss them.
	for _, index := range []string{
		"uidx_rgx_report_revision",
		"uidx_rgx_report_job_attempt_slice",
		"uidx_rgx_report_job_attempt_runtime",
		"uidx_rgx_report_section_version",
		"uidx_rgx_report_citation_section_ord",
		"uidx_rgx_report_claim_section_ord",
		"uidx_rgx_report_charge_ledger",
		"uidx_rgx_report_evidence_dedup",
		"uidx_rgx_report_job_event_seq",
		"uidx_rgx_report_job_idempotency",
		"uidx_rgx_export_artifact_version",
	} {
		var name string
		if err := gdb.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", index).
			Scan(&name).Error; err != nil {
			t.Fatalf("probe index %s: %v", index, err)
		}
		if name != index {
			t.Fatalf("unique index %s missing", index)
		}
	}

	// Artifact version identity is per report revision, and the index must stay
	// PARTIAL: two answer exports in the same format (empty report_revision_id)
	// have to coexist, otherwise the answer-export path breaks on the second run.
	insertArtifact := `INSERT INTO rgx_export_artifact (id, tenant_id, project_id, export_job_id,
		subject_type, report_revision_id, artifact_version, artifact_status, format,
		renderer_version, file_ref, filename, mime_type, byte_size, sha256, created_at, expires_at)
		VALUES (?, 't1', 'p1', ?, ?, ?, ?, 'ready', ?, 'export.renderer.v1', '', 'a.docx',
		'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 10, 'sha',
		datetime('now'), datetime('now', '+30 day'))`
	// Same format as the legacy row above: with a non-partial index this insert
	// would collide on ('' , 'md', 1).
	if err := gdb.Exec(insertArtifact, "art-ans-2", "ej-2", "answer", "", 1, "md").Error; err != nil {
		t.Fatalf("second answer artifact rejected by a non-partial index: %v", err)
	}
	if err := gdb.Exec(insertArtifact, "art-rep-1", "ej-3", "report", "rev-1", 1, "docx").Error; err != nil {
		t.Fatalf("seed report artifact: %v", err)
	}
	if err := gdb.Exec(insertArtifact, "art-rep-2", "ej-4", "report", "rev-1", 1, "docx").Error; err == nil {
		t.Fatal("duplicate (report_revision_id, format, artifact_version) accepted")
	}
}

// TestMigrationV174AddsTheReportRevisionColumnToAnExistingAuditTable replays
// the upgrade path a real installation takes, which a fresh-database test can
// never see: the database already recorded v168 *before* the audit model grew
// its `report_revision_id` field, so AutoMigrate for v168 never ran again and
// the column was missing on every upgraded box. The first live report download
// then failed its audit insert (SQLSTATE 42703) and answered 500 — the audit
// table is immutable, so "serve the bytes anyway" was not an option.
func TestMigrationV174AddsTheReportRevisionColumnToAnExistingAuditTable(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: t.TempDir() + "/report-v174.db"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := MigrateTo(gdb, 173); err != nil {
		t.Fatalf("migrate to 173: %v", err)
	}

	// Rewind the audit table to the pre-v174 shape: no report_revision_id, plus
	// one immutable row written by an answer download before the upgrade.
	if err := gdb.Exec("DROP TABLE rgx_export_download_audit").Error; err != nil {
		t.Fatalf("drop audit table: %v", err)
	}
	if err := gdb.Exec(`CREATE TABLE rgx_export_download_audit (
		id VARCHAR(32) PRIMARY KEY,
		tenant_id VARCHAR(32) NOT NULL,
		project_id VARCHAR(32),
		export_artifact_id VARCHAR(32) NOT NULL,
		export_job_id VARCHAR(32) NOT NULL,
		answer_snapshot_id VARCHAR(32) NOT NULL,
		downloaded_by VARCHAR(32) NOT NULL,
		downloaded_at DATETIME NOT NULL,
		download_count BIGINT NOT NULL DEFAULT 1,
		decision_hash VARCHAR(64) NOT NULL,
		trace_id VARCHAR(64))`).Error; err != nil {
		t.Fatalf("create pre-v174 audit table: %v", err)
	}
	if err := gdb.Exec(`INSERT INTO rgx_export_download_audit
		(id, tenant_id, project_id, export_artifact_id, export_job_id, answer_snapshot_id,
		 downloaded_by, downloaded_at, download_count, decision_hash, trace_id)
		VALUES ('legacy-audit', 't1', '', 'art-1', 'ej-1', 'snap-1', 'u1',
		        '2026-11-01 00:00:00', 1, 'hash-1', 'trace-1')`).Error; err != nil {
		t.Fatalf("seed legacy audit row: %v", err)
	}

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !gdb.Migrator().HasColumn("rgx_export_download_audit", "report_revision_id") {
		t.Fatal("rgx_export_download_audit.report_revision_id missing after the upgrade")
	}

	// Additive, not destructive: the row written before the upgrade is intact.
	var legacy int64
	if err := gdb.Table("rgx_export_download_audit").Where("id = ?", "legacy-audit").Count(&legacy).Error; err != nil {
		t.Fatalf("count legacy row: %v", err)
	}
	if legacy != 1 {
		t.Fatalf("the upgrade must not drop existing audit rows: found %d", legacy)
	}

	// And the report download that used to 500 can now be audited.
	if err := gdb.Exec(`INSERT INTO rgx_export_download_audit
		(id, tenant_id, project_id, export_artifact_id, export_job_id, answer_snapshot_id,
		 report_revision_id, downloaded_by, downloaded_at, download_count, decision_hash, trace_id)
		VALUES ('report-audit', 't1', '', 'art-2', 'ej-2', '', 'rev-1', 'u1',
		        '2026-11-18 00:00:00', 1, 'hash-2', 'trace-2')`).Error; err != nil {
		t.Fatalf("report download audit insert: %v", err)
	}
}

// columnNullable reports whether a column is nullable, and whether the probe
// could answer at all. It uses the migrator so the same test serves SQLite and
// PostgreSQL; an unknown dialect simply skips the assertion instead of guessing.
func columnNullable(t *testing.T, gdb *gorm.DB, table, column string) (bool, bool) {
	t.Helper()
	types, err := gdb.Migrator().ColumnTypes(table)
	if err != nil {
		return false, false
	}
	for _, ct := range types {
		if ct.Name() != column {
			continue
		}
		nullable, ok := ct.Nullable()
		return nullable, ok
	}
	t.Fatalf("%s.%s not found by the migrator", table, column)
	return false, false
}
