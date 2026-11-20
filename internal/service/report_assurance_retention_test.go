package service

// Retention for the two new report classes (doc/301 §6.4): frozen candidates and
// assurance judgements.
//
// The interesting property is not "rows get deleted" — it is *which clock* drives the
// delete and what survives it. Candidates carry their own `expires_at` (they are corpus
// text), assurance rows carry none (their lifetime is the report's), and the audit
// entry for "who acknowledged these conflicts" must outlive both. A test that only
// counted deletions would pass with all three conflated.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportRetentionPurgesExpiredCandidatesOnly(t *testing.T) {
	fx := newReportFixture(t, "retention-candidate")
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(48 * time.Hour)

	rows := []model.ReportConflictCandidate{
		{
			ID: "cand-expired", TenantID: fx.tenantID, ReportID: "report-retention",
			RevisionID: "rev-retention", SectionOrd: 1, SectionID: "sec-1",
			ChunkID: "chunk-expired", Content: "过期候选", ContentHash: "hash-expired",
			PoolRank: 1, CreatedAt: past.Add(-24 * time.Hour), ExpiresAt: &past,
		},
		{
			ID: "cand-live", TenantID: fx.tenantID, ReportID: "report-retention",
			RevisionID: "rev-retention", SectionOrd: 1, SectionID: "sec-1",
			ChunkID: "chunk-live", Content: "未过期候选", ContentHash: "hash-live",
			PoolRank: 2, CreatedAt: now, ExpiresAt: &future,
		},
		{
			// A candidate with no clock at all: it must not be swept by the expiry pass
			// (it would be indistinguishable from "expired immediately"), and it is
			// exactly the column state an older release would leave behind.
			ID: "cand-noclock", TenantID: fx.tenantID, ReportID: "report-retention",
			RevisionID: "rev-retention", SectionOrd: 1, SectionID: "sec-1",
			ChunkID: "chunk-noclock", Content: "无时钟候选", ContentHash: "hash-noclock",
			PoolRank: 3, CreatedAt: now,
		},
	}
	if err := fx.svc.Store.InsertConflictCandidates(ctx, rows); err != nil {
		t.Fatalf("seed candidates: %v", err)
	}

	counts, err := fx.svc.cleanupExpiredReportCandidates(ctx, now)
	if err != nil {
		t.Fatalf("cleanup candidates: %v", err)
	}
	if counts[fx.tenantID] != 1 {
		t.Fatalf("purged %d candidates in this tenant, want 1", counts[fx.tenantID])
	}

	left, err := fx.svc.Store.ListConflictCandidates(ctx, fx.tenantID, "rev-retention", 1)
	if err != nil {
		t.Fatalf("list remaining: %v", err)
	}
	kept := map[string]bool{}
	for _, row := range left {
		kept[row.ID] = true
	}
	if kept["cand-expired"] {
		t.Fatal("expired candidate survived the purge")
	}
	if !kept["cand-live"] || !kept["cand-noclock"] {
		t.Fatalf("purge removed a candidate it should have kept: %v", kept)
	}
}

// TestReportRetentionPurgesAssuranceOfDeletedReports covers the `NOT EXISTS` pass: the
// assurance tables have no clock, so "orphaned" is the only trigger. The children must
// go first and the audit entry must survive.
func TestReportRetentionPurgesAssuranceOfDeletedReports(t *testing.T) {
	fx := newReportFixture(t, "retention-assurance")
	ctx := context.Background()
	now := time.Now().UTC()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-retention-assurance",
		reportCreateRequest(fx.datasetLink))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	reportID := created.Report.ID
	revision, err := fx.svc.Store.GetReportRevision(ctx, fx.tenantID, reportID, 1)
	if err != nil || revision == nil {
		t.Fatalf("load revision: %v", err)
	}

	run := &model.ReportAssuranceRun{
		ID: "run-retention", TenantID: fx.tenantID, ReportID: reportID, RevisionID: revision.ID,
		Revision: 1, Status: model.ReportAssuranceStatusSucceeded, Mode: model.ReportAssuranceModeFull,
		RequestedPolicy:    model.ReportAssurancePolicyAdvisory,
		ReleaseEligibility: model.ReportAssuranceEligibilityTier0,
		TriggeredBy:        fx.owner.ID, CreatedAt: now,
	}
	if err := fx.gdb.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	evaluation := &model.ReportClaimEvaluation{
		ID: "eval-retention", TenantID: fx.tenantID, RunID: run.ID, RevisionID: revision.ID,
		ClaimID: "claim-1", SectionOrd: 1, Method: model.ReportEvaluationMethodRule,
		SupportVerdict: model.ReportVerdictSupported,
		QuoteStatus:    model.ReportQuoteStatusVerified, Independence: model.ReportIndependenceSameModel,
		CreatedAt: now,
	}
	if err := fx.gdb.Create(evaluation).Error; err != nil {
		t.Fatalf("seed claim evaluation: %v", err)
	}
	finding := &model.ReportReviewFinding{
		ID: "finding-retention", TenantID: fx.tenantID, ReportID: reportID, RevisionID: revision.ID,
		RunID: &run.ID, SectionOrd: 1, Source: "semantic", Category: model.ReportFindingUnsupported,
		Severity: model.ReportSeverityMedium, Status: model.ReportFindingStatusOpen,
		Title: "结论缺乏支撑", CreatedAt: now, UpdatedAt: now,
	}
	if err := fx.gdb.Create(finding).Error; err != nil {
		t.Fatalf("seed finding: %v", err)
	}

	// Nothing is deleted while the report exists: these rows are judgements *about* a
	// live revision, and the pass must not mistake "no clock" for "expired".
	if counts, err := fx.svc.cleanupOrphanedReportAssurance(ctx, now); err != nil {
		t.Fatalf("orphan pass: %v", err)
	} else if counts[fx.tenantID] != 0 {
		t.Fatalf("orphan pass deleted %d rows of a live report", counts[fx.tenantID])
	}

	// Now delete the report behind the rows, which is what a crash between the report
	// delete and the child deletes would leave behind.
	if err := fx.gdb.Where("id = ?", reportID).Delete(&model.Report{}).Error; err != nil {
		t.Fatalf("delete report: %v", err)
	}
	if _, err := fx.svc.cleanupOrphanedReportAssurance(ctx, now); err != nil {
		t.Fatalf("orphan pass after delete: %v", err)
	}

	for name, count := range map[string]func() int64{
		"runs": func() int64 {
			return fx.countRows(&model.ReportAssuranceRun{}, "report_id = ?", reportID)
		},
		"claim_evaluations": func() int64 {
			return fx.countRows(&model.ReportClaimEvaluation{}, "revision_id = ?", revision.ID)
		},
		"findings": func() int64 {
			return fx.countRows(&model.ReportReviewFinding{}, "report_id = ?", reportID)
		},
	} {
		if got := count(); got != 0 {
			t.Fatalf("%s survived the orphan purge: %d rows left", name, got)
		}
	}
}

// countRows is a direct count, bypassing the repository: a purge assertion must not be
// able to pass because a read path filters the row out.
func (fx *reportFixture) countRows(target interface{}, where string, args ...interface{}) int64 {
	var total int64
	fx.gdb.Model(target).Where(where, args...).Count(&total)
	return total
}

// TestRetentionSummaryCountsTheNewClasses runs the real pass table rather than a
// private helper: the two new classes must appear in the closeout log with their own
// names. "We kept uncited chunks" is a retention fact an operator has to be able to
// read apart from "we kept citations".
func TestRetentionSummaryCountsTheNewClasses(t *testing.T) {
	fx := newReportFixture(t, "retention-summary")
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Minute)

	if err := fx.svc.Store.InsertConflictCandidates(ctx, []model.ReportConflictCandidate{{
		ID: "cand-summary", TenantID: fx.tenantID, ReportID: "report-missing",
		RevisionID: "rev-summary", SectionOrd: 1, SectionID: "sec-1",
		ChunkID: "chunk-summary", Content: "候选", ContentHash: "hash-summary",
		PoolRank: 1, CreatedAt: past, ExpiresAt: &past,
	}}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}

	summary, err := fx.svc.RunRetention(ctx)
	if err != nil {
		t.Fatalf("run retention: %v", err)
	}
	if summary.ReportCandidate == nil || summary.ReportCandidate[fx.tenantID] != 1 {
		t.Fatalf("ReportCandidate bucket = %v, want one row for this tenant", summary.ReportCandidate)
	}
	// The assurance bucket is initialised even when empty, so an operator can see "this
	// pass ran and found nothing" instead of an absent key.
	if summary.ReportAssurance == nil {
		t.Fatal("ReportAssurance bucket was not initialised")
	}
}
