package service

// A15 (P0-B) — the quality trend is a *read of stored verdicts*, and its whole value
// depends on three properties, none of which a happy-path test would notice:
//
//   - a ratio nobody could compute is `null`, not `0` (a trend line that draws 0%
//     quality for a week where nothing was judged is a lie the chart cannot correct);
//   - only delivered revisions move the quality ratios, so one crashed run does not
//     read as a workspace-wide quality drop — but the crash is still counted, in its
//     own column;
//   - the rollup is scoped: `view=own` covers the caller's reports, `view=tenant` needs
//     `manage:report`, and a soft-deleted report never counts (A47 promises it is
//     gone, and metrics are the one place nobody would check).
//
// The rows are seeded directly instead of being produced by a real run: the numbers
// under test are the *stored* ones, and driving five revisions through the pipeline to
// obtain five specific metrics_json payloads would test finalize (already covered)
// rather than the rollup.

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// seedMetricsRevision writes one report + one terminal revision with the given stored
// metrics, created `at`.
func seedMetricsRevision(
	t *testing.T, gdb *gorm.DB, tenantID, ownerID, reportID, revisionID string,
	status, completeness string, at time.Time, metricsJSON string,
) {
	t.Helper()
	report := model.Report{
		ID: reportID, TenantID: tenantID, OwnerUserID: ownerID,
		Title: "指标趋势样例", TemplateKey: reportComplianceTemplateKey,
		AccessPolicy: model.ReportAccessOwnerOnly, Status: model.ReportStatusActive,
		ExpiresAt: at.AddDate(0, 0, 90), CreatedAt: at, UpdatedAt: at,
	}
	if err := gdb.Create(&report).Error; err != nil {
		t.Fatalf("seed report: %v", err)
	}
	revision := model.ReportRevision{
		ID: revisionID, ReportID: reportID, TenantID: tenantID, Revision: 1,
		TemplateKey: reportComplianceTemplateKey, TemplateVersion: 1,
		Status: status, Completeness: completeness, MetricsJSON: metricsJSON,
		CreatedAt: at,
	}
	if err := gdb.Create(&revision).Error; err != nil {
		t.Fatalf("seed revision: %v", err)
	}
}

// deliveredMetrics is one published revision's stored verdict: two of three sections
// judged, one passed.
func deliveredMetrics() string {
	return `{
		"structure_completeness": 1.0,
		"citation_coverage": 0.8,
		"evidence_availability": 1.0,
		"unsupported_ratio": 0.0,
		"attribution_consistency": 1.0,
		"quality_gate_pass_rate": 0.5,
		"quality_judged": 2,
		"quality_passed": 1,
		"quality_needs_review": 1,
		"quality_not_checked": 1,
		"quality_flag": "ok"
	}`
}

// unjudgedMetrics is the shape the live drill produced before the caliber fix: a
// stored `0` next to `not_checked: 4`. The rollup must not reproduce that `0`.
func unjudgedMetrics() string {
	return `{
		"structure_completeness": 1.0,
		"citation_coverage": 0.0,
		"quality_gate_pass_rate": 0,
		"quality_judged": 0,
		"quality_not_checked": 4,
		"quality_flag": "ok"
	}`
}

func TestMetricsTrendLeavesUndeliveredRevisionsOutOfTheRatios(t *testing.T) {
	fx := newReportFixture(t, "metrics-rollup")
	ctx := context.Background()
	// Both revisions must share one day bucket. Anchor them at noon of *today* instead
	// of "now minus N hours": run between 00:00 and 03:00 UTC and the 3-hour offset
	// silently lands on the previous day, turning this into a clock-dependent test.
	now := time.Now().UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-ok", "rev-metrics-ok",
		model.ReportRevisionPublished, model.ReportCompletenessFull, base.Add(-time.Hour), deliveredMetrics())
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-bad", "rev-metrics-bad",
		model.ReportRevisionFailed, model.ReportCompletenessPartial, base.Add(-2*time.Hour), unjudgedMetrics())

	view, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, ReportMetricsTrendRequest{})
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	totals := view.Totals
	if totals.Revisions != 2 || totals.Published != 1 || totals.Failed != 1 {
		t.Fatalf("counts must cover every terminal revision: %+v", totals)
	}
	// Structure comes from the delivered revision only (1.0), even though the failed
	// one stored a partial score.
	if totals.StructureCompleteness == nil || *totals.StructureCompleteness != 1 {
		t.Fatalf("structure_completeness must average delivered revisions: %+v", totals.StructureCompleteness)
	}
	if totals.CitationCoverage == nil || *totals.CitationCoverage != 0.8 {
		t.Fatalf("citation_coverage must average delivered revisions: %+v", totals.CitationCoverage)
	}
	if totals.QualityJudged != 2 || totals.QualityPassed != 1 || totals.QualityNeedsReview != 1 {
		t.Fatalf("gate counts must come from the delivered revision: %+v", totals)
	}
	if totals.QualityGatePassRate == nil || *totals.QualityGatePassRate != 0.5 {
		t.Fatalf("gate rate must be passed/judged: %+v", totals.QualityGatePassRate)
	}
	// `not_checked` is copied only from the delivered revision (which stored 1). The
	// failed one stored 4; a total of 1 is the proof it was not folded in.
	if totals.QualityNotChecked != 1 {
		t.Fatalf("undelivered revisions must not feed the gate counts: %d", totals.QualityNotChecked)
	}
	if len(view.Buckets) != 1 || view.Buckets[0].Date != base.Format("2006-01-02") {
		t.Fatalf("two same-day revisions must share one bucket: %+v", view.Buckets)
	}
}

func TestMetricsTrendStoresNullInsteadOfAZeroRate(t *testing.T) {
	fx := newReportFixture(t, "metrics-null")
	ctx := context.Background()
	now := time.Now().UTC()
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-none", "rev-metrics-none",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-time.Hour), unjudgedMetrics())

	view, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, ReportMetricsTrendRequest{})
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if view.Totals.QualityGatePassRate != nil {
		t.Fatalf("no judged section means no rate, got %v", *view.Totals.QualityGatePassRate)
	}
	if view.Buckets[0].QualityGatePassRate != nil {
		t.Fatal("bucket rate must be null too")
	}
	if view.Totals.QualityNotChecked != 4 {
		t.Fatalf("not_checked must still be reported: %d", view.Totals.QualityNotChecked)
	}
	// A stored `0.0` citation coverage is a real measurement (nothing was cited), so
	// it must NOT be turned into null: only *absent denominators* are null.
	if view.Totals.CitationCoverage == nil || *view.Totals.CitationCoverage != 0 {
		t.Fatalf("a measured zero must stay zero: %+v", view.Totals.CitationCoverage)
	}
}

func TestMetricsTrendExcludesSoftDeletedReports(t *testing.T) {
	fx := newReportFixture(t, "metrics-deleted")
	ctx := context.Background()
	now := time.Now().UTC()
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-live", "rev-metrics-live",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-time.Hour), deliveredMetrics())
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-gone", "rev-metrics-gone",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-2*time.Hour), deliveredMetrics())

	if err := fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, "rep-metrics-gone", "", false); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	view, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, ReportMetricsTrendRequest{})
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if view.Totals.Revisions != 1 {
		t.Fatalf("a deleted report must not be counted: %+v", view.Totals)
	}
}

func TestMetricsTrendViewsOwnVersusTenantViews(t *testing.T) {
	fx := newReportFixture(t, "metrics-scope")
	ctx := context.Background()
	now := time.Now().UTC()
	colleague, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-metrics-colleague", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-mine", "rev-metrics-mine",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-time.Hour), deliveredMetrics())
	seedMetricsRevision(t, fx.gdb, fx.tenantID, colleague.ID, "rep-metrics-theirs", "rev-metrics-theirs",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-time.Hour), deliveredMetrics())

	own, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, ReportMetricsTrendRequest{})
	if err != nil {
		t.Fatalf("own trend: %v", err)
	}
	if own.Totals.Revisions != 1 || own.View != "own" {
		t.Fatalf("default view must be the caller's own reports: %+v", own)
	}
	// An operator may not ask for the workspace rollup: that is governance.
	if _, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID,
		ReportMetricsTrendRequest{View: "tenant"}); err == nil {
		t.Fatal("view=tenant must require manage:report")
	}

	admin, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-metrics-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fx.svc.ReportMetricsTrend(ctx, admin.ID, fx.tenantID,
		ReportMetricsTrendRequest{View: "tenant"})
	if err != nil {
		t.Fatalf("tenant trend: %v", err)
	}
	if workspace.Totals.Revisions != 2 || workspace.View != "tenant" {
		t.Fatalf("view=tenant must cover the workspace: %+v", workspace)
	}
}

func TestMetricsTrendWindowAndBuckets(t *testing.T) {
	fx := newReportFixture(t, "metrics-window")
	ctx := context.Background()
	now := time.Now().UTC()
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-today", "rev-metrics-today",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-time.Hour), deliveredMetrics())
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-old", "rev-metrics-old",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.AddDate(0, 0, -20), deliveredMetrics())

	week, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID,
		ReportMetricsTrendRequest{Days: 7, Bucket: "week"})
	if err != nil {
		t.Fatalf("week trend: %v", err)
	}
	if week.Totals.Revisions != 1 {
		t.Fatalf("a 7-day window must exclude a 20-day-old revision: %+v", week.Totals)
	}
	// A week bucket is keyed by its Monday, and a Monday is what the key must be.
	for _, bucket := range week.Buckets {
		parsed, err := time.Parse("2006-01-02", bucket.Date)
		if err != nil {
			t.Fatalf("bucket key must be a date: %q", bucket.Date)
		}
		if parsed.Weekday() != time.Monday {
			t.Fatalf("week bucket must start on Monday, got %s", parsed.Weekday())
		}
	}
	month, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID,
		ReportMetricsTrendRequest{Days: 30, Bucket: "day"})
	if err != nil {
		t.Fatalf("month trend: %v", err)
	}
	if month.Totals.Revisions != 2 {
		t.Fatalf("a 30-day window must include both revisions: %+v", month.Totals)
	}
	// Buckets are only emitted when they exist: a quiet day is an absence, not a 0.
	if len(month.Buckets) != 2 {
		t.Fatalf("only populated buckets are emitted, got %d: %+v", len(month.Buckets), month.Buckets)
	}
	if month.Truncated {
		t.Fatal("two rows are not a truncation")
	}
	// The window echoes back half-open bounds, so a client can page without
	// recomputing them.
	if !month.To.After(month.From) || month.To.Sub(month.From) != 30*24*time.Hour {
		t.Fatalf("window bounds wrong: %v → %v", month.From, month.To)
	}
}

func TestMetricsTrendValidatesItsQuery(t *testing.T) {
	fx := newReportFixture(t, "metrics-validate")
	ctx := context.Background()
	cases := []ReportMetricsTrendRequest{
		{Days: -1},
		{Days: reportMetricsTrendMaxDays + 1},
		{Bucket: "month"},
		{View: "everyone"},
		{TemplateKey: "Not A Key"},
	}
	for _, request := range cases {
		if _, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, request); err == nil {
			t.Fatalf("request %+v must be refused", request)
		} else if code := reportErrorCode(err); code != codeReportOutlineInvalid {
			t.Fatalf("request %+v must be 44105, got %d", request, code)
		}
	}
}

// A revision whose stored metrics cannot be parsed must be counted as unreadable, not
// skipped: a trend that silently drops rows reports a healthier workspace than the one
// that exists.
func TestMetricsTrendCountsUnreadableMetrics(t *testing.T) {
	fx := newReportFixture(t, "metrics-broken")
	ctx := context.Background()
	now := time.Now().UTC()
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-metrics-broken", "rev-metrics-broken",
		model.ReportRevisionPublished, model.ReportCompletenessFull, now.Add(-time.Hour), "{not json")

	view, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, ReportMetricsTrendRequest{})
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if view.Totals.Revisions != 1 || view.Totals.MetricsMissing != 1 {
		t.Fatalf("unreadable metrics must be counted: %+v", view.Totals)
	}
	if view.Totals.CitationCoverage != nil || view.Totals.QualityGatePassRate != nil {
		t.Fatal("an unreadable row must not feed the ratios")
	}
}
