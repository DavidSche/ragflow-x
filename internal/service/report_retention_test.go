package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report retention contract (doc/288 §7.6 / T20). These tests run the real
// janitor against a real migrated schema; the invariants they pin are the ones
// that turn "90/90/30/7/7" from a config comment into behaviour:
//
//   - the row clock decides, never the live policy;
//   - a soft delete buys 7 days of recovery, not 7 extra days of TTL;
//   - the four classes expire independently;
//   - one report purge takes the whole aggregate with it.

type reportSeed struct {
	tenantID   string
	reportID   string
	revisionID string
	jobID      string
	sectionID  string
}

// seedReport writes one full report aggregate: report + revision + section +
// claim + citation + job + attempt + charge + event + evidence snapshot. Every
// row carries its own clock so the test can age classes independently.
func seedReport(t *testing.T, gdb *gorm.DB, seed reportSeed, reportExpires, evidenceExpires, eventExpires time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		t.Helper()
		if err := gdb.WithContext(ctx).Exec(query, args...).Error; err != nil {
			t.Fatalf("seed %q: %v", seed.reportID, err)
		}
	}

	exec(`INSERT INTO rgx_report (id, tenant_id, title, question, owner_user_id, template_key,
		access_policy, status, expires_at, created_at, updated_at)
		VALUES (?, ?, '季度合规报告', '本季度合规要点', 'u1', 'report-compliance-v1',
		'owner_only', 'active', ?, ?, ?)`,
		seed.reportID, seed.tenantID, reportExpires, now, now)
	exec(`INSERT INTO rgx_report_revision (id, report_id, tenant_id, revision, template_key,
		outline_confirmation, status, completeness, created_by, created_at)
		VALUES (?, ?, ?, 1, 'report-compliance-v1', 'required', 'published', 'full', 'u1', ?)`,
		seed.revisionID, seed.reportID, seed.tenantID, now)
	exec(`INSERT INTO rgx_report_section (id, tenant_id, revision_id, ord, section_key,
		section_version, title, requires_evidence, status)
		VALUES (?, ?, ?, 1, 'summary', 1, '摘要', 1, 'succeeded')`,
		seed.sectionID, seed.tenantID, seed.revisionID)
	exec(`INSERT INTO rgx_report_claim (id, tenant_id, revision_id, section_id, section_ord,
		section_version, ord, claim_text, support_status)
		VALUES (?, ?, ?, ?, 1, 1, 1, '合规无重大缺陷', 'supported')`,
		seed.sectionID+"-claim", seed.tenantID, seed.revisionID, seed.sectionID)
	exec(`INSERT INTO rgx_report_citation (id, tenant_id, revision_id, section_id, section_ord,
		section_version, ord, evidence_snapshot_id, referenced_at, used_in_text)
		VALUES (?, ?, ?, ?, 1, 1, 1, ?, ?, 1)`,
		seed.sectionID+"-cite", seed.tenantID, seed.revisionID, seed.sectionID,
		seed.reportID+"-ev", now)
	exec(`INSERT INTO rgx_report_job (id, tenant_id, report_id, revision_id, job_kind, status,
		attempt_seq, slice_seq, event_seq, progress_done, progress_total, reserved_tokens,
		actual_tokens, charged_tokens, released_tokens, overage_tokens, pending_reconcile_tokens,
		idempotency_key, updated_at)
		VALUES (?, ?, ?, ?, 'generate', 'succeeded', 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, '', ?)`,
		seed.jobID, seed.tenantID, seed.reportID, seed.revisionID, now)
	exec(`INSERT INTO rgx_report_job_attempt (id, tenant_id, report_job_id, attempt_seq,
		slice_seq, runtime_job_id, status)
		VALUES (?, ?, ?, 1, 1, ?, 'succeeded')`,
		seed.jobID+"-att", seed.tenantID, seed.jobID, seed.jobID+"-rt")
	exec(`INSERT INTO rgx_report_charge (id, tenant_id, report_job_id, attempt_seq, slice_seq,
		section_id, section_attempt, call_seq, ledger_key, state, reserved_tokens,
		actual_tokens, charged_tokens, released_tokens, created_at)
		VALUES (?, ?, ?, 1, 1, ?, 1, 1, ?, 'settled', 100, 90, 90, 0, ?)`,
		seed.jobID+"-chg", seed.tenantID, seed.jobID, seed.sectionID,
		"report:"+seed.jobID+":1:1:"+seed.sectionID+":1:1", now)
	exec(`INSERT INTO rgx_report_job_event (id, tenant_id, job_id, seq, type, payload_json,
		created_at, expires_at)
		VALUES (?, ?, ?, 1, 'job.succeeded', '{}', ?, ?)`,
		seed.jobID+"-ev", seed.tenantID, seed.jobID, now, eventExpires)
	exec(`INSERT INTO rgx_report_evidence_snapshot (id, tenant_id, chunk_id, source_content_hash,
		content, content_hash, content_size, truncated, created_at, expires_at)
		VALUES (?, ?, ?, 'sha-chunk', '原文片段', 'sha-content', 12, 0, ?, ?)`,
		seed.reportID+"-ev", seed.tenantID, seed.reportID+"-chunk", now, evidenceExpires)
}

func countRows(t *testing.T, gdb *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := gdb.WithContext(context.Background()).Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// newReportRetentionSvc mirrors newRetentionSvc but also hands back the gorm
// handle: seeding report aggregates with their frozen clocks and counting what
// survived is plain SQL, and the Store interface intentionally has no WithContext.
func newReportRetentionSvc(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	dsn := testSQLiteDSN(t, "report-retention.db") + "?_pragma=busy_timeout(10000)"
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc, gdb
}

func setRetentionEnabled(t *testing.T, svc *Service) {
	t.Helper()
	policy := config.Retention{
		Enabled: true, AuditDays: 180, UsageDays: 90, FeedbackDays: 180, JobDays: 30,
		ReportDays: 90, ReportEvidenceDays: 90, ReportArtifactDays: 30,
		ReportEventDays: 7, ReportCleanupHoldDays: 7,
	}
	svc.SetRetentionPolicy(policy)
}

// TestReportRetentionUsesRowClockNotLivePolicy proves the "参数只作用于新建行"
// rule from the other side: a row whose frozen clock is already past gets purged
// even though the live policy now says 3650 days. If the janitor recomputed a
// cutoff from config, this test would keep the row forever.
func TestReportRetentionUsesRowClockNotLivePolicy(t *testing.T) {
	svc, gdb := newReportRetentionSvc(t)
	ctx := context.Background()
	setRetentionEnabled(t, svc)

	seed := reportSeed{tenantID: "t-rowclock", reportID: "rep-rowclock",
		revisionID: "rev-rowclock", jobID: "job-rowclock", sectionID: "sec-rowclock"}
	now := time.Now().UTC()
	seedReport(t, gdb, seed,
		now.Add(-time.Hour), now.Add(24*time.Hour), now.Add(24*time.Hour))

	policy := config.Retention{Enabled: true, AuditDays: 180, UsageDays: 90,
		FeedbackDays: 180, JobDays: 30, ReportDays: 3650, ReportEvidenceDays: 3650,
		ReportArtifactDays: 30, ReportEventDays: 3650, ReportCleanupHoldDays: 7}
	svc.SetRetentionPolicy(policy)

	summary, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary.Report[seed.tenantID]; got != 1 {
		t.Fatalf("report purged = %d, want 1 (row clock already expired)", got)
	}
	if n := countRows(t, gdb, "rgx_report", "id = ?", seed.reportID); n != 0 {
		t.Fatalf("report row survived: %d", n)
	}
}

// TestReportRetentionCascadesAggregate checks that one expired report takes its
// revisions, sections, claims, citations, jobs, attempts and charges with it —
// orphans would keep pointing at a report that no longer answers.
func TestReportRetentionCascadesAggregate(t *testing.T) {
	svc, gdb := newReportRetentionSvc(t)
	ctx := context.Background()
	setRetentionEnabled(t, svc)

	seed := reportSeed{tenantID: "t-cascade", reportID: "rep-cascade",
		revisionID: "rev-cascade", jobID: "job-cascade", sectionID: "sec-cascade"}
	now := time.Now().UTC()
	// Evidence keeps its own (future) clock so this test isolates the cascade.
	seedReport(t, gdb, seed, now.Add(-time.Hour), now.Add(48*time.Hour), now.Add(-time.Hour))

	if _, err := svc.RunRetention(ctx); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		table, where string
		args         []any
	}{
		{"rgx_report", "id = ?", []any{seed.reportID}},
		{"rgx_report_revision", "report_id = ?", []any{seed.reportID}},
		{"rgx_report_section", "revision_id = ?", []any{seed.revisionID}},
		{"rgx_report_claim", "revision_id = ?", []any{seed.revisionID}},
		{"rgx_report_citation", "revision_id = ?", []any{seed.revisionID}},
		{"rgx_report_job", "report_id = ?", []any{seed.reportID}},
		{"rgx_report_job_attempt", "report_job_id = ?", []any{seed.jobID}},
		{"rgx_report_charge", "report_job_id = ?", []any{seed.jobID}},
		{"rgx_report_job_event", "job_id = ?", []any{seed.jobID}},
	} {
		if n := countRows(t, gdb, target.table, target.where, target.args...); n != 0 {
			t.Fatalf("%s kept %d rows after report purge", target.table, n)
		}
	}
	// Evidence must survive: it never expires before its report.
	if n := countRows(t, gdb, "rgx_report_evidence_snapshot", "id = ?", seed.reportID+"-ev"); n != 1 {
		t.Fatalf("evidence snapshot deleted with the report (%d rows)", n)
	}
}

// TestReportRetentionHonoursCleanupHold: a soft delete inside its 7-day recovery
// window is skipped by the janitor even when `expires_at` already passed, and the
// hold is what changes the timing — not a rewritten TTL.
func TestReportRetentionHonoursCleanupHold(t *testing.T) {
	svc, gdb := newReportRetentionSvc(t)
	ctx := context.Background()
	setRetentionEnabled(t, svc)

	now := time.Now().UTC()
	held := reportSeed{tenantID: "t-hold", reportID: "rep-held",
		revisionID: "rev-held", jobID: "job-held", sectionID: "sec-held"}
	seedReport(t, gdb, held, now.Add(-time.Hour), now.Add(48*time.Hour), now.Add(48*time.Hour))
	holdUntil := now.Add(24 * time.Hour)
	if err := gdb.WithContext(ctx).Exec(
		`UPDATE rgx_report SET deleted_at = ?, cleanup_hold_until = ? WHERE id = ?`,
		now, holdUntil, held.reportID).Error; err != nil {
		t.Fatal(err)
	}
	released := reportSeed{tenantID: "t-hold", reportID: "rep-released",
		revisionID: "rev-released", jobID: "job-released", sectionID: "sec-released"}
	seedReport(t, gdb, released, now.Add(-time.Hour), now.Add(48*time.Hour), now.Add(48*time.Hour))
	if err := gdb.WithContext(ctx).Exec(
		`UPDATE rgx_report SET deleted_at = ?, cleanup_hold_until = ? WHERE id = ?`,
		now.Add(-8*24*time.Hour), now.Add(-24*time.Hour), released.reportID).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.RunRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, gdb, "rgx_report", "id = ?", held.reportID); n != 1 {
		t.Fatalf("report inside its recovery window was purged (%d rows)", n)
	}
	if n := countRows(t, gdb, "rgx_report", "id = ?", released.reportID); n != 0 {
		t.Fatalf("report past its recovery window survived (%d rows)", n)
	}
	// The hold never rewrote the frozen TTL.
	var expiresAt time.Time
	if err := gdb.WithContext(ctx).Table("rgx_report").
		Select("expires_at").Where("id = ?", held.reportID).Scan(&expiresAt).Error; err != nil {
		t.Fatal(err)
	}
	if d := expiresAt.Sub(now.Add(-time.Hour)); d > time.Second || d < -time.Second {
		t.Fatalf("cleanup hold changed the frozen expires_at by %s", d)
	}
}

// TestReportRetentionClassesExpireIndependently pins the "four clocks" rule:
// evidence and events are purged by their own timestamps while the report stays.
func TestReportRetentionClassesExpireIndependently(t *testing.T) {
	svc, gdb := newReportRetentionSvc(t)
	ctx := context.Background()
	setRetentionEnabled(t, svc)

	now := time.Now().UTC()
	seed := reportSeed{tenantID: "t-classes", reportID: "rep-classes",
		revisionID: "rev-classes", jobID: "job-classes", sectionID: "sec-classes"}
	// Report lives on; evidence and the event stream are past their clocks.
	seedReport(t, gdb, seed, now.Add(48*time.Hour), now.Add(-time.Hour), now.Add(-time.Hour))
	fresh := reportSeed{tenantID: "t-classes", reportID: "rep-classes-fresh",
		revisionID: "rev-classes-fresh", jobID: "job-classes-fresh", sectionID: "sec-classes-fresh"}
	seedReport(t, gdb, fresh, now.Add(48*time.Hour), now.Add(48*time.Hour), now.Add(48*time.Hour))

	summary, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary.Report[seed.tenantID]; got != 0 {
		t.Fatalf("live report purged = %d, want 0", got)
	}
	if got := summary.ReportEvidence[seed.tenantID]; got != 1 {
		t.Fatalf("evidence purged = %d, want 1", got)
	}
	if got := summary.ReportEvent[seed.tenantID]; got != 1 {
		t.Fatalf("events purged = %d, want 1", got)
	}
	if n := countRows(t, gdb, "rgx_report_evidence_snapshot", "id = ?", seed.reportID+"-ev"); n != 0 {
		t.Fatal("expired evidence snapshot survived")
	}
	if n := countRows(t, gdb, "rgx_report_job_event", "job_id = ?", seed.jobID); n != 0 {
		t.Fatal("expired event survived")
	}
	if n := countRows(t, gdb, "rgx_report_evidence_snapshot", "id = ?", fresh.reportID+"-ev"); n != 1 {
		t.Fatal("live evidence snapshot was purged")
	}
	// The report itself, its revision and its job are untouched.
	if n := countRows(t, gdb, "rgx_report", "id = ?", seed.reportID); n != 1 {
		t.Fatal("live report purged by a sibling class")
	}
	if n := countRows(t, gdb, "rgx_report_job", "id = ?", seed.jobID); n != 1 {
		t.Fatal("job purged when only its event stream expired")
	}
}

// TestReportPurgeSecondPassIsRecordNotFound pins the accounting rule: purging a
// report that a concurrent pass already removed returns ErrRecordNotFound so the
// janitor skips the counter instead of writing an inflated retention.purge audit.
func TestReportPurgeSecondPassIsRecordNotFound(t *testing.T) {
	svc, gdb := newReportRetentionSvc(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seed := reportSeed{tenantID: "t-idem", reportID: "rep-idem",
		revisionID: "rev-idem", jobID: "job-idem", sectionID: "sec-idem"}
	seedReport(t, gdb, seed, now.Add(-time.Hour), now.Add(48*time.Hour), now.Add(48*time.Hour))

	if err := svc.Store.PurgeReport(ctx, seed.tenantID, seed.reportID); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	err := svc.Store.PurgeReport(ctx, seed.tenantID, seed.reportID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second purge err = %v, want gorm.ErrRecordNotFound", err)
	}
}
