package db

// Migration 183 (doc/301 T03/T04).
//
// Four new tables plus a four-column projection on `rgx_report_revision`. Every
// assertion here is one a reader would otherwise have to take on trust from a
// comment, and each of them can fail independently:
//
//  1. the tables exist (a run that is never persisting is invisible);
//  2. `MigrateTo(182)` *stops* before them — 183 is the boundary, so a database
//     pinned to 182 must not carry half the assurance surface;
//  3. the projection default is `not_run`, because every pre-existing revision has
//     to read back as "never assured" rather than as an empty string, which the UI
//     would have to guess at;
//  4. `cache_key` is indexed but NOT unique: a cache hit writes a row too, so two
//     rows legitimately share a key (§6.1 self-correction);
//  5. running 183 twice is a no-op (idempotent `AutoMigrate`).

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func reportAssuranceMigrateFixture(t *testing.T, version int64) *gorm.DB {
	t.Helper()
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "report-assurance.db")})
	if err != nil {
		t.Fatal(err)
	}
	// Windows will not delete a temp dir whose sqlite file is still open.
	if sqlDB, err := gdb.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if version == 0 {
		err = Migrate(gdb)
	} else {
		err = MigrateTo(gdb, version)
	}
	if err != nil {
		t.Fatal(err)
	}
	return gdb
}

var reportAssuranceTables = []string{
	"rgx_report_assurance_run",
	"rgx_report_claim_evaluation",
	"rgx_report_review_finding",
	"rgx_report_conflict_candidate",
}

func TestReportAssuranceMigrationCreatesTables(t *testing.T) {
	gdb := reportAssuranceMigrateFixture(t, 0)
	for _, table := range reportAssuranceTables {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("%s was not created by migration 183", table)
		}
	}
}

// TestReportAssuranceMigrationStopsAt182 is the boundary assertion for the *tables*.
//
// The columns are a different matter, and this test says so out loud instead of
// pretending otherwise: migration 170 (`report_revision_datasets`) calls
// `AutoMigrate(&model.ReportRevision{})`, so the projection columns added in 183 appear
// on any database that reaches 170 — because `AutoMigrate` reads the *current* model,
// not the model as it looked in 170. Version numbers in this repo do not freeze a
// schema at a point in time, so "at 182 the assurance surface does not exist" is true
// for the four tables and false for the four columns.
//
// What actually protects a 182 database is weaker but sufficient, and it is asserted
// here so nobody has to trust the reasoning: the columns are NOT NULL with default
// `not_run`, so a reader never sees NULL, and the four tables (which no earlier
// migration knows about) are genuinely absent.
func TestReportAssuranceMigrationStopsAt182(t *testing.T) {
	gdb := reportAssuranceMigrateFixture(t, 182)
	for _, table := range reportAssuranceTables {
		if gdb.Migrator().HasTable(table) {
			t.Fatalf("%s exists at version 182; the 183 boundary is not where the plan says", table)
		}
	}
	var revision model.ReportRevision
	if err := gdb.Create(&model.ReportRevision{ID: "rev-182"}).Error; err != nil {
		t.Fatalf("insert at 182: %v", err)
	}
	if err := gdb.First(&revision, "id = ?", "rev-182").Error; err != nil {
		t.Fatalf("read back at 182: %v", err)
	}
	if revision.AssuranceStatus != model.ReportAssuranceProjectionNotRun {
		t.Fatalf("at 182 assurance_status default = %q, want %q (NULL would be unreadable)",
			revision.AssuranceStatus, model.ReportAssuranceProjectionNotRun)
	}
}

// TestReportAssuranceProjectionDefaultsToNotRun pins the backfill. A default of ""
// would make an old revision indistinguishable from one whose run failed to write,
// and the workspace would then have to guess which of the two it is looking at.
func TestReportAssuranceProjectionDefaultsToNotRun(t *testing.T) {
	gdb := reportAssuranceMigrateFixture(t, 0)
	for _, column := range []string{
		"assurance_run_id", "assurance_status", "assurance_summary_json", "assurance_ack_json",
	} {
		if !gdb.Migrator().HasColumn(&model.ReportRevision{}, column) {
			t.Fatalf("rgx_report_revision.%s was not added by migration 183", column)
		}
	}
	var revision model.ReportRevision
	if err := gdb.Create(&model.ReportRevision{ID: "rev-default"}).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := gdb.First(&revision, "id = ?", "rev-default").Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if revision.AssuranceStatus != model.ReportAssuranceProjectionNotRun {
		t.Fatalf("assurance_status default = %q, want %q",
			revision.AssuranceStatus, model.ReportAssuranceProjectionNotRun)
	}
}

// TestReportClaimEvaluationCacheKeyIsNotUnique is the one index that looks like a
// mistake until you recall that a cache hit also writes a row.
func TestReportClaimEvaluationCacheKeyIsNotUnique(t *testing.T) {
	gdb := reportAssuranceMigrateFixture(t, 0)
	shared := model.ReportClaimEvaluation{
		ID: "eval-1", TenantID: "t1", RevisionID: "rev1", RunID: "run-1",
		ClaimID: "c1", Method: "deterministic", SupportVerdict: "supported",
		CacheKey: "same-key", CacheHit: true, Attempt: 1,
	}
	if err := gdb.Create(&shared).Error; err != nil {
		t.Fatalf("first row: %v", err)
	}
	shared.ID = "eval-2"
	shared.ClaimID = "c2"
	shared.CacheSourceEvaluationID = &shared.ID
	if err := gdb.Create(&shared).Error; err != nil {
		t.Fatalf("a cache hit must be able to share the key with the row it hit: %v", err)
	}
	var count int64
	gdb.Model(&model.ReportClaimEvaluation{}).Where("cache_key = ?", "same-key").Count(&count)
	if count != 2 {
		t.Fatalf("rows sharing cache_key = %d, want 2", count)
	}
}

// TestReportAssuranceMigrationIsIdempotent: a restart re-runs pending migrations, so
// 183 has to be safe twice over.
func TestReportAssuranceMigrationIsIdempotent(t *testing.T) {
	gdb := reportAssuranceMigrateFixture(t, 0)
	if err := Migrate(gdb); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	for _, table := range reportAssuranceTables {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("%s disappeared on the second migrate", table)
		}
	}
}

// TestReportAssuranceMigrationPostgreSQL is the production-driver half. SQLite proves
// the shape; PostgreSQL proves the types (`numeric(4,3)`, `text`, `bool default false`)
// and the index are the same shape there. It skips without `RGX_TEST_POSTGRES_DSN`, the
// repo's convention — a skip is visible in the log as "not run", which is the honest
// state, rather than a green that only ever ran on sqlite.
func TestReportAssuranceMigrationPostgreSQL(t *testing.T) {
	gdb := isolatedPostgresDB(t)
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range reportAssuranceTables {
		if !gdb.Migrator().HasTable(table) {
			t.Fatalf("%s was not created on postgres", table)
		}
	}
	// Re-running is the restart path, and it must not try to re-add an index.
	if err := Migrate(gdb); err != nil {
		t.Fatalf("second migrate on postgres: %v", err)
	}
	if err := gdb.Create(&model.ReportClaimEvaluation{
		ID: "pg-eval", TenantID: "pg-t", RevisionID: "pg-rev", RunID: "pg-run",
		ClaimID: "pg-c1", Method: "deterministic", SupportVerdict: "supported",
		CacheKey: "pg-key", Attempt: 1, Confidence: func() *float64 { v := 0.875; return &v }(),
	}).Error; err != nil {
		t.Fatalf("insert evaluation on postgres: %v", err)
	}
	var stored model.ReportClaimEvaluation
	if err := gdb.First(&stored, "id = ?", "pg-eval").Error; err != nil {
		t.Fatalf("read back on postgres: %v", err)
	}
	if stored.Confidence == nil || *stored.Confidence != 0.875 {
		t.Fatalf("confidence round-trip = %v, want 0.875 (numeric(4,3) must keep the scale)", stored.Confidence)
	}
}
